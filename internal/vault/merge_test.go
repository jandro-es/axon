package vault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write is a raw note-file helper for merge tests.
func write(t *testing.T, v *FS, rel, content string) {
	t.Helper()
	abs := filepath.Join(v.Root(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func bodyOf(t *testing.T, v *FS, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMergeSurvivorByInboundLinks(t *testing.T) {
	v := NewFS(t.TempDir())
	// dup-b has more inbound links (referrer1 + referrer2) than dup-a (loser-ref
	// only), so dup-b must survive; the link to the loser dup-a must retarget.
	write(t, v, "notes/dup-a.md", "# Dup A\n\nUnique A prose.\n")
	write(t, v, "notes/dup-b.md", "# Dup B\n\nUnique B prose.\n")
	write(t, v, "refs/referrer1.md", "See [[dup-b]] for details.\n")
	write(t, v, "refs/referrer2.md", "Also [[dup-b]] here.\n")
	write(t, v, "refs/loser-ref.md", "Points at [[dup-a]] here.\n")

	survivor, err := v.Merge(context.Background(), "notes/dup-a.md", "notes/dup-b.md")
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if survivor != "notes/dup-b" {
		t.Fatalf("survivor = %q, want notes/dup-b", survivor)
	}
	// Loser file gone from the live vault, present in the archive.
	if v.Exists("notes/dup-a.md") {
		t.Fatal("loser still in live vault")
	}
	if !v.Exists(".trash/merged/dup-a.md") {
		t.Fatal("loser not archived to .trash/merged/")
	}
	// Survivor gained the loser body in its axon:merged block.
	n, err := v.Read(context.Background(), "notes/dup-b.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.Body, "Merged from [[notes/dup-a]]") {
		t.Fatalf("survivor missing merged header:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "Unique A prose.") {
		t.Fatalf("survivor missing loser content:\n%s", n.Body)
	}
	// The inbound link to the loser was retargeted to the survivor — none dangle.
	if bodyOf(t, v, "refs/loser-ref.md") != "Points at [[notes/dup-b]] here.\n" {
		t.Fatalf("loser-ref not retargeted:\n%s", bodyOf(t, v, "refs/loser-ref.md"))
	}
}

func TestMergeArchivesLoserBytesExactly(t *testing.T) {
	v := NewFS(t.TempDir())
	write(t, v, "a.md", "---\ntitle: A\n---\n\n# Dup A\n\nExact bytes.\n")
	write(t, v, "b.md", "# Dup B\n\nProse.\n")
	write(t, v, "ref.md", "[[b]]\n") // b has an inbound link → b survives, a is loser
	if _, err := v.Merge(context.Background(), "a.md", "b.md"); err != nil {
		t.Fatal(err)
	}
	got := bodyOf(t, v, ".trash/merged/a.md")
	if got != "---\ntitle: A\n---\n\n# Dup A\n\nExact bytes.\n" {
		t.Fatalf("archived bytes = %q", got)
	}
}

func TestMergeNeutralizesManagedMarkers(t *testing.T) {
	v := NewFS(t.TempDir())
	// The loser body carries its own axon:links block; merged into the survivor
	// it must not corrupt the survivor's axon:merged block.
	write(t, v, "a.md", "# A\n\n<!-- axon:links:start -->\n- [[x]]\n<!-- axon:links:end -->\n")
	write(t, v, "keep.md", "# Keep\n")
	write(t, v, "ref.md", "[[keep]]\n") // keep has an inbound link → keep survives
	survivor, err := v.Merge(context.Background(), "a.md", "keep.md")
	if err != nil {
		t.Fatal(err)
	}
	if survivor != "keep" {
		t.Fatalf("survivor = %q, want keep", survivor)
	}
	n, _ := v.Read(context.Background(), "keep.md")
	// Exactly one axon:merged:start and one axon:merged:end — markers from the
	// loser body were neutralized, so the block parser sees a single clean region.
	if strings.Count(n.Body, "<!-- axon:merged:start -->") != 1 ||
		strings.Count(n.Body, "<!-- axon:merged:end -->") != 1 {
		t.Fatalf("merged block corrupted:\n%s", n.Body)
	}
	if strings.Contains(n.Body, "<!-- axon:links:start -->") {
		t.Fatalf("loser markers not neutralized:\n%s", n.Body)
	}
}

func TestMergeRefusesBadInput(t *testing.T) {
	v := NewFS(t.TempDir())
	write(t, v, "a.md", "# A\n")
	cases := map[string][2]string{
		"same note": {"a.md", "a.md"},
		"missing":   {"a.md", "gone.md"},
		"not md":    {"a.md", "b.txt"},
	}
	for name, pair := range cases {
		if _, err := v.Merge(context.Background(), pair[0], pair[1]); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
}
