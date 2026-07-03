package tokens

import (
	"context"
	"database/sql"
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
