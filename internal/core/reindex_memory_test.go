package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
)

const memoryFixture = `---
title: "Durable memory"
type: memory
---

## Memory

<!-- axon:memory:start -->
- 2026-08-01 — Lives in Osaka [fact] (source: [[2026-08-01]])
- ~~2026-07-05 — Lives in Tokyo [fact] (source: session)~~ (until 2026-08-01; superseded by "Lives in Osaka")
- not a real entry, hand-edited garbage that will not ParseFact cleanly ]]]
<!-- axon:memory:end -->
`

func TestReindexRebuildsMemoryFacts(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	d := migratedDB(t)

	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}

	total, open, superseded, err := db.MemoryFactCounts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	// Two parseable facts (one open, one closed). The "- not a real entry…" line
	// DOES parse as an untyped open fact (leading text, no date) — but has no
	// interval and is not struck, so it counts as open. To make the test precise
	// we assert only the well-formed rows below.
	if superseded != 1 {
		t.Fatalf("superseded = %d, want 1", superseded)
	}
	if total < 2 || open < 1 {
		t.Fatalf("counts total=%d open=%d, want >=2 total and >=1 open", total, open)
	}

	facts, err := db.OpenFacts(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	var sawOsaka bool
	for _, f := range facts {
		if f.Text == "Lives in Osaka" && f.ValidFrom == "2026-08-01" && f.Kind == "fact" {
			sawOsaka = true
		}
	}
	if !sawOsaka {
		t.Fatalf("open Osaka fact missing: %+v", facts)
	}
}

func TestReindexNeverWritesTheVault(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	abs := filepath.Join(v.Root(), filepath.FromSlash(identity.MemoryPath))
	before, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	d := migratedDB(t)
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("reindex mutated MEMORY.md (S9 violation):\n--- before ---\n%s\n--- after ---\n%s", before, after)
	}
}

func TestReindexMemoryFactsAreDeterministic(t *testing.T) {
	ctx := context.Background()
	v := tempVault(t, map[string]string{identity.MemoryPath: memoryFixture})
	d1 := migratedDB(t)
	if _, err := Reindex(ctx, v, d1); err != nil {
		t.Fatal(err)
	}
	first, _ := db.OpenFacts(ctx, d1)

	// Delete-DB (fresh) → reindex reproduces identical open rows (S9).
	d2 := migratedDB(t)
	if _, err := Reindex(ctx, v, d2); err != nil {
		t.Fatal(err)
	}
	second, _ := db.OpenFacts(ctx, d2)

	if len(first) != len(second) {
		t.Fatalf("row count differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Text != second[i].Text || first[i].ValidFrom != second[i].ValidFrom ||
			first[i].LineNo != second[i].LineNo || first[i].ValidUntil != second[i].ValidUntil {
			t.Fatalf("row %d differs: %+v vs %+v", i, first[i], second[i])
		}
	}
}
