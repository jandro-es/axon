package vault

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTempVault builds an FS over a temp dir seeded with the given files
// (vault-relative path -> content).
func newTempVault(t *testing.T, files map[string]string) *FS {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return NewFS(dir)
}

func TestFSListSkipsSystemDirs(t *testing.T) {
	v := newTempVault(t, map[string]string{
		"00-Inbox/a.md":          "a",
		"01-Projects/b.md":       "b",
		".obsidian/workspace.md": "should be skipped",
		".axon/logs/run.md":      "should be skipped",
		".git/notes.md":          "should be skipped",
		"03-Resources/notes.txt": "not markdown",
	})
	got, err := v.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"00-Inbox/a.md", "01-Projects/b.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("List = %v, want %v", got, want)
	}
}

func TestFSReadParsesFrontmatter(t *testing.T) {
	v := newTempVault(t, map[string]string{
		"n.md": "---\ntitle: T\ntype: note\n---\nThe body.\n",
	})
	n, err := v.Read(context.Background(), "n.md")
	if err != nil {
		t.Fatal(err)
	}
	if n.FrontmatterString("title") != "T" || n.Body != "The body.\n" {
		t.Errorf("parsed note wrong: %+v", n)
	}
}

func TestFSPatchPreservesFrontmatterAndProse(t *testing.T) {
	v := newTempVault(t, map[string]string{
		"n.md": "---\ntitle: T\ncreated: 2026-06-28\n---\nHuman prose stays.\n",
	})
	ctx := context.Background()
	if err := v.Patch(ctx, "n.md", "summary", "agent summary"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "n.md"))
	got := string(raw)

	if !strings.Contains(got, "---\ntitle: T\ncreated: 2026-06-28\n---") {
		t.Errorf("frontmatter not preserved:\n%s", got)
	}
	if !strings.Contains(got, "Human prose stays.") {
		t.Errorf("human prose clobbered:\n%s", got)
	}
	if !strings.Contains(got, "<!-- axon:summary:start -->\nagent summary\n<!-- axon:summary:end -->") {
		t.Errorf("managed block missing:\n%s", got)
	}

	// Second patch replaces content, doesn't duplicate the block.
	if err := v.Patch(ctx, "n.md", "summary", "updated"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(v.Root(), "n.md"))
	if strings.Count(string(raw), "axon:summary:start") != 1 {
		t.Errorf("managed block duplicated:\n%s", raw)
	}
	if !strings.Contains(string(raw), "updated") || strings.Contains(string(raw), "agent summary") {
		t.Errorf("block content not replaced:\n%s", raw)
	}
}

func TestFSWriteIsAtomicNoTempLeft(t *testing.T) {
	v := newTempVault(t, nil)
	ctx := context.Background()
	if err := v.Write(ctx, "sub/new.md", &Note{
		Frontmatter: map[string]any{"title": "New"},
		Body:        "Hello.\n",
	}); err != nil {
		t.Fatal(err)
	}
	if !v.Exists("sub/new.md") {
		t.Fatal("note not written")
	}
	// No leftover temp files in the destination directory.
	entries, _ := os.ReadDir(filepath.Join(v.Root(), "sub"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".axon-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestFSCreateNeverClobbers(t *testing.T) {
	v := newTempVault(t, map[string]string{"keep.md": "original"})
	created, err := v.Create("keep.md", "REPLACED")
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("Create reported creation over an existing file")
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "keep.md"))
	if string(raw) != "original" {
		t.Errorf("existing file was clobbered: %q", raw)
	}

	created, err = v.Create("fresh.md", "brand new")
	if err != nil || !created {
		t.Fatalf("Create(fresh) = (%v,%v), want (true,nil)", created, err)
	}
}

func TestFSMoveZeroBrokenLinks(t *testing.T) {
	// A and C both link to B by bare and path forms; moving/renaming B must
	// rewrite every inbound link so none dangle (the S5 gate, at the FS layer).
	v := newTempVault(t, map[string]string{
		"01-Projects/a.md":  "---\ntitle: A\n---\nlinks [[Beta]] and [[02-Areas/Beta|b]].\n",
		"02-Areas/Beta.md":  "---\ntitle: Beta\n---\nI am beta.\n",
		"03-Resources/c.md": "embed ![[Beta]] and [[Beta#Section]].\n",
	})
	ctx := context.Background()

	if err := v.Move(ctx, "02-Areas/Beta.md", "03-Resources/Knowledge/Renamed.md"); err != nil {
		t.Fatal(err)
	}

	// Old gone, new present.
	if v.Exists("02-Areas/Beta.md") {
		t.Error("source still exists after move")
	}
	if !v.Exists("03-Resources/Knowledge/Renamed.md") {
		t.Error("destination missing after move")
	}

	// Every inbound link now points to the new path and resolves.
	all, _ := v.List(ctx)
	newKey := RelNoExt("03-Resources/Knowledge/Renamed.md")
	for _, p := range all {
		n, _ := v.Read(ctx, p)
		for _, l := range ParseLinks(n.Body) {
			if l.Kind == KindTag {
				continue
			}
			key, isPath := TargetKey(l.Target)
			// Any link that used to point at Beta must now point at the new path.
			if !isPath {
				if key == "Beta" {
					t.Errorf("%s still has a dangling bare link to Beta", p)
				}
				continue
			}
			if key != newKey && strings.HasSuffix(key, "/Beta") {
				t.Errorf("%s has stale path link %q", p, l.Target)
			}
		}
	}
}

func TestFSMoveRefusesExistingDestination(t *testing.T) {
	v := newTempVault(t, map[string]string{
		"a.md": "a",
		"b.md": "b",
	})
	if err := v.Move(context.Background(), "a.md", "b.md"); err == nil {
		t.Error("expected error moving onto an existing destination")
	}
}
