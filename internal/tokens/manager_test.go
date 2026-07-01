package tokens

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func testManager(t *testing.T, limits config.LimitsConfig, ag agent.Agent) *manager {
	t.Helper()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	return &manager{
		db:        d,
		agent:     ag,
		estimator: newCachingEstimator(HeuristicEstimator{}),
		cfg: Config{
			Profile:  "test",
			AuthMode: "subscription",
			Models:   config.ModelsConfig{Classify: "haiku", Routine: "sonnet", Synthesis: "opus"},
			Limits:   limits,
		},
		now: func() time.Time { return fixed },
	}
}

func generousLimits() config.LimitsConfig {
	return config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80}
}

func TestRunLedgersEveryCall(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "done", Model: r.Model, Usage: agent.Usage{InputTokens: 100, OutputTokens: 50}}, nil
	}
	m := testManager(t, generousLimits(), fake)

	res, err := m.Run(ctx, AgentCall{Operation: "ingest.enrich", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "summarise this please"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Auth.Decision != DecisionProceed {
		t.Errorf("decision = %q, want proceed", res.Auth.Decision)
	}
	if res.LedgerID == 0 {
		t.Error("expected a ledger id")
	}
	if res.Model != "sonnet" {
		t.Errorf("model = %q, want sonnet (routine)", res.Model)
	}

	// S4: the call is in the ledger with model/operation/counts.
	n, _ := db.CountLedger(ctx, m.db)
	if n != 1 {
		t.Fatalf("ledger rows = %d, want 1", n)
	}
	// And the day/week budgets advanced by input+output tokens.
	st, _ := m.Status(ctx, "test")
	if st.Day.Used != 150 || st.Week.Used != 150 {
		t.Errorf("budgets = day %d week %d, want 150/150", st.Day.Used, st.Week.Used)
	}
}

// TestRunLedgersFailedCalls: an adapter error (timeout killing claude -p
// mid-generation, unparseable output after a completed run) may still have
// burned real quota — the spend must land in the ledger and budget windows,
// or the guard can never trip on it (cardinal rule 1: ledger on every path).
func TestRunLedgersFailedCalls(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.Err = errors.New("claude -p: parse output: unexpected end of JSON input")
	m := testManager(t, generousLimits(), fake)

	_, err := m.Run(ctx, AgentCall{Operation: "automation.daily-log", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "summarise today"}}})
	if err == nil {
		t.Fatal("expected the failed call to return an error")
	}

	n, _ := db.CountLedger(ctx, m.db)
	if n != 1 {
		t.Fatalf("ledger rows after failed call = %d, want 1", n)
	}
	var op string
	var input int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT operation, input_tokens FROM token_ledger LIMIT 1;`).Scan(&op, &input); err != nil {
		t.Fatal(err)
	}
	if op != "automation.daily-log:failed" {
		t.Errorf("operation = %q, want the :failed marker", op)
	}
	if input <= 0 {
		t.Errorf("input_tokens = %d, want the pre-flight estimate (> 0)", input)
	}
	st, _ := m.Status(ctx, "test")
	if st.Day.Used <= 0 {
		t.Errorf("day budget used = %d after failed call, want > 0", st.Day.Used)
	}
}

func TestRunNoUsageFallsBackToEstimate(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	// Report zero usage (headless path that returns nothing).
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "some generated output text", Model: r.Model}, nil
	}
	m := testManager(t, generousLimits(), fake)

	res, err := m.Run(ctx, AgentCall{Operation: "x", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "a reasonably sized prompt here"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens == 0 || res.Usage.OutputTokens == 0 {
		t.Errorf("expected estimate fallback to populate usage, got %+v", res.Usage)
	}
}

// TestRunRedactsAtChokepoint: redaction is enforced inside Run itself, so no
// caller (automations sending raw vault note content included) can forget it.
func TestRunRedactsAtChokepoint(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	m := testManager(t, generousLimits(), fake)
	m.cfg.RedactionRules = []string{`sk-[A-Za-z0-9]{8}`}
	m.redact = compileRedaction(m.cfg.RedactionRules)

	_, err := m.Run(ctx, AgentCall{Operation: "automation.compact", ModelKey: "routine",
		System:   "system context holding sk-SECRET99",
		Messages: []Message{{Role: "user", Content: "note body with an sk-ABCD1234 credential"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("agent calls = %d, want 1", len(fake.Calls))
	}
	req := fake.Calls[0]
	if strings.Contains(req.Prompt, "sk-ABCD1234") {
		t.Error("secret reached the adapter in the prompt")
	}
	if strings.Contains(req.System, "sk-SECRET99") {
		t.Error("secret reached the adapter in the system prompt")
	}
	if !strings.Contains(req.Prompt, "[REDACTED]") || !strings.Contains(req.System, "[REDACTED]") {
		t.Errorf("expected [REDACTED] placeholders; prompt=%q system=%q", req.Prompt, req.System)
	}
}

// TestConcurrentRunsAccountEveryToken: the chokepoint must not lose or double
// count spend under concurrency (SetMaxOpenConns(1) + per-call transactions
// serialize the read-modify-write on budget_state — this proves it).
func TestConcurrentRunsAccountEveryToken(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "ok", Model: r.Model, Usage: agent.Usage{InputTokens: 10, OutputTokens: 5}}, nil
	}
	m := testManager(t, generousLimits(), fake)

	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Run(ctx, AgentCall{Operation: "concurrent.op", ModelKey: "routine",
				Messages: []Message{{Role: "user", Content: "work item"}}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	if n, _ := db.CountLedger(ctx, m.db); n != workers {
		t.Errorf("ledger rows = %d, want %d", n, workers)
	}
	st, _ := m.Status(ctx, "test")
	want := int64(workers * 15)
	if st.Day.Used != want || st.Week.Used != want {
		t.Errorf("budgets = day %d week %d, want %d/%d (no lost updates)", st.Day.Used, st.Week.Used, want, want)
	}
}

func TestAuthorizeDowngradeOverBudget(t *testing.T) {
	ctx := context.Background()
	// Day limit tiny so any call is "over"; week generous.
	limits := config.LimitsConfig{DailyTokens: 1, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80}
	m := testManager(t, limits, agent.NewFake())

	auth, err := m.Authorize(ctx, AgentCall{Operation: "synth", ModelKey: "synthesis",
		Messages: []Message{{Role: "user", Content: "a long synthesis prompt that exceeds the daily cap"}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Decision != DecisionDowngrade {
		t.Fatalf("decision = %q, want downgrade", auth.Decision)
	}
	if auth.Model != "sonnet" { // synthesis(opus) -> routine(sonnet)
		t.Errorf("downgraded model = %q, want sonnet", auth.Model)
	}
}

func TestAuthorizeDenyWhenExhaustedAndCheapest(t *testing.T) {
	ctx := context.Background()
	limits := config.LimitsConfig{DailyTokens: 10, WeeklyTokens: 5_000_000, GuardPauseAtPct: 80}
	m := testManager(t, limits, agent.NewFake())

	// Pre-spend the day window past its limit.
	if err := db.AddBudgetUsage(ctx, m.db, "test", "day", dayPeriod(m.now()), 10, 0); err != nil {
		t.Fatal(err)
	}
	// Cheapest tier (classify) cannot downgrade further -> deny.
	auth, err := m.Authorize(ctx, AgentCall{Operation: "c", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "anything"}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Decision != DecisionDeny {
		t.Errorf("decision = %q, want deny", auth.Decision)
	}
}

func TestRunDeniedDoesNotExecuteOrLedger(t *testing.T) {
	ctx := context.Background()
	limits := config.LimitsConfig{DailyTokens: 5, WeeklyTokens: 5_000_000}
	fake := agent.NewFake()
	m := testManager(t, limits, fake)
	_ = db.AddBudgetUsage(ctx, m.db, "test", "day", dayPeriod(m.now()), 5, 0)

	_, err := m.Run(ctx, AgentCall{Operation: "c", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "x"}}})
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("err = %v, want ErrDenied", err)
	}
	if fake.CallCount() != 0 {
		t.Error("denied call still executed the agent")
	}
	if n, _ := db.CountLedger(ctx, m.db); n != 0 {
		t.Errorf("denied call wrote %d ledger rows, want 0", n)
	}
}

func TestPerCallBudgetDefers(t *testing.T) {
	ctx := context.Background()
	m := testManager(t, generousLimits(), agent.NewFake())
	// A large prompt with a tiny per-call budget -> defer (shrink context).
	auth, err := m.Authorize(ctx, AgentCall{Operation: "big", ModelKey: "routine",
		BudgetTokens: 2,
		Messages:     []Message{{Role: "user", Content: strings.Repeat("word ", 200)}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Decision != DecisionDefer {
		t.Errorf("decision = %q, want defer", auth.Decision)
	}
}

func TestEssentialBypassesBudget(t *testing.T) {
	ctx := context.Background()
	limits := config.LimitsConfig{DailyTokens: 1, WeeklyTokens: 1}
	fake := agent.NewFake()
	m := testManager(t, limits, fake)

	res, err := m.Run(ctx, AgentCall{Operation: "heartbeat", ModelKey: "classify", Essential: true,
		Messages: []Message{{Role: "user", Content: "status check"}}})
	if err != nil {
		t.Fatalf("essential call should proceed: %v", err)
	}
	if res.Auth.Decision != DecisionProceed {
		t.Errorf("essential decision = %q, want proceed", res.Auth.Decision)
	}
	if fake.CallCount() != 1 {
		t.Error("essential call did not execute")
	}
}

// TestDailyCostCapEnforced (FR-42): in api_key mode the daily_cost_usd cap is
// a hard pre-flight gate, with the same downgrade/deny ladder as tokens.
func TestDailyCostCapEnforced(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.Mode = "api_key"
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "ok", Model: r.Model, Usage: agent.Usage{InputTokens: 1000, OutputTokens: 100}}, nil
	}
	m := testManager(t, generousLimits(), fake)
	m.cfg.AuthMode = "api_key"
	m.cfg.Limits.DailyCostUSD = 0.10
	m.cfg.Prices = map[string]config.Price{
		"opus":   {Input: 0.0001, Output: 0.0005}, // synthesis: pricey
		"sonnet": {Input: 0.00001, Output: 0.00005},
		"haiku":  {Input: 0.000001, Output: 0.000005},
	}

	// Burn most of the cap: 1000 in * 0.0001 + 100 out * 0.0005 = $0.15 > cap.
	if _, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "synthesis",
		Messages: []Message{{Role: "user", Content: "expensive work"}}}); err != nil {
		t.Fatal(err)
	}
	st, err := m.Status(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if st.Day.CostUsed <= 0 || st.Day.CostCap != 0.10 {
		t.Fatalf("status cost = used %.4f cap %.2f, want used > 0 / cap 0.10", st.Day.CostUsed, st.Day.CostCap)
	}

	// Next synthesis call is over the cap → downgraded (cheaper tier exists).
	auth, err := m.Authorize(ctx, AgentCall{Operation: "op", ModelKey: "synthesis",
		Messages: []Message{{Role: "user", Content: "more work"}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Decision != DecisionDowngrade {
		t.Errorf("decision = %q (%s), want downgrade over the cost cap", auth.Decision, auth.Reason)
	}

	// At the cheapest tier with the cap exhausted → deny; nothing executes.
	auth, err = m.Authorize(ctx, AgentCall{Operation: "op", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "cheap work"}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Decision != DecisionDeny {
		t.Errorf("decision = %q (%s), want deny with the cap exhausted", auth.Decision, auth.Reason)
	}

	// Essential still proceeds (surfaced), mirroring the token windows.
	auth, err = m.Authorize(ctx, AgentCall{Operation: "op", ModelKey: "classify", Essential: true,
		Messages: []Message{{Role: "user", Content: "essential"}}})
	if err != nil || auth.Decision != DecisionProceed {
		t.Errorf("essential decision = %q, %v — want proceed", auth.Decision, err)
	}
}

// TestCostCapIgnoredOutsideAPIKeyMode: subscription mode never gates on cost.
func TestCostCapIgnoredOutsideAPIKeyMode(t *testing.T) {
	ctx := context.Background()
	m := testManager(t, generousLimits(), agent.NewFake())
	m.cfg.Limits.DailyCostUSD = 0.000001 // absurdly low; must not matter
	auth, err := m.Authorize(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "hello"}}})
	if err != nil || auth.Decision != DecisionProceed {
		t.Errorf("subscription-mode decision = %q, %v — want proceed (cost cap is api_key only)", auth.Decision, err)
	}
}

func TestStatusGuardPause(t *testing.T) {
	ctx := context.Background()
	limits := config.LimitsConfig{DailyTokens: 100, WeeklyTokens: 1000, GuardPauseAtPct: 80}
	m := testManager(t, limits, agent.NewFake())
	_ = db.AddBudgetUsage(ctx, m.db, "test", "day", dayPeriod(m.now()), 85, 0)

	st, err := m.Status(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !st.GuardPaused {
		t.Errorf("expected guard paused at 85%% of 100 (threshold 80%%); status=%+v", st)
	}
	if st.Day.Pct != 85 {
		t.Errorf("day pct = %.1f, want 85", st.Day.Pct)
	}
}

func TestCostOnlyInAPIKeyMode(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "x", Model: "sonnet", Usage: agent.Usage{InputTokens: 1000, OutputTokens: 500}}, nil
	}
	m := testManager(t, generousLimits(), fake)
	m.cfg.AuthMode = "api_key"
	m.cfg.Prices = map[string]config.Price{"sonnet": {Input: 0.000003, Output: 0.000015}}

	if _, err := m.Run(ctx, AgentCall{Operation: "x", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	var cost *float64
	row := m.db.QueryRowContext(ctx, "SELECT cost_usd FROM token_ledger LIMIT 1;")
	if err := row.Scan(&cost); err != nil {
		t.Fatal(err)
	}
	if cost == nil {
		t.Fatal("expected cost_usd populated in api_key mode")
	}
	want := 1000*0.000003 + 500*0.000015
	if *cost < want-1e-9 || *cost > want+1e-9 {
		t.Errorf("cost = %v, want %v", *cost, want)
	}
}
