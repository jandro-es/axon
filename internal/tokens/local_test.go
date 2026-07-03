package tokens

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

// testManagerRouter builds a manager with a full router + config, on a fresh
// in-memory DB (parallel to testManager, which wires a single Claude agent).
func testManagerRouter(t *testing.T, cfg Config, router agent.Router) *manager {
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
	cfg.Now = func() time.Time { return fixed }
	return NewWithRouter(d, router, nil, nil, cfg).(*manager)
}

func localTestConfig() Config {
	return Config{
		Profile:  "test",
		AuthMode: "subscription",
		Models: config.ModelsConfig{
			Classify:  "ollama:qwen3:8b",
			Routine:   "claude-sonnet-4-6",
			Synthesis: "claude-opus-4-8",
		},
		Limits: config.LimitsConfig{DailyTokens: 100, WeeklyTokens: 100},
	}
}

// ledgerRows reads (operation, model) pairs straight off token_ledger.
func ledgerRows(t *testing.T, d *sql.DB) [][2]string {
	t.Helper()
	rows, err := d.Query(`SELECT operation, model FROM token_ledger ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var op, model string
		if err := rows.Scan(&op, &model); err != nil {
			t.Fatal(err)
		}
		out = append(out, [2]string{op, model})
	}
	return out
}

func TestLocalCallBudgetExempt(t *testing.T) {
	ctx := context.Background()
	fake := agent.NewFake()
	fake.Reply = "02-Areas"
	m := testManagerRouter(t, localTestConfig(), agent.Router{Claude: agent.NewFake(), Ollama: fake})

	// A prompt far larger than the 100-token day window: a Claude call would
	// be denied/deferred; a local call must proceed (FR-78).
	big := strings.Repeat("word ", 2000)
	res, err := m.Run(ctx, AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: big}},
	})
	if err != nil {
		t.Fatalf("local call: %v", err)
	}
	if res.Auth.Decision != DecisionProceed {
		t.Fatalf("decision = %s, want proceed", res.Auth.Decision)
	}
	if res.Auth.Provider != config.ProviderOllama {
		t.Fatalf("provider = %s, want ollama", res.Auth.Provider)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("ollama adapter calls = %d, want 1", fake.CallCount())
	}

	// Ledgered with the provider-identifying model string…
	rows := ledgerRows(t, m.db)
	if len(rows) != 1 || rows[0][1] != "ollama:qwen3:8b" {
		t.Fatalf("ledger rows = %v, want one row with model ollama:qwen3:8b", rows)
	}
	// …but the budget windows untouched.
	st, err := m.Status(ctx, "test")
	if err != nil {
		t.Fatal(err)
	}
	if st.Day.Used != 0 || st.Week.Used != 0 {
		t.Fatalf("windows used = %d/%d, want 0/0 (budget-exempt)", st.Day.Used, st.Week.Used)
	}
}

func TestDowngradeSkipsLocalTiers(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	m := testManagerRouter(t, localTestConfig(), agent.Router{Claude: claude, Ollama: agent.NewFake()})

	// Exhaust the day window so a routine (claude) call is over budget.
	seedBudget(t, m, 100)

	auth, err := m.Authorize(ctx, AgentCall{
		Operation: "test.routine", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// classify is local, so no Claude tier below routine exists → defer/deny,
	// never a downgrade into a local tier.
	if auth.Decision == DecisionDowngrade {
		t.Fatalf("downgraded into a local tier: %+v", auth)
	}
	if auth.Decision != DecisionDeny {
		t.Fatalf("decision = %s, want deny (window exhausted, no claude tier below)", auth.Decision)
	}
}

func TestLocalFallForwardToClaude(t *testing.T) {
	ctx := context.Background()
	broken := agent.NewFake()
	broken.Err = errors.New("connection refused")
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	m := testManagerRouter(t, localTestConfig(), agent.Router{Claude: claude, Ollama: broken})

	res, err := m.Run(ctx, AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("fall-forward should succeed: %v", err)
	}
	if res.Text != "from-claude" {
		t.Fatalf("Text = %q, want from-claude", res.Text)
	}
	if broken.CallCount() != 2 { // one attempt + one retry (FR-79)
		t.Fatalf("local attempts = %d, want 2", broken.CallCount())
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude calls = %d, want 1", claude.CallCount())
	}
	// The Claude fallback consumed budget as a normal call.
	st, _ := m.Status(ctx, "test")
	if st.Day.Used == 0 {
		t.Fatal("claude fallback should consume the day window")
	}
}

func TestLocalFailModeSurfacesError(t *testing.T) {
	ctx := context.Background()
	cfg := localTestConfig()
	cfg.Models.LocalFallback = "fail"
	broken := agent.NewFake()
	broken.Err = errors.New("connection refused")
	claude := agent.NewFake()
	m := testManagerRouter(t, cfg, agent.Router{Claude: claude, Ollama: broken})

	_, err := m.Run(ctx, AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("fail mode must surface the error")
	}
	if claude.CallCount() != 0 {
		t.Fatalf("claude calls = %d, want 0 in fail mode", claude.CallCount())
	}
	// A :failed ledger row exists (standard failure accounting).
	rows := ledgerRows(t, m.db)
	if len(rows) == 0 || !strings.HasSuffix(rows[0][0], ":failed") {
		t.Fatalf("rows = %v, want a :failed row", rows)
	}
}

func TestLocalValidateOutputRetriesThenFallsForward(t *testing.T) {
	ctx := context.Background()
	junk := agent.NewFake()
	junk.Reply = "not json"
	claude := agent.NewFake()
	claude.Reply = `{"ok":true}`
	m := testManagerRouter(t, localTestConfig(), agent.Router{Claude: claude, Ollama: junk})

	res, err := m.Run(ctx, AgentCall{
		Operation: "test.local", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
		ValidateOutput: func(s string) error {
			if !strings.HasPrefix(strings.TrimSpace(s), "{") {
				return errors.New("not a JSON object")
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if junk.CallCount() != 2 || claude.CallCount() != 1 {
		t.Fatalf("calls local=%d claude=%d, want 2/1", junk.CallCount(), claude.CallCount())
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("Text = %q", res.Text)
	}
}

func TestClaudeValidateOutputFailsCall(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "garbage"
	cfg := localTestConfig()
	cfg.Models.Classify = "claude-haiku-4-5" // all claude
	m := testManagerRouter(t, cfg, agent.Router{Claude: claude})

	_, err := m.Run(ctx, AgentCall{
		Operation: "test.claude", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hello"}},
		ValidateOutput: func(s string) error {
			return errors.New("bad output")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "output validation") {
		t.Fatalf("err = %v, want output-validation failure", err)
	}
	rows := ledgerRows(t, m.db)
	if len(rows) != 1 || !strings.HasSuffix(rows[0][0], ":failed") {
		t.Fatalf("rows = %v, want one :failed row", rows)
	}
}

func TestAppleInputCapShortCircuits(t *testing.T) {
	ctx := context.Background()
	cfg := localTestConfig()
	cfg.Models.Classify = "apple"
	// Generous windows: this test exercises the apple input cap, and the
	// Claude fallback for the oversized prompt must not itself be deferred.
	cfg.Limits = config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 1_000_000}
	apple := agent.NewFake()
	claude := agent.NewFake()
	claude.Reply = "ok"
	m := testManagerRouter(t, cfg, agent.Router{Claude: claude, Apple: apple})

	big := strings.Repeat("word ", 8000) // ≫ appleInputCapTokens at ~4 chars/token
	_, err := m.Run(ctx, AgentCall{
		Operation: "test.apple", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: big}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if apple.CallCount() != 0 {
		t.Fatalf("apple calls = %d, want 0 (input cap short-circuit)", apple.CallCount())
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude calls = %d, want 1 (fallback)", claude.CallCount())
	}
}

func seedBudget(t *testing.T, m *manager, used int64) {
	t.Helper()
	ctx := context.Background()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	ts := m.now().UTC()
	if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "day", dayPeriod(ts), used, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.AddBudgetUsage(ctx, tx, m.cfg.Profile, "week", weekPeriod(ts), used, 0); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
