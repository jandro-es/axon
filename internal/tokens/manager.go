package tokens

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/search"
)

// Decision is the pre-flight verdict for a call (docs/07 §2).
type Decision string

const (
	DecisionProceed   Decision = "proceed"
	DecisionDowngrade Decision = "downgrade"
	DecisionDefer     Decision = "defer"
	DecisionDeny      Decision = "deny"
)

// ErrDenied / ErrDeferred are returned by Run when a call is not executed.
var (
	ErrDenied   = errors.New("call denied: token budget exhausted")
	ErrDeferred = errors.New("call deferred: would exceed token budget")
)

// ErrRunBudgetExceeded re-exports the adapter's kill-switch sentinel so
// automations can detect it without importing agent (dependency rule:
// tokens is the only importer of agent).
var ErrRunBudgetExceeded = agent.ErrRunBudgetExceeded

// Message is one turn of an assembled prompt.
type Message struct {
	Role    string // "user" | "assistant"
	Content string
}

// AgentCall is a unit of work for Claude. Callers never construct a raw agent
// request; they hand the manager an AgentCall and call Run.
type AgentCall struct {
	Operation    string // ledger label, e.g. "ingest.enrich"
	ModelKey     string // "classify"|"routine"|"synthesis" or a concrete model
	System       string
	Messages     []Message
	BudgetTokens int  // per-call input cap (0 = none)
	Essential    bool // surfaced, never silently blocked (budget-guard, heartbeat, interactive)
	RunID        *int64
	// ValidateOutput, when set, is applied to every response text. For local
	// providers a failure triggers the retry/fallback ladder (FR-79); for
	// Claude it fails the call (ledgered conservatively under the :failed
	// operation label, like adapter errors).
	ValidateOutput func(string) error
	// OutputSchema optionally carries a JSON Schema for providers with
	// structured output (Apple guided generation; Ollama JSON mode).
	OutputSchema json.RawMessage
	// Tools + MaxTurns request an agentic run (ADR-017): Claude provider
	// only; BudgetTokens becomes the per-run TOTAL cap enforced by the
	// adapter's streaming kill-switch (for one-shot calls it remains the
	// pre-flight input cap).
	Tools    []string
	MaxTurns int
}

// Authorization is the pre-flight result.
type Authorization struct {
	Decision Decision
	Model    string // resolved concrete model to use
	Provider string // "claude" | "ollama" | "apple" (ADR-015)
	EstInput int
	Reason   string
}

// AgentResult is a completed call plus its ledger id and the authorization used.
type AgentResult struct {
	Text     string
	Model    string
	Usage    agent.Usage
	LedgerID int64
	Auth     Authorization
}

// Window is a single budget window's state.
type Window struct {
	Used  int64
	Limit int64
	Pct   float64
	// CostUsed/CostCap track dollars (api_key mode; zero otherwise). Only the
	// day window carries a cap (limits.daily_cost_usd, FR-42); the week window
	// still reports its accrued cost for display.
	CostUsed float64
	CostCap  float64
	CostPct  float64
}

// BudgetStatus is the read-only view powering `axon status`, the tokens.status
// MCP tool and the dashboard gauges.
type BudgetStatus struct {
	Profile     string
	Day         Window
	Week        Window
	GuardPct    int
	GuardPaused bool
	// GuardReason is a human-readable explanation of why the guard is paused
	// (which window crossed the threshold), or "" when not paused. It gives
	// automations and the CLI a clear, actionable message instead of a bare flag.
	GuardReason string
}

// Context is a token-bounded assembled context from retrieval.
type Context struct {
	Messages []Message
	Tokens   int
	Sources  []string
}

// RetrieveOpts parameterise BuildContext.
type RetrieveOpts struct {
	TopK             int
	MaxContextTokens int
}

// Manager is the chokepoint interface (docs/07 §1).
type Manager interface {
	BuildContext(ctx context.Context, query string, opts RetrieveOpts) (Context, error)
	Authorize(ctx context.Context, call AgentCall) (Authorization, error)
	Run(ctx context.Context, call AgentCall) (AgentResult, error)
	Status(ctx context.Context, profile string) (BudgetStatus, error)
}

