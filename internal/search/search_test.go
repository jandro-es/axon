package search

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
)

func TestSearcherSearch(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}

	// Seed one searchable chunk with a vector.
	noteID, _ := db.UpsertNote(ctx, d, db.NoteRow{Path: "n.md", Title: "Note"})
	cid, _ := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &noteID, Text: "graph databases and traversal", ContentHash: "h"})
	_ = db.InsertChunkFTS(ctx, d, cid, "graph databases and traversal")
	_ = db.UpsertChunkVector(ctx, d, cid, "fake", []float32{1, 0, 0, 0})

	s := New(d, embeddings.NewFake())
	hits, err := s.Search(ctx, "graph traversal", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].Path != "n.md" {
		t.Errorf("search hits = %+v", hits)
	}
}

func TestRetrieveBoundsContext(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	noteID, _ := db.UpsertNote(ctx, d, db.NoteRow{Path: "n.md"})
	cid, _ := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &noteID, Text: "alpha beta gamma delta", ContentHash: "h"})
	_ = db.InsertChunkFTS(ctx, d, cid, "alpha beta gamma delta")

	s := New(d, nil) // lexical-only (no embedder)
	r, err := s.Retrieve(ctx, "alpha", 5, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if r.Context == "" || len(r.Sources) == 0 {
		t.Errorf("expected assembled context + sources, got %+v", r)
	}
}
