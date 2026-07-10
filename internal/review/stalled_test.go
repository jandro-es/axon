package review

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestStalledKindParseAndAccept(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	writeNote(t, v, "01-Projects/p.md", "## Todo\n- [ ] tidy backlog\n")
	if err := v.Append(".axon/review-queue.md",
		"## Stale actions\n- [ ] stalled action \"tidy backlog\" in [[01-Projects/p]] (42d) — still relevant?\n"); err != nil {
		t.Fatal(err)
	}

	items, err := Load(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, x := range items {
		if x.Kind == "stalled" {
			it = x
		}
	}
	if it.ID == "" {
		t.Fatalf("stalled item not parsed: %+v", items)
	}
	if it.Note != "01-Projects/p" || it.Target != "tidy backlog" {
		t.Errorf("stalled fields wrong: note=%q target=%q", it.Note, it.Target)
	}

	if _, err := Accept(ctx, v, it.ID); err != nil {
		t.Fatal(err)
	}
	n, _ := v.Read(ctx, "01-Projects/p.md")
	if !strings.Contains(n.Body, "- [ ] tidy backlog #someday") {
		t.Errorf("accept did not tag #someday:\n%s", n.Body)
	}
}
