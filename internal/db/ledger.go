package db

import (
	"context"
	"database/sql"
	"fmt"
)

// LedgerRow is one token_ledger entry: a single Claude call's accounting. On
// subscription/enterprise CostUSD is nil and the row counts toward the token
// window; in api_key mode CostUSD is populated.
type LedgerRow struct {
	TS           string
	Profile      string
	Operation    string
	Model        string
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
	EstInput     int
	CostUSD      *float64
	RunID        *int64
}

// InsertLedger records a Claude call and returns the new ledger id.
func InsertLedger(ctx context.Context, q Execer, r LedgerRow) (int64, error) {
	res, err := q.ExecContext(ctx,
		`INSERT INTO token_ledger
		   (ts, profile, operation, model, input_tokens, output_tokens, cache_read, cache_write, est_input, cost_usd, run_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);`,
		r.TS, r.Profile, r.Operation, r.Model, r.InputTokens, r.OutputTokens,
		r.CacheRead, r.CacheWrite, r.EstInput, r.CostUSD, r.RunID)
	if err != nil {
		return 0, fmt.Errorf("insert ledger: %w", err)
	}
	return res.LastInsertId()
}

// CountLedger returns the number of ledger rows (probe for tests/status).
func CountLedger(ctx context.Context, q Queryer) (int, error) {
	return scanCount(q.QueryRowContext(ctx, "SELECT COUNT(*) FROM token_ledger;"))
}

// BudgetWindow is the cached usage for a profile's day or week window.
type BudgetWindow struct {
	Profile     string
	Window      string // "day" | "week"
	PeriodStart string
	TokensUsed  int64
	CostUsed    float64
}

// GetBudgetWindow returns the usage for (profile, window) at periodStart. If the
// stored row is for a different (older) period, or no row exists, it reports
// zero usage — the window has rolled over.
func GetBudgetWindow(ctx context.Context, q Queryer, profile, window, periodStart string) (BudgetWindow, error) {
	bw := BudgetWindow{Profile: profile, Window: window, PeriodStart: periodStart}
	var storedStart string
	var tokens int64
	var cost float64
	err := q.QueryRowContext(ctx,
		`SELECT period_start, tokens_used, cost_used FROM budget_state WHERE profile = ? AND window = ?;`,
		profile, window).Scan(&storedStart, &tokens, &cost)
	if err == sql.ErrNoRows {
		return bw, nil
	}
	if err != nil {
		return bw, fmt.Errorf("get budget %s/%s: %w", profile, window, err)
	}
	if storedStart != periodStart {
		return bw, nil // rolled over: usage resets
	}
	bw.TokensUsed = tokens
	bw.CostUsed = cost
	return bw, nil
}

// AddBudgetUsage adds tokens/cost to a profile's window for the current period,
// resetting the window if the stored period differs (rollover). budget_state has
// one row per (profile, window).
func AddBudgetUsage(ctx context.Context, q DBTX, profile, window, periodStart string, tokens int64, cost float64) error {
	cur, err := GetBudgetWindow(ctx, q, profile, window, periodStart)
	if err != nil {
		return err
	}
	newTokens := cur.TokensUsed + tokens
	newCost := cur.CostUsed + cost
	if _, err := q.ExecContext(ctx,
		`INSERT INTO budget_state (profile, window, period_start, tokens_used, cost_used)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(profile, window) DO UPDATE SET
		   period_start=excluded.period_start, tokens_used=excluded.tokens_used, cost_used=excluded.cost_used;`,
		profile, window, periodStart, newTokens, newCost); err != nil {
		return fmt.Errorf("add budget usage %s/%s: %w", profile, window, err)
	}
	return nil
}
