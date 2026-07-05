// Package rerank reorders retrieval candidates by relevance to a query using a
// local model. It is a retrieval primitive (ADR-027): local, non-Claude, and
// OUTSIDE the token-manager chokepoint — like embeddings, it produces an
// ordering, never vault- or Claude-bound content. Reranking is best-effort;
// callers fall back to the original order on error.
package rerank

import (
	"context"
	"fmt"
	"strings"
)

// Candidate is one passage to score, with its original fused score for
// tie-breaking.
type Candidate struct {
	Text  string
	Score float64
}

// Reranker reorders retrieval candidates by relevance to a query.
type Reranker interface {
	// Rerank returns candidate indices best-first. On failure it returns an
	// error and the caller keeps the original order.
	Rerank(ctx context.Context, query string, cands []Candidate) ([]int, error)
	// Name identifies the reranker for diagnostics.
	Name() string
}

// RerankerFor builds the configured reranker, or nil when off. host is the
// Ollama server (embeddings.host or the default). A malformed value errors so
// wiring can leave reranking off and doctor can surface it.
func RerankerFor(rerank, host string) (Reranker, error) {
	switch {
	case rerank == "" || rerank == "off":
		return nil, nil
	case strings.HasPrefix(rerank, "ollama:"):
		model := strings.TrimSpace(strings.TrimPrefix(rerank, "ollama:"))
		if model == "" {
			return nil, fmt.Errorf("retrieval.rerank: ollama: needs a model name")
		}
		return NewOllamaReranker(host, model), nil
	default:
		return nil, fmt.Errorf("retrieval.rerank: unknown provider %q (use off or ollama:<model>)", rerank)
	}
}
