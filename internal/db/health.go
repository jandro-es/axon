package db

import (
	"context"
	"database/sql"
	"fmt"
)

// LatestSourceFetch returns the most recent sources.fetched_at, or "" if there
// are no ingested sources. Used as a "knowledge freshness" signal.
func LatestSourceFetch(ctx context.Context, q Queryer) (string, error) {
	return maxTimestamp(ctx, q, "SELECT MAX(fetched_at) FROM sources;")
}

// LatestRunFinish returns the most recent runs.finished_at across all
// automations, or "" if nothing has run. Used as a "knowledge freshness" signal.
func LatestRunFinish(ctx context.Context, q Queryer) (string, error) {
	return maxTimestamp(ctx, q, "SELECT MAX(finished_at) FROM runs WHERE status != 'running';")
}

func maxTimestamp(ctx context.Context, q Queryer, query string) (string, error) {
	var ts sql.NullString
	if err := q.QueryRowContext(ctx, query).Scan(&ts); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("max timestamp: %w", err)
	}
	return ts.String, nil
}
