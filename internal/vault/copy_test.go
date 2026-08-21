package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyFile(t *testing.T) {
	vaultDir := t.TempDir()
	v := NewFS(vaultDir)
	src := filepath.Join(t.TempDir(), "recording.m4a")
	payload := []byte("not really audio, but bytes are bytes")
	if err := os.WriteFile(src, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := v.CopyFile("03-Resources/Knowledge/attachments/abc.m4a", src); err != nil {
		t.Fatalf("copy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vaultDir, "03-Resources/Knowledge/attachments/abc.m4a"))
	if err != nil {
		t.Fatalf("destination not written: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("bytes differ: %q", got)
	}
	// A copy, never a move: the owner's file stays where it is.
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source must survive: %v", err)
	}
}

func TestCopyFileRefusesOverwrite(t *testing.T) {
	v := NewFS(t.TempDir())
	src := filepath.Join(t.TempDir(), "a.m4a")
	if err := os.WriteFile(src, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := v.CopyFile("attachments/a.m4a", src); err != nil {
		t.Fatal(err)
	}
	if err := v.CopyFile("attachments/a.m4a", src); err == nil {
		t.Fatal("overwriting an existing attachment must be refused")
	}
}

func TestCopyFileRefusesEscape(t *testing.T) {
	v := NewFS(t.TempDir())
	src := filepath.Join(t.TempDir(), "a.m4a")
	if err := os.WriteFile(src, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../outside.m4a", "/etc/evil.m4a"} {
		if err := v.CopyFile(bad, src); err == nil {
			t.Errorf("destination %q must be refused", bad)
		}
	}
}

func TestCopyFileMissingSource(t *testing.T) {
	v := NewFS(t.TempDir())
	err := v.CopyFile("attachments/a.m4a", filepath.Join(t.TempDir(), "nope.m4a"))
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("a missing source must be a clear error, got %v", err)
	}
}
