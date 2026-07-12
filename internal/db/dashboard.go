package db

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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

// TokenBucket is token usage grouped by day, operation and model, including
// the cache-token split (FR-60).
type TokenBucket struct {
	Day        string `json:"day"`
	Operation  string `json:"operation"`
	Model      string `json:"model"`
	Input      int64  `json:"input"`
	Output     int64  `json:"output"`
	CacheRead  int64  `json:"cache_read"`
	CacheWrite int64  `json:"cache_write"`
}

// TokenSeries returns token usage bucketed by day/operation/model since sinceTS,
// for the Tokens view (stacked by automation + model).
func TokenSeries(ctx context.Context, q Queryer2, sinceTS string) ([]TokenBucket, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT substr(ts,1,10) AS day, operation, model,
		        COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0),
		        COALESCE(SUM(cache_read),0), COALESCE(SUM(cache_write),0)
		   FROM token_ledger WHERE ts >= ?
		   GROUP BY day, operation, model ORDER BY day;`, sinceTS)
	if err != nil {
		return nil, fmt.Errorf("token series: %w", err)
	}
	defer rows.Close()
	var out []TokenBucket
	for rows.Next() {
		var b TokenBucket
		if err := rows.Scan(&b.Day, &b.Operation, &b.Model, &b.Input, &b.Output, &b.CacheRead, &b.CacheWrite); err != nil {
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
	Error      string `json:"error"`
}

// RecentRuns returns recent runs, newest first.
func RecentRuns(ctx context.Context, q Queryer2, limit int) ([]RunRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := q.QueryContext(ctx,
		`SELECT id, automation, COALESCE(started_at,''), COALESCE(finished_at,''),
		        COALESCE(status,''), COALESCE(skip_reason,''), COALESCE(tokens,0), COALESCE(error,'')
		   FROM runs ORDER BY id DESC LIMIT ?;`, limit)
	if err != nil {
		return nil, fmt.Errorf("recent runs: %w", err)
	}
	defer rows.Close()
	var out []RunRow
	for rows.Next() {
		var r RunRow
		if err := rows.Scan(&r.ID, &r.Automation, &r.StartedAt, &r.FinishedAt, &r.Status, &r.SkipReason, &r.Tokens, &r.Error); err != nil {
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

// GraphEdge is an edge between two notes: a resolved wikilink/embed
// (kind "link") or a vector-similarity neighbour (kind "similar", FR-61).
type GraphEdge struct {
	Source int64   `json:"source"`
	Target int64   `json:"target"`
	Kind   string  `json:"kind"`
	Sim    float64 `json:"sim,omitempty"` // cosine similarity (similar edges only)
}

// Graph is the knowledge-graph payload.
type Graph struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
}

// Similarity-edge bounds (FR-61): an edge needs at least simThreshold cosine
// similarity, each note keeps at most simTopK neighbours, and the O(n²) sweep
// is skipped beyond simMaxNotes noted-with-vectors (personal-vault scale).
const (
	simThreshold = 0.75
	simTopK      = 3
	simMaxNotes  = 1500
)

// GraphData returns nodes (notes) and resolved wikilink edges — plus, when
// includeSimilar is set, vector-similarity edges (FR-61) — capped at limit
// nodes. Filtering by folder/tag is applied client-side.
func GraphData(ctx context.Context, q Queryer2, limit int, includeSimilar bool) (Graph, error) {
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
		e := GraphEdge{Kind: "link"}
		if err := erows.Scan(&e.Source, &e.Target); err != nil {
			return g, err
		}
		if present[e.Source] && present[e.Target] {
			g.Edges = append(g.Edges, e)
		}
	}
	if err := erows.Err(); err != nil {
		return g, err
	}

	if includeSimilar {
		sim, err := similarityEdges(ctx, q, present)
		if err != nil {
			return g, err
		}
		g.Edges = append(g.Edges, sim...)
	}
	return g, nil
}

// similarityEdges computes note-level vector-similarity edges: a note's vector
// is the mean of its chunks' embeddings; notes whose cosine similarity is at
// least simThreshold are connected, keeping the simTopK strongest neighbours
// per note. Brute-force O(n²) over note vectors — fine at personal-vault scale
// (see docs/05 §7 / ADR-010); skipped entirely above simMaxNotes.
func similarityEdges(ctx context.Context, q Queryer2, present map[int64]bool) ([]GraphEdge, error) {
	means, err := NoteMeanVectors(ctx, q, present)
	if err != nil {
		return nil, err
	}
	if len(means) < 2 || len(means) > simMaxNotes {
		return nil, nil
	}

	ids := make([]int64, 0, len(means))
	for id := range means {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	return similarityEdgesFromMeans(ids, means), nil
}

// similarityEdgesFromMeans is the O(n²) sweep over precomputed means.
func similarityEdgesFromMeans(ids []int64, means map[int64][]float32) []GraphEdge {

	type scored struct {
		a, b int64
		sim  float64
	}
	var candidates []scored
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			a, b := ids[i], ids[j]
			if len(means[a]) != len(means[b]) {
				continue
			}
			if s := Cosine(means[a], means[b]); s >= simThreshold {
				candidates = append(candidates, scored{a, b, s})
			}
		}
	}
	// Strongest first; keep an edge while either endpoint still has neighbour
	// capacity, so every note gets at most ~simTopK similarity edges.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sim != candidates[j].sim {
			return candidates[i].sim > candidates[j].sim
		}
		if candidates[i].a != candidates[j].a {
			return candidates[i].a < candidates[j].a
		}
		return candidates[i].b < candidates[j].b
	})
	degree := map[int64]int{}
	var out []GraphEdge
	for _, c := range candidates {
		if degree[c.a] >= simTopK && degree[c.b] >= simTopK {
			continue
		}
		degree[c.a]++
		degree[c.b]++
		out = append(out, GraphEdge{Source: c.a, Target: c.b, Kind: "similar", Sim: c.sim})
	}
	return out
}

// GrowthPoint is one cumulative vault-size sample (FR-60).
type GrowthPoint struct {
	Day   string `json:"day"`
	Notes int    `json:"notes"`
	Words int    `json:"words"`
}

// VaultGrowth returns cumulative note and word counts by note-creation date —
// the vault-growth-over-time series (FR-60). It is derived from the current
// notes table (each note attributed to its `created` date, falling back to
// last_indexed), not snapshotted, so it stays rebuildable from Markdown alone
// (ADR-006); notes with no usable date are omitted.
func VaultGrowth(ctx context.Context, q Queryer2) ([]GrowthPoint, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT substr(COALESCE(NULLIF(created,''), last_indexed, ''),1,10) AS day,
		        COUNT(*), COALESCE(SUM(word_count),0)
		   FROM notes GROUP BY day ORDER BY day;`)
	if err != nil {
		return nil, fmt.Errorf("vault growth: %w", err)
	}
	defer rows.Close()
	var out []GrowthPoint
	cumNotes, cumWords := 0, 0
	for rows.Next() {
		var day string
		var notes, words int
		if err := rows.Scan(&day, &notes, &words); err != nil {
			return nil, err
		}
		if day == "" {
			continue
		}
		cumNotes += notes
		cumWords += words
		out = append(out, GrowthPoint{Day: day, Notes: cumNotes, Words: cumWords})
	}
	return out, rows.Err()
}

