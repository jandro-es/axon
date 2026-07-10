package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestActionKindParseAndAccept(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	writeNote(t, v, "Daily/2026-07-10.md", "## Log\nMet Sam; I should email John re contract.\n")
	if err := v.Append(".axon/review-queue.md",
		"## Extracted actions\n- [ ] action \"email John re contract\" from [[Daily/2026-07-10]]\n"); err != nil {
		t.Fatal(err)
	}

	items, err := Load(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, x := range items {
		if x.Kind == "action" {
			it = x
		}
	}
	if it.ID == "" || it.Note != "Daily/2026-07-10" || it.Target != "email John re contract" {
		t.Fatalf("action item wrong: %+v", it)
	}

	if _, err := Accept(ctx, v, it.ID); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Read(ctx, "Daily/2026-07-10.md")
	if !strings.Contains(n.Body, "<!-- axon:tasks:start -->") || !strings.Contains(n.Body, "- [ ] email John re contract") {
		t.Errorf("accept did not add checkbox to axon:tasks:\n%s", n.Body)
	}
}
