package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EvalRun is one row of eval_runs: the outcome of `axon eval` for one task
// family against one model ref on this machine (FR-142). DB-only, S9-exempt.
type EvalRun struct {
	Family   string
	ModelRef string
	Digest   string
	Passed   int
	Total    int
	PassPct  int
	RanAt    time.Time
}

// RecordEvalRun inserts one eval outcome.
func RecordEvalRun(ctx context.Context, ex Execer, r EvalRun) error {
	if _, err := ex.ExecContext(ctx,
		`INSERT INTO eval_runs (family, model_ref, digest, passed, total, pass_pct, ran_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?);`,
		r.Family, r.ModelRef, r.Digest, r.Passed, r.Total, r.PassPct,
		r.RanAt.UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("insert eval_run %s/%s: %w", r.Family, r.ModelRef, err)
	}
	return nil
}

// LatestEvalRun returns the most recent row for (family, modelRef); ok=false
// when none exists.
func LatestEvalRun(ctx context.Context, q Queryer, family, modelRef string) (EvalRun, bool, error) {
	var (
		r     EvalRun
		ranAt string
	)
	err := q.QueryRowContext(ctx,
		`SELECT family, model_ref, digest, passed, total, pass_pct, ran_at
		   FROM eval_runs
		  WHERE family = ? AND model_ref = ?
		  ORDER BY ran_at DESC, id DESC
		  LIMIT 1;`, family, modelRef).
		Scan(&r.Family, &r.ModelRef, &r.Digest, &r.Passed, &r.Total, &r.PassPct, &ranAt)
	if errors.Is(err, sql.ErrNoRows) {
		return EvalRun{}, false, nil
	}
	if err != nil {
		return EvalRun{}, false, fmt.Errorf("query latest eval_run: %w", err)
	}
	r.RanAt, _ = time.Parse(time.RFC3339, ranAt)
	return r, true, nil
}
