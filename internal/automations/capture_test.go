package automations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/ingestion"
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

// stubFetcher serves canned HTML and counts fetches (mirrors pipeline_test.go's
// countingFetcher shape).
type stubFetcher struct {
	calls int
	fail  bool
}

func (s *stubFetcher) Fetch(ctx context.Context, url string) (*ingestion.Document, error) {
	s.calls++
	if s.fail {
		return nil, errors.New("connection refused")
	}
	return &ingestion.Document{
		URL:         url,
		ContentType: "text/html",
		Body:        []byte("<html><head><title>Captured Page</title></head><body><article><p>Some interesting article content for capture testing purposes. It has several sentences so extraction has something to work with.</p></article></body></html>"),
	}, nil
}

func TestCaptureURLIngestThenKnownSkip(t *testing.T) {
	rc, _ := newRC(t, map[string]string{
		"00-Inbox/reading.md": "# List\nhttps://example.com/article\n",
	})
	fetcher := &stubFetcher{}
	rc.Pipeline.Fetcher = fetcher
	ctx := context.Background()

	res, err := (Capture{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetches = %d, want 1", fetcher.calls)
	}
	if !strings.Contains(res.Summary, "captured 1") {
		t.Fatalf("summary = %q", res.Summary)
	}
	// A knowledge note exists.
	paths, _ := rc.Vault.List(ctx)
	var found bool
	for _, p := range paths {
		if strings.HasPrefix(p, "03-Resources/Knowledge/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no knowledge note created; vault: %v", paths)
	}
	// The inbox note was NOT modified (cardinal rule 2).
	n, _ := rc.Vault.Read(ctx, "00-Inbox/reading.md")
	if !strings.Contains(n.Body, "https://example.com/article") {
		t.Fatal("inbox note was modified")
	}

	// Second run: URL known in sources → skip WITHOUT fetching.
	res2, err := (Capture{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("second run fetched (calls=%d), want DB-only skip", fetcher.calls)
	}
	if !strings.Contains(res2.Summary, "skipped 1") {
		t.Fatalf("summary = %q", res2.Summary)
	}
}

func TestCaptureFileIngestAndArchive(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	writeInbox(t, rc.Vault.Root(), map[string]string{
		"notes.txt": "Plain text knowledge dropped into the inbox for capture testing.",
	})

	res, err := (Capture{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "captured 1") {
		t.Fatalf("summary = %q (changes: %v)", res.Summary, res.Changes)
	}
	// Original moved out of the inbox into the dated archive folder.
	if _, err := os.Stat(filepath.Join(rc.Vault.Root(), "00-Inbox", "notes.txt")); !os.IsNotExist(err) {
		t.Fatal("original still in inbox")
	}
	month := rc.now().UTC().Format("2006-01")
	archived := filepath.Join(rc.Vault.Root(), "04-Archive", "Capture", month, "notes.txt")
	if _, err := os.Stat(archived); err != nil {
		t.Fatalf("archived original missing at %s: %v", archived, err)
	}
	// Review queue records the capture.
	q, _ := os.ReadFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"))
	if !strings.Contains(string(q), "Capture") {
		t.Fatalf("review queue missing capture section:\n%s", q)
	}
}

func TestCaptureFailureMemory(t *testing.T) {
	rc, _ := newRC(t, map[string]string{
		"00-Inbox/reading.md": "https://example.com/broken\n",
	})
	fetcher := &stubFetcher{fail: true}
	rc.Pipeline.Fetcher = fetcher
	ctx := context.Background()

	res, err := (Capture{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err) // per-item failure must not fail the run
	}
	if !strings.Contains(res.Summary, "failed 1") {
		t.Fatalf("summary = %q", res.Summary)
	}
	if fetcher.calls != 1 {
		t.Fatalf("fetches = %d, want 1", fetcher.calls)
	}

	// Second run: failure remembered, NOT retried, queue not re-appended.
	if _, err := (Capture{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 1 {
		t.Fatalf("failed URL was retried (calls=%d)", fetcher.calls)
	}
	q, _ := os.ReadFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"))
	if n := strings.Count(string(q), "example.com/broken"); n != 1 {
		t.Fatalf("failure surfaced %d times in review queue, want once:\n%s", n, q)
	}
}

func TestCaptureDryRunWritesNothing(t *testing.T) {
	rc, _ := newRC(t, map[string]string{
		"00-Inbox/reading.md": "https://example.com/article\n",
	})
	fetcher := &stubFetcher{}
	rc.Pipeline.Fetcher = fetcher
	rc.DryRun = true
	writeInbox(t, rc.Vault.Root(), map[string]string{"drop.txt": "text"})

	res, err := (Capture{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if fetcher.calls != 0 {
		t.Fatalf("dry-run fetched (calls=%d)", fetcher.calls)
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes = %v, want 2 'would' lines", res.Changes)
	}
	if _, err := os.Stat(filepath.Join(rc.Vault.Root(), "00-Inbox", "drop.txt")); err != nil {
		t.Fatal("dry-run moved a file")
	}
	if _, err := os.Stat(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote the review queue")
	}
}

func TestCaptureDetectChangeGate(t *testing.T) {
	rc, _ := newRC(t, map[string]string{"00-Inbox/reading.md": "hello\n"})
	ctx := context.Background()

	ch1, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil || !ch1.Changed || ch1.Cursor == "" {
		t.Fatalf("first detect = %+v err=%v, want changed with cursor", ch1, err)
	}
	rc.LastCursor = ch1.Cursor
	ch2, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil || ch2.Changed {
		t.Fatalf("unchanged inbox: %+v err=%v, want not-changed", ch2, err)
	}
}

func TestCaptureArchiveCollisionSuffix(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	month := rc.now().UTC().Format("2006-01")
	pre := filepath.Join(rc.Vault.Root(), "04-Archive", "Capture", month)
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pre, "notes.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeInbox(t, rc.Vault.Root(), map[string]string{"notes.txt": "new capture content for collision test"})

	if _, err := (Capture{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pre, "notes-2.txt")); err != nil {
		t.Fatalf("collision suffix missing: %v", err)
	}
}
