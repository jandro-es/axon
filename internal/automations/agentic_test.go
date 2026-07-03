package automations

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/tokens"
)

func TestRunModelInjectsConfiguredBudget(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.BudgetTokens = 77_000
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "ok", Usage: agent.Usage{InputTokens: 10, OutputTokens: 5}}, nil
	}
	// One-shot: the config budget becomes the pre-flight input cap on the
	// AgentCall; it is not threaded into the Request (no RunBudgetTokens).
	_, _, _, err := runModel(context.Background(), rc, tokens.AgentCall{
		Operation: "t", ModelKey: "routine",
		Messages: []tokens.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.RunBudgetTokens != 0 {
		t.Fatalf("one-shot request carries RunBudgetTokens %d, want 0", got.RunBudgetTokens)
	}
}

func TestRunAgenticThreadsToolsBudgetAndTurns(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.BudgetTokens = 90_000
	var got agent.Request
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		got = r
		return &agent.Response{Text: "researched", Turns: 4,
			Usage: agent.Usage{InputTokens: 500, OutputTokens: 100}}, nil
	}
	text, _, degraded, err := runAgentic(context.Background(), rc, tokens.AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages: []tokens.Message{{Role: "user", Content: "go"}},
	}, []string{"vault_search", "vault_read"}, 6)
	if err != nil || degraded {
		t.Fatalf("err=%v degraded=%v", err, degraded)
	}
	if text != "researched" {
		t.Fatalf("text = %q", text)
	}
	if len(got.Tools) != 2 || got.MaxTurns != 6 || got.RunBudgetTokens != 90_000 {
		t.Fatalf("request = %+v, want tools/turns/config budget", got)
	}
}

func TestRunAgenticKillDegradesGracefully(t *testing.T) {
	rc, fake := newRC(t, nil)
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Usage: agent.Usage{InputTokens: 999, OutputTokens: 1}},
			agent.ErrRunBudgetExceeded
	}
	_, _, degraded, err := runAgentic(context.Background(), rc, tokens.AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages: []tokens.Message{{Role: "user", Content: "go"}},
	}, []string{"vault_read"}, 4)
	if err != nil {
		t.Fatalf("kill must degrade, not fail: %v", err)
	}
	if !degraded {
		t.Fatal("degraded = false, want true on kill")
	}
}

func TestRunAgenticDryRunMakesNoCall(t *testing.T) {
	rc, fake := newRC(t, nil)
	rc.DryRun = true
	_, est, _, err := runAgentic(context.Background(), rc, tokens.AgentCall{
		Operation: "automation.test", ModelKey: "synthesis",
		Messages: []tokens.Message{{Role: "user", Content: "estimate me"}},
	}, []string{"vault_read"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 0 {
		t.Fatalf("dry-run called the agent %d times", fake.CallCount())
	}
	if est == 0 {
		t.Fatal("dry-run should return the estimate")
	}
}

func TestAgenticEnabled(t *testing.T) {
	rc, _ := newRC(t, nil)
	if !agenticEnabled(rc, "knowledge-digest", true) {
		t.Fatal("default true when config silent")
	}
	f := false
	rc.Config.Automations = map[string]config.Automation{
		"knowledge-digest": {Enabled: true, Agentic: &f},
	}
	if agenticEnabled(rc, "knowledge-digest", true) {
		t.Fatal("explicit agentic:false must win")
	}
}
