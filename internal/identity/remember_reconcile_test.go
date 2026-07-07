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
	if !strings.Contains(block, "~~2026-06-01 — Prefers Go for daemons") ||
		!strings.Contains(block, `(until 2026-07-05; superseded by "Uses Rust for daemons")`) {
		t.Fatalf("old entry not tombstoned with interval:\n%s", block)
	}
	if !strings.Contains(block, "Uses Rust for daemons (source: reconcile)") {
		t.Fatalf("new entry not prepended:\n%s", block)
	}
	// New entry must come before the tombstone (newest-first).
	if strings.Index(block, "Uses Rust") > strings.Index(block, "Prefers Go") {
		t.Fatal("new entry should be prepended above the superseded one")
	}
}

func TestReconcileClosesIntervalWithSupersededBy(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "Lives in Tokyo", Kind: "fact", Source: "session", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	matched, err := Reconcile(ctx, v, "Lives in Tokyo", "Lives in Osaka", "2026-08-01")
	if err != nil || !matched {
		t.Fatalf("Reconcile matched=%v err=%v", matched, err)
	}
	body, _ := readBody(ctx, v, MemoryPath)
	block := extractBlock(body, MemoryBlock)

	// The old fact is tombstoned with the interval + superseded-by pointer.
	if !strings.Contains(block, `(until 2026-08-01; superseded by "Lives in Osaka")`) {
		t.Fatalf("interval not closed:\n%s", block)
	}
	// Parse the closed line and assert the interval fields.
	var closed, open bool
	for _, line := range parseEntries(block) {
		f, _ := ParseFact(line)
		if f.Struck && f.ValidUntil == "2026-08-01" && f.SupersededBy == "Lives in Osaka" {
			closed = true
		}
		if !f.Struck && f.Text == "Lives in Osaka" && f.ValidFrom == "2026-08-01" && f.Source == "reconcile" {
			open = true
		}
	}
	if !closed || !open {
		t.Fatalf("closed=%v open=%v\n%s", closed, open, block)
	}
	// New fact is prepended above the tombstone (newest-first).
	if strings.Index(block, "Lives in Osaka (source: reconcile)") > strings.Index(block, "~~2026-07-05") {
		t.Fatal("new open fact should be prepended above the superseded one")
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
