package tokens

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

// countingAgent is a fake that also satisfies the exact-counter seam, returning
// a fixed exact count distinct from the heuristic so the test can tell them
// apart.
type countingAgent struct {
	*agent.Fake
	exact  int
	called bool
}

func (c *countingAgent) CountTokens(ctx context.Context, model, system, prompt string) (int, error) {
	c.called = true
	return c.exact, nil
}

func TestAuthorizeUsesExactCountInApiKeyMode(t *testing.T) {
	ctx := context.Background()
	ca := &countingAgent{Fake: agent.NewFake(), exact: 12345}
	m := testManager(t, generousLimits(), ca)
	m.cfg.AuthMode = "api_key"

	auth, err := m.Authorize(ctx, AgentCall{
		Operation: "ingest.enrich", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "a short prompt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ca.called {
		t.Fatal("api_key mode should call the exact token counter")
	}
	if auth.EstInput != 12345 {
		t.Errorf("EstInput = %d, want exact 12345", auth.EstInput)
	}
}

func TestAuthorizeUsesHeuristicWhenNotApiKey(t *testing.T) {
	ctx := context.Background()
	ca := &countingAgent{Fake: agent.NewFake(), exact: 99999}
	m := testManager(t, generousLimits(), ca) // AuthMode defaults to subscription

	auth, err := m.Authorize(ctx, AgentCall{
		Operation: "x", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "a short prompt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ca.called {
		t.Error("non-api_key mode must not call the exact counter")
	}
	if auth.EstInput == 99999 {
		t.Error("non-api_key mode should use the heuristic, not the exact count")
	}
}
