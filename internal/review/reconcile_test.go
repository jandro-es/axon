package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/vault"
)

const reconcileQueue = `# Review queue

## Memory reconciliation (2026-07-05 05:00)
- [ ] reconcile: "Uses Rust for daemons" supersedes "Prefers Go for daemons"
`

func reconcileVault(t *testing.T) *vault.FS {
	t.Helper()
	v := vault.NewFS(t.TempDir())
	if err := os.MkdirAll(filepath.Join(v.Root(), ".axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root(), ".axon", "review-queue.md"), []byte(reconcileQueue), 0o644); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestLoadParsesReconcile(t *testing.T) {
	items, err := Load(context.Background(), reconcileVault(t))
	if err != nil {
		t.Fatal(err)
	}
	var it *Item
	for i := range items {
		if items[i].Kind == "reconcile" {
			it = &items[i]
		}
	}
	if it == nil {
		t.Fatalf("reconcile item not parsed: %+v", items)
	}
	if it.Note != "Uses Rust for daemons" || it.Target != "Prefers Go for daemons" {
		t.Fatalf("reconcile fields = %+v", it)
	}
}

func TestAcceptReconcileSupersedes(t *testing.T) {
	ctx := context.Background()
	v := reconcileVault(t)
	// The contradicted entry must exist in MEMORY.md.
	if _, err := identity.Remember(ctx, v, identity.Entry{Text: "Prefers Go for daemons", Source: "session", Date: "2026-06-01"}); err != nil {
		t.Fatal(err)
	}
	items, _ := Load(ctx, v)
	var id string
	for _, it := range items {
		if it.Kind == "reconcile" {
			id = it.ID
		}
	}
	item, err := Accept(ctx, v, id)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Checked {
		t.Fatal("accepted reconcile should come back checked")
	}
	mem, _ := v.Read(ctx, identity.MemoryPath)
	if !strings.Contains(mem.Body, "~~2026-06-01 — Prefers Go for daemons") || !strings.Contains(mem.Body, "(superseded") {
		t.Fatalf("old entry not tombstoned:\n%s", mem.Body)
	}
	if !strings.Contains(mem.Body, "Uses Rust for daemons (source: reconcile)") {
		t.Fatalf("new entry not added:\n%s", mem.Body)
	}
	q, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if !strings.Contains(string(q), "— ✓ reconciled") {
		t.Fatalf("queue line not marked reconciled:\n%s", q)
	}
}
