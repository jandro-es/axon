package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

func TestActionExtractProposesToQueue(t *testing.T) {
	ctx := context.Background()
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-07-10.md": "---\nupdated: 2099-01-01\n---\nMet Sam. I should email John re contract.\n",
	})
	mustReindex(t, rc)
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: `{"actions":["email John re contract"]}`, Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 8}}, nil
	}

	if _, err := (ActionExtract{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	q := readReviewQueue(t, rc)
	if !strings.Contains(q, `action "email John re contract" from [[Daily/2026-07-10]]`) {
		t.Errorf("extracted action not proposed:\n%s", q)
	}
	if _, err := (ActionExtract{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if strings.Count(readReviewQueue(t, rc), "email John") != 1 {
		t.Error("re-run must not re-propose")
	}
}

func TestActionExtractUsesRoutineTier(t *testing.T) {
	ctx := context.Background()
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-07-10.md": "---\nupdated: 2099-01-01\n---\nI should call the bank.\n",
	})
	mustReindex(t, rc)
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: `{"actions":[]}`, Model: r.Model, Usage: agent.Usage{InputTokens: 10, OutputTokens: 2}}, nil
	}
	if _, err := (ActionExtract{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if len(fake.Calls) == 0 || fake.Calls[0].Model != "sonnet" {
		t.Errorf("expected a routine-tier (sonnet) call, got %+v", fake.Calls)
	}
}
