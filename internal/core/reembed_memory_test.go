package core

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/identity"
)

func TestEmbedPendingMemoryFacts(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	d := migratedDB(t)
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}

	// Nil embedder is a no-op (Ollama down → embeddings stay NULL, best-effort).
	if n, err := EmbedPendingMemoryFacts(ctx, d, nil); err != nil || n != 0 {
		t.Fatalf("nil embedder = (%d, %v), want (0, nil)", n, err)
	}

	// A fake embedder fills every pending fact.
	n, err := EmbedPendingMemoryFacts(ctx, d, embeddings.NewFake())
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected at least one fact embedded")
	}
	// Re-running finds nothing pending.
	if again, err := EmbedPendingMemoryFacts(ctx, d, embeddings.NewFake()); err != nil || again != 0 {
		t.Fatalf("second pass = (%d, %v), want (0, nil)", again, err)
	}

	facts, _ := db.OpenFacts(ctx, d)
	for _, f := range facts {
		if len(f.Embedding) == 0 {
			t.Fatalf("fact %q left without embedding", f.Text)
		}
	}
}
