// Package embeddings is the seam to the local embedding model. The production
// implementation calls Ollama; Phase 0 defines the contract and a deterministic
// fake. The interface is small so a different provider can be swapped in behind
// it without touching callers (see ADR-001).
package embeddings

import "context"

// Provider turns text into vectors. Dim must equal the length of every returned
// vector and must match the configured embeddings.dim; a mismatch forces a
// re-index. Implementations must be safe for concurrent use.
type Provider interface {
	// Embed returns one vector per input text, in order.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Model reports the embedding model identifier.
	Model() string
	// Dim reports the output vector dimension.
	Dim() int
}
