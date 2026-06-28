package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// InsertEvent persists an observability event to the events table (history for
// the dashboard activity feed). data is JSON-encoded; secrets are never placed
// in event data by emitters.
func InsertEvent(ctx context.Context, q Execer, ts time.Time, level, kind, message string, data map[string]any) error {
	var dataJSON string
	if len(data) > 0 {
		if b, err := json.Marshal(data); err == nil {
			dataJSON = string(b)
		}
	}
	_, err := q.ExecContext(ctx,
		`INSERT INTO events (ts, level, kind, message, data) VALUES (?, ?, ?, ?, ?);`,
		ts.UTC().Format(time.RFC3339), level, kind, message, nullIfEmpty(dataJSON))
	if err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

// EventRow is one activity-feed entry.
type EventRow struct {
	ID      int64  `json:"id"`
	TS      string `json:"ts"`
	Level   string `json:"level"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// RecentEvents returns the most recent events, newest first.
func RecentEvents(ctx context.Context, q Queryer2, limit int) ([]EventRow, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := q.QueryContext(ctx,
		`SELECT id, ts, COALESCE(level,''), COALESCE(kind,''), COALESCE(message,'')
		   FROM events ORDER BY id DESC LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent events: %w", err)
	}
	defer rows.Close()
	var out []EventRow
	for rows.Next() {
		var e EventRow
		if err := rows.Scan(&e.ID, &e.TS, &e.Level, &e.Kind, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// TokenBucket is token usage grouped by day, operation and model.
type TokenBucket struct {
	Day       string `json:"day"`
	Operation string `json:"operation"`
	Model     string `json:"model"`
	Input     int64  `json:"input"`
	Output    int64  `json:"output"`
}

// TokenSeries returns token usage bucketed by day/operation/model since sinceTS,
// for the Tokens view (stacked by automation + model).
func TokenSeries(ctx context.Context, q Queryer2, sinceTS string) ([]TokenBucket, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT substr(ts,1,10) AS day, operation, model,
		        COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		   FROM token_ledger WHERE ts >= ?
		   GROUP BY day, operation, model ORDER BY day;`, sinceTS)
	if err != nil {
		return nil, fmt.Errorf("token series: %w", err)
	}
	defer rows.Close()
	var out []TokenBucket
	for rows.Next() {
		var b TokenBucket
		if err := rows.Scan(&b.Day, &b.Operation, &b.Model, &b.Input, &b.Output); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// RunRow is a recent automation run for the Runs view.
type RunRow struct {
	ID         int64  `json:"id"`
	Automation string `json:"automation"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Status     string `json:"status"`
	SkipReason string `json:"skip_reason"`
	Tokens     int64  `json:"tokens"`
}

// RecentRuns returns recent runs, newest first.
func RecentRuns(ctx context.Context, q Queryer2, limit int) ([]RunRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.QueryContext(ctx,
		`SELECT id, automation, COALESCE(started_at,''), COALESCE(finished_at,''),
		        COALESCE(status,''), COALESCE(skip_reason,''), COALESCE(tokens,0)
		   FROM runs ORDER BY id DESC LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.ID, &r.Automation, &r.StartedAt, &r.FinishedAt, &r.Status, &r.SkipReason, &r.Tokens); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SourceBucket is ingestion counts grouped by day and status.
type SourceBucket struct {
	Day    string `json:"day"`
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// SourceSeries returns ingestion counts by day/status, plus the count of chunks
// still pending embedding (queue depth).
func SourceSeries(ctx context.Context, q Queryer2) ([]SourceBucket, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT substr(COALESCE(fetched_at,''),1,10) AS day, COALESCE(status,''), COUNT(*)
		   FROM sources GROUP BY day, status ORDER BY day;`)
	if err != nil {
		return nil, fmt.Errorf("source series: %w", err)
	}
	defer rows.Close()
	var out []SourceBucket
	for rows.Next() {
		var b SourceBucket
		if err := rows.Scan(&b.Day, &b.Status, &b.Count); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PendingEmbeddings returns the number of chunks without a stored vector.
func PendingEmbeddings(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunks c LEFT JOIN vec_chunks v ON v.chunk_id=c.id WHERE v.chunk_id IS NULL;`))
}

// VaultStats is the Vault-growth snapshot.
type VaultStats struct {
	Notes        int `json:"notes"`
	Links        int `json:"links"`
	Words        int `json:"words"`
	Sources      int `json:"sources"`
	InboxBacklog int `json:"inbox_backlog"`
}

// Stats returns current vault counts.
func Stats(ctx context.Context, q Queryer) (VaultStats, error) {
	var s VaultStats
	var err error
	if s.Notes, err = scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM notes;")); err != nil {
		return s, err
	}
	if s.Links, err = scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM links;")); err != nil {
		return s, err
	}
	if err = q.QueryRowContext(ctx, "SELECT COALESCE(SUM(word_count),0) FROM notes;").Scan(&s.Words); err != nil {
		return s, err
	}
	if s.Sources, err = scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM sources;")); err != nil {
		return s, err
	}
	if s.InboxBacklog, err = scanCount(q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM notes WHERE path LIKE '00-Inbox/%' AND path NOT LIKE '%/README.md';")); err != nil {
		return s, err
	}
	return s, nil
}

// GraphNode is a knowledge-graph node (a note).
type GraphNode struct {
	ID    int64  `json:"id"`
	Path  string `json:"path"`
	Type  string `json:"type"`
	Tags  string `json:"tags"`
	Words int    `json:"words"`
}

// GraphEdge is a resolved wikilink edge between two notes.
type GraphEdge struct {
	Source int64 `json:"source"`
	Target int64 `json:"target"`
}

// Graph is the knowledge-graph payload.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// GraphData returns nodes (notes) and resolved wikilink edges, capped at limit
// nodes. Filtering by folder/tag is applied client-side.
func GraphData(ctx context.Context, q Queryer2, limit int) (Graph, error) {
	if limit <= 0 {
		limit = 1000
	}
	g := Graph{}
	nrows, err := q.QueryContext(ctx,
		`SELECT id, path, COALESCE(type,''), COALESCE(tags,''), COALESCE(word_count,0)
		   FROM notes ORDER BY id LIMIT ?;`, limit)
	if err != nil {
		return g, fmt.Errorf("graph nodes: %w", err)
	}
	defer nrows.Close()
	present := map[int64]bool{}
	for nrows.Next() {
		var n GraphNode
		if err := nrows.Scan(&n.ID, &n.Path, &n.Type, &n.Tags, &n.Words); err != nil {
			return g, err
		}
		g.Nodes = append(g.Nodes, n)
		present[n.ID] = true
	}
	if err := nrows.Err(); err != nil {
		return g, err
	}

	erows, err := q.QueryContext(ctx,
		`SELECT src_note_id, dst_note_id FROM links
		   WHERE dst_note_id IS NOT NULL AND kind IN ('wikilink','embed');`)
	if err != nil {
		return g, fmt.Errorf("graph edges: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var e GraphEdge
		if err := erows.Scan(&e.Source, &e.Target); err != nil {
			return g, err
		}
		if present[e.Source] && present[e.Target] {
			g.Edges = append(g.Edges, e)
		}
	}
	return g, erows.Err()
}
