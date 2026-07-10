package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
)

func TestActionsCheckReportsCounts(t *testing.T) {
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
	if err := db.ReplaceActions(ctx, d, []db.Action{
		{Hash: "h", SourcePath: "a.md", LineNo: 1, Text: "x", Raw: "- [ ] x",
			State: "open", Checkbox: " ", Updated: "2026-07-10T00:00:00Z"},
	}); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()

	c := actionsCheck(config.ResolvedPaths{DBPath: dbPath, VaultPath: dir})
	if c.Status != StatusOK {
		t.Fatalf("status = %q, want ok", c.Status)
	}
	if c.Name != "actions" {
		t.Fatalf("name = %q, want actions", c.Name)
	}
	if !strings.Contains(c.Detail, "1 action") {
		t.Fatalf("detail = %q, want the count", c.Detail)
	}
}

func TestActionsCheckNoDatabase(t *testing.T) {
	c := actionsCheck(config.ResolvedPaths{DBPath: filepath.Join(t.TempDir(), "nope.sqlite")})
	if c.Status != StatusOK || !strings.Contains(c.Detail, "no database") {
		t.Fatalf("check = %+v, want OK/no-database", c)
	}
}
