package db

import (
	"context"
	"database/sql"
	"testing"
)

func seedNote(t *testing.T, d *sql.DB, path, updated string) int64 {
	t.Helper()
	res, err := d.Exec(`INSERT INTO notes (path, title, updated) VALUES (?, ?, ?)`, path, path, updated)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedNoteVector(t *testing.T, d *sql.DB, noteID int64, vec []float32) {
	t.Helper()
	res, err := d.Exec(`INSERT INTO chunks (note_id, ordinal, text, token_count, content_hash) VALUES (?, 0, 'x', 1, 'h')`, noteID)
	if err != nil {
		t.Fatal(err)
	}
	chunkID, _ := res.LastInsertId()
	if _, err := d.Exec(`INSERT INTO vec_chunks (chunk_id, dim, model, embedding) VALUES (?, ?, 'test', ?)`,
		chunkID, len(vec), EncodeVector(vec)); err != nil {
		t.Fatal(err)
	}
}

func TestNotesUpdatedSinceAndBefore(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	seedNote(t, d, "old.md", "2026-01-10")
	seedNote(t, d, "mid.md", "2026-06-01")
	seedNote(t, d, "new.md", "2026-07-03")
	seedNote(t, d, "blank.md", "")

	recent, err := NotesUpdatedSince(ctx, d, "2026-06-27", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Path != "new.md" {
		t.Fatalf("recent = %+v, want [new.md]", recent)
	}

	dormant, err := NotesUpdatedBefore(ctx, d, "2026-04-05")
	if err != nil {
		t.Fatal(err)
	}
	if len(dormant) != 1 || dormant[0].Path != "old.md" {
		t.Fatalf("dormant = %+v, want [old.md] (blank updated excluded)", dormant)
	}

	// Cap respected, newest first.
	seedNote(t, d, "new2.md", "2026-07-04")
	capped, _ := NotesUpdatedSince(ctx, d, "2026-06-27", 1)
	if len(capped) != 1 || capped[0].Path != "new2.md" {
		t.Fatalf("capped = %+v, want newest only", capped)
	}
}

func TestNoteMeanVectorsAndCosine(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	a := seedNote(t, d, "a.md", "2026-07-01")
	b := seedNote(t, d, "b.md", "2026-01-01")
	// a has two chunks whose mean is (1, 0); b has one chunk (0.6, 0.8).
	seedNoteVector(t, d, a, []float32{1, 0})
	seedNoteVector(t, d, a, []float32{1, 0})
	seedNoteVector(t, d, b, []float32{0.6, 0.8})

	means, err := NoteMeanVectors(ctx, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(means) != 2 {
		t.Fatalf("means = %v, want 2 notes", means)
	}
	if got := Cosine(means[a], means[b]); got < 0.59 || got > 0.61 {
		t.Fatalf("cosine = %f, want ~0.6", got)
	}
	// present filter honored.
	only, _ := NoteMeanVectors(ctx, d, map[int64]bool{a: true})
	if len(only) != 1 {
		t.Fatalf("filtered means = %v, want a only", only)
	}
}