// NoteMeanVectors returns each note's mean chunk vector. present (when
// non-nil) filters which notes are included. Best-effort: undecodable
// vectors and mixed-dimension chunks are skipped, mirroring the graph's
// long-standing behavior. Shared by the graph's similarity edges and the
// resurfacer (ADR-018).
func NoteMeanVectors(ctx context.Context, q Queryer2, present map[int64]bool) (map[int64][]float32, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT c.note_id, v.embedding FROM vec_chunks v
		   JOIN chunks c ON c.id = v.chunk_id
		  WHERE c.note_id IS NOT NULL;`)
	if err != nil {
		return nil, fmt.Errorf("note vectors: %w", err)
	}
	defer rows.Close()

	sums := map[int64][]float64{}
	counts := map[int64]int{}
	for rows.Next() {
		var noteID int64
		var blob []byte
		if err := rows.Scan(&noteID, &blob); err != nil {
			return nil, err
		}
		if present != nil && !present[noteID] {
			continue
		}
		vec, err := DecodeVector(blob)
		if err != nil {
			continue // skip undecodable vectors; similarity is best-effort
		}
		sum := sums[noteID]
		if sum == nil {
			sum = make([]float64, len(vec))
			sums[noteID] = sum
		}
		if len(sum) != len(vec) {
			continue // mixed dims (model change mid-reembed); skip mismatches
		}
		for i, f := range vec {
			sum[i] += float64(f)
		}
		counts[noteID]++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	means := make(map[int64][]float32, len(sums))
	for id, sum := range sums {
		mean := make([]float32, len(sum))
		for i, f := range sum {
			mean[i] = float32(f / float64(counts[id]))
		}
		means[id] = mean
	}
	return means, nil
}
