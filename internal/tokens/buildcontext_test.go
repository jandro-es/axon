package tokens

import (
	"context"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/search"
)

func TestBuildContextIsTokenBounded(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}

	// Seed a few searchable chunks.
	noteID, _ := db.UpsertNote(ctx, d, db.NoteRow{Path: "n.md", Title: "N"})
	for i, txt := range []string{
		"graph databases store nodes and edges",
		"vector search uses embeddings for similarity",
		"full text search uses an inverted index",
	} {
		cid, _ := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &noteID, Ordinal: i, Text: txt, ContentHash: txt})
		_ = db.InsertChunkFTS(ctx, d, cid, txt)
	}

	searcher := search.New(d, embeddings.NewFake())
	m := &manager{
		db:        d,
		searcher:  searcher,
		estimator: newCachingEstimator(HeuristicEstimator{}),
		cfg:       Config{Profile: "test", Limits: config.LimitsConfig{}},
		now:       func() time.Time { return time.Unix(0, 0) },
	}

	c, err := m.BuildContext(ctx, "search index", RetrieveOpts{TopK: 5, MaxContextTokens: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Messages) == 0 || c.Messages[0].Content == "" {
		t.Error("expected assembled context messages")
	}
	if c.Tokens == 0 {
		t.Error("expected a non-zero token estimate for the context")
	}
	if len(c.Sources) == 0 {
		t.Error("expected source note paths for citation")
	}
}

func TestBuildContextWithoutSearcherErrors(t *testing.T) {
	m := &manager{estimator: newCachingEstimator(HeuristicEstimator{})}
	if _, err := m.BuildContext(context.Background(), "q", RetrieveOpts{}); err == nil {
		t.Error("expected an error when no searcher is configured")
	}
}
