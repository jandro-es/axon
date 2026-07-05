package db

import (
	"context"
	"database/sql"
	"math"
	"testing"
)

// seedIVFCorpus plants n deterministic vectors in 8-dim space and returns them
// for query construction. chunk_id for row i is i+1 on a fresh DB.
func seedIVFCorpus(t *testing.T, d *sql.DB, n int) [][]float32 {
	t.Helper()
	ctx := context.Background()
	vecs := make([][]float32, n)
	for i := 0; i < n; i++ {
		v := make([]float32, 8)
		v[i%8] = 1 // cluster direction
		// A tiny per-index fingerprint across all dims makes every vector
		// unique, so exact-match queries have an unambiguous top-1.
		for j := 0; j < 8; j++ {
			v[j] += float32((i*31+j*7)%97) / 970.0
		}
		vecs[i] = v
		if _, err := d.ExecContext(ctx,
			`INSERT INTO chunks (note_id, ordinal, text, token_count, content_hash) VALUES (NULL,?,?,1,?)`,
			i, "t", "h"); err != nil {
			t.Fatal(err)
		}
		if err := UpsertChunkVector(ctx, d, int64(i+1), "m", v); err != nil {
			t.Fatal(err)
		}
	}
	return vecs
}

func TestIVFParityBelowThreshold(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	seedIVFCorpus(t, d, 50)
	if _, err := BuildIVF(ctx, d); err != nil {
		t.Fatal(err)
	}
	q := []float32{1, 0, 0, 0, 0, 0, 0, 0}
	// Threshold above corpus size → IVF must delegate to brute (identical).
	ivf, _, err := IVFIndex{Threshold: 1000, NProbe: 8}.Candidates(ctx, d, q, 10)
	if err != nil {
		t.Fatal(err)
	}
	brute, _, _ := BruteIndex{}.Candidates(ctx, d, q, 10)
	if !equalIDs(ivf, brute) {
		t.Fatalf("below-threshold not identical:\n ivf=%v\n brute=%v", ivf, brute)
	}
}

func TestIVFParityAtNProbeK(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	seedIVFCorpus(t, d, 200)
	k, err := BuildIVF(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	q := []float32{1, 0.2, 0, 0, 0, 0, 0, 0}
	// nprobe >= k visits every list → identical ordered ids to brute.
	ivf, _, _ := IVFIndex{Threshold: 1, NProbe: k}.Candidates(ctx, d, q, 10)
	brute, _, _ := BruteIndex{}.Candidates(ctx, d, q, 10)
	if !equalIDs(ivf, brute) {
		t.Fatalf("nprobe=k not identical:\n ivf=%v\n brute=%v", ivf, brute)
	}
}

func TestIVFRecallTop1(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	vecs := seedIVFCorpus(t, d, 300)
	if _, err := BuildIVF(ctx, d); err != nil {
		t.Fatal(err)
	}
	// Query = exact copy of vector #123 → true top-1 is chunk 124 (id = idx+1).
	q := vecs[123]
	ids, _, _ := IVFIndex{Threshold: 1, NProbe: 8}.Candidates(ctx, d, q, 1)
	if len(ids) != 1 || ids[0] != 124 {
		t.Fatalf("recall miss: got %v, want [124]", ids)
	}
}

func TestIVFOverflowScanned(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	vecs := seedIVFCorpus(t, d, 200)
	if _, err := BuildIVF(ctx, d); err != nil {
		t.Fatal(err)
	}
	// Insert a NEW near-duplicate of vec[10] AFTER the build; it stays
	// centroid=NULL (overflow) yet must be found by a probe.
	nv := append([]float32(nil), vecs[10]...)
	if _, err := d.ExecContext(ctx,
		`INSERT INTO chunks (note_id, ordinal, text, token_count, content_hash) VALUES (NULL,999,'n',1,'h9')`); err != nil {
		t.Fatal(err)
	}
	newID := int64(201)
	if err := UpsertChunkVector(ctx, d, newID, "m", nv); err != nil {
		t.Fatal(err)
	}
	ids, _, _ := IVFIndex{Threshold: 1, NProbe: 1}.Candidates(ctx, d, nv, 5)
	found := false
	for _, id := range ids {
		if id == newID {
			found = true
		}
	}
	if !found {
		t.Fatalf("overflow vector %d not found via probe: %v", newID, ids)
	}
}

func TestBuildIVFDeterministic(t *testing.T) {
	d := newMigratedDB(t)
	ctx := context.Background()
	seedIVFCorpus(t, d, 120)
	if _, err := BuildIVF(ctx, d); err != nil {
		t.Fatal(err)
	}
	first := dumpCentroids(t, d)
	if _, err := BuildIVF(ctx, d); err != nil {
		t.Fatal(err)
	}
	second := dumpCentroids(t, d)
	if len(first) != len(second) {
		t.Fatalf("centroid count changed: %d vs %d", len(first), len(second))
	}
	for i := range first {
		for j := range first[i] {
			if math.Abs(float64(first[i][j]-second[i][j])) > 1e-6 {
				t.Fatalf("centroid %d drifted between builds", i)
			}
		}
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func dumpCentroids(t *testing.T, d *sql.DB) [][]float32 {
	t.Helper()
	rows, err := d.QueryContext(context.Background(), `SELECT vector FROM vec_centroids ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out [][]float32
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		v, _ := DecodeVector(b)
		out = append(out, v)
	}
	return out
}
