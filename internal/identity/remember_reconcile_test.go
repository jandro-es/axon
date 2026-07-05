package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestReconcileTombstonesOldAndPrependsNew(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "Prefers Go for daemons", Source: "session", Date: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	matched, err := Reconcile(ctx, v, "Prefers Go for daemons", "Uses Rust for daemons", "2026-07-05")
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("expected the old entry to be matched and struck")
	}
	body, _ := readBody(ctx, v, MemoryPath)
	block := extractBlock(body, MemoryBlock)
	if !strings.Contains(block, "~~2026-06-01 — Prefers Go for daemons") || !strings.Contains(block, "(superseded 2026-07-05)") {
		t.Fatalf("old entry not tombstoned:\n%s", block)
	}
	if !strings.Contains(block, "Uses Rust for daemons (source: reconcile)") {
		t.Fatalf("new entry not prepended:\n%s", block)
	}
	// New entry must come before the tombstone (newest-first).
	if strings.Index(block, "Uses Rust") > strings.Index(block, "Prefers Go") {
		t.Fatal("new entry should be prepended above the superseded one")
	}
}

func TestReconcileMissingOldStillAddsNew(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "An unrelated fact", Date: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	matched, err := Reconcile(ctx, v, "a fact that is not present", "A brand new fact", "2026-07-05")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("no existing entry matched; matched should be false")
	}
	body, _ := readBody(ctx, v, MemoryPath)
	if !strings.Contains(body, "A brand new fact (source: reconcile)") {
		t.Fatalf("new entry not added on the not-found path:\n%s", body)
	}
}
