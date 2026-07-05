package db

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	ivfMinK       = 16
	ivfMaxK       = 4096
	ivfIterations = 15
)

// IVFIndex is an inverted-file (IVF-flat) approximate vector index (ADR-025).
// It probes only the NProbe nearest centroids' lists (plus the unassigned
// overflow), falling back to exact BruteIndex when the index is unbuilt or the
// corpus is smaller than Threshold.
type IVFIndex struct {
	Threshold int
	NProbe    int
}

// Candidates implements VectorIndex.
func (ix IVFIndex) Candidates(ctx context.Context, q DBTX, query []float32, limit int) ([]int64, map[int64]float64, error) {
	sims := map[int64]float64{}
	if len(query) == 0 {
		return nil, sims, nil
	}
	nCentroids, err := CountCentroids(ctx, q)
	if err != nil {
		return nil, sims, err
	}
	nVectors, err := CountVectors(ctx, q)
	if err != nil {
		return nil, sims, err
	}
	if nCentroids == 0 || nVectors < ix.Threshold {
		return BruteIndex{}.Candidates(ctx, q, query, limit)
	}

	cents, err := loadCentroids(ctx, q)
	if err != nil {
		return nil, sims, err
	}
	nprobe := ix.NProbe
	if nprobe < 1 {
		nprobe = 1
	}
	if nprobe > len(cents) {
		nprobe = len(cents)
	}
	// Rank centroids by cosine to the query, take the nprobe nearest ids.
	type cs struct {
		id  int64
		sim float64
	}
	ranked := make([]cs, len(cents))
	for i, c := range cents {
		ranked[i] = cs{c.id, Cosine(query, c.vec)}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].sim != ranked[j].sim {
			return ranked[i].sim > ranked[j].sim
		}
		return ranked[i].id < ranked[j].id
	})
	probeIDs := make([]int64, nprobe)
	for i := 0; i < nprobe; i++ {
		probeIDs[i] = ranked[i].id
	}

	// Scan the probed lists plus the always-included NULL overflow.
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(probeIDs)), ",")
	args := make([]any, len(probeIDs))
	for i, id := range probeIDs {
		args[i] = id
	}
	rows, err := q.QueryContext(ctx,
		`SELECT chunk_id, embedding FROM vec_chunks
		  WHERE centroid IN (`+placeholders+`) OR centroid IS NULL;`, args...)
	if err != nil {
		return nil, sims, fmt.Errorf("ivf probe: %w", err)
	}
	defer rows.Close()
	type scored struct {
		id  int64
		sim float64
	}
	var all []scored
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, sims, err
		}
		v, err := DecodeVector(blob)
		if err != nil {
			return nil, sims, err
		}
		s := Cosine(query, v)
		all = append(all, scored{id, s})
		sims[id] = s
	}
	if err := rows.Err(); err != nil {
		return nil, sims, err
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].sim != all[j].sim {
			return all[i].sim > all[j].sim
		}
		return all[i].id < all[j].id
	})
	if len(all) > limit {
		all = all[:limit]
	}
	ordered := make([]int64, len(all))
	for i, s := range all {
		ordered[i] = s.id
	}
	return ordered, sims, nil
}

type centroid struct {
	id  int64
	vec []float32
}

