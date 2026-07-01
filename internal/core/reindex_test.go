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
	if r1.Notes != r2.Notes || r1.Links != r2.Links || r1.BrokenWikilink != r2.BrokenWikilink {
		t.Errorf("reindex not repeatable: %+v vs %+v", r1, r2)
	}
	// The second pass must not churn: nothing changed, so nothing rechunks —
	// this is what keeps existing vectors (and ingestion's source-anchored
	// chunks) alive across routine reindexes.
	if r2.Rechunked != 0 {
		t.Errorf("second reindex rechunked %d notes, want 0 (no churn on unchanged vault)", r2.Rechunked)
	}

	// Vectors on unchanged notes survive further reindexes.
	pending, err := db.ListPendingChunks(ctx, d, false)
	if err != nil || len(pending) == 0 {
		t.Fatalf("expected pending chunks to embed, got %d (err %v)", len(pending), err)
	}
	if err := db.UpsertChunkVector(ctx, d, pending[0].ID, "fake", []float32{1, 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountVectors(ctx, d); n != 1 {
		t.Errorf("vectors after reindex of unchanged vault = %d, want 1 (preserved)", n)
	}
}

// TestReindexResolvesLinksCaseInsensitively mirrors Obsidian: [[beta]]
// resolves to Beta.md, so it must not be counted as a broken wikilink.
func TestReindexResolvesLinksCaseInsensitively(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{
		"a.md":             "links [[beta]] and [[02-areas/BETA]].\n",
		"02-Areas/Beta.md": "I am beta.\n",
	})
	d := migratedDB(t)

	res, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.BrokenWikilink != 0 {
		t.Errorf("broken wikilinks = %d, want 0 (case-insensitive resolution)", res.BrokenWikilink)
	}
}

// TestReindexRebuildsSearchFromMarkdown is the ADR-006 contract: delete the
// database, reindex from the vault alone, and lexical search must work.
func TestReindexRebuildsSearchFromMarkdown(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{
		"notes/espresso.md": "Grinder settings for espresso: 18 grams in, 36 out.\n",
		"notes/other.md":    "Completely unrelated gardening notes.\n",
	})
	d := migratedDB(t) // fresh, empty database — the rm-db recovery path

	res, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rechunked != 2 {
		t.Errorf("rechunked = %d, want 2 (both notes need chunks)", res.Rechunked)
	}

	hits, err := db.HybridSearch(ctx, d, db.SearchOpts{Query: "espresso grinder", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "notes/espresso.md" {
		t.Fatalf("search after rebuild-from-scratch found %+v, want notes/espresso.md", hits)
	}
}

// TestReindexRefreshesChunksOnEdit: hand-editing a note must replace its stale
// chunks so search reflects the vault, not history.
func TestReindexRefreshesChunksOnEdit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	notePath := filepath.Join(dir, "topic.md")
	if err := os.WriteFile(notePath, []byte("All about penguins and Antarctica.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(dir)
	d := migratedDB(t)

	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}

	// Human rewrites the note.
	if err := os.WriteFile(notePath, []byte("Now it covers walruses in the Arctic instead.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Reindex(ctx, v, d)
	if err != nil {
		t.Fatal(err)
	}
	if res.Rechunked != 1 {
		t.Errorf("rechunked = %d, want 1 (edited note)", res.Rechunked)
	}

	if hits, err := db.HybridSearch(ctx, d, db.SearchOpts{Query: "penguins Antarctica", TopK: 5}); err != nil {
		t.Fatal(err)
	} else if len(hits) != 0 {
		t.Errorf("stale content still searchable after edit+reindex: %+v", hits)
	}
	if hits, err := db.HybridSearch(ctx, d, db.SearchOpts{Query: "walruses Arctic", TopK: 5}); err != nil {
		t.Fatal(err)
	} else if len(hits) != 1 {
		t.Errorf("new content not searchable after edit+reindex: %+v", hits)
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
