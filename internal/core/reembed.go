package core

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
)

// ReembedResult reports how many chunks were embedded.
type ReembedResult struct {
	Embedded int
	Total    int
}

// ReembedPending embeds chunks that have no stored vector (or all chunks when
// forceAll is set, e.g. after an embedding-model change). It is the worker
// behind `reindex --embeddings` and recovers vectors that were left pending when
// Ollama was unavailable during ingest. Embeddings are local and free (no Claude
// cost), so this respects the cardinal token rule trivially.
func ReembedPending(ctx context.Context, sqlDB *sql.DB, embedder embeddings.Provider, forceAll bool) (ReembedResult, error) {
	var res ReembedResult
	if embedder == nil {
		return res, fmt.Errorf("re-embed requires an embedding provider")
	}
	pending, err := db.ListPendingChunks(ctx, sqlDB, forceAll)
	if err != nil {
		return res, err
	}
	res.Total = len(pending)
	if len(pending) == 0 {
		return res, nil
	}

	texts := make([]string, len(pending))
	for i, c := range pending {
		texts[i] = c.Text
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		return res, fmt.Errorf("embed pending chunks: %w", err)
	}
	model := embedder.Model()
	for i, c := range pending {
		if i >= len(vecs) {
			break
		}
		if err := db.UpsertChunkVector(ctx, sqlDB, c.ID, model, vecs[i]); err != nil {
			return res, err
		}
		res.Embedded++
	}
	return res, nil
}

// EmbedPendingMemoryFacts fills embeddings for memory_facts rows that have none,
// best-effort (ADR-028): a nil embedder or an unreachable Ollama leaves them
// NULL — the interval/injection paths do not need them, and the next reindex
// with Ollama up backfills. Embeddings are local and free, so this respects the
// token rule trivially. Returns how many facts were embedded.
func EmbedPendingMemoryFacts(ctx context.Context, sqlDB *sql.DB, embedder embeddings.Provider) (int, error) {
	if embedder == nil {
		return 0, nil
	}
	pending, err := db.MemoryFactsMissingEmbedding(ctx, sqlDB)
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	texts := make([]string, len(pending))
	for i, f := range pending {
		texts[i] = f.Text
	}
	vecs, err := embedder.Embed(ctx, texts)
	if err != nil {
		return 0, nil // best-effort: Ollama down; leave embeddings NULL
	}
	n := 0
	for i, f := range pending {
		if i >= len(vecs) {
			break
		}
		if err := db.SetMemoryFactEmbedding(ctx, sqlDB, f.ID, vecs[i]); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
