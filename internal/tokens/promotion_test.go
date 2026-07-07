package tokens

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

func seedEvalRun(t *testing.T, m *manager, family, ref string, pct int) {
	t.Helper()
	if _, err := m.db.Exec(
		`INSERT INTO eval_runs (family, model_ref, digest, passed, total, pass_pct, ran_at)
		 VALUES (?, ?, '', ?, 100, ?, '2026-07-07T00:00:00Z');`,
		family, ref, pct, pct); err != nil {
		t.Fatal(err)
	}
}

func gateConfig() Config {
	c := localTestConfig()
	c.Models.Classify = "ollama:qwen"
	c.EvalMinPass = 80
	return c
}

func TestGateAdmitsVettedLocal(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	m := testManagerRouter(t, gateConfig(), agent.Router{Claude: claude, Ollama: local})
	seedEvalRun(t, m, "classify", m.resolveModel("classify"), 90)

	res, err := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from-local" {
		t.Fatalf("vetted local should serve, got %q", res.Text)
	}
}

func TestGateRetargetsUnvetted(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	m := testManagerRouter(t, gateConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from-claude" {
		t.Fatalf("unvetted local must retarget to Claude, got %q", res.Text)
	}
	if local.CallCount() != 0 {
		t.Fatalf("local must not be called when unvetted, got %d", local.CallCount())
	}
}

func TestGateBelowThresholdRetargets(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := agent.NewFake()
	local.Reply = "from-local"
	m := testManagerRouter(t, gateConfig(), agent.Router{Claude: claude, Ollama: local})
	seedEvalRun(t, m, "classify", m.resolveModel("classify"), 60)

	res, _ := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if res.Text != "from-claude" {
		t.Fatalf("below-threshold must retarget, got %q", res.Text)
	}
}

func TestGateOffBypasses(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	c := localTestConfig()
	c.Models.Classify = "ollama:qwen" // EvalMinPass stays 0
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})
	res, _ := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if res.Text != "from-local" {
		t.Fatalf("gate off (min_pass 0) must route local, got %q", res.Text)
	}
}

func TestPromotionGateOffBypasses(t *testing.T) {
	ctx := context.Background()
	local := agent.NewFake()
	local.Reply = "from-local"
	claude := agent.NewFake()
	c := gateConfig()
	c.PromotionGateOff = true
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})
	res, _ := m.Run(ctx, AgentCall{Operation: "t", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "hi"}}})
	if res.Text != "from-local" {
		t.Fatalf("PromotionGateOff must route local even unvetted, got %q", res.Text)
	}
}
