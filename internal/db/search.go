package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// rrfK is the reciprocal-rank-fusion constant; 60 is the value from the original
// RRF paper and a sane default for blending lexical and semantic rankings.
const rrfK = 60.0

// SearchOpts parameterises a hybrid search. QueryVector may be nil (lexical
// only, e.g. when embeddings are unavailable); Query may be empty (vector only).
type SearchOpts struct {
	Query       string
	QueryVector []float32
	TopK        int
}

// ChunkHit is one fused search result.
type ChunkHit struct {
	ChunkID int64
	NoteID  *int64
	Path    string
	Snippet string
	Lexical float64 // bm25 score (lower is a better lexical match); 0 if not matched lexically
	Vector  float64 // cosine similarity in [-1,1]; 0 if not matched semantically
	Score   float64 // fused reciprocal-rank score (higher is better)
}

// HybridSearch fuses FTS5/bm25 lexical results with brute-force cosine vector
// results via reciprocal rank fusion (docs/05 §3). Either leg may be empty;
// results are hydrated with note path and a snippet.
func HybridSearch(ctx context.Context, q DBTX, opts SearchOpts) ([]ChunkHit, error) {
	if opts.TopK <= 0 {
		opts.TopK = 8
	}
	// Pull a deeper candidate pool per leg than TopK so fusion has material.
	pool := opts.TopK * 4

	lex, lexScore, err := lexicalCandidates(ctx, q, opts.Query, pool)
	if err != nil {
		return nil, err
	}
	vec, vecScore, err := vectorCandidates(ctx, q, opts.QueryVector, pool)
	if err != nil {
		return nil, err
	}

	// Reciprocal rank fusion across the two ranked lists.
	fused := map[int64]float64{}
	for rank, id := range lex {
		fused[id] += 1.0 / (rrfK + float64(rank+1))
	}
	for rank, id := range vec {
		fused[id] += 1.0 / (rrfK + float64(rank+1))
	}

	ids := make([]int64, 0, len(fused))
	for id := range fused {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if fused[ids[i]] != fused[ids[j]] {
			return fused[ids[i]] > fused[ids[j]]
		}
		return ids[i] < ids[j] // stable tie-break
	})
	if len(ids) > opts.TopK {
		ids = ids[:opts.TopK]
	}

	hits := make([]ChunkHit, 0, len(ids))
	for _, id := range ids {
		h, err := hydrateChunk(ctx, q, id)
		if errors.Is(err, sql.ErrNoRows) {
			// An index entry outlived its chunk (orphaned FTS row). Skip it
			// rather than failing the whole search.
			continue
		}
		if err != nil {
			return nil, err
		}
		h.Score = fused[id]
		h.Lexical = lexScore[id]
		h.Vector = vecScore[id]
		hits = append(hits, h)
	}
	return hits, nil
}

// lexicalCandidates runs an FTS5 bm25 search and returns chunk ids in rank order
// plus a map of chunk id -> bm25 score.
func lexicalCandidates(ctx context.Context, q DBTX, query string, limit int) ([]int64, map[int64]float64, error) {
	scores := map[int64]float64{}
	match := ftsQuery(query)
	if match == "" {
		return nil, scores, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT chunk_id, bm25(fts_chunks) AS rank
		   FROM fts_chunks WHERE fts_chunks MATCH ?
		   ORDER BY rank LIMIT ?;`, match, limit)
	if err != nil {
		return nil, scores, fmt.Errorf("fts search: %w", err)
	}
	defer rows.Close()
	var ordered []int64
	for rows.Next() {
		var id int64
		var score float64
		if err := rows.Scan(&id, &score); err != nil {
			return nil, scores, err
		}
		ordered = append(ordered, id)
		scores[id] = score
	}
	return ordered, scores, rows.Err()
}

// vectorCandidates brute-forces cosine similarity over all stored vectors and
// returns the top chunk ids in descending similarity, plus the similarity map.
func vectorCandidates(ctx context.Context, q DBTX, query []float32, limit int) ([]int64, map[int64]float64, error) {
	sims := map[int64]float64{}
	if len(query) == 0 {
		return nil, sims, nil
	}
	rows, err := q.QueryContext(ctx, `SELECT chunk_id, embedding FROM vec_chunks;`)
	if err != nil {
		return nil, sims, fmt.Errorf("scan vectors: %w", err)
	}
	defer rows.Close()
	type scored struct {
		id  int64
		sim float64
	}
	var all []scored
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, sims, err
		}
		v, err := DecodeVector(blob)
		if err != nil {
			return nil, sims, err
		}
		s := Cosine(query, v)
		all = append(all, scored{id, s})
		sims[id] = s
	}
	if err := rows.Err(); err != nil {
		return nil, sims, err
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].sim != all[j].sim {
			return all[i].sim > all[j].sim
		}
		return all[i].id < all[j].id
	})
	if len(all) > limit {
		all = all[:limit]
	}
	ordered := make([]int64, len(all))
	for i, s := range all {
		ordered[i] = s.id
	}
	return ordered, sims, nil
}

// hydrateChunk loads a chunk's note path and a snippet of its text.
func hydrateChunk(ctx context.Context, q DBTX, chunkID int64) (ChunkHit, error) {
	row := q.QueryRowContext(ctx,
		`SELECT c.note_id, c.text, n.path
		   FROM chunks c LEFT JOIN notes n ON n.id = c.note_id
		  WHERE c.id = ?;`, chunkID)
	var noteID sql.NullInt64
	var text string
	var path sql.NullString
	if err := row.Scan(&noteID, &text, &path); err != nil {
		return ChunkHit{}, fmt.Errorf("hydrate chunk %d: %w", chunkID, err)
	}
	h := ChunkHit{ChunkID: chunkID, Snippet: snippet(text)}
	if noteID.Valid {
		id := noteID.Int64
		h.NoteID = &id
	}
	if path.Valid {
		h.Path = path.String
	}
	return h, nil
}

// ftsWordRe extracts search terms; punctuation and FTS operators are dropped so
// a free-text user query never produces an FTS5 syntax error.
var ftsWordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// ftsQuery turns free text into a safe FTS5 MATCH expression: each token is
// quoted and the tokens are OR-ed for recall. Returns "" if there are no terms.
func ftsQuery(query string) string {
	words := ftsWordRe.FindAllString(strings.ToLower(query), -1)
	if len(words) == 0 {
		return ""
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + w + `"`
	}
	return strings.Join(quoted, " OR ")
}

// snippet returns a short single-line preview of chunk text.
func snippet(text string) string {
	s := strings.Join(strings.Fields(text), " ")
	const max = 200
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
