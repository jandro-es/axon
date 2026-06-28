package db

import (
	"context"
	"testing"
)

func TestInsertAndCountLedger(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	cost := 0.0125
	id, err := InsertLedger(ctx, d, LedgerRow{
		TS: "2026-06-28T12:00:00Z", Profile: "p", Operation: "ingest.enrich", Model: "sonnet",
		InputTokens: 1000, OutputTokens: 500, EstInput: 900, CostUSD: &cost,
	})
	if err != nil || id == 0 {
		t.Fatalf("InsertLedger = (%d, %v)", id, err)
	}
	if n, _ := CountLedger(ctx, d); n != 1 {
		t.Errorf("CountLedger = %d, want 1", n)
	}
}

func TestBudgetWindowRollover(t *testing.T) {
	ctx := context.Background()
	d := newMigratedDB(t)

	// Day 1 usage.
	if err := AddBudgetUsage(ctx, d, "p", "day", "2026-06-28", 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := AddBudgetUsage(ctx, d, "p", "day", "2026-06-28", 50, 0); err != nil {
		t.Fatal(err)
	}
	bw, _ := GetBudgetWindow(ctx, d, "p", "day", "2026-06-28")
	if bw.TokensUsed != 150 {
		t.Errorf("day-1 used = %d, want 150", bw.TokensUsed)
	}

	// New period resets the window.
	got, _ := GetBudgetWindow(ctx, d, "p", "day", "2026-06-29")
	if got.TokensUsed != 0 {
		t.Errorf("new-period used = %d, want 0 (rolled over)", got.TokensUsed)
	}
	if err := AddBudgetUsage(ctx, d, "p", "day", "2026-06-29", 7, 0); err != nil {
		t.Fatal(err)
	}
	bw2, _ := GetBudgetWindow(ctx, d, "p", "day", "2026-06-29")
	if bw2.TokensUsed != 7 {
		t.Errorf("day-2 used = %d, want 7 (window reset, not accumulated)", bw2.TokensUsed)
	}
}
