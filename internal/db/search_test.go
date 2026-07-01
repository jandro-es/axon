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

func TestDeleteNoteCleansFTSAndSearchSurvives(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	seedChunk(t, d, "dead.md", "ephemeral quantum widget content", nil)
	seedChunk(t, d, "alive.md", "durable quantum widget content", nil)

	deadID, err := GetNoteIDByPath(ctx, d, "dead.md")
	if err != nil || deadID == nil {
		t.Fatalf("note id for dead.md: %v %v", deadID, err)
	}
	if err := DeleteNote(ctx, d, *deadID); err != nil {
		t.Fatal(err)
	}

	// The FTS mirror must not retain rows for the deleted note's chunks.
	var orphans int
	if err := d.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM fts_chunks WHERE chunk_id NOT IN (SELECT id FROM chunks);`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("orphaned fts_chunks rows after DeleteNote = %d, want 0", orphans)
	}

	// Search matching the deleted content must not error and must only return
	// the surviving note.
	hits, err := HybridSearch(ctx, d, SearchOpts{Query: "quantum widget", TopK: 5})
	if err != nil {
		t.Fatalf("search after DeleteNote errored: %v", err)
	}
	for _, h := range hits {
		if h.Path == "dead.md" {
			t.Errorf("deleted note still surfaced in search: %+v", h)
		}
	}
	if len(hits) != 1 {
		t.Errorf("hits = %d, want 1 (alive.md only): %+v", len(hits), hits)
	}
}

func TestHybridSearchSkipsOrphanedIndexEntries(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	cid := seedChunk(t, d, "gone.md", "orphaned searchable text", nil)
	seedChunk(t, d, "kept.md", "orphaned but kept text", nil)

	// Simulate historical corruption: chunk deleted, FTS row left behind
	// (pre-fix DeleteNote behavior, or a crash between the two deletes).
	if _, err := d.ExecContext(ctx, `DELETE FROM chunks WHERE id = ?;`, cid); err != nil {
		t.Fatal(err)
	}

	hits, err := HybridSearch(ctx, d, SearchOpts{Query: "orphaned text", TopK: 5})
	if err != nil {
		t.Fatalf("search over orphaned FTS row errored: %v", err)
	}
	if len(hits) != 1 || hits[0].Path != "kept.md" {
		t.Errorf("hits = %+v, want exactly kept.md", hits)
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
