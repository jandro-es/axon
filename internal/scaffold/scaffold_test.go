package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestApplyCreatesLayout(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)

	res, err := Apply(v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed() {
		t.Fatal("first Apply reported no changes")
	}
	if len(res.CreatedDirs) != len(dirs) {
		t.Errorf("created %d dirs, want %d", len(res.CreatedDirs), len(dirs))
	}
	if len(res.CreatedFiles) != len(files) {
		t.Errorf("created %d files, want %d", len(res.CreatedFiles), len(files))
	}

	// Spot-check key paths exist.
	for _, p := range []string{
		"00-Inbox/README.md",
		"03-Resources/Knowledge/README.md",
		"Templates/Daily Note.md",
		".axon/logs",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected %q to exist: %v", p, err)
		}
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)

	if _, err := Apply(v); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(v)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() {
		t.Errorf("second Apply changed things: dirs=%v files=%v", res.CreatedDirs, res.CreatedFiles)
	}
	if res.SkippedDirs != len(dirs) || res.SkippedFiles != len(files) {
		t.Errorf("expected all skipped: dirs=%d files=%d", res.SkippedDirs, res.SkippedFiles)
	}
}

func TestApplyNeverClobbersUserContent(t *testing.T) {
	dir := t.TempDir()
	// A pre-existing README with the user's own content.
	inbox := filepath.Join(dir, "00-Inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(inbox, "README.md")
	if err := os.WriteFile(readme, []byte("MY OWN NOTES"), 0o644); err != nil {
		t.Fatal(err)
	}
	// And a user note that must survive untouched.
	userNote := filepath.Join(inbox, "thought.md")
	if err := os.WriteFile(userNote, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	v := vault.NewFS(dir)
	if _, err := Apply(v); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(readme)
	if string(got) != "MY OWN NOTES" {
		t.Errorf("user README clobbered: %q", got)
	}
	got, _ = os.ReadFile(userNote)
	if string(got) != "keep me" {
		t.Errorf("user note clobbered: %q", got)
	}
}
