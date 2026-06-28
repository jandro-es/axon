package automations

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/identity"
)

func TestMemoryDistillExtractsFromDailyNotes(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- decided to keep the vector store brute-force\n",
	})
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- Keep the vector store brute-force until 10^5 chunks.", Model: r.Model, Usage: agent.Usage{InputTokens: 50, OutputTokens: 12}}, nil
	}
	ctx := context.Background()

	ch, err := MemoryDistill{}.DetectChange(ctx, rc)
	if err != nil || !ch.Changed {
		t.Fatalf("DetectChange = %+v, %v", ch, err)
	}
	res, err := MemoryDistill{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "distilled 1") {
		t.Errorf("summary = %q", res.Summary)
	}
	entries, _ := identity.RecentEntries(ctx, rc.Vault, 10)
	found := false
	for _, e := range entries {
		if strings.Contains(e, "brute-force") {
			found = true
		}
	}
	if !found {
		t.Errorf("distilled entry not stored: %v", entries)
	}
	if fake.CallCount() != 1 {
		t.Errorf("expected exactly one model call, got %d", fake.CallCount())
	}
}

func TestMemoryDistillChangeGateSkipsWhenIdle(t *testing.T) {
	rc, _ := newRC(t, nil) // no daily notes, no memory
	ch, _ := MemoryDistill{}.DetectChange(context.Background(), rc)
	if ch.Changed {
		t.Error("memory-distill should skip with no activity")
	}
}

func TestMemoryDistillDryRunWritesNothing(t *testing.T) {
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- a durable decision\n",
	})
	rc.DryRun = true
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- A durable decision was made.", Model: r.Model, Usage: agent.Usage{InputTokens: 40, OutputTokens: 8}}, nil
	}
	ctx := context.Background()
	res, err := MemoryDistill{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Summary, "would add") {
		t.Errorf("dry-run summary = %q", res.Summary)
	}
	// No MEMORY.md should have been created under dry-run.
	if rc.Vault.Exists(identity.MemoryPath) {
		t.Error("dry-run must not write the memory layer")
	}
}

func TestMemoryDistillCompactsOverThreshold(t *testing.T) {
	rc, fake := newRC(t, nil)
	ctx := context.Background()
	// Seed more entries than the (low) threshold so compaction mode triggers.
	for i := range 6 {
		if _, err := identity.Remember(ctx, rc.Vault, identity.Entry{Text: "old fact number " + string(rune('a'+i)), Date: "2026-06-2" + string(rune('0'+i))}); err != nil {
			t.Fatal(err)
		}
	}
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "- Folded summary of older facts.", Model: r.Model, Usage: agent.Usage{InputTokens: 80, OutputTokens: 10}}, nil
	}
	res, err := MemoryDistill{CompactThreshold: 3}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "compacted") {
		t.Errorf("expected compaction, got %q", res.Summary)
	}
	entries, _ := identity.RecentEntries(ctx, rc.Vault, 100)
	for _, e := range entries {
		if strings.Contains(e, "Folded summary") {
			return // compaction summary present
		}
	}
	t.Errorf("compaction summary not written: %v", entries)
}
