package db

import "context"

// VectorIndex selects vector-search candidates for HybridSearch's semantic leg
// (ADR-025). Implementations return up to `limit` chunk ids in descending cosine
// similarity to query, plus the similarity for each returned id.
type VectorIndex interface {
	Candidates(ctx context.Context, q DBTX, query []float32, limit int) ([]int64, map[int64]float64, error)
}

// BruteIndex scores every stored vector exactly. It is the default and the
// source-of-truth fallback for IVFIndex.
type BruteIndex struct{}

// Candidates brute-forces cosine similarity over all stored vectors.
func (BruteIndex) Candidates(ctx context.Context, q DBTX, query []float32, limit int) ([]int64, map[int64]float64, error) {
	return vectorCandidates(ctx, q, query, limit)
}
