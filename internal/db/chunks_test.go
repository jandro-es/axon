package db

import (
	"context"
	"database/sql"
	"testing"
)

// seedSource inserts one note + one source row.
func seedSource(t *testing.T, d *sql.DB, path, url, kind, fetchedAt, status string) {
	t.Helper()
	ctx := context.Background()
	var notePtr *int64
	if path != "" {
		id, err := InsertNote(ctx, d, NoteRow{Path: path, Title: path, Updated: "2026-01-01"})
		if err != nil {
			t.Fatal(err)
		}
		notePtr = &id
	}
	if _, err := UpsertSource(ctx, d, SourceRow{
		NoteID: notePtr, URL: url, Kind: kind,
		FetchedAt: fetchedAt, ContentHash: "h", Status: status,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSourcesOlderThan(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	seedSource(t, d, "03-Resources/Knowledge/Old.md", "https://old.example/a", "url", "2025-01-01T00:00:00Z", "ok")
	seedSource(t, d, "03-Resources/Knowledge/New.md", "https://new.example/b", "url", "2026-08-01T00:00:00Z", "ok")
	seedSource(t, d, "", "https://orphan.example/c", "pdf", "2024-06-01T00:00:00Z", "failed")

	// Age filter: only the two predating the cutoff.
	got, err := SourcesOlderThan(ctx, d, "2026-01-01T00:00:00Z", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("age filter: want 2 rows, got %d: %+v", len(got), got)
	}
	// Newest first.
	if got[0].URL != "https://old.example/a" || got[1].URL != "https://orphan.example/c" {
		t.Fatalf("ordering wrong: %+v", got)
	}
	// A source with no note row carries an empty Path.
	if got[1].Path != "" || got[1].Kind != "pdf" || got[1].Status != "failed" {
		t.Fatalf("orphan projection wrong: %+v", got[1])
	}
	if got[0].Path != "03-Resources/Knowledge/Old.md" {
		t.Fatalf("joined path wrong: %+v", got[0])
	}

	// Empty cutoff means no age filter.
	all, err := SourcesOlderThan(ctx, d, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("no-filter: want 3 rows, got %d", len(all))
	}

	// Limit caps the result.
	one, err := SourcesOlderThan(ctx, d, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].URL != "https://new.example/b" {
		t.Fatalf("limit: want the newest single row, got %+v", one)
	}
}
