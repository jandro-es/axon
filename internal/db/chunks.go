package db

import (
	"context"
	"database/sql"
	"fmt"
)

// SourceRow is a row in the ingested-sources table.
type SourceRow struct {
	ID          int64
	NoteID      *int64
	URL         string
	Kind        string // url | pdf | file
	FetchedAt   string
	ContentHash string
	Status      string // ok | failed | redacted
}

// ChunkRow is a row in the chunks table (text + metadata; vectors live in
// vec_chunks, lexical text is mirrored into fts_chunks).
type ChunkRow struct {
	ID          int64
	NoteID      *int64
	SourceID    *int64
	Ordinal     int
	Text        string
	TokenCount  int
	ContentHash string
}

// GetSourceByURL returns the source row for a URL, or (nil, nil) if none.
func GetSourceByURL(ctx context.Context, q Queryer, url string) (*SourceRow, error) {
	row := q.QueryRowContext(ctx,
		`SELECT id, note_id, url, kind, fetched_at, content_hash, status
		   FROM sources WHERE url = ? LIMIT 1;`, url)
	var s SourceRow
	err := row.Scan(&s.ID, &s.NoteID, &s.URL, &s.Kind, &s.FetchedAt, &s.ContentHash, &s.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get source %q: %w", url, err)
	}
	return &s, nil
}

// UpsertSource inserts or updates (by URL) a source row and returns its id.
// `sources.url` carries no UNIQUE constraint in the schema, so this does an
// explicit check-then-write rather than ON CONFLICT.
func UpsertSource(ctx context.Context, q DBTX, s SourceRow) (int64, error) {
	existing, err := GetSourceByURL(ctx, q, s.URL)
	if err != nil {
		return 0, err
	}
	if existing != nil {
		if _, err := q.ExecContext(ctx,
			`UPDATE sources SET note_id=?, kind=?, fetched_at=?, content_hash=?, status=? WHERE id=?;`,
			s.NoteID, s.Kind, s.FetchedAt, s.ContentHash, s.Status, existing.ID); err != nil {
			return 0, fmt.Errorf("update source %q: %w", s.URL, err)
		}
		return existing.ID, nil
	}
	r, err := q.ExecContext(ctx,
		`INSERT INTO sources (note_id, url, kind, fetched_at, content_hash, status)
		 VALUES (?, ?, ?, ?, ?, ?);`,
		s.NoteID, s.URL, s.Kind, s.FetchedAt, s.ContentHash, s.Status)
	if err != nil {
		return 0, fmt.Errorf("insert source %q: %w", s.URL, err)
	}
	return r.LastInsertId()
}

// DeleteChunksForSource removes all chunks (and, by cascade, their vectors) for
// a source, plus their FTS rows. Used when re-ingesting changed content.
func DeleteChunksForSource(ctx context.Context, q DBTX, sourceID int64) error {
	rows, err := q.QueryContext(ctx, `SELECT id FROM chunks WHERE source_id = ?;`, sourceID)
	if err != nil {
		return fmt.Errorf("list chunks for source %d: %w", sourceID, err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list chunks for source %d: %w", sourceID, err)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := q.ExecContext(ctx, `DELETE FROM fts_chunks WHERE chunk_id = ?;`, id); err != nil {
			return fmt.Errorf("delete fts row %d: %w", id, err)
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM chunks WHERE source_id = ?;`, sourceID); err != nil {
		return fmt.Errorf("delete chunks for source %d: %w", sourceID, err)
	}
	return nil
}

// DeleteChunksForNote removes all chunks (and, by cascade, their vectors) for a
// note, plus their FTS rows. Used by reindex when a note's body has changed and
// by DeleteNote.
func DeleteChunksForNote(ctx context.Context, q DBTX, noteID int64) error {
	rows, err := q.QueryContext(ctx, `SELECT id FROM chunks WHERE note_id = ?;`, noteID)
	if err != nil {
		return fmt.Errorf("list chunks for note %d: %w", noteID, err)
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("list chunks for note %d: %w", noteID, err)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := q.ExecContext(ctx, `DELETE FROM fts_chunks WHERE chunk_id = ?;`, id); err != nil {
			return fmt.Errorf("delete fts row %d: %w", id, err)
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM chunks WHERE note_id = ?;`, noteID); err != nil {
		return fmt.Errorf("delete chunks for note %d: %w", noteID, err)
	}
	return nil
}

// InsertChunk inserts a chunk row and returns its id.
func InsertChunk(ctx context.Context, q Execer, c ChunkRow) (int64, error) {
	r, err := q.ExecContext(ctx,
		`INSERT INTO chunks (note_id, source_id, ordinal, text, token_count, content_hash)
		 VALUES (?, ?, ?, ?, ?, ?);`,
		c.NoteID, c.SourceID, c.Ordinal, c.Text, c.TokenCount, c.ContentHash)
	if err != nil {
		return 0, fmt.Errorf("insert chunk: %w", err)
	}
	return r.LastInsertId()
}

// InsertChunkFTS mirrors a chunk's text into the FTS5 index.
func InsertChunkFTS(ctx context.Context, q Execer, chunkID int64, text string) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO fts_chunks (chunk_id, text) VALUES (?, ?);`, chunkID, text); err != nil {
		return fmt.Errorf("index chunk %d: %w", chunkID, err)
	}
	return nil
}

// UpsertChunkVector stores (or replaces) a chunk's embedding vector.
func UpsertChunkVector(ctx context.Context, q Execer, chunkID int64, model string, vec []float32) error {
	if _, err := q.ExecContext(ctx,
		`INSERT INTO vec_chunks (chunk_id, dim, model, embedding) VALUES (?, ?, ?, ?)
		 ON CONFLICT(chunk_id) DO UPDATE SET dim=excluded.dim, model=excluded.model, embedding=excluded.embedding;`,
		chunkID, len(vec), model, EncodeVector(vec)); err != nil {
		return fmt.Errorf("store vector for chunk %d: %w", chunkID, err)
	}
	return nil
}

// PendingChunk is a chunk that has no stored vector yet.
type PendingChunk struct {
	ID   int64
	Text string
}

// ListPendingChunks returns chunks that lack an embedding (vectors pending),
// optionally limited. When forceAll is true, every chunk is returned (used by
// `reindex --embeddings` after a model change).
func ListPendingChunks(ctx context.Context, q Queryer2, forceAll bool) ([]PendingChunk, error) {
	query := `SELECT c.id, c.text FROM chunks c
	          LEFT JOIN vec_chunks v ON v.chunk_id = c.id
	          WHERE v.chunk_id IS NULL ORDER BY c.id;`
	if forceAll {
		query = `SELECT id, text FROM chunks ORDER BY id;`
	}
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list pending chunks: %w", err)
	}
	defer rows.Close()
	var out []PendingChunk
	for rows.Next() {
		var c PendingChunk
		if err := rows.Scan(&c.ID, &c.Text); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountChunks / CountVectors are small probes for tests and status.
func CountChunks(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM chunks;"))
}

func CountVectors(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM vec_chunks;"))
}

// CountCentroids reports how many IVF centroids are stored (0 = index not built).
func CountCentroids(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM vec_centroids;"))
}

// DBTX is the read+write surface shared by *sql.DB and *sql.Tx, used by repo
// functions that both query and mutate within a caller-chosen scope.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
