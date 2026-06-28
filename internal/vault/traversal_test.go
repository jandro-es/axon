package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestPathTraversalRefused verifies the vault sandbox: no helper may read or
// write outside the vault root, even with agent-supplied "../" or absolute
// paths (these arrive through the MCP tools).
func TestPathTraversalRefused(t *testing.T) {
	root := t.TempDir()
	// A sentinel file OUTSIDE the vault that must never be reachable.
	outside := filepath.Join(filepath.Dir(root), "secret.txt")
	if err := os.WriteFile(outside, []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })

	v := NewFS(root)
	ctx := context.Background()
	evil := []string{
		"../secret.txt",
		"../../secret.txt",
		"a/../../secret.txt",
		"/etc/passwd",
		"/" + filepath.Base(outside),
		"..",
		"",
	}

	for _, p := range evil {
		t.Run("read "+p, func(t *testing.T) {
			if _, err := v.Read(ctx, p); err == nil {
				t.Errorf("Read(%q) should be refused", p)
			}
		})
		t.Run("write "+p, func(t *testing.T) {
			if err := v.Write(ctx, p, &Note{Body: "x"}); err == nil {
				t.Errorf("Write(%q) should be refused", p)
			}
		})
		t.Run("patch "+p, func(t *testing.T) {
			if err := v.Patch(ctx, p, "summary", "x"); err == nil {
				t.Errorf("Patch(%q) should be refused", p)
			}
		})
		t.Run("create "+p, func(t *testing.T) {
			if _, err := v.Create(p, "x"); err == nil {
				t.Errorf("Create(%q) should be refused", p)
			}
		})
		t.Run("append "+p, func(t *testing.T) {
			if err := v.Append(p, "x"); err == nil {
				t.Errorf("Append(%q) should be refused", p)
			}
		})
		if v.Exists(p) {
			t.Errorf("Exists(%q) should be false for an escaping path", p)
		}
	}

	// The sentinel must be untouched and never have been read.
	if data, _ := os.ReadFile(outside); string(data) != "TOP SECRET" {
		t.Errorf("outside file was modified: %q", data)
	}

	// Move must refuse escaping source or destination.
	_, _ = v.Create("real.md", "real note")
	if err := v.Move(ctx, "real.md", "../escaped.md"); err == nil {
		t.Error("Move to an escaping destination should be refused")
	}
	if err := v.Move(ctx, "../secret.txt", "x.md"); err == nil {
		t.Error("Move from an escaping source should be refused")
	}

	// A legitimate nested path with internal ".." that stays inside is allowed.
	if _, err := v.Create("01-Projects/sub/../note.md", "ok"); err != nil {
		t.Errorf("a contained path with internal .. should be allowed: %v", err)
	}
	if !v.Exists("01-Projects/note.md") {
		t.Error("contained-.. path did not resolve to the expected location")
	}
}
