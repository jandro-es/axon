package db

import (
	"context"
	"testing"
)

// BruteIndex must return the same ordered ids as the pre-refactor vector leg.
func TestBruteIndexCandidates(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	id1 := seedChunk(t, d, "a.md", "alpha", []float32{1, 0, 0})
	id2 := seedChunk(t, d, "b.md", "beta", []float32{0, 1, 0})
	id3 := seedChunk(t, d, "c.md", "gamma", []float32{0.9, 0.1, 0})

	ids, sims, err := BruteIndex{}.Candidates(ctx, d, []float32{1, 0, 0}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != id1 || ids[1] != id3 {
		t.Fatalf("ids = %v, want [%d %d]", ids, id1, id3)
	}
	if sims[id1] <= sims[id2] {
		t.Fatalf("chunk %d should outrank chunk %d: %v", id1, id2, sims)
	}
}
