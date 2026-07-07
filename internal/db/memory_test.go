package db

import (
	"context"
	"testing"
)

func TestReplaceMemoryFactsRoundTrip(t *testing.T) {
	ctx := context.Background()
	d, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := Migrate(d); err != nil {
		t.Fatal(err)
	}

	facts := []MemoryFact{
		{Text: "Lives in Osaka", Kind: "fact", Source: "[[2026-08-01]]", ValidFrom: "2026-08-01", LineNo: 0, Updated: "2026-08-01"},
		{Text: "Lives in Tokyo", Kind: "fact", Source: "session", ValidFrom: "2026-07-05", ValidUntil: "2026-08-01", SupersededBy: "Lives in Osaka", Struck: true, LineNo: 1, Updated: "2026-08-01"},
	}
	if err := ReplaceMemoryFacts(ctx, d, facts); err != nil {
		t.Fatal(err)
	}

	total, open, superseded, err := MemoryFactCounts(ctx, d)
	if err != nil || total != 2 || open != 1 || superseded != 1 {
		t.Fatalf("counts = (%d,%d,%d) err=%v, want (2,1,1)", total, open, superseded, err)
	}

	got, err := OpenFacts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "Lives in Osaka" || got[0].ValidUntil != "" || got[0].Struck {
		t.Fatalf("OpenFacts = %+v, want only the open Osaka fact", got)
	}
	if got[0].Kind != "fact" || got[0].Source != "[[2026-08-01]]" || got[0].ValidFrom != "2026-08-01" {
		t.Fatalf("open fact fields lost: %+v", got[0])
	}
}

func TestReplaceMemoryFactsIsDeleteAllThenInsert(t *testing.T) {
	ctx := context.Background()
	d, err := Open(MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := Migrate(d); err != nil {
		t.Fatal(err)
	}
	first := []MemoryFact{{Text: "old", ValidFrom: "2026-01-01", LineNo: 0, Updated: "2026-01-01"}}
	if err := ReplaceMemoryFacts(ctx, d, first); err != nil {
		t.Fatal(err)
	}
	// A second replace fully supersedes the first — no accumulation, exact set.
	second := []MemoryFact{{Text: "new", ValidFrom: "2026-02-02", LineNo: 0, Updated: "2026-02-02"}}
	if err := ReplaceMemoryFacts(ctx, d, second); err != nil {
		t.Fatal(err)
	}
	total, _, _, _ := MemoryFactCounts(ctx, d)
	if total != 1 {
		t.Fatalf("total after re-replace = %d, want 1 (delete-all+insert)", total)
	}
	open, _ := OpenFacts(ctx, d)
	if len(open) != 1 || open[0].Text != "new" {
		t.Fatalf("re-replace did not reproduce exact set: %+v", open)
	}
}
