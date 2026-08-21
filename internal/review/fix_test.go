package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func fixQueueVault(t *testing.T, line string) *vault.FS {
	t.Helper()
	v := vault.NewFS(t.TempDir())
	if err := v.Append(".axon/review-queue.md", "## Self-check (2026-08-21)\n"+line); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestFixKindParsesAndAcceptsWithoutMutating(t *testing.T) {
	const line = "- [ ] fix service-path — \"service unit PATH cannot resolve claude\" → `axon service reinstall`\n"
	v := fixQueueVault(t, line)
	ctx := context.Background()

	items, err := Load(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	var it Item
	for _, c := range items {
		if c.Kind == "fix" {
			it = c
		}
	}
	if it.ID == "" {
		t.Fatalf("fix kind not parsed from %q; got %+v", line, items)
	}
	if it.Note != "service-path" {
		t.Fatalf("Note must be the check name, got %q", it.Note)
	}
	if it.Target != "axon service reinstall" {
		t.Fatalf("Target must be the remediation command, got %q", it.Target)
	}

	// Accept acknowledges and mutates NOTHING outside the queue line — the
	// single most important property of this kind (FR-207).
	before, err := os.ReadDir(v.Root())
	if err != nil {
		t.Fatal(err)
	}
	got, err := Accept(ctx, v, it.ID)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if got.Kind != "fix" {
		t.Fatalf("accepted the wrong item: %+v", got)
	}
	after, err := os.ReadDir(v.Root())
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Fatal("accept created or removed a vault entry — it must acknowledge only")
	}
	q, err := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(q), "✓ noted") {
		t.Fatalf("accept must mark the line noted:\n%s", q)
	}
}

func TestFixKindSurvivesQuotesAndBackticksAfterSanitization(t *testing.T) {
	// What the automation renders after sanitizing: quotes downgraded to
	// single, backticks stripped from the command.
	const line = "- [ ] fix claude-cli — \"claude 'CLI' not found on PATH\" → `brew install claude`\n"
	v := fixQueueVault(t, line)
	items, err := Load(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range items {
		if c.Kind == "fix" {
			if c.Note != "claude-cli" || c.Target != "brew install claude" {
				t.Fatalf("round-trip wrong: %+v", c)
			}
			return
		}
	}
	t.Fatalf("fix kind not parsed: %+v", items)
}
