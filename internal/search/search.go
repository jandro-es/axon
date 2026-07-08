// Package search is the read-layer entry point for hybrid retrieval over the
// vault + ingested knowledge. It embeds the query (best-effort) and fuses
// FTS5/bm25 lexical results with brute-force cosine vector results via the db
// repository (docs/05 §3). The MCP server and CLI both call it.
package search

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/rerank"
)

// Searcher runs hybrid searches. Embedder may be nil or unreachable, in which
// case search degrades to lexical-only (still useful).
type Searcher struct {
	DB       *sql.DB
	Embedder embeddings.Provider

	// Vector backend selection (ADR-025). Zero value ⇒ exact brute-force.
	IndexMode string
	Threshold int
	NProbe    int

	// Reranker, when non-nil, reorders a wider candidate pool (retrieval
	// primitive, ADR-027). Overfetch is the candidate multiple (≤0 ⇒ 3).
	Reranker  rerank.Reranker
	Overfetch int
}

// WithReranker attaches an optional local reranker and returns the receiver.
// overfetch is the candidate multiple fetched before reranking (≤0 ⇒ 3).
func (s *Searcher) WithReranker(r rerank.Reranker, overfetch int) *Searcher {
	s.Reranker = r
	s.Overfetch = overfetch
	return s
}

// Configure sets the vector backend from retrieval config and returns the
// receiver for chaining. Call sites that don't call it stay on brute-force.
func (s *Searcher) Configure(cfg config.RetrievalConfig) *Searcher {
	s.IndexMode = cfg.IndexMode()
	s.Threshold = cfg.ANN.ThresholdOr()
	s.NProbe = cfg.ANN.NProbeOr()
	return s
}

// vindex builds the db.VectorIndex for this searcher's configuration.
func (s *Searcher) vindex() db.VectorIndex {
	if s.IndexMode == "ann" {
		return db.IVFIndex{Threshold: s.Threshold, NProbe: s.NProbe}
	}
	return db.BruteIndex{}
}

// New constructs a Searcher.
func New(database *sql.DB, embedder embeddings.Provider) *Searcher {
	return &Searcher{DB: database, Embedder: embedder}
}

// Search returns the top-k hybrid results for a free-text query. When a
// reranker is configured it overfetches top-k×overfetch candidates, reorders
// them locally, and returns the top-k; any reranker failure falls back to the
// fused order (best-effort, never breaks search).
func (s *Searcher) Search(ctx context.Context, query string, topK int) ([]db.ChunkHit, error) {
	var qv []float32
	if s.Embedder != nil && strings.TrimSpace(query) != "" {
		if vecs, err := s.Embedder.Embed(ctx, []string{query}); err == nil && len(vecs) == 1 {
			qv = vecs[0]
		}
	}
	fetch := topK
	if s.Reranker != nil {
		of := s.Overfetch
		if of <= 0 {
			of = 3
		}
		fetch = topK * of
	}
	hits, err := db.HybridSearch(ctx, s.DB, db.SearchOpts{Query: query, QueryVector: qv, TopK: fetch, Index: s.vindex()})
	if err != nil {
		return nil, err
	}
	if s.Reranker == nil || len(hits) <= 1 {
		return clampHits(hits, topK), nil
	}
	cands := make([]rerank.Candidate, len(hits))
	for i, h := range hits {
		cands[i] = rerank.Candidate{Text: h.Snippet, Score: h.Score}
	}
	order, rerr := s.Reranker.Rerank(ctx, query, cands)
	if rerr != nil {
		return clampHits(hits, topK), nil // best-effort fallback to fused order
	}
	return clampHits(reorder(hits, order), topK), nil
}

// reorder applies an index permutation defensively: valid unseen indices first,
// then any leftover hits in original order (robust to partial/garbage input).
func reorder(hits []db.ChunkHit, order []int) []db.ChunkHit {
	out := make([]db.ChunkHit, 0, len(hits))
	seen := make([]bool, len(hits))
	for _, idx := range order {
		if idx < 0 || idx >= len(hits) || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, hits[idx])
	}
	for i, h := range hits {
		if !seen[i] {
			out = append(out, h)
		}
	}
	return out
}

// clampHits caps the slice to topK (topK ≤ 0 ⇒ unchanged).
func clampHits(hits []db.ChunkHit, topK int) []db.ChunkHit {
	if topK > 0 && len(hits) > topK {
		return hits[:topK]
	}
	return hits
}

