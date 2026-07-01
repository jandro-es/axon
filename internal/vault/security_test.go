package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIsSystemPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{".claude/settings.local.json", true},
		{".CLAUDE/CLAUDE.md", true},
		{"notes/../.git/config", true},
		{".obsidian/app.json", true},
		{".axon/review-queue.md", true},
		{".trash/old.md", true},
		{"01-Projects/x.md", false},
		{"claude/notes.md", false}, // no dot — a normal folder
		{"deep/nested/.claude/x", true},
	}
	for _, tt := range tests {
		if got := IsSystemPath(tt.path); got != tt.want {
			t.Errorf("IsSystemPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// TestSafeAbsBlocksSymlinkEscape: a symlink planted inside the vault must not
// let vault-relative paths read or write outside the root — the lexical ".."
// check alone cannot see it.
func TestSafeAbsBlocksSymlinkEscape(t *testing.T) {
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	v := NewFS(root)
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	if _, err := v.Read(context.Background(), "escape/secret.md"); err == nil {
		t.Error("Read through an escaping symlink succeeded, want refusal")
	}
	if err := v.Write(context.Background(), "escape/planted.md", &Note{Body: "x"}); err == nil {
		t.Error("Write through an escaping symlink succeeded, want refusal")
	}

	// A symlink that stays INSIDE the vault is fine.
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := v.Write(context.Background(), "alias/ok.md", &Note{Body: "inside"}); err != nil {
		t.Errorf("Write through an internal symlink refused: %v", err)
	}
}
