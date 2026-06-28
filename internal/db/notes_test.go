package db

import (
	"context"
	"database/sql"
	"testing"
)

func newMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInsertAndCountNotesLinks(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	idA, err := InsertNote(ctx, d, NoteRow{Path: "a.md", Title: "A", Type: "note", Tags: []string{"x"}})
	if err != nil {
		t.Fatal(err)
	}
	idB, err := InsertNote(ctx, d, NoteRow{Path: "b.md", Title: "B"})
	if err != nil {
		t.Fatal(err)
	}

	// One resolved wikilink, one dangling, one tag.
	if err := InsertLink(ctx, d, LinkRow{SrcNoteID: idA, DstPath: "b", DstNoteID: &idB, Kind: "wikilink"}); err != nil {
		t.Fatal(err)
	}
	if err := InsertLink(ctx, d, LinkRow{SrcNoteID: idA, DstPath: "ghost", Kind: "wikilink"}); err != nil {
		t.Fatal(err)
	}
	if err := InsertLink(ctx, d, LinkRow{SrcNoteID: idA, DstPath: "topic", Kind: "tag"}); err != nil {
		t.Fatal(err)
	}

	if n, _ := CountNotes(ctx, d); n != 2 {
		t.Errorf("CountNotes = %d, want 2", n)
	}
	if n, _ := CountLinks(ctx, d); n != 3 {
		t.Errorf("CountLinks = %d, want 3", n)
	}
	if n, _ := CountBrokenWikilinks(ctx, d); n != 1 {
		t.Errorf("CountBrokenWikilinks = %d, want 1 (the ghost link)", n)
	}
}

func TestClearLinksAndDeleteNote(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	id, _ := InsertNote(ctx, d, NoteRow{Path: "a.md"})
	_ = InsertLink(ctx, d, LinkRow{SrcNoteID: id, DstPath: "x", Kind: "tag"})

	if err := ClearLinks(ctx, d); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountLinks(ctx, d); n != 0 {
		t.Errorf("links after ClearLinks = %d, want 0", n)
	}
	if n, _ := CountNotes(ctx, d); n != 1 {
		t.Errorf("notes should survive ClearLinks: got %d, want 1", n)
	}

	if err := DeleteNote(ctx, d, id); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountNotes(ctx, d); n != 0 {
		t.Errorf("notes after DeleteNote = %d, want 0", n)
	}
}

func TestUpsertNoteByPathKeepsID(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	id1, err := UpsertNote(ctx, d, NoteRow{Path: "a.md", Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := UpsertNote(ctx, d, NoteRow{Path: "a.md", Title: "A updated"})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Errorf("UpsertNote changed id for same path: %d -> %d", id1, id2)
	}
	if n, _ := CountNotes(ctx, d); n != 1 {
		t.Errorf("upsert created a duplicate: %d notes", n)
	}
}
