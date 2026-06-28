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

// ClearLinks removes all link-graph edges so reindex can rebuild them. Links
// have no dependents, so wiping them is safe (unlike notes, whose ids anchor
// chunks/vectors).
func ClearLinks(ctx context.Context, q Execer) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM links;"); err != nil {
		return fmt.Errorf("clear links: %w", err)
	}
	return nil
}

// NotePathIDs returns a map of vault-relative path -> note id for every note.
func NotePathIDs(ctx context.Context, q Queryer2) (map[string]int64, error) {
	rows, err := q.QueryContext(ctx, "SELECT path, id FROM notes;")
	if err != nil {
		return nil, fmt.Errorf("list note paths: %w", err)
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var path string
		var id int64
		if err := rows.Scan(&path, &id); err != nil {
			return nil, err
		}
		out[path] = id
	}
	return out, rows.Err()
}

// DeleteNote removes a note by id (cascading its chunks/vectors/links).
func DeleteNote(ctx context.Context, q Execer, id int64) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM notes WHERE id = ?;", id); err != nil {
		return fmt.Errorf("delete note %d: %w", id, err)
	}
	return nil
}

// Queryer2 is the read surface needing multi-row queries.
type Queryer2 interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
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

// GetNoteIDByPath returns the id of the note at a vault-relative path, or nil.
func GetNoteIDByPath(ctx context.Context, q Queryer, path string) (*int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM notes WHERE path = ? LIMIT 1;", path).Scan(&id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get note id %q: %w", path, err)
	}
	return &id, nil
}

// OversizedNote is a note exceeding a word-count threshold (compaction target).
type OversizedNote struct {
	Path      string
	WordCount int
}

// NotesOverWordCount returns notes whose word_count exceeds threshold, largest
// first, capped at limit.
func NotesOverWordCount(ctx context.Context, q Queryer2, threshold, limit int) ([]OversizedNote, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT path, word_count FROM notes WHERE word_count > ? ORDER BY word_count DESC LIMIT ?;`,
		threshold, limit)
	if err != nil {
		return nil, fmt.Errorf("oversized notes: %w", err)
	}
	defer rows.Close()
	var out []OversizedNote
	for rows.Next() {
		var n OversizedNote
		if err := rows.Scan(&n.Path, &n.WordCount); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CountSourcesSince returns how many sources were fetched at/after sinceTS (an
// RFC3339 string), used to gate the weekly knowledge-digest.
func CountSourcesSince(ctx context.Context, q Queryer, sinceTS string) (int, error) {
	return scanCount(q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sources WHERE fetched_at >= ?;`, sinceTS))
}

// OutboundLinks returns the link targets (dst_path) of wikilink/embed edges from
// a note, in stable order.
func OutboundLinks(ctx context.Context, q Queryer2, noteID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT dst_path FROM links WHERE src_note_id = ? AND kind IN ('wikilink','embed') ORDER BY dst_path;`, noteID)
	if err != nil {
		return nil, fmt.Errorf("outbound links %d: %w", noteID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Backlinks returns the paths of notes whose wikilink/embed edges resolve to the
// given note id.
func Backlinks(ctx context.Context, q Queryer2, noteID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT n.path FROM links l JOIN notes n ON n.id = l.src_note_id
		  WHERE l.dst_note_id = ? AND l.kind IN ('wikilink','embed') ORDER BY n.path;`, noteID)
	if err != nil {
		return nil, fmt.Errorf("backlinks %d: %w", noteID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetNotePathByID returns the vault-relative path for a note id, or "".
func GetNotePathByID(ctx context.Context, q Queryer, id int64) (string, error) {
	var path string
	err := q.QueryRowContext(ctx, "SELECT path FROM notes WHERE id = ? LIMIT 1;", id).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get note path %d: %w", id, err)
	}
	return path, nil
}

// UpsertNote inserts the note, or updates it in place if a row already exists
// for its path, returning the row id. Used by ingestion to register a freshly
// written note immediately (reindex rebuilds the full mirror separately).
func UpsertNote(ctx context.Context, q DBTX, n NoteRow) (int64, error) {
	existing, err := GetNoteIDByPath(ctx, q, n.Path)
	if err != nil {
		return 0, err
	}
	tags, err := json.Marshal(n.Tags)
	if err != nil {
		return 0, fmt.Errorf("marshal tags for %q: %w", n.Path, err)
	}
	if existing != nil {
		if _, err := q.ExecContext(ctx,
			`UPDATE notes SET title=?, type=?, status=?, tags=?, content_hash=?, word_count=?, created=?, updated=?, last_indexed=? WHERE id=?;`,
			n.Title, n.Type, n.Status, string(tags), n.ContentHash, n.WordCount, n.Created, n.Updated, n.LastIndexed, *existing); err != nil {
			return 0, fmt.Errorf("update note %q: %w", n.Path, err)
		}
		return *existing, nil
	}
	return InsertNote(ctx, q, n)
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
