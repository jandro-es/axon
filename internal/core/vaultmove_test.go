package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func moveFixture(t *testing.T) (config.Profile, string, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "vault")
	if err := os.MkdirAll(filepath.Join(src, "00-Inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "00-Inbox", "note.md"), []byte("# hello [[world]]"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := config.Profile{
		VaultPath: src,
		DataDir:   filepath.Join(root, "data"),
		Claude:    config.ClaudeConfig{AuthMode: "subscription", ConfigDir: filepath.Join(root, "data", "claude")},
	}
	return p, src, filepath.Join(root, "new-vault")
}

func TestMoveVaultHappyPath(t *testing.T) {
	p, src, dst := moveFixture(t)
	var setKeys []string
	rep, err := MoveVault(context.Background(), VaultMoveOptions{
		ProfileName: "personal", Profile: p, Dest: dst,
		ConfigPath: "/tmp/config.yaml", BinaryPath: "/usr/local/bin/axon",
		SetConfig: func(key, value string) error {
			setKeys = append(setKeys, key+"="+value)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("%v (steps: %+v)", err, rep.Steps)
	}
	if _, err := os.Stat(filepath.Join(dst, "00-Inbox", "note.md")); err != nil {
		t.Error("note did not move:", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("old vault dir should be gone")
	}
	if len(setKeys) != 1 || setKeys[0] != "vault_path="+dst {
		t.Errorf("config updates = %v", setKeys)
	}
	if _, err := os.Stat(filepath.Join(dst, ".claude", ".mcp.json")); err != nil {
		t.Error("claude wiring not regenerated at destination:", err)
	}
}

func TestMoveVaultRefusesNonEmptyDest(t *testing.T) {
	p, _, dst := moveFixture(t)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "occupied.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := MoveVault(context.Background(), VaultMoveOptions{
		ProfileName: "p", Profile: p, Dest: dst,
		SetConfig: func(string, string) error { return nil },
	})
	if err == nil {
		t.Error("non-empty destination must be refused")
	}
}

func TestMoveVaultRefusesDestInsideVault(t *testing.T) {
	p, src, _ := moveFixture(t)
	_, err := MoveVault(context.Background(), VaultMoveOptions{
		ProfileName: "p", Profile: p, Dest: filepath.Join(src, "sub"),
		SetConfig: func(string, string) error { return nil },
	})
	if err == nil {
		t.Error("destination inside the vault must be refused")
	}
}

func TestCopyTreePreservesContent(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a", "b", "f.md"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	n, err := copyTree(src, dst)
	if err != nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "a", "b", "f.md"))
	if err != nil || string(got) != "content" {
		t.Errorf("copy content = %q err=%v", got, err)
	}
	st, _ := os.Stat(filepath.Join(dst, "a", "b", "f.md"))
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", st.Mode().Perm())
	}
}
