package db

import (
	"context"
	"testing"
)

// seedChunk inserts a note (if new), a chunk, its FTS row and optional vector.
func seedChunk(t *testing.T, d interface {
	Execer
	Queryer
	DBTX
}, path, text string, vec []float32) int64 {
	t.Helper()
	ctx := context.Background()
	noteID, err := UpsertNote(ctx, d, NoteRow{Path: path, Title: path})
	if err != nil {
		t.Fatal(err)
	}
	cid, err := InsertChunk(ctx, d, ChunkRow{NoteID: &noteID, Ordinal: 0, Text: text, ContentHash: "h:" + text})
	if err != nil {
		t.Fatal(err)
	}
	if err := InsertChunkFTS(ctx, d, cid, text); err != nil {
		t.Fatal(err)
	}
	if vec != nil {
		if err := UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
	}
	return cid
}

func TestHybridSearchLexical(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	seedChunk(t, d, "vec.md", "vector databases index embeddings for semantic search", nil)
	seedChunk(t, d, "cook.md", "a recipe for sourdough bread and pancakes", nil)

	hits, err := HybridSearch(ctx, d, SearchOpts{Query: "semantic vector search", TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 {
		t.Fatal("no lexical hits")
	}
	if hits[0].Path != "vec.md" {
		t.Errorf("top hit = %q, want vec.md", hits[0].Path)
	}
}

func TestHybridSearchVector(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	// Two orthogonal-ish vectors; query matches the first exactly.
	seedChunk(t, d, "a.md", "alpha content", []float32{1, 0, 0, 0})
	seedChunk(t, d, "b.md", "beta content", []float32{0, 1, 0, 0})

	hits, err := HybridSearch(ctx, d, SearchOpts{QueryVector: []float32{1, 0, 0, 0}, TopK: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "a.md" {
		t.Errorf("vector search top hit wrong: %+v", hits)
	}
	if hits[0].Vector <= hits[len(hits)-1].Vector && len(hits) > 1 {
		t.Errorf("expected a.md to have the higher cosine similarity")
	}
}

func TestHybridSearchFusesBothLegs(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	seedChunk(t, d, "match.md", "graph theory and network analysis", []float32{1, 0, 0, 0})
	seedChunk(t, d, "other.md", "unrelated cooking content", []float32{0, 1, 0, 0})

	hits, err := HybridSearch(ctx, d, SearchOpts{
		Query:       "network graph",
		QueryVector: []float32{1, 0, 0, 0},
		TopK:        5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "match.md" {
		t.Errorf("fused top hit wrong: %+v", hits)
	}
	if hits[0].Score <= 0 {
		t.Errorf("fused score should be positive, got %f", hits[0].Score)
	}
}

func TestFtsQuerySanitises(t *testing.T) {
	// A query full of FTS operators/punctuation must not error or inject syntax.
	ctx := context.Background()
	d := newMigratedDB(t)
	seedChunk(t, d, "n.md", "the answer is forty two", nil)

	if _, err := HybridSearch(ctx, d, SearchOpts{Query: `forty AND/OR "two" (*)`, TopK: 5}); err != nil {
		t.Errorf("punctuation-heavy query errored: %v", err)
	}
}

func TestEncodeDecodeVector(t *testing.T) {
	in := []float32{1.5, -2.25, 0, 3.125}
	out, err := DecodeVector(EncodeVector(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != len(in) {
		t.Fatalf("len = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("round-trip mismatch at %d: %v != %v", i, out[i], in[i])
		}
	}
}
