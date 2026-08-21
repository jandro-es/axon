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

func TestNotesUpdatedBeforeLimit(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	for _, u := range []string{"2024-01-01", "2024-02-01", "2024-03-01"} {
		if _, err := InsertNote(ctx, d, NoteRow{Path: "n-" + u + ".md", Title: u, Updated: u}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := NotesUpdatedBefore(ctx, d, "2025-01-01", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("limit 0 must be unlimited, got %d", len(all))
	}
	// Oldest first (existing ORDER BY updated, path).
	if all[0].Updated != "2024-01-01" {
		t.Fatalf("ordering changed: %+v", all[0])
	}
	two, err := NotesUpdatedBefore(ctx, d, "2025-01-01", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(two) != 2 || two[0].Updated != "2024-01-01" {
		t.Fatalf("limit 2 wrong: %+v", two)
	}
}

func TestOrphanNotes(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	mk := func(path, updated string) int64 {
		id, err := InsertNote(ctx, d, NoteRow{Path: path, Title: path, Updated: updated})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	link := func(src int64, dstPath string, dst *int64, kind string) {
		if err := InsertLink(ctx, d, LinkRow{SrcNoteID: src, DstPath: dstPath, DstNoteID: dst, Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}

	mk("Z Orphan.md", "2026-08-01")
	inboundOnly := mk("Inbound Only.md", "2026-07-01")
	outboundOnly := mk("Outbound Only.md", "2026-06-01")
	brokenOnly := mk("Broken Only.md", "2026-05-01")
	taggedOnly := mk("Tagged Only.md", "2026-04-01")
	hub := mk("Hub.md", "2026-03-01")

	link(hub, "Inbound Only", &inboundOnly, "wikilink") // gives inboundOnly an inbound edge
	link(outboundOnly, "Hub", &hub, "wikilink")         // gives outboundOnly a resolved outbound edge
	link(brokenOnly, "Nonexistent", nil, "wikilink")    // broken: dst_note_id IS NULL
	link(taggedOnly, "#some-tag", nil, "tag")           // a tag is not a link to a note

	got, err := OrphanNotes(ctx, d, 0)
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, len(got))
	for i, n := range got {
		paths[i] = n.Path
	}
	// Newest first. hub has an outbound edge; inboundOnly and outboundOnly are
	// connected. Broken-only and tag-only are STILL orphans.
	want := []string{"Z Orphan.md", "Broken Only.md", "Tagged Only.md"}
	if len(paths) != len(want) {
		t.Fatalf("want %v, got %v", want, paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("want %v, got %v", want, paths)
		}
	}

	// Limit caps the result, newest first.
	one, err := OrphanNotes(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].Path != "Z Orphan.md" {
		t.Fatalf("limit 1: want [Z Orphan.md], got %+v", one)
	}
}
