package automations

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/identity"
)

func TestMemoryEntryText(t *testing.T) {
	cases := map[string]string{
		"- 2026-06-01 — Prefers Go for daemons [preference] (source: session)": "Prefers Go for daemons",
		"- 2026-06-01 — Keep the store brute-force":                            "Keep the store brute-force",
		"- 2026-07-05 — Uses Rust (source: reconcile)":                         "Uses Rust",
	}
	for in, want := range cases {
		if got := memoryEntryText(in); got != want {
			t.Errorf("memoryEntryText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDistillOutput(t *testing.T) {
	existing := []string{"Prefers Go for daemons", "Lives in Madrid"}

	newFacts, conflicts := parseDistillOutput(
		"- A fresh durable fact\nCONFLICT 1: Uses Rust for daemons\n",
		existing,
	)
	if !reflect.DeepEqual(newFacts, []string{"A fresh durable fact"}) {
		t.Errorf("newFacts = %v", newFacts)
	}
	if len(conflicts) != 1 || conflicts[0].New != "Uses Rust for daemons" || conflicts[0].Old != "Prefers Go for daemons" {
		t.Errorf("conflicts = %+v", conflicts)
	}

	// A fact echoed as both a bullet and a CONFLICT is handled once (as a conflict).
	nf, cf := parseDistillOutput("- Uses Rust for daemons\nCONFLICT 1: Uses Rust for daemons\n", existing)
	if len(nf) != 0 {
		t.Errorf("duplicate new fact not deduped: %v", nf)
	}
	if len(cf) != 1 {
		t.Errorf("expected 1 conflict, got %+v", cf)
	}

	// Out-of-range and garbage CONFLICT lines are ignored; NONE yields nothing.
	nf2, cf2 := parseDistillOutput("CONFLICT 9: nope\nCONFLICT x: bad\nNONE\n", existing)
	if len(nf2) != 0 || len(cf2) != 0 {
		t.Errorf("out-of-range/garbage not ignored: nf=%v cf=%+v", nf2, cf2)
	}
}

func TestMemoryDistillProposesReconcileNotSilentAdd(t *testing.T) {
	ctx := context.Background()
	rc, fake := newRC(t, map[string]string{
		"Daily/2026-06-28.md": "---\ntype: daily\n---\n## Log\n- migrated all daemons to Rust today\n",
	})
	// An existing memory entry the new fact contradicts.
	if _, err := identity.Remember(ctx, rc.Vault, identity.Entry{Text: "Prefers Go for daemons", Source: "session", Date: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	fake.RespondFn = func(r agent.Request) (*agent.Response, error) {
		return &agent.Response{Text: "CONFLICT 1: Uses Rust for daemons\n", Model: r.Model, Usage: agent.Usage{InputTokens: 60, OutputTokens: 8}}, nil
	}

	res, err := MemoryDistill{}.Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "proposed 1 reconciliation") {
		t.Errorf("summary = %q", res.Summary)
	}
	// The contradiction is in the queue, NOT silently added to memory.
	if !rc.Vault.Exists(".axon/review-queue.md") {
		t.Fatal("no review queue written")
	}
	q, _ := rc.Vault.Read(ctx, ".axon/review-queue.md")
	if !strings.Contains(q.Body, `reconcile: "Uses Rust for daemons" supersedes "Prefers Go for daemons"`) {
		t.Fatalf("reconcile line missing:\n%s", q.Body)
	}
	entries, _ := identity.RecentEntries(ctx, rc.Vault, 100)
	for _, e := range entries {
		if strings.Contains(e, "Uses Rust") {
			t.Fatalf("new fact was silently added to memory: %v", entries)
		}
	}

	// Re-run: proposal memory suppresses a duplicate queue line.
	if _, err := (MemoryDistill{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	q2, _ := rc.Vault.Read(ctx, ".axon/review-queue.md")
	if n := strings.Count(q2.Body, "reconcile: \"Uses Rust for daemons\""); n != 1 {
		t.Fatalf("expected exactly one reconcile line after re-run, got %d:\n%s", n, q2.Body)
	}
}
