package embeddings

import (
	"context"
	"testing"
)

func TestFakeEmbedDeterministicAndShaped(t *testing.T) {
	f := NewFake()
	ctx := context.Background()

	v1, err := f.Embed(ctx, []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(v1) != 2 {
		t.Fatalf("got %d vectors, want 2", len(v1))
	}
	for i, vec := range v1 {
		if len(vec) != f.Dim() {
			t.Errorf("vector %d has dim %d, want %d", i, len(vec), f.Dim())
		}
	}

	// Deterministic: same input -> same output.
	v2, _ := f.Embed(ctx, []string{"alpha", "beta"})
	for i := range v1 {
		for j := range v1[i] {
			if v1[i][j] != v2[i][j] {
				t.Fatalf("embedding not deterministic at [%d][%d]", i, j)
			}
		}
	}

	// Different inputs -> different vectors.
	if v1[0][0] == v1[1][0] && v1[0][1] == v1[1][1] {
		t.Error("distinct inputs produced suspiciously identical vectors")
	}
}

func TestFakeDimDefault(t *testing.T) {
	if got := (&Fake{}).Dim(); got != 768 {
		t.Errorf("default Dim = %d, want 768", got)
	}
}

func TestFakeRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewFake().Embed(ctx, []string{"x"}); err == nil {
		t.Error("expected context error")
	}
}
