package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestRecipeKindParseAndAcceptAcknowledges(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if err := v.Append(".axon/review-queue.md",
		"## Recipe test-recipe (2026-08-20)\n- [ ] recipe \"read the new paper\" (from test-recipe)\n"); err != nil {
		t.Fatal(err)
	}

	items, err := Load(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, x := range items {
		if x.Kind == "recipe" {
			it = x
		}
	}
	if it.ID == "" {
		t.Fatalf("recipe item not parsed: %+v", items)
	}
	if it.Target != "read the new paper" || it.Note != "test-recipe" {
		t.Errorf("recipe fields wrong: target=%q note=%q", it.Target, it.Note)
	}

	if _, err := Accept(ctx, v, it.ID); err != nil {
		t.Fatal(err)
	}
	q, err := v.Read(ctx, ".axon/review-queue.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Body, "✓ noted") {
		t.Errorf("accept did not acknowledge:\n%s", q.Body)
	}
	// Acknowledge-only: the vault gained no notes from accepting.
	paths, err := v.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Errorf("accept must not touch the vault: %v", paths)
	}
}
