package search

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
)

// seedRelated builds an in-memory DB with three single-chunk notes whose
// vectors have known cosine relationships to the target:
//
//	target.md → [1,0,0,0]
//	near.md   → [0.9,0.1,0,0]  (cosine ≈ 0.994, above the 0.3 floor)
//	far.md    → [0,0,1,0]      (cosine 0, below the floor → dropped)
func seedRelated(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	seed := func(path string, vec []float32) {
		id, err := db.UpsertNote(ctx, d, db.NoteRow{Path: path, Title: path})
		if err != nil {
			t.Fatal(err)
		}
		cid, err := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &id, Text: path + " body", ContentHash: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertChunkVector(ctx, d, cid, "fake", vec); err != nil {
			t.Fatal(err)
		}
	}
	seed("target.md", []float32{1, 0, 0, 0})
	seed("near.md", []float32{0.9, 0.1, 0, 0})
	seed("far.md", []float32{0, 0, 1, 0})
	return d
}

func TestRelatedRanksExcludesTargetAndFloors(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	s := New(d, embeddings.NewFake())

	got, err := s.Related(ctx, "target.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 related note (near.md), got %d: %+v", len(got), got)
	}
	if got[0].Path != "near.md" {
		t.Errorf("want near.md, got %q", got[0].Path)
	}
	if got[0].Similarity < 0.9 {
		t.Errorf("expected high similarity for near.md, got %.3f", got[0].Similarity)
	}
	for _, r := range got {
		if r.Path == "target.md" {
			t.Error("target note must be excluded from its own related list")
		}
		if r.Path == "far.md" {
			t.Error("far.md is below the floor and must be dropped")
		}
	}
}

func TestRelatedUnknownPathErrors(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	s := New(d, embeddings.NewFake())
	if _, err := s.Related(ctx, "nope.md", 5); err == nil {
		t.Fatal("expected an error for an unknown note path")
	}
}

func TestRelatedUnembeddedNoteIsEmptyNotError(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	// A note that exists but has no embedded chunk.
	if _, err := db.UpsertNote(ctx, d, db.NoteRow{Path: "bare.md", Title: "bare"}); err != nil {
		t.Fatal(err)
	}
	s := New(d, embeddings.NewFake())
	got, err := s.Related(ctx, "bare.md", 5)
	if err != nil {
		t.Fatalf("unembedded note should return empty, not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestRelatedAnnBackendMatchesBrute(t *testing.T) {
	ctx := context.Background()
	d := seedRelated(t)
	brute := New(d, embeddings.NewFake())
	// ann configured but below threshold ⇒ IVFIndex auto-falls back to brute
	// (the small-vault guarantee; bit-identical parity at nprobe≥k is proven
	// in the db/IVF tests, ADR-025). This asserts Related threads s.vindex().
	ann := New(d, embeddings.NewFake()).Configure(config.RetrievalConfig{
		Index: "ann", ANN: config.ANNConfig{Threshold: 1000, NProbe: 8},
	})
	gb, err := brute.Related(ctx, "target.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	ga, err := ann.Related(ctx, "target.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(gb) != len(ga) {
		t.Fatalf("brute %d vs ann %d results", len(gb), len(ga))
	}
	for i := range gb {
		if gb[i].Path != ga[i].Path {
			t.Errorf("order differs at %d: brute %q ann %q", i, gb[i].Path, ga[i].Path)
		}
	}
}