func loadCentroids(ctx context.Context, q DBTX) ([]centroid, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, vector FROM vec_centroids ORDER BY id;`)
	if err != nil {
		return nil, fmt.Errorf("load centroids: %w", err)
	}
	defer rows.Close()
	var out []centroid
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		v, err := DecodeVector(blob)
		if err != nil {
			return nil, err
		}
		out = append(out, centroid{id, v})
	}
	return out, rows.Err()
}

// BuildIVF (re)builds the IVF-flat index from every stored vector using
// deterministic spherical k-means, writing vec_centroids and every
// vec_chunks.centroid in one transaction. Returns the centroid count k.
func BuildIVF(ctx context.Context, sqlDB *sql.DB) (int, error) {
	rows, err := sqlDB.QueryContext(ctx, `SELECT chunk_id, model, embedding FROM vec_chunks ORDER BY chunk_id;`)
	if err != nil {
		return 0, fmt.Errorf("load vectors: %w", err)
	}
	var ids []int64
	var vecs [][]float32
	var model string
	for rows.Next() {
		var id int64
		var m string
		var blob []byte
		if err := rows.Scan(&id, &m, &blob); err != nil {
			rows.Close()
			return 0, err
		}
		v, err := DecodeVector(blob)
		if err != nil {
			rows.Close()
			return 0, err
		}
		if model == "" {
			model = m
		}
		ids = append(ids, id)
		vecs = append(vecs, normalize(v))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := len(vecs)
	if n == 0 {
		// Nothing to index: clear any stale centroids/assignments.
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return 0, err
		}
		defer func() { _ = tx.Rollback() }()
		if _, err := tx.ExecContext(ctx, `DELETE FROM vec_centroids;`); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE vec_chunks SET centroid = NULL;`); err != nil {
			return 0, err
		}
		return 0, tx.Commit()
	}

	k := clampK(n)
	dim := len(vecs[0])
	// Deterministic init: evenly strided vectors as seed centroids.
	cents := make([][]float32, k)
	for i := 0; i < k; i++ {
		src := vecs[(i*n)/k]
		cents[i] = append([]float32(nil), src...)
	}
	assign := make([]int, n)
	for iter := 0; iter < ivfIterations; iter++ {
		changed := false
		for i, v := range vecs {
			best, bestSim := 0, math.Inf(-1)
			for c := range cents {
				s := dot(v, cents[c])
				if s > bestSim {
					bestSim, best = s, c
				}
			}
			if assign[i] != best {
				assign[i] = best
				changed = true
			}
		}
		// Recompute centroids as normalized means; empty clusters keep their seed.
		sums := make([][]float32, k)
		counts := make([]int, k)
		for c := range sums {
			sums[c] = make([]float32, dim)
		}
		for i, v := range vecs {
			c := assign[i]
			counts[c]++
			for j := range v {
				sums[c][j] += v[j]
			}
		}
		for c := range cents {
			if counts[c] == 0 {
				continue
			}
			cents[c] = normalize(sums[c])
		}
		if !changed {
			break
		}
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM vec_centroids;`); err != nil {
		return 0, err
	}
	for c := range cents {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO vec_centroids (id, dim, model, vector) VALUES (?,?,?,?);`,
			c, dim, model, EncodeVector(cents[c])); err != nil {
			return 0, fmt.Errorf("insert centroid %d: %w", c, err)
		}
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE vec_chunks SET centroid = ? WHERE chunk_id = ?;`, assign[i], id); err != nil {
			return 0, fmt.Errorf("assign chunk %d: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit ivf build: %w", err)
	}
	return k, nil
}

// AssignPendingCentroids assigns every unassigned (overflow) vector to its
// nearest existing centroid. Cheap maintenance between full rebuilds; a no-op
// when the index is unbuilt. Returns the number reassigned.
func AssignPendingCentroids(ctx context.Context, sqlDB *sql.DB) (int, error) {
	cents, err := loadCentroids(ctx, sqlDB)
	if err != nil {
		return 0, err
	}
	if len(cents) == 0 {
		return 0, nil
	}
	// Pre-normalize centroids once.
	for i := range cents {
		cents[i].vec = normalize(cents[i].vec)
	}
	rows, err := sqlDB.QueryContext(ctx,
		`SELECT chunk_id, embedding FROM vec_chunks WHERE centroid IS NULL;`)
	if err != nil {
		return 0, err
	}
	type pend struct {
		id  int64
		vec []float32
	}
	var pending []pend
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			rows.Close()
			return 0, err
		}
		v, err := DecodeVector(blob)
		if err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, pend{id, normalize(v)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	count := 0
	for _, p := range pending {
		best, bestSim := cents[0].id, math.Inf(-1)
		for _, c := range cents {
			s := dot(p.vec, c.vec)
			if s > bestSim {
				bestSim, best = s, c.id
			}
		}
		if _, err := sqlDB.ExecContext(ctx,
			`UPDATE vec_chunks SET centroid = ? WHERE chunk_id = ?;`, best, p.id); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func clampK(n int) int {
	k := int(math.Round(math.Sqrt(float64(n))))
	if k < ivfMinK {
		k = ivfMinK
	}
	if k > ivfMaxK {
		k = ivfMaxK
	}
	if k > n {
		k = n
	}
	return k
}

func normalize(v []float32) []float32 {
	var norm float64
	for _, f := range v {
		norm += float64(f) * float64(f)
	}
	if norm == 0 {
		return append([]float32(nil), v...)
	}
	inv := 1.0 / math.Sqrt(norm)
	out := make([]float32, len(v))
	for i, f := range v {
		out[i] = float32(float64(f) * inv)
	}
	return out
}

func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
