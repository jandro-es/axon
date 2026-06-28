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
