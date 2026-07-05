package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

// openCoreTestDB opens a migrated DB in a temp dir.
func openCoreTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// seedCoreVectors inserts count chunk+vector rows with deterministic 8-dim
// vectors (chunk_id = i+1 on a fresh DB).
func seedCoreVectors(t *testing.T, d *sql.DB, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		v := make([]float32, 8)
		v[i%8] = 1
		if _, err := d.ExecContext(ctx,
			`INSERT INTO chunks (note_id, ordinal, text, token_count, content_hash) VALUES (NULL,?,?,1,?)`,
			i, "t", "h"); err != nil {
			t.Fatal(err)
		}
		if err := db.UpsertChunkVector(ctx, d, int64(i+1), "m", v); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRefreshVectorIndexBuildsWhenAnn(t *testing.T) {
	d := openCoreTestDB(t)
	ctx := context.Background()
	seedCoreVectors(t, d, 40)

	if err := RefreshVectorIndex(ctx, d, config.RetrievalConfig{Index: "brute"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountCentroids(ctx, d); n != 0 {
		t.Fatalf("brute mode built %d centroids, want 0", n)
	}

	if err := RefreshVectorIndex(ctx, d, config.RetrievalConfig{Index: "ann"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.CountCentroids(ctx, d); n == 0 {
		t.Fatal("ann mode built 0 centroids")
	}
}
