package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// NoteRow is a row in the derived notes mirror. It is rebuildable from the vault
// (ADR-006), so reindex clears and repopulates it.
type NoteRow struct {
	Path        string
	Title       string
	Type        string
	Status      string
	Tags        []string
	ContentHash string
	WordCount   int
	Created     string
	Updated     string
	LastIndexed string
}

// LinkRow is a row in the derived link graph. DstNoteID is nil for links whose
// target does not (yet) resolve to a known note.
type LinkRow struct {
	SrcNoteID int64
	DstPath   string
	DstNoteID *int64
	Kind      string
}

// ResetDerived clears the vault-derived tables (notes and, by cascade, links)
// so reindex can rebuild them from Markdown. Sources/chunks belong to the
// ingestion pipeline (Phase 2) and are left untouched.
func ResetDerived(ctx context.Context, q Execer) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM links;"); err != nil {
		return fmt.Errorf("clear links: %w", err)
	}
	if _, err := q.ExecContext(ctx, "DELETE FROM notes;"); err != nil {
		return fmt.Errorf("clear notes: %w", err)
	}
	return nil
}

// InsertNote inserts a note row and returns its new id.
func InsertNote(ctx context.Context, q Execer, n NoteRow) (int64, error) {
	tags, err := json.Marshal(n.Tags)
	if err != nil {
		return 0, fmt.Errorf("marshal tags for %q: %w", n.Path, err)
	}
	res, err := q.ExecContext(ctx,
		`INSERT INTO notes (path, title, type, status, tags, content_hash, word_count, created, updated, last_indexed)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		n.Path, n.Title, n.Type, n.Status, string(tags), n.ContentHash, n.WordCount, n.Created, n.Updated, n.LastIndexed)
	if err != nil {
		return 0, fmt.Errorf("insert note %q: %w", n.Path, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("note id for %q: %w", n.Path, err)
	}
	return id, nil
}

// InsertLink inserts a link-graph edge.
func InsertLink(ctx context.Context, q Execer, l LinkRow) error {
	_, err := q.ExecContext(ctx,
		`INSERT OR IGNORE INTO links (src_note_id, dst_path, dst_note_id, kind)
		 VALUES (?, ?, ?, ?);`,
		l.SrcNoteID, l.DstPath, l.DstNoteID, l.Kind)
	if err != nil {
		return fmt.Errorf("insert link %d->%q: %w", l.SrcNoteID, l.DstPath, err)
	}
	return nil
}

// CountNotes returns the number of rows in the notes table.
func CountNotes(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes;"))
}

// CountLinks returns the number of rows in the links table.
func CountLinks(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM links;"))
}

// CountBrokenWikilinks returns the number of wikilink/embed edges whose target
// did not resolve to a known note. This is the link-integrity probe used by the
// S5 gate. Tag edges never resolve to notes and are excluded.
func CountBrokenWikilinks(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM links
		 WHERE kind IN ('wikilink','embed') AND dst_note_id IS NULL;`))
}

func scanCount(row *sql.Row) (int, error) {
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// Execer is the subset of *sql.DB / *sql.Tx used for writes.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Queryer is the subset of *sql.DB / *sql.Tx used for reads.
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