// Config carries the per-profile settings the manager needs.
type Config struct {
	Profile  string
	AuthMode string
	Models   config.ModelsConfig
	Limits   config.LimitsConfig
	Prices   map[string]config.Price
	// RedactionRules are the profile's policy regexes, applied to every prompt
	// and system string at the chokepoint before it reaches the adapter —
	// defence-in-depth on top of ingestion-time redaction, and the only cover
	// for automation prompts built from raw vault notes (NFR-05/NFR-14:
	// redaction is enforced in code, not by asking the model nicely).
	RedactionRules []string
	// Now optionally overrides the clock used for budget windows and ledger
	// timestamps. Defaults to time.Now; set only by tests for determinism.
	Now func() time.Time
}

// manager is the concrete chokepoint.
type manager struct {
	db        *sql.DB
	router    agent.Router     // per-provider adapters; Claude may be nil for read-only use
	searcher  *search.Searcher // may be nil if BuildContext is unused
	bus       *events.Bus
	estimator Estimator
	cfg       Config
	redact    []*regexp.Regexp
	now       func() time.Time
}

// New builds a Manager around a single Claude adapter (the pre-ADR-015
// shape). ag and searcher may be nil for read-only callers.
func New(database *sql.DB, ag agent.Agent, searcher *search.Searcher, bus *events.Bus, cfg Config) Manager {
	return NewWithRouter(database, agent.Router{Claude: ag}, searcher, bus, cfg)
}

// NewWithRouter builds a Manager that dispatches per-tier to the router's
// adapters (ADR-015). Run requires the referenced adapters to be non-nil.
func NewWithRouter(database *sql.DB, router agent.Router, searcher *search.Searcher, bus *events.Bus, cfg Config) Manager {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &manager{
		db:        database,
		router:    router,
		searcher:  searcher,
		bus:       bus,
		estimator: newCachingEstimator(HeuristicEstimator{}),
		cfg:       cfg,
		redact:    compileRedaction(cfg.RedactionRules),
		now:       now,
	}
}

// compileRedaction compiles the profile's redaction regexes. Invalid patterns
// are skipped here (config validation is the place that rejects them loudly);
// a skipped pattern must not disable the valid ones.
func compileRedaction(rules []string) []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, r := range rules {
		if re, err := regexp.Compile(r); err == nil {
			out = append(out, re)
		}
	}
	return out
}

