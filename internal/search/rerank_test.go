package search

import (
	"context"
	"errors"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/rerank"
)

func seedThree(t *testing.T) (*Searcher, context.Context) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	for i, txt := range []string{"alpha graph one", "beta graph two", "gamma graph three"} {
		nid, _ := db.UpsertNote(ctx, d, db.NoteRow{Path: string(rune('a'+i)) + ".md", Title: "N"})
		cid, _ := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &nid, Text: txt, ContentHash: "h" + txt})
		_ = db.InsertChunkFTS(ctx, d, cid, txt)
		_ = db.UpsertChunkVector(ctx, d, cid, "fake", []float32{1, 0, 0, 0})
	}
	return New(d, embeddings.NewFake()), ctx
}

func TestSearchReordersWithReranker(t *testing.T) {
	s, ctx := seedThree(t)
	baseline, err := s.Search(ctx, "graph", 3)
	if err != nil || len(baseline) != 3 {
		t.Fatalf("baseline hits=%d err=%v", len(baseline), err)
	}
	// A reranker that reverses order must flip the top result.
	s.WithReranker(&rerank.Fake{}, 3)
	reranked, err := s.Search(ctx, "graph", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(reranked) != 3 {
		t.Fatalf("reranked hits=%d", len(reranked))
	}
	if reranked[0].ChunkID != baseline[len(baseline)-1].ChunkID {
		t.Fatalf("reranker did not reorder: top=%d baseline-last=%d", reranked[0].ChunkID, baseline[len(baseline)-1].ChunkID)
	}
}

func TestSearchFallsBackWhenRerankerErrors(t *testing.T) {
	s, ctx := seedThree(t)
	baseline, _ := s.Search(ctx, "graph", 3)
	s.WithReranker(&rerank.Fake{Err: errors.New("ollama down")}, 3)
	got, err := s.Search(ctx, "graph", 3)
	if err != nil {
		t.Fatalf("rerank error must not fail search: %v", err)
	}
	if len(got) != len(baseline) || got[0].ChunkID != baseline[0].ChunkID {
		t.Fatalf("expected fallback to fused order; got top=%d want=%d", got[0].ChunkID, baseline[0].ChunkID)
	}
}
