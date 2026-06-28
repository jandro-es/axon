package core

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/vault"
)

func tempVault(t *testing.T, files map[string]string) *vault.FS {
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
	return vault.NewFS(dir)
}

func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestReindexBuildsNotesAndResolvesLinks(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{
		"01-Projects/a.md":  "---\ntitle: A\n---\nlinks [[Beta]] and [[02-Areas/Beta]].\n",
		"02-Areas/Beta.md":  "I am beta. #topic\n",
		"03-Resources/c.md": "dangling [[Ghost]] here.\n",
	})
	d := migratedDB(t)

	res, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Notes != 3 {
		t.Errorf("notes = %d, want 3", res.Notes)
	}
	// a->Beta (bare, resolves), a->02-Areas/Beta (path, resolves),
	// Beta-> #topic (tag), c->Ghost (dangling) = 4 links.
	if res.Links != 4 {
		t.Errorf("links = %d, want 4", res.Links)
	}
	if res.BrokenWikilink != 1 {
		t.Errorf("broken wikilinks = %d, want 1 (Ghost)", res.BrokenWikilink)
	}
}

func TestReindexIsRepeatableAndRebuildsFromScratch(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{
		"a.md": "link [[b]]\n",
		"b.md": "hi\n",
	})
	d := migratedDB(t)

	r1, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if r1 != r2 {
		t.Errorf("reindex not repeatable: %+v vs %+v", r1, r2)
	}
}

// TestReindexAfterMoveHasZeroBrokenLinks is the S5 gate at the index level:
// renaming/moving a note through the wikilink-safe Move and reindexing leaves
// no previously-resolved wikilink dangling.
func TestReindexAfterMoveHasZeroBrokenLinks(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{
		"01-Projects/a.md":  "links [[Beta]] and [[02-Areas/Beta|b]].\n",
		"02-Areas/Beta.md":  "I am beta.\n",
		"03-Resources/c.md": "embed ![[Beta]] and [[Beta#Section]].\n",
	})
	d := migratedDB(t)

	// Before the move every wikilink resolves.
	before, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if before.BrokenWikilink != 0 {
		t.Fatalf("precondition: %d broken links before move, want 0", before.BrokenWikilink)
	}

	if err := v.Move(ctx, "02-Areas/Beta.md", "03-Resources/Knowledge/Renamed.md"); err != nil {
		t.Fatal(err)
	}

	after, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if after.BrokenWikilink != 0 {
		t.Errorf("after move: %d broken wikilinks, want 0", after.BrokenWikilink)
	}
	if after.Notes != before.Notes {
		t.Errorf("note count changed across move: %d -> %d", before.Notes, after.Notes)
	}
}
