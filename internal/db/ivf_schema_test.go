package db

import (
	"context"
	"testing"
)

func TestMigration0004AddsIVFSchema(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()

	// centroid column exists on vec_chunks (chunk id 1 on a fresh DB).
	if _, err := d.ExecContext(ctx,
		`INSERT INTO chunks (note_id, ordinal, text, token_count, content_hash) VALUES (NULL, 0, 'x', 1, 'h')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ExecContext(ctx,
		`INSERT INTO vec_chunks (chunk_id, dim, model, embedding, centroid) VALUES (1, 1, 'm', X'00000000', 3)`); err != nil {
		t.Fatalf("centroid column missing: %v", err)
	}
	// vec_centroids table exists.
	if _, err := d.ExecContext(ctx,
		`INSERT INTO vec_centroids (id, dim, model, vector) VALUES (0, 1, 'm', X'00000000')`); err != nil {
		t.Fatalf("vec_centroids table missing: %v", err)
	}
	n, err := CountCentroids(ctx, d)
	if err != nil || n != 1 {
		t.Fatalf("CountCentroids = %d, err=%v, want 1", n, err)
	}
}
