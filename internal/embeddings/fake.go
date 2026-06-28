package embeddings

import (
	"context"
	"hash/fnv"
)

// Fake is a deterministic, dependency-free Provider for tests and the Phase 0
// skeleton. It derives each vector from a hash of the input text, so the same
// text always embeds to the same vector without contacting Ollama.
type Fake struct {
	ModelName string
	Dimension int
}

// NewFake returns a Fake matching the personal-profile default (nomic-embed-text,
// 768 dims).
func NewFake() *Fake {
	return &Fake{ModelName: "fake-embed", Dimension: 768}
}

// Embed returns one deterministic unit-ish vector per text.
func (f *Fake) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.vector(t)
	}
	return out, nil
}

func (f *Fake) vector(text string) []float32 {
	dim := f.Dim()
	v := make([]float32, dim)
	h := fnv.New64a()
	_, _ = h.Write([]byte(text))
	seed := h.Sum64()
	for i := range v {
		// Simple LCG-style spread so values vary across dimensions
		// deterministically; magnitude is irrelevant for the fake.
		seed = seed*6364136223846793005 + 1442695040888963407
		v[i] = float32(seed>>40) / float32(1<<24)
	}
	return v
}

// Model reports the fake model name.
func (f *Fake) Model() string {
	if f.ModelName == "" {
		return "fake-embed"
	}
	return f.ModelName
}

// Dim reports the configured dimension (defaulting to 768).
func (f *Fake) Dim() int {
	if f.Dimension <= 0 {
		return 768
	}
	return f.Dimension
}

// compile-time assertion that Fake satisfies Provider.
var _ Provider = (*Fake)(nil)
