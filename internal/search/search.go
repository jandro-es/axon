// Package search is the read-layer entry point for hybrid retrieval over the
// vault + ingested knowledge. It embeds the query (best-effort) and fuses
// FTS5/bm25 lexical results with brute-force cosine vector results via the db
// repository (docs/05 §3). The MCP server and CLI both call it.
package search

import (
	"context"
	"database/sql"
	"strings"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
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

// Search returns the top-k hybrid results for a free-text query.
func (s *Searcher) Search(ctx context.Context, query string, topK int) ([]db.ChunkHit, error) {
	var qv []float32
	if s.Embedder != nil && strings.TrimSpace(query) != "" {
		if vecs, err := s.Embedder.Embed(ctx, []string{query}); err == nil && len(vecs) == 1 {
			qv = vecs[0]
		}
	}
	return db.HybridSearch(ctx, s.DB, db.SearchOpts{Query: query, QueryVector: qv, TopK: topK, Index: s.vindex()})
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
