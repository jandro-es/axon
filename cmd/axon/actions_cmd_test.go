package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestActionsCmdJSON(t *testing.T) {
	dir := t.TempDir()
	cfg := writeTempConfig(t, dir)
	if _, err := run(t, "init", "--config", cfg); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Seed a note with a spread of tasks, then reindex.
	note := filepath.Join(dir, "vault", "01-Projects", "p.md")
	if err := os.MkdirAll(filepath.Dir(note), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "## Todo\n- [ ] overdue one 📅 2000-01-01\n- [ ] someday one #someday\n- [x] done one\n"
	if err := os.WriteFile(note, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "reindex", "--config", cfg); err != nil {
		t.Fatalf("reindex: %v", err)
	}

	out, err := run(t, "actions", "--json", "--config", cfg)
	if err != nil {
		t.Fatalf("actions: %v\n%s", err, out)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("bad json: %v\n%s", err, out)
	}
	// done excluded by default; overdue + someday remain.
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2 (open only): %s", len(items), out)
	}
	var buckets []string
	for _, it := range items {
		buckets = append(buckets, it["bucket"].(string))
	}
	if !hasStr(buckets, "overdue") || !hasStr(buckets, "someday") {
		t.Errorf("buckets=%v want overdue+someday", buckets)
	}
}

func hasStr(ss []string, w string) bool {
	for _, s := range ss {
		if s == w {
			return true
		}
	}
	return false
}
