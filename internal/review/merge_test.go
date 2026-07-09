package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func writeNote(t *testing.T, v *vault.FS, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMergeItemLoadAndAccept(t *testing.T) {
	v := vault.NewFS(t.TempDir())
	writeNote(t, v, "a.md", "# A\n\nA prose.\n")
	writeNote(t, v, "b.md", "# B\n\nB prose.\n")
	writeNote(t, v, "ref.md", "[[b]]\n") // b has an inbound link → b survives
	if err := v.Append(".axon/review-queue.md",
		"## Near-duplicate merges\n- [ ] merge [[a]] + [[b]] (sim 0.94)\n"); err != nil {
		t.Fatal(err)
	}

	items, err := Load(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, i := range items {
		if i.Kind == "merge" {
			it = i
		}
	}
	if it.Kind != "merge" || it.Note != "a" || it.Target != "b" {
		t.Fatalf("parsed item = %+v, want merge a/b", it)
	}

	got, err := Accept(context.Background(), v, it.ID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if !strings.Contains(got.Line, "✓ merged into [[b]]") {
		t.Fatalf("resolution line = %q", got.Line)
	}
	if v.Exists("a.md") {
		t.Fatal("loser a.md still present after merge accept")
	}
	if !v.Exists(".trash/merged/a.md") {
		t.Fatal("loser not archived")
	}
}