// Retrieved is a token-bounded context block assembled from search hits, the
// standard way to feed a model without dumping the vault (docs/05 §3, FR-46).
// Phase 2 provides assembly + a character/token-estimate ceiling; the exact
// token accounting belongs to the token manager (Component 07, Phase 3).
type Retrieved struct {
	Hits    []db.ChunkHit
	Context string
	Sources []string
}

// Retrieve runs Search and packs snippets up to roughly maxContextTokens
// (estimated locally at ~4 chars/token), returning the assembled context and
// the distinct source note paths for citation/link-back.
func (s *Searcher) Retrieve(ctx context.Context, query string, topK, maxContextTokens int) (Retrieved, error) {
	hits, err := s.Search(ctx, query, topK)
	if err != nil {
		return Retrieved{}, err
	}
	budgetChars := maxContextTokens * 4
	var b strings.Builder
	seen := map[string]bool{}
	var sources []string
	used := 0
	for _, h := range hits {
		block := h.Snippet
		if h.Path != "" {
			block = h.Path + ": " + block
		}
		if used+len(block) > budgetChars && used > 0 {
			break
		}
		b.WriteString(block)
		b.WriteString("\n\n")
		used += len(block) + 2
		if h.Path != "" && !seen[h.Path] {
			seen[h.Path] = true
			sources = append(sources, h.Path)
		}
	}
	return Retrieved{Hits: hits, Context: strings.TrimSpace(b.String()), Sources: sources}, nil
}

// Related-notes surface (R8/FR-148): the top notes most similar to a given
// note, by pure vector math over the ANN seam — zero model calls, read-only.
const (
	relatedDefaultTopK    = 10  // default result count when topK ≤ 0
	relatedMinSimilarity  = 0.3 // cosine floor; drops clearly-unrelated notes
	relatedChunkOverfetch = 8   // chunk headroom so chunk→note dedup still fills topK
)

// RelatedNote is one entry in a note's related list.
type RelatedNote struct {
	Path       string  `json:"path"`
	Similarity float64 `json:"similarity"` // raw cosine in [-1,1], typically (0,1]
}

// Related returns the notes most similar to notePath, ranked by cosine over the
// note's mean chunk vector. It makes NO model call: the target's vector is read
// from the DB and matched against candidates through the ANN VectorIndex seam
// (ADR-025), which auto-falls back to exact brute below retrieval.ann.threshold.
// An unknown path is an error; a known-but-unembedded note returns an empty list.
func (s *Searcher) Related(ctx context.Context, notePath string, topK int) ([]RelatedNote, error) {
	if topK <= 0 {
		topK = relatedDefaultTopK
	}
	id, err := db.GetNoteIDByPath(ctx, s.DB, notePath)
	if err != nil {
		return nil, err
	}
	if id == nil {
		return nil, fmt.Errorf("note %q not found in the index (run `axon reindex`?)", notePath)
	}
	means, err := db.NoteMeanVectors(ctx, s.DB, map[int64]bool{*id: true})
	if err != nil {
		return nil, err
	}
	mean, ok := means[*id]
	if !ok || len(mean) == 0 {
		return nil, nil // note has no embedded chunks yet
	}
	// Overfetch chunk candidates (empty Query ⇒ vector-only) so chunk→note
	// collapse still yields topK distinct notes after excluding the target.
	hits, err := db.HybridSearch(ctx, s.DB, db.SearchOpts{
		QueryVector: mean,
		TopK:        (topK + 1) * relatedChunkOverfetch,
		Index:       s.vindex(),
	})
	if err != nil {
		return nil, err
	}
	best := map[string]float64{} // note path -> max cosine
	for _, h := range hits {
		if h.NoteID == nil || *h.NoteID == *id || h.Path == "" {
			continue // skip orphan chunks and the target's own chunks
		}
		if h.Vector > best[h.Path] {
			best[h.Path] = h.Vector
		}
	}
	out := make([]RelatedNote, 0, len(best))
	for path, sim := range best {
		if sim >= relatedMinSimilarity {
			out = append(out, RelatedNote{Path: path, Similarity: sim})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return out[i].Path < out[j].Path // stable tie-break
	})
	if len(out) > topK {
		out = out[:topK]
	}
	return out, nil
}
