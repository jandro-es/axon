package tokens

import (
	"context"
	"database/sql"
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
}

// Authorization is the pre-flight result.
type Authorization struct {
	Decision Decision
	Model    string // resolved concrete model to use
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
	agent     agent.Agent      // may be nil for read-only use (Status/Authorize)
	searcher  *search.Searcher // may be nil if BuildContext is unused
	bus       *events.Bus
	estimator Estimator
	cfg       Config
	redact    []*regexp.Regexp
	now       func() time.Time
}

// New builds a Manager. agent and searcher may be nil for read-only callers;
// Run requires a non-nil agent and BuildContext a non-nil searcher.
func New(database *sql.DB, ag agent.Agent, searcher *search.Searcher, bus *events.Bus, cfg Config) Manager {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &manager{
		db:        database,
		agent:     ag,
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
// model. In api_key mode it uses the exact count_tokens endpoint (falling back
// to the heuristic on error); otherwise it uses the local heuristic (FR-40).
func (m *manager) estimateInput(ctx context.Context, model string, call AgentCall) int {
	if m.cfg.AuthMode == "api_key" {
		if c, ok := m.agent.(tokenCounter); ok {
			if n, err := c.CountTokens(ctx, model, call.System, joinMessages(call.Messages)); err == nil {
				return n
			}
		}
	}
	return m.estimateCall(call)
}

// Authorize runs the pre-flight: estimate, resolve model, check per-call and
// day/week windows, and decide proceed/downgrade/defer/deny (docs/07 §2).
func (m *manager) Authorize(ctx context.Context, call AgentCall) (Authorization, error) {
	model := m.resolveModel(call.ModelKey)
	est := m.estimateInput(ctx, model, call)
	auth := Authorization{Decision: DecisionProceed, Model: model, EstInput: est}

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
	// Try to downgrade to a cheaper model tier to conserve the plan/limits.
	if dk := downgradeKey(call.ModelKey); dk != "" {
		auth.Decision = DecisionDowngrade
		auth.Model = m.resolveModel(dk)
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

	if m.agent == nil {
		return res, fmt.Errorf("token manager: no agent adapter configured")
	}

	// Redaction is enforced HERE, at the chokepoint, so no caller (automations
	// building prompts from raw vault notes included) can forget it.
	resp, err := m.agent.Run(ctx, agent.Request{
		Operation: call.Operation,
		Model:     auth.Model,
		System:    m.applyRedaction(call.System),
		Prompt:    m.applyRedaction(joinMessages(call.Messages)),
	})
	if err != nil {
		// A failed call may still have consumed real tokens upstream — an
		// adapter timeout that killed `claude -p` mid-generation, or garbled
		// stdout after a completed run. Record a conservative ledger row from
		// the pre-flight estimate so quota is never burned invisibly and the
		// budget guard can still trip (cardinal rule 1: ledger on EVERY path).
		failed := call
		failed.Operation = call.Operation + ":failed"
		failedRes := res
		failedRes.Usage.InputTokens = auth.EstInput
		if _, lerr := m.record(ctx, failed, auth, failedRes); lerr != nil {
			m.emit(events.LevelError, "token.error", call.Operation, auth,
				map[string]any{"error": "ledger write after failed call: " + lerr.Error()})
		}
		m.emit(events.LevelError, "token.error", call.Operation, auth, map[string]any{"error": err.Error()})
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

	// Post-hoc accounting: ledger + budget windows + event (FR-41, S4).
	ledgerID, err := m.record(ctx, call, auth, res)
	if err != nil {
		return res, err
	}
	res.LedgerID = ledgerID
	return res, nil
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

	ledgerID, err := db.InsertLedger(ctx, tx, db.LedgerRow{
		TS: ts.Format(time.RFC3339), Profile: m.cfg.Profile, Operation: call.Operation,
		Model: res.Model, InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens,
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
	if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "day", dayPeriod(ts), total, costVal); err != nil {
		return 0, err
	}
	if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "week", weekPeriod(ts), total, costVal); err != nil {
		return 0, err
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
