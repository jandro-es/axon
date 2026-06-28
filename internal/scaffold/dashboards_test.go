package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestDashboardsGeneratesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)

	res, err := Dashboards(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CreatedFiles) != len(dashboardFiles) {
		t.Errorf("created %d dashboards, want %d", len(res.CreatedFiles), len(dashboardFiles))
	}
	// Each carries a Dataview block.
	got, _ := os.ReadFile(filepath.Join(dir, ".axon", "dashboards", "Active Projects.md"))
	if !strings.Contains(string(got), "```dataview") {
		t.Errorf("dashboard missing dataview query:\n%s", got)
	}

	res2, err := Dashboards(v)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.CreatedFiles) != 0 {
		t.Errorf("second Dashboards created %v, want none (idempotent)", res2.CreatedFiles)
	}
}

func TestDashboardsNeverClobbers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".axon", "dashboards")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(p, "Active Projects.md")
	if err := os.WriteFile(custom, []byte("MY OWN DASHBOARD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Dashboards(vault.NewFS(dir)); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(custom)
	if string(got) != "MY OWN DASHBOARD" {
		t.Errorf("user dashboard clobbered: %q", got)
	}
}