// applyRedaction scrubs every configured pattern from s.
func (m *manager) applyRedaction(s string) string {
	for _, re := range m.redact {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

// resolveModel maps a model key (classify|routine|synthesis) to a concrete model
// string, or returns the key unchanged if it is already concrete.
func (m *manager) resolveModel(key string) string {
	switch key {
	case "classify":
		return m.cfg.Models.Classify
	case "routine":
		return m.cfg.Models.Routine
	case "synthesis":
		return m.cfg.Models.Synthesis
	default:
		return key // already a concrete model string
	}
}

// resolveRef resolves a model key to (provider, concrete model) — ADR-015.
func (m *manager) resolveRef(key string) config.ModelRef {
	return config.ParseModelRef(m.resolveModel(key))
}

// downgradeClaudeKey returns the next cheaper tier whose provider is Claude,
// or "". Local tiers are skipped: they are budget-exempt already and never
// the target of a budget downgrade (FR-78).
func (m *manager) downgradeClaudeKey(key string) string {
	for k := downgradeKey(key); k != ""; k = downgradeKey(k) {
		if m.resolveRef(k).Provider == config.ProviderClaude {
			return k
		}
	}
	return ""
}

// downgradeKey returns the next cheaper tier for a model key, or "" if already
// at the cheapest (classify) or unknown. Downgrading conserves the plan's
// limits (subscription) or dollar cost (api_key).
func downgradeKey(key string) string {
	switch key {
	case "synthesis":
		return "routine"
	case "routine":
		return "classify"
	default:
		return ""
	}
}

// tokenCounter is satisfied by an adapter that can count input tokens exactly
// (the api_key direct-API adapter). When present, the manager uses it for an
// exact pre-flight estimate instead of the local heuristic (FR-40).
type tokenCounter interface {
	CountTokens(ctx context.Context, model, system, prompt string) (int, error)
}

// estimateCall returns the heuristic pre-flight input estimate for a call.
func (m *manager) estimateCall(call AgentCall) int {
	var b strings.Builder
	b.WriteString(call.System)
	for _, msg := range call.Messages {
		b.WriteString("\n")
		b.WriteString(msg.Content)
	}
	return m.estimator.Estimate(b.String())
}

// estimateInput returns the pre-flight input estimate for a call at a resolved
// model ref. In api_key mode (Claude provider only) it uses the exact
// count_tokens endpoint (falling back to the heuristic on error); otherwise it
// uses the local heuristic (FR-40).
func (m *manager) estimateInput(ctx context.Context, ref config.ModelRef, call AgentCall) int {
	if m.cfg.AuthMode == "api_key" && ref.Provider == config.ProviderClaude {
		if ag, err := m.router.Resolve(ref.Provider); err == nil {
			if c, ok := ag.(tokenCounter); ok {
				if n, err := c.CountTokens(ctx, ref.Model, call.System, joinMessages(call.Messages)); err == nil {
					return n
				}
			}
		}
	}
	return m.estimateCall(call)
}

// Authorize runs the pre-flight: estimate, resolve model, check per-call and
// day/week windows, and decide proceed/downgrade/defer/deny (docs/07 §2).
func (m *manager) Authorize(ctx context.Context, call AgentCall) (Authorization, error) {
	ref := m.resolveRef(call.ModelKey)
	model := ref.Model
	est := m.estimateInput(ctx, ref, call)
	auth := Authorization{Decision: DecisionProceed, Model: model, Provider: ref.Provider, EstInput: est}

	// Local providers are budget-exempt: no window checks, no defer/deny/
	// downgrade (FR-78). Failure handling is Run's fallback ladder's job.
	if ref.Provider != config.ProviderClaude {
		return auth, nil
	}

	// Per-call input cap: too-large context can't be made to fit by switching
	// models, so defer for the caller to shrink retrieval (unless essential).
	if call.BudgetTokens > 0 && est > call.BudgetTokens {
		if call.Essential {
			auth.Reason = "over per-call budget but essential; proceeding"
			return auth, nil
		}
		auth.Decision = DecisionDefer
		auth.Reason = fmt.Sprintf("estimated input %d exceeds per-call budget %d", est, call.BudgetTokens)
		return auth, nil
	}

	day, week, err := m.windows(ctx)
	if err != nil {
		return auth, err
	}

	// Dollar cap (api_key mode only, FR-42): the day window accrues cost_used
	// with every ledgered call; enforce limits.daily_cost_usd against it, using
	// the priced input estimate as the pre-flight increment (output cost is
	// unknown pre-call — the cap is re-checked with real numbers next call).
	if day.CostCap > 0 {
		estCost := 0.0
		if p, ok := m.cfg.Prices[model]; ok {
			estCost = float64(est) * p.Input
		}
		if day.CostUsed+estCost > day.CostCap {
			if call.Essential {
				auth.Reason = "over daily cost cap but essential; proceeding (surfaced)"
				return auth, nil
			}
			if dk := m.downgradeClaudeKey(call.ModelKey); dk != "" {
				auth.Decision = DecisionDowngrade
				auth.Model = m.resolveRef(dk).Model
				auth.Reason = fmt.Sprintf("over daily cost cap ($%.2f/$%.2f); downgraded %s -> %s", day.CostUsed, day.CostCap, call.ModelKey, dk)
				return auth, nil
			}
			if day.CostUsed >= day.CostCap {
				auth.Decision = DecisionDeny
				auth.Reason = fmt.Sprintf("daily cost cap exhausted ($%.2f/$%.2f)", day.CostUsed, day.CostCap)
				return auth, nil
			}
			auth.Decision = DecisionDefer
			auth.Reason = fmt.Sprintf("would exceed daily cost cap ($%.2f/$%.2f)", day.CostUsed, day.CostCap)
			return auth, nil
		}
	}

	overDay := day.Limit > 0 && day.Used+int64(est) > day.Limit
	overWeek := week.Limit > 0 && week.Used+int64(est) > week.Limit
	if !overDay && !overWeek {
		return auth, nil // within budget at the requested model
	}

	which := "daily"
	if overWeek {
		which = "weekly"
	}
	if call.Essential {
		auth.Reason = fmt.Sprintf("over %s token window but essential; proceeding (surfaced)", which)
		return auth, nil
	}
	// Try to downgrade to a cheaper Claude tier to conserve the plan/limits.
	if dk := m.downgradeClaudeKey(call.ModelKey); dk != "" {
		auth.Decision = DecisionDowngrade
		auth.Model = m.resolveRef(dk).Model
		auth.Reason = fmt.Sprintf("over %s token window; downgraded %s -> %s", which, call.ModelKey, dk)
		return auth, nil
	}
	// Already cheapest: deny if the window is fully spent, else defer.
	if (overDay && day.Used >= day.Limit) || (overWeek && week.Used >= week.Limit) {
		auth.Decision = DecisionDeny
		auth.Reason = fmt.Sprintf("%s token window exhausted", which)
		return auth, nil
	}
	auth.Decision = DecisionDefer
	auth.Reason = fmt.Sprintf("would exceed %s token window", which)
	return auth, nil
}

// Run authorises, executes through the agent adapter (unless denied/deferred),
// records usage to the ledger, updates the budget windows and emits an event.
// This is the only sanctioned path to Claude.
func (m *manager) Run(ctx context.Context, call AgentCall) (AgentResult, error) {
	auth, err := m.Authorize(ctx, call)
	if err != nil {
		return AgentResult{}, err
	}
	res := AgentResult{Auth: auth, Model: auth.Model}

	switch auth.Decision {
	case DecisionDeny:
		m.emit(events.LevelWarn, "token.deny", call.Operation, auth, nil)
		return res, fmt.Errorf("%w: %s", ErrDenied, auth.Reason)
	case DecisionDefer:
		m.emit(events.LevelWarn, "token.defer", call.Operation, auth, nil)
		return res, fmt.Errorf("%w: %s", ErrDeferred, auth.Reason)
	case DecisionDowngrade:
		m.emit(events.LevelInfo, "token.downgrade", call.Operation, auth, nil)
	}

	// Agentic (tool-using) calls require the Claude provider — local models
	// cannot drive MCP tools; never silently drop tools (ADR-017).
	if len(call.Tools) > 0 && auth.Provider != "" && auth.Provider != config.ProviderClaude {
		return res, fmt.Errorf("agentic call %q: tools require the claude provider, but %s resolves to %s (ADR-017)",
			call.Operation, call.ModelKey, auth.Provider)
	}

	// Local providers get their own execution path: retry + fallback ladder
	// instead of budget decisions (ADR-015, FR-79).
	if auth.Provider != "" && auth.Provider != config.ProviderClaude {
		return m.runLocal(ctx, call, auth)
	}

	ag, err := m.router.Resolve(auth.Provider)
	if err != nil {
		return res, fmt.Errorf("token manager: %w", err)
	}

	// Redaction is enforced HERE, at the chokepoint, so no caller (automations
	// building prompts from raw vault notes included) can forget it.
	resp, err := ag.Run(ctx, m.buildRequest(call, auth))
	if err != nil {
		// A failed call may still have consumed real tokens upstream — an
		// adapter timeout that killed `claude -p` mid-generation, garbled
		// stdout, or an agentic run killed at its budget. When the adapter
		// reported real accumulated usage, ledger THAT; otherwise fall back
		// to the pre-flight estimate (cardinal rule 1: ledger on EVERY path).
		if resp != nil {
			res.Usage = resp.Usage
		}
		m.recordFailure(ctx, call, auth, res)
		kind, level := "token.error", events.LevelError
		if errors.Is(err, agent.ErrRunBudgetExceeded) {
			kind, level = "token.run_budget_kill", events.LevelWarn
		}
		m.emit(level, kind, call.Operation, auth, map[string]any{"error": err.Error()})
		return res, fmt.Errorf("agent run %q: %w", call.Operation, err)
	}

	res.Text = resp.Text
	res.Usage = resp.Usage
	if resp.Model != "" {
		res.Model = resp.Model
	}
	// If the execution path reported no usage (headless path that returns
	// nothing), fall back to the pre-flight estimate for input and a measured
	// output-length estimate (docs/07 §3).
	if res.Usage.InputTokens+res.Usage.OutputTokens == 0 {
		res.Usage.InputTokens = auth.EstInput
		res.Usage.OutputTokens = HeuristicEstimator{}.Estimate(res.Text)
	}

	// Output validation at the chokepoint: a completed Claude call whose text
	// fails the caller's validator is a failed call, not a silent success.
	if call.ValidateOutput != nil {
		if verr := call.ValidateOutput(res.Text); verr != nil {
			m.recordFailure(ctx, call, auth, res)
			m.emit(events.LevelError, "token.error", call.Operation, auth,
				map[string]any{"error": "output validation: " + verr.Error()})
			return res, fmt.Errorf("agent run %q: output validation: %w", call.Operation, verr)
		}
	}

	// Post-hoc accounting: ledger + budget windows + event (FR-41, S4).
	ledgerID, err := m.record(ctx, call, auth, res)
	if err != nil {
		return res, err
	}
	res.LedgerID = ledgerID
	return res, nil
}

// appleInputCapTokens is a conservative pre-flight cap for the Apple
// on-device model, whose small context window is shared between prompt and
// response. Oversized inputs skip straight to the fallback ladder.
const appleInputCapTokens = 3500

// buildRequest assembles the (redacted) adapter request for a call.
func (m *manager) buildRequest(call AgentCall, auth Authorization) agent.Request {
	req := agent.Request{
		Operation:    call.Operation,
		Model:        auth.Model,
		System:       m.applyRedaction(call.System),
		Prompt:       m.applyRedaction(joinMessages(call.Messages)),
		JSONOutput:   call.OutputSchema != nil,
		OutputSchema: call.OutputSchema,
	}
	if len(call.Tools) > 0 {
		req.Tools = call.Tools
		req.MaxTurns = call.MaxTurns
		req.RunBudgetTokens = call.BudgetTokens
	}
	return req
}

// fallbackClaudeKey returns the first tier at or above key whose provider is
// Claude. Synthesis is always Claude (config-validated), so this terminates.
func (m *manager) fallbackClaudeKey(key string) string {
	order := []string{"classify", "routine", "synthesis"}
	start := 0
	switch key {
	case "routine":
		start = 1
	case "synthesis":
		start = 2
	}
	for _, k := range order[start:] {
		if m.resolveRef(k).Provider == config.ProviderClaude {
			return k
		}
	}
	return "synthesis"
}

// recordFailure writes the conservative :failed ledger row for a call that
// consumed (or may have consumed) work without a usable result.
func (m *manager) recordFailure(ctx context.Context, call AgentCall, auth Authorization, res AgentResult) {
	failed := call
	failed.Operation = call.Operation + ":failed"
	failedRes := res
	// Real accumulated usage (killed/partial agentic runs) beats the
	// conservative pre-flight estimate (FR-85).
	if failedRes.Usage.InputTokens+failedRes.Usage.OutputTokens == 0 {
		failedRes.Usage.InputTokens = auth.EstInput
	}
	if _, lerr := m.record(ctx, failed, auth, failedRes); lerr != nil {
		m.emit(events.LevelError, "token.error", call.Operation, auth,
			map[string]any{"error": "ledger write after failed call: " + lerr.Error()})
	}
}

// runLocal executes a call on a local provider: pre-flight input cap (apple),
// one attempt + one retry, output validation, then the configured fallback —
// fall forward to Claude through the normal budget path, or fail visibly
// (FR-79). Every outcome is ledgered (FR-78).
func (m *manager) runLocal(ctx context.Context, call AgentCall, auth Authorization) (AgentResult, error) {
	res := AgentResult{Auth: auth, Model: auth.Model}

	fallForward := func(cause error) (AgentResult, error) {
		m.recordFailure(ctx, call, auth, res)
		if m.cfg.Models.Fallback() == "fail" {
			m.emit(events.LevelError, "token.error", call.Operation, auth, map[string]any{"error": cause.Error()})
			return res, fmt.Errorf("local model %q failed (local_fallback: fail): %w", auth.Model, cause)
		}
		fb := call
		fb.ModelKey = m.fallbackClaudeKey(call.ModelKey)
		m.emit(events.LevelWarn, "token.local_fallback", call.Operation, auth,
			map[string]any{"error": cause.Error(), "fallback_tier": fb.ModelKey})
		return m.Run(ctx, fb) // resolves to Claude: normal budget-checked path
	}

	if auth.Provider == config.ProviderApple && auth.EstInput > appleInputCapTokens {
		return fallForward(fmt.Errorf("estimated input %d exceeds the on-device context cap %d", auth.EstInput, appleInputCapTokens))
	}
	ag, err := m.router.Resolve(auth.Provider)
	if err != nil {
		return fallForward(err)
	}

	req := m.buildRequest(call, auth)
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ { // one attempt + one retry
		resp, err := ag.Run(ctx, req)
		if err != nil {
			lastErr = err
			continue
		}
		if call.ValidateOutput != nil {
			if verr := call.ValidateOutput(resp.Text); verr != nil {
				lastErr = fmt.Errorf("output validation: %w", verr)
				continue
			}
		}
		res.Text = resp.Text
		res.Usage = resp.Usage
		if resp.Model != "" {
			res.Model = resp.Model
		}
		if res.Usage.InputTokens+res.Usage.OutputTokens == 0 {
			res.Usage.InputTokens = auth.EstInput
			res.Usage.OutputTokens = HeuristicEstimator{}.Estimate(res.Text)
		}
		ledgerID, err := m.record(ctx, call, auth, res)
		if err != nil {
			return res, err
		}
		res.LedgerID = ledgerID
		return res, nil
	}
	return fallForward(lastErr)
}

// record writes the ledger row, updates day/week budgets and emits the event.
// It detaches from the caller's context: the tokens are already spent, and a
// run context that was cancelled mid-call (the most likely failure) must not
// also cancel the accounting for that spend.
func (m *manager) record(ctx context.Context, call AgentCall, auth Authorization, res AgentResult) (int64, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	ts := m.now().UTC()
	total := int64(res.Usage.InputTokens + res.Usage.OutputTokens)

	var cost *float64
	if m.cfg.AuthMode == "api_key" {
		if c, ok := m.cost(res.Model, res.Usage); ok {
			cost = &c
		}
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Ledger rows name the provider so local traffic is distinguishable
	// (FR-77): "ollama:<model>" / "apple-foundation-v1" / bare Claude string.
	ledgerModel := res.Model
	if auth.Provider == config.ProviderOllama {
		ledgerModel = config.ProviderOllama + ":" + res.Model
	}
	ledgerID, err := db.InsertLedger(ctx, tx, db.LedgerRow{
		TS: ts.Format(time.RFC3339), Profile: m.cfg.Profile, Operation: call.Operation,
		Model: ledgerModel, InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens,
		CacheRead: res.Usage.CacheRead, CacheWrite: res.Usage.CacheWrite,
		EstInput: auth.EstInput, CostUSD: cost, RunID: call.RunID,
	})
	if err != nil {
		return 0, err
	}
	costVal := 0.0
	if cost != nil {
		costVal = *cost
	}
	// Local calls are budget-exempt (FR-78): ledgered above, but they never
	// consume the day/week windows that protect the Claude quota.
	if auth.Provider == "" || auth.Provider == config.ProviderClaude {
		if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "day", dayPeriod(ts), total, costVal); err != nil {
			return 0, err
		}
		if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "week", weekPeriod(ts), total, costVal); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	committed = true

	m.emit(events.LevelInfo, "token.ledger", call.Operation, auth, map[string]any{
		"model": res.Model, "input": res.Usage.InputTokens, "output": res.Usage.OutputTokens,
		"ledger_id": ledgerID, "cost_usd": cost,
	})
	return ledgerID, nil
}

// cost computes cost_usd from the price table (api_key mode only).
func (m *manager) cost(model string, u agent.Usage) (float64, bool) {
	p, ok := m.cfg.Prices[model]
	if !ok {
		return 0, false
	}
	c := float64(u.InputTokens)*p.Input + float64(u.OutputTokens)*p.Output + float64(u.CacheRead)*p.CacheRead
	return c, true
}

// BuildContext assembles a token-bounded context from hybrid retrieval (FR-46):
// never dump the vault, always retrieve top-k and pack to max_context_tokens.
func (m *manager) BuildContext(ctx context.Context, query string, opts RetrieveOpts) (Context, error) {
	if m.searcher == nil {
		return Context{}, fmt.Errorf("BuildContext: no searcher configured")
	}
	if opts.TopK <= 0 {
		opts.TopK = 8
	}
	if opts.MaxContextTokens <= 0 {
		opts.MaxContextTokens = 12000
	}
	r, err := m.searcher.Retrieve(ctx, query, opts.TopK, opts.MaxContextTokens)
	if err != nil {
		return Context{}, err
	}
	msgs := []Message{{Role: "user", Content: r.Context}}
	return Context{Messages: msgs, Tokens: m.estimator.Estimate(r.Context), Sources: r.Sources}, nil
}

// Status reports remaining day/week budget and guard state.
func (m *manager) Status(ctx context.Context, profile string) (BudgetStatus, error) {
	day, week, err := m.windows(ctx)
	if err != nil {
		return BudgetStatus{}, err
	}
	guard := m.cfg.Limits.GuardPauseAtPct
	st := BudgetStatus{
		Profile:  m.cfg.Profile,
		Day:      day,
		Week:     week,
		GuardPct: guard,
	}
	if guard > 0 && (day.Pct >= float64(guard) || week.Pct >= float64(guard)) {
		st.GuardPaused = true
		st.GuardReason = guardReason(guard, day, week)
	}
	// The dollar cap participates in the guard too (api_key mode, FR-42).
	if guard > 0 && day.CostCap > 0 && day.CostPct >= float64(guard) && !st.GuardPaused {
		st.GuardPaused = true
		st.GuardReason = fmt.Sprintf("budget guard active — daily cost $%.2f is %.0f%% of the $%.2f cap (≥ %d%% pause threshold); non-essential automations pause until the window rolls over",
			day.CostUsed, day.CostPct, day.CostCap, guard)
	}
	return st, nil
}

// guardReason builds the human-readable explanation for a paused guard, naming
// the window(s) that crossed the threshold so the message is actionable.
func guardReason(guard int, day, week Window) string {
	which := "daily"
	pct := day.Pct
	if week.Pct >= float64(guard) && (day.Pct < float64(guard) || week.Pct >= day.Pct) {
		which = "weekly"
		pct = week.Pct
	}
	return fmt.Sprintf("budget guard active — %s usage %.0f%% ≥ %d%% pause threshold; non-essential automations pause until the window rolls over",
		which, pct, guard)
}

// windows reads the current day/week usage and computes percentages.
func (m *manager) windows(ctx context.Context) (Window, Window, error) {
	ts := m.now().UTC()
	dayBW, err := db.GetBudgetWindow(ctx, m.db, m.cfg.Profile, "day", dayPeriod(ts))
	if err != nil {
		return Window{}, Window{}, err
	}
	weekBW, err := db.GetBudgetWindow(ctx, m.db, m.cfg.Profile, "week", weekPeriod(ts))
	if err != nil {
		return Window{}, Window{}, err
	}
	day := makeWindow(dayBW.TokensUsed, m.cfg.Limits.DailyTokens.Int())
	week := makeWindow(weekBW.TokensUsed, m.cfg.Limits.WeeklyTokens.Int())
	if m.cfg.AuthMode == "api_key" {
		day.CostUsed = dayBW.CostUsed
		week.CostUsed = weekBW.CostUsed
		if cap := m.cfg.Limits.DailyCostUSD; cap > 0 {
			day.CostCap = cap
			day.CostPct = day.CostUsed / cap * 100
		}
	}
	return day, week, nil
}

func makeWindow(used, limit int64) Window {
	w := Window{Used: used, Limit: limit}
	if limit > 0 {
		w.Pct = float64(used) / float64(limit) * 100
	}
	return w
}

func (m *manager) emit(level events.Level, kind, op string, auth Authorization, extra map[string]any) {
	if m.bus == nil {
		return
	}
	data := map[string]any{
		"profile":   m.cfg.Profile,
		"operation": op,
		"decision":  string(auth.Decision),
		"est_input": auth.EstInput,
		"reason":    auth.Reason,
	}
	for k, v := range extra {
		data[k] = v
	}
	m.bus.Publish(events.Event{Level: level, Kind: kind, Message: op + ": " + auth.Reason, Data: data})
}

func joinMessages(msgs []Message) string {
	parts := make([]string, len(msgs))
	for i, msg := range msgs {
		parts[i] = msg.Content
	}
	return strings.Join(parts, "\n\n")
}
