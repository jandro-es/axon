package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Run status values (mirrors docs/04).
const (
	RunRunning = "running"
	RunOK      = "ok"
	RunSkipped = "skipped"
	RunFailed  = "failed"
	RunDryRun  = "dry-run"
)

// InsertRun opens a runs row in the running state and returns its id.
func InsertRun(ctx context.Context, q Execer, automation, startedAt string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO runs (automation, started_at, status) VALUES (?, ?, ?);`,
		automation, startedAt, RunRunning)
	if err != nil {
		return 0, fmt.Errorf("open run for %q: %w", automation, err)
	}
	return res.LastInsertId()
}

// RunUpdate carries the terminal state of a run.
type RunUpdate struct {
	ID         int64
	Status     string
	FinishedAt string
	SkipReason string
	Changes    string
	Tokens     int64
	Error      string
}

// FinishRun records a run's terminal status and accounting.
func FinishRun(ctx context.Context, q Execer, u RunUpdate) error {
	_, err := q.ExecContext(ctx,
		`UPDATE runs SET status=?, finished_at=?, skip_reason=?, changes=?, tokens=?, error=? WHERE id=?;`,
		u.Status, u.FinishedAt, nullIfEmpty(u.SkipReason), nullIfEmpty(u.Changes), u.Tokens, nullIfEmpty(u.Error), u.ID)
	if err != nil {
		return fmt.Errorf("finish run %d: %w", u.ID, err)
	}
	return nil
}

// SumRunTokens returns the total input+output tokens ledgered against a run.
func SumRunTokens(ctx context.Context, q Queryer, runID int64) (int64, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens + output_tokens), 0) FROM token_ledger WHERE run_id = ?;`, runID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("sum run tokens %d: %w", runID, err)
	}
	return n.Int64, nil
}

// CountRuns returns the total number of run rows (probe for tests).
func CountRuns(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs;"))
}

// RunRecord is one row of the runs table, as read back for reporting.
type RunRecord struct {
	ID         int64  `json:"id"`
	Automation string `json:"automation"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Status     string `json:"status"`
	SkipReason string `json:"skip_reason,omitempty"`
	Tokens     int64  `json:"tokens"`
	Error      string `json:"error,omitempty"`
}

// LastRun returns the most recent run for an automation. found is false when the
// automation has never run.
func LastRun(ctx context.Context, q Queryer, automation string) (rec RunRecord, found bool, err error) {
	var finished, skip, errStr sql.NullString
	var tokens sql.NullInt64
	row := q.QueryRowContext(ctx,
		`SELECT id, automation, started_at, finished_at, status, skip_reason, tokens, error
		   FROM runs WHERE automation = ? ORDER BY id DESC LIMIT 1;`, automation)
	if err := row.Scan(&rec.ID, &rec.Automation, &rec.StartedAt, &finished, &rec.Status, &skip, &tokens, &errStr); err != nil {
		if err == sql.ErrNoRows {
			return RunRecord{}, false, nil
		}
		return RunRecord{}, false, fmt.Errorf("last run %q: %w", automation, err)
	}
	rec.FinishedAt = finished.String
	rec.SkipReason = skip.String
	rec.Tokens = tokens.Int64
	rec.Error = errStr.String
	return rec, true, nil
}

// LastRunStatus returns the status of the most recent run for an automation, or "".
func LastRunStatus(ctx context.Context, q Queryer, automation string) (string, error) {
	var status string
	err := q.QueryRowContext(ctx,
		`SELECT status FROM runs WHERE automation = ? ORDER BY id DESC LIMIT 1;`, automation).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("last run status %q: %w", automation, err)
	}
	return status, nil
}

// GetCursor returns the persisted change-gate cursor for an automation, or "".
func GetCursor(ctx context.Context, q Queryer, automation string) (string, error) {
	var cursor sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT cursor FROM automation_state WHERE automation = ?;`, automation).Scan(&cursor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get cursor %q: %w", automation, err)
	}
	return cursor.String, nil
}

// SetCursor persists the change-gate cursor for an automation.
func SetCursor(ctx context.Context, q Execer, automation, cursor, updated string) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO automation_state (automation, cursor, updated) VALUES (?, ?, ?)
		 ON CONFLICT(automation) DO UPDATE SET cursor=excluded.cursor, updated=excluded.updated;`,
		automation, cursor, updated)
	if err != nil {
		return fmt.Errorf("set cursor %q: %w", automation, err)
	}
	return nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
