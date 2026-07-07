package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/identity"
)

func TestMemoryDistillRunsAtRoutineTier(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- decided to keep the vector store brute-force\n",
	})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- Keep the vector store brute-force until 10^5 chunks.", Model: r.Model, Usage: agent.Usage{InputTokens: 50, OutputTokens: 12}}, nil
	}
	ctx := context.Background()
	if _, err := (MemoryDistill{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 1 {
		t.Fatalf("expected one model call, got %d", fake.CallCount())
	}
	// newRC configures Routine: "sonnet", Synthesis: "opus". Routine tier => sonnet.
	if got := fake.Calls[0].Model; got != "sonnet" {
		t.Fatalf("distill model = %q, want routine tier (sonnet)", got)
	}
}

func TestMemoryDistillPromotesIntervalBearingFact(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- moved to Osaka\n",
	})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- Lives in Osaka.", Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 8}}, nil
	}
	ctx := context.Background()
	if _, err := (MemoryDistill{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	lines, _ := identity.BlockLines(ctx, rc.Vault)
	var found bool
	for _, line := range lines {
		f, ok := identity.ParseFact(line)
		if ok && strings.Contains(f.Text, "Lives in Osaka") {
			found = true
			if f.Kind != "fact" {
				t.Errorf("promoted fact kind = %q, want fact", f.Kind)
			}
			if f.ValidFrom != "2026-06-28" { // fixedNow date
				t.Errorf("promoted ValidFrom = %q, want 2026-06-28", f.ValidFrom)
			}
			if !strings.HasPrefix(f.Source, "[[") || !strings.HasSuffix(f.Source, "]]") {
				t.Errorf("promoted source = %q, want a [[wikilink]]", f.Source)
			}
		}
	}
	if !found {
		t.Fatalf("promoted fact not stored: %v", lines)
	}
}

func TestMemoryDistillContradictionQueuesReconcile(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- moved to Osaka\n",
	})
	ctx := context.Background()
	// Seed a current fact the new activity will contradict.
	if _, err := identity.Remember(ctx, rc.Vault, identity.Entry{Text: "Lives in Tokyo", Kind: "fact", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "CONFLICT 1: Lives in Osaka", Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 8}}, nil
	}
	res, err := (MemoryDistill{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "proposed 1 reconciliation") {
		t.Fatalf("summary = %q, want a queued reconciliation", res.Summary)
	}
	// The contradiction is held in the review queue, NOT written to memory yet.
	lines, _ := identity.BlockLines(ctx, rc.Vault)
	for _, line := range lines {
		if strings.Contains(line, "Osaka") {
			t.Fatalf("contradiction must not auto-write to memory: %q", line)
		}
	}
}
