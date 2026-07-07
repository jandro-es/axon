package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/identity"
	"github.com/jandro-es/axon/internal/vault"
)

func TestMemoryFactsCheckReportsCounts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(dir)
	if _, err := identity.Remember(ctx, v, identity.Entry{Text: "Lives in Tokyo", Kind: "fact", ValidFrom: "2026-07-05"}); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Reconcile(ctx, v, "Lives in Tokyo", "Lives in Osaka", "2026-08-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := Reindex(ctx, v, d); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	paths := config.ResolvedPaths{DBPath: dbPath, VaultPath: dir}
	c := memoryFactsCheck(paths)
	if c.Status != StatusOK {
		t.Fatalf("status = %q, want ok", c.Status)
	}
	// Exactly one fact is superseded (Tokyo). The open count also includes the
	// onboarding entry Remember auto-seeds, so assert on "open" presence + the
	// deterministic superseded count.
	if !strings.Contains(c.Detail, "open") || !strings.Contains(c.Detail, "1 superseded") {
		t.Fatalf("detail = %q, want open/superseded counts", c.Detail)
	}
}

const badMemoryFixture = `---
type: memory
---
## Memory

<!-- axon:memory:start -->
- ~~unterminated strike with no closing markers
<!-- axon:memory:end -->
`

func TestMemoryFactsCheckFlagsUnparseableLine(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	// A hand-edited block with a line that fails ParseFact (unterminated strike).
	v := tempVault(t, map[string]string{identity.MemoryPath: badMemoryFixture})
	paths := config.ResolvedPaths{DBPath: dbPath, VaultPath: v.Root()}
	c := memoryFactsCheck(paths)
	if c.Status != StatusWarn {
		t.Fatalf("status = %q, want warn for an unparseable block line", c.Status)
	}
}

func TestMemoryFactsCheckNoDatabaseIsOK(t *testing.T) {
	paths := config.ResolvedPaths{DBPath: filepath.Join(t.TempDir(), "missing.sqlite"), VaultPath: t.TempDir()}
	if c := memoryFactsCheck(paths); c.Status != StatusOK {
		t.Fatalf("missing DB should be ok, got %q", c.Status)
	}
}
