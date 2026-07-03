package automations

import (
	"os"
	"path/filepath"
	"testing"
)

func writeInbox(t *testing.T, root string, files map[string]string) {
	t.Helper()
	dir := filepath.Join(root, "00-Inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestListInboxDir(t *testing.T) {
	root := t.TempDir()
	writeInbox(t, root, map[string]string{
		"note.md":   "hello",
		"paper.pdf": "%PDF-fake",
		"README.md": "readme",
		".DS_Store": "junk",
	})
	if err := os.MkdirAll(filepath.Join(root, "00-Inbox", "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := listInboxDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e.Name] = e.IsMD
	}
	if len(got) != 2 || got["note.md"] != true || got["paper.pdf"] != false {
		t.Fatalf("entries = %v, want note.md(md)+paper.pdf only", got)
	}

	// Missing inbox dir is not an error (fresh vault).
	if es, err := listInboxDir(t.TempDir()); err != nil || es != nil {
		t.Fatalf("missing dir: entries=%v err=%v, want nil/nil", es, err)
	}
}

func TestInboxFingerprintChangesOnDrop(t *testing.T) {
	root := t.TempDir()
	writeInbox(t, root, map[string]string{"note.md": "one"})
	fp1, err := inboxFingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	writeInbox(t, root, map[string]string{"drop.pdf": "content"})
	fp2, _ := inboxFingerprint(root)
	if fp1 == fp2 {
		t.Fatal("fingerprint must change when a file is dropped")
	}
	fp3, _ := inboxFingerprint(root)
	if fp2 != fp3 {
		t.Fatal("fingerprint must be stable with no changes")
	}
}

func TestExtractCaptureURLs(t *testing.T) {
	body := `# Reading list
https://example.com/article
  https://example.com/indented
Check https://example.com/midsentence out.
[A title](https://example.com/linked)
[bad](not-a-url)
https://example.com/article
plain text line
`
	got := extractCaptureURLs(body)
	want := []string{
		"https://example.com/article",
		"https://example.com/indented",
		"https://example.com/linked",
	}
	if len(got) != len(want) {
		t.Fatalf("urls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("urls[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
