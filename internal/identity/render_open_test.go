package identity

import (
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestRecentEntriesExcludesSupersededFacts(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "Lives in Tokyo", Kind: "fact", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Reconcile(ctx, v, "Lives in Tokyo", "Lives in Osaka", "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	got, err := RecentEntries(ctx, v, 10)
	if err != nil {
		t.Fatal(err)
	}
	// The current open fact is injected; the closed Tokyo fact is excluded.
	// (Remember auto-creates the layer, which seeds an onboarding entry — also an
	// open fact — so we assert on content, not exact count.)
	var sawOsaka bool
	for _, line := range got {
		if strings.Contains(line, "Lives in Osaka") {
			sawOsaka = true
		}
		if strings.Contains(line, "~~") || strings.Contains(line, "until ") || strings.Contains(line, "Lives in Tokyo") {
			t.Fatalf("a closed fact leaked into injection: %q", line)
		}
	}
	if !sawOsaka {
		t.Fatalf("RecentEntries = %v, want the open Osaka fact present", got)
	}
}

func TestRecentEntriesIncludesLegacyUntypedLines(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	if _, err := Remember(ctx, v, Entry{Text: "An unrelated fact", Date: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	got, _ := RecentEntries(ctx, v, 10)
	// The legacy untyped line is open and must be included (alongside the
	// auto-seeded onboarding entry).
	var found bool
	for _, line := range got {
		if strings.Contains(line, "An unrelated fact") {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy untyped line should be included: %v", got)
	}
}

func TestRecentEntriesEmptyBlockUnchanged(t *testing.T) {
	ctx := context.Background()
	v := vault.NewFS(t.TempDir())
	got, err := RecentEntries(ctx, v, 10)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty block should yield no entries: %v, %v", got, err)
	}
}
