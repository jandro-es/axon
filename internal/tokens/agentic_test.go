package tokens

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
)

func agenticTestConfig() Config {
	cfg := localTestConfig()
	cfg.Limits = config.LimitsConfig{DailyTokens: 1_000_000, WeeklyTokens: 5_000_000}
	return cfg
}

func agenticCall() AgentCall {
	return AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages:     []Message{{Role: "user", Content: "survey the sources"}},
		Tools:        []string{"vault_search", "vault_read"},
		MaxTurns:     6,
		BudgetTokens: 50_000,
	}
}

func TestAgenticPassesToolsAndRunBudget(t *testing.T) {
	fake := agent.NewFake()
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		if len(r.Tools) != 2 || r.MaxTurns != 6 || r.RunBudgetTokens != 50_000 {
			t.Errorf("request = %+v, want tools/turns/budget threaded", r)
		}
		return &agent.Response{Text: "done", Turns: 3,
			Usage: agent.Usage{InputTokens: 1000, OutputTokens: 200}}, nil
	}
	m := testManagerRouter(t, agenticTestConfig(), agent.Router{Claude: fake})
	res, err := m.Run(context.Background(), agenticCall())
	if err != nil {
		t.Fatal(err)
	}
	if res.Usage.InputTokens != 1000 {
		t.Fatalf("usage = %+v", res.Usage)
	}
}

func TestAgenticRequiresClaudeProvider(t *testing.T) {
	cfg := agenticTestConfig() // classify = ollama:qwen3:8b
	m := testManagerRouter(t, cfg, agent.Router{Claude: agent.NewFake(), Ollama: agent.NewFake()})
	call := agenticCall()
	call.ModelKey = "classify"
	_, err := m.Run(context.Background(), call)
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("err = %v, want agentic-requires-claude error", err)
	}
}

func TestAgenticKillLedgersRealUsage(t *testing.T) {
	fake := agent.NewFake()
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Usage: agent.Usage{InputTokens: 40_000, OutputTokens: 12_000}},
			agent.ErrRunBudgetExceeded
	}
	m := testManagerRouter(t, agenticTestConfig(), agent.Router{Claude: fake})
	_, err := m.Run(context.Background(), agenticCall())
	if !errors.Is(err, agent.ErrRunBudgetExceeded) {
		t.Fatalf("err = %v, want kill error surfaced", err)
	}
	rows := ledgerRows(t, m.db)
	if len(rows) != 1 || !strings.HasSuffix(rows[0][0], ":failed") {
		t.Fatalf("rows = %v, want one :failed row", rows)
	}
	// Real accumulated usage, not the tiny pre-flight estimate.
	var in, out int
	if err := m.db.QueryRow(`SELECT input_tokens, output_tokens FROM token_ledger`).Scan(&in, &out); err != nil {
		t.Fatal(err)
	}
	if in != 40_000 || out != 12_000 {
		t.Fatalf("ledgered %d/%d, want real 40000/12000 (FR-85)", in, out)
	}
	// And the budget windows advanced by the real spend (claude provider).
	st, _ := m.Status(context.Background(), "test")
	if st.Day.Used != 52_000 {
		t.Fatalf("day used = %d, want 52000", st.Day.Used)
	}
}
