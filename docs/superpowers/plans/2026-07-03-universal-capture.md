# Universal Capture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `capture` automation that scans `00-Inbox/` on the scheduler, ingests own-line URLs from notes and dropped files through the existing pipeline, archives originals wikilink-safely, and remembers failures.

**Architecture:** One new automation in `internal/automations` (registry + catalog entry — no start-loop changes), change-gated on an inbox-listing fingerprint. URL bookkeeping lives in SQLite (`sources` lookup); failure memory lives in a second `automation_state` row (`capture:failures`). File ingestion is sandboxed to the physical inbox listing; archiving uses `vault.FS.Move`. Spec: `docs/superpowers/specs/2026-07-03-universal-capture-design.md`; ADR-016; FR-26 + FR-81…FR-83.

**Tech Stack:** Go stdlib only (no new dependencies). Existing seams: `ingestion.Pipeline.Ingest`, `db.GetCursor/SetCursor`, `db.GetSourceByURL`, `vault.FS` (Root/Read/Move/Append/Exists), automation engine.

## Global Constraints

- No new Go dependencies (ADR-016 rejected fsnotify).
- Cardinal rule 1: the only model path is `capture.enrich: claude` → `ingestion.ClaudeEnricher{Manager: rc.Manager, ModelKey: "routine"}` — never a direct agent call.
- Cardinal rule 2: inbox notes are **never modified**; originals are **moved** (wikilink-safe `vault.FS.Move`), never deleted.
- NFR-05 sandbox: file ingestion only for files physically enumerated in `00-Inbox/` (top level); paths written inside notes are never file targets.
- Dry-run: no writes, no fetches, no archive moves — report only.
- Every task ends with `go test ./...` green and a commit on `feature/universal-capture`.

### Key facts discovered during planning (differ from naive readings of the spec)

- `vault.FS.List` returns **only `.md` files** (`internal/vault/fs.go:115`), so `inbox-triage` already never sees dropped PDFs — the spec's "targeted triage fix" needs **no code**, only a covering regression test. Capture must therefore enumerate the inbox with `os.ReadDir`, not `Vault.List`.
- The engine persists `Change.Cursor` (from DetectChange) after a successful non-dry run (`internal/automations/engine.go:168`), keyed by automation name in `automation_state`. `db.GetCursor/SetCursor(ctx, q, key, ...)` accept arbitrary keys, so capture's failure memory is a second row keyed `"capture:failures"` — no schema change.
- Because a capture run that archives files changes the inbox listing *after* the cursor was computed, the next tick runs once more and finds nothing new (DB lookups only, no network). Accepted; note it in the code comment.
- `Pipeline` is a struct; capture takes a **shallow copy** of `rc.Pipeline` before overriding `Enricher`, never mutating the shared instance.

---

### Task 1: Config — `CaptureConfig` + validation + starter entry

**Files:**
- Modify: `internal/config/types.go` (Profile, after the `Memory MemoryConfig` field ~line 43)
- Modify: `internal/config/load.go` (`Config.Validate`)
- Modify: `internal/config/starter.go` (automations block, line 71-81 area)
- Test: `internal/config/capture_test.go`

**Interfaces:**
- Produces: `config.CaptureConfig{Enrich, ArchiveDir string}` with methods `(CaptureConfig) EnrichMode() string` (default `"heuristic"`) and `(CaptureConfig) Archive() string` (default `"04-Archive/Capture"`); `Profile.Capture CaptureConfig`; validation rejecting bad `enrich` values and non-vault-relative `archive_dir`.

- [ ] **Step 1: Write the failing test**

`internal/config/capture_test.go`:

```go
package config

import "testing"

func TestCaptureConfigDefaults(t *testing.T) {
	var c CaptureConfig
	if c.EnrichMode() != "heuristic" {
		t.Errorf("EnrichMode = %q, want heuristic", c.EnrichMode())
	}
	if c.Archive() != "04-Archive/Capture" {
		t.Errorf("Archive = %q, want 04-Archive/Capture", c.Archive())
	}
	c = CaptureConfig{Enrich: "claude", ArchiveDir: "04-Archive/Clips"}
	if c.EnrichMode() != "claude" || c.Archive() != "04-Archive/Clips" {
		t.Errorf("overrides not honored: %+v", c)
	}
}

func TestValidateCapture(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CaptureConfig
		wantErr bool
	}{
		{"zero value ok", CaptureConfig{}, false},
		{"heuristic ok", CaptureConfig{Enrich: "heuristic"}, false},
		{"claude ok", CaptureConfig{Enrich: "claude"}, false},
		{"bad enrich", CaptureConfig{Enrich: "gpt"}, true},
		{"absolute archive dir", CaptureConfig{ArchiveDir: "/tmp/x"}, true},
		{"escaping archive dir", CaptureConfig{ArchiveDir: "../outside"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCapture(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestCaptureConfig|TestValidateCapture' -v`
Expected: FAIL — `undefined: CaptureConfig`.

- [ ] **Step 3: Implement**

`internal/config/types.go` — new type next to `MemoryConfig`, and a `Capture CaptureConfig \`yaml:"capture"\`` field on `Profile` (optional block: no `required` tag, zero value = defaults, existing configs untouched):

```go
// CaptureConfig tunes the capture automation (ADR-016). Optional: an absent
// block resolves to heuristic enrichment and the default archive folder.
type CaptureConfig struct {
	// Enrich selects metadata enrichment for captured items:
	// "heuristic" (default, zero tokens) or "claude" (through the
	// token-manager chokepoint on the routine tier).
	Enrich string `yaml:"enrich,omitempty"`
	// ArchiveDir is the vault-relative folder for ingested inbox originals.
	// Default: 04-Archive/Capture.
	ArchiveDir string `yaml:"archive_dir,omitempty"`
}

// EnrichMode returns the enrichment mode, defaulting to "heuristic".
func (c CaptureConfig) EnrichMode() string {
	if c.Enrich == "" {
		return "heuristic"
	}
	return c.Enrich
}

// Archive returns the archive folder, defaulting to 04-Archive/Capture.
func (c CaptureConfig) Archive() string {
	if c.ArchiveDir == "" {
		return "04-Archive/Capture"
	}
	return c.ArchiveDir
}
```

`internal/config/load.go` — add beside `validateLocalRouting` usage in `Config.Validate` (inside the existing profiles loop):

```go
		if err := validateCapture(p.Capture); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
```

and the function (put it in `types.go` next to `CaptureConfig`, or in `load.go` — either; keep it unexported):

```go
// validateCapture applies the capture-block rules struct tags can't express.
func validateCapture(c CaptureConfig) error {
	if c.Enrich != "" && c.Enrich != "heuristic" && c.Enrich != "claude" {
		return fmt.Errorf("capture.enrich must be heuristic or claude (got %q)", c.Enrich)
	}
	if c.ArchiveDir != "" {
		if strings.HasPrefix(c.ArchiveDir, "/") || strings.Contains(c.ArchiveDir, "..") {
			return fmt.Errorf("capture.archive_dir must be a vault-relative path (got %q)", c.ArchiveDir)
		}
	}
	return nil
}
```

`internal/config/starter.go` — add to the starter automations block (alongside the existing entries at lines 72-81):

```yaml
      capture:           { enabled: true,  schedule: "*/5 * * * *",     model: none,      budget_tokens: 0, catch_up: run-once }
```

(Match the file's exact indentation/alignment. `model: none` follows the convention of the other no-model automations.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: PASS (including the starter-config parse test, which now exercises the new entry).

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): capture block (enrich toggle, archive dir) + starter entry (ADR-016)"
```

---

### Task 2: Capture helpers — inbox enumeration, fingerprint, URL extraction

**Files:**
- Create: `internal/automations/capture.go` (helpers only in this task)
- Test: `internal/automations/capture_test.go`

**Interfaces:**
- Produces (all in package `automations`): `listInboxDir(root string) ([]inboxEntry, error)` with `inboxEntry{Name string, IsMD bool}`; `inboxFingerprint(root string) (string, error)`; `extractCaptureURLs(body string) []string`; constants `inboxDir = "00-Inbox"`, `captureFailureState = "capture:failures"`.

- [ ] **Step 1: Write the failing tests**

`internal/automations/capture_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/automations/ -run 'TestListInboxDir|TestInboxFingerprint|TestExtractCaptureURLs' -v`
Expected: FAIL — `undefined: listInboxDir`.

- [ ] **Step 3: Implement** — `internal/automations/capture.go`:

```go
package automations

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// inboxDir is the capture funnel folder (scaffold convention).
	inboxDir = "00-Inbox"
	// captureFailureState is the automation_state key for capture's failure
	// memory — a second row beside the engine-managed "capture" cursor row.
	captureFailureState = "capture:failures"
)

// inboxEntry is one top-level item in the inbox listing.
type inboxEntry struct {
	Name string
	IsMD bool
}

// listInboxDir enumerates top-level inbox files, skipping README*, dotfiles
// and subdirectories. It reads the filesystem directly because vault.List is
// markdown-only and capture must see dropped PDFs/binaries. This listing is
// also the NFR-05 sandbox: ONLY files enumerated here are ever ingested as
// local files — paths written inside notes are never file targets.
func listInboxDir(root string) ([]inboxEntry, error) {
	entries, err := os.ReadDir(filepath.Join(root, inboxDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // fresh vault: nothing to capture
		}
		return nil, fmt.Errorf("list inbox: %w", err)
	}
	var out []inboxEntry
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.EqualFold(stem, "README") {
			continue
		}
		out = append(out, inboxEntry{Name: name, IsMD: strings.EqualFold(filepath.Ext(name), ".md")})
	}
	return out, nil
}

// inboxFingerprint hashes the inbox listing (name + size + mtime) — the
// capture change gate. Deliberately does not read content: a tick over an
// unchanged inbox must be near-free.
func inboxFingerprint(root string) (string, error) {
	entries, err := listInboxDir(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range entries {
		st, err := os.Stat(filepath.Join(root, inboxDir, e.Name))
		if err != nil {
			continue // raced away between ReadDir and Stat; next tick catches it
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", e.Name, st.Size(), st.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// captureURLLine matches a URL standing alone on a (trimmed) line: either a
// bare http(s) URL or a single markdown link. Mid-sentence URLs are NOT
// capture requests (FR-26: deliberate paste, predictable trigger).
var captureURLLine = regexp.MustCompile(`^(?:(https?://\S+)|\[[^\]]*\]\((https?://[^)\s]+)\))$`)

// extractCaptureURLs returns the own-line URLs in a note body, deduplicated,
// in order of first appearance.
func extractCaptureURLs(body string) []string {
	var urls []string
	seen := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		m := captureURLLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		u := m[1]
		if u == "" {
			u = m[2]
		}
		if !seen[u] {
			seen[u] = true
			urls = append(urls, u)
		}
	}
	return urls
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/automations/ -run 'TestListInboxDir|TestInboxFingerprint|TestExtractCaptureURLs' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/capture.go internal/automations/capture_test.go
git commit -m "feat(automations): capture helpers — inbox listing, fingerprint, URL extraction"
```

---

### Task 3: The Capture automation — DetectChange + Run (URLs and files)

**Files:**
- Modify: `internal/automations/capture.go`
- Test: `internal/automations/capture_test.go` (extend)

**Interfaces:**
- Consumes: Task 2 helpers; `db.GetSourceByURL(ctx, q, url) (*SourceRow, error)`; `db.GetCursor/SetCursor(ctx, q, key, ...)`; `ingestion.Pipeline.Ingest(ctx, arg, IngestOptions) (IngestResult, error)` with `IngestOptions{DryRun, ApplyLinks, AllowLocalFiles bool}` and `IngestResult{Status, NotePath, SkippedReason, ...}`; `ingestion.ClaudeEnricher{Manager, ModelKey}`; `vault.FS.{Read,Move,Append,Exists,Root}`; `config.CaptureConfig.{EnrichMode,Archive}`.
- Produces: `Capture{}` implementing `Automation` (`Name() == "capture"`, `Essential() == false`); unexported `capturePipeline(rc RunCtx) *ingestion.Pipeline`, `loadCaptureFailures(ctx, rc) map[string]string`, `saveCaptureFailures(ctx, rc, map[string]string)`, `archiveInboxFile(ctx, rc, name string) (string, error)`, `fileCaptureKey(abs string) (string, error)`.

- [ ] **Step 1: Write the failing tests** (append to `capture_test.go`; `newRC(t, files)` is the existing harness in `standard_test.go` — it builds a real temp vault, in-memory DB, fake embedder, and a `*ingestion.Pipeline` whose `Fetcher` field tests may override):

```go
// stubFetcher serves canned HTML and counts fetches. Mirrors the shape used
// by internal/ingestion/pipeline_test.go's countingFetcher.
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
		URL:  url,
		Body: []byte("<html><head><title>Captured Page</title></head><body><article><p>Some interesting article content for capture testing purposes.</p></article></body></html>"),
	}, nil
}
// NOTE: check internal/ingestion/fetcher.go for Document's exact fields
// (e.g. a content-type field) and mirror what pipeline_test.go's fixtures set.

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
		"notes.txt": "Plain text knowledge dropped into the inbox for capture.",
	})

	res, err := (Capture{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "captured 1") {
		t.Fatalf("summary = %q", res.Summary)
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
		t.Fatalf("failure surfaced %d times in review queue, want once", n)
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
	// Pre-existing archived file with the same name.
	pre := filepath.Join(rc.Vault.Root(), "04-Archive", "Capture", month)
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pre, "notes.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeInbox(t, rc.Vault.Root(), map[string]string{"notes.txt": "new capture content"})

	if _, err := (Capture{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pre, "notes-2.txt")); err != nil {
		t.Fatalf("collision suffix missing: %v", err)
	}
}
```

Add imports to the test file as needed: `"context"`, `"errors"`, `"strings"`, `"github.com/jandro-es/axon/internal/ingestion"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/automations/ -run 'TestCapture' -v`
Expected: FAIL — `Capture` has no `Run`/`DetectChange`.

- [ ] **Step 3: Implement** (append to `internal/automations/capture.go`; add imports `"context"`, `"encoding/json"`, `"time"`, `"github.com/jandro-es/axon/internal/db"`, `"github.com/jandro-es/axon/internal/ingestion"`):

```go
// Capture is the FR-26 capture funnel (ADR-016): own-line URLs in inbox notes
// and files dropped into 00-Inbox are ingested through the pipeline on each
// tick; originals are archived wikilink-safely; failures are remembered.
// No model call of its own (enrichment goes through the chokepoint when
// capture.enrich is "claude").
type Capture struct{}

func (Capture) Name() string    { return "capture" }
func (Capture) Essential() bool { return false }

func (Capture) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	fp, err := inboxFingerprint(rc.Vault.Root())
	if err != nil {
		return Change{}, err
	}
	if fp == rc.LastCursor {
		return Change{Changed: false, Reason: "inbox unchanged since last capture"}, nil
	}
	// A run that archives files changes the listing after this cursor was
	// computed, so the next tick runs once more and finds nothing new — DB
	// lookups only, no network. Accepted cost of the engine's cursor timing.
	return Change{Changed: true, Reason: "inbox changed", Cursor: fp}, nil
}

func (Capture) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	root := rc.Vault.Root()
	entries, err := listInboxDir(root)
	if err != nil {
		return RunResult{}, err
	}
	failures := loadCaptureFailures(ctx, rc)
	pl := capturePipeline(rc)

	var (
		changes                   []string
		queue                     []string
		captured, skipped, failed int
	)

	for _, e := range entries {
		if e.IsMD {
			note, rerr := rc.Vault.Read(ctx, inboxDir+"/"+e.Name)
			if rerr != nil {
				continue
			}
			for _, u := range extractCaptureURLs(note.Body) {
				if _, known := failures[u]; known {
					skipped++
					continue
				}
				if src, _ := db.GetSourceByURL(ctx, rc.DB, u); src != nil {
					skipped++ // already ingested: DB-only skip, no network
					continue
				}
				if rc.DryRun {
					changes = append(changes, "would ingest "+u)
					continue
				}
				res, ierr := pl.Ingest(ctx, u, ingestion.IngestOptions{})
				if ierr != nil || res.Status == "failed" {
					failed++
					failures[u] = captureErr(ierr, res.SkippedReason)
					queue = append(queue, fmt.Sprintf("- [ ] capture FAILED: %s — %s", u, failures[u]))
					continue
				}
				captured++
				changes = append(changes, fmt.Sprintf("%s → %s", u, res.NotePath))
				queue = append(queue, fmt.Sprintf("- [x] captured %s → [[%s]]", u, stripExt(res.NotePath)))
			}
			continue
		}

		// Dropped file. The NFR-05 sandbox: e.Name came from the physical
		// inbox listing; note contents never reach this path.
		abs := filepath.Join(root, inboxDir, e.Name)
		key, kerr := fileCaptureKey(abs)
		if kerr != nil {
			continue // raced away; next tick
		}
		if _, known := failures[key]; known {
			skipped++
			continue
		}
		if rc.DryRun {
			changes = append(changes, fmt.Sprintf("would ingest %s and archive the original", e.Name))
			continue
		}
		res, ierr := pl.Ingest(ctx, abs, ingestion.IngestOptions{AllowLocalFiles: true})
		if ierr != nil || res.Status == "failed" {
			failed++
			failures[key] = captureErr(ierr, res.SkippedReason)
			queue = append(queue, fmt.Sprintf("- [ ] capture FAILED: %s — %s", e.Name, failures[key]))
			continue
		}
		// ok / skipped(hash match) / redacted all count as ingested content:
		// archive the original either way so the inbox stays an inbox.
		dest, merr := archiveInboxFile(ctx, rc, e.Name)
		if merr != nil {
			failed++
			failures[key] = "ingested but archive move failed: " + merr.Error()
			queue = append(queue, fmt.Sprintf("- [ ] capture FAILED: %s — %s", e.Name, failures[key]))
			continue
		}
		captured++
		changes = append(changes, fmt.Sprintf("%s → %s (original: %s)", e.Name, res.NotePath, dest))
		if res.NotePath != "" {
			queue = append(queue, fmt.Sprintf("- [x] captured %s → [[%s]] (original: %s)", e.Name, stripExt(res.NotePath), dest))
		} else {
			queue = append(queue, fmt.Sprintf("- [x] archived %s → %s (%s)", e.Name, dest, res.SkippedReason))
		}
	}

	if !rc.DryRun {
		if len(queue) > 0 {
			header := fmt.Sprintf("\n## Capture (%s)\n", rc.now().UTC().Format("2006-01-02 15:04"))
			if aerr := rc.Vault.Append(".axon/review-queue.md", header+strings.Join(queue, "\n")+"\n"); aerr != nil {
				rc.Log.Warn("capture: review queue append failed", "err", aerr)
			}
		}
		saveCaptureFailures(ctx, rc, failures)
	}

	return RunResult{
		Summary: fmt.Sprintf("captured %d, skipped %d, failed %d", captured, skipped, failed),
		Changes: changes,
	}, nil
}

// capturePipeline returns the pipeline to ingest with: a shallow copy of the
// shared one (never mutated), with the enricher per capture.enrich. "claude"
// goes through the token-manager chokepoint on the routine tier (cardinal
// rule 1); the default stays the pipeline's zero-token heuristic.
func capturePipeline(rc RunCtx) *ingestion.Pipeline {
	pl := *rc.Pipeline
	if rc.Config.Capture.EnrichMode() == "claude" {
		pl.Enricher = ingestion.ClaudeEnricher{Manager: rc.Manager, ModelKey: "routine"}
	}
	return &pl
}

// fileCaptureKey identifies a dropped file for failure memory: path + content
// hash, so an edited/replaced file is retried but an unchanged one is not.
func fileCaptureKey(abs string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return abs + "\x00" + hex.EncodeToString(sum[:])[:16], nil
}

// archiveInboxFile moves an ingested original to <archive>/<YYYY-MM>/<name>
// via the wikilink-safe vault move, suffixing -2, -3… on collision. Returns
// the vault-relative destination.
func archiveInboxFile(ctx context.Context, rc RunCtx, name string) (string, error) {
	month := rc.now().UTC().Format("2006-01")
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 100; i++ {
		candidate := stem
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", stem, i)
		}
		dest := fmt.Sprintf("%s/%s/%s%s", rc.Config.Capture.Archive(), month, candidate, ext)
		if rc.Vault.Exists(dest) {
			continue
		}
		if err := rc.Vault.Move(ctx, inboxDir+"/"+name, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("no free archive name for %q", name)
}

// loadCaptureFailures reads the failure memory (empty map on any problem —
// worst case a failed item is retried once).
func loadCaptureFailures(ctx context.Context, rc RunCtx) map[string]string {
	out := map[string]string{}
	raw, err := db.GetCursor(ctx, rc.DB, captureFailureState)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// saveCaptureFailures persists the failure memory beside the engine cursor.
func saveCaptureFailures(ctx context.Context, rc RunCtx, failures map[string]string) {
	raw, err := json.Marshal(failures)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, captureFailureState, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("capture: persist failure memory", "err", err)
	}
}

// captureErr picks the most informative failure text.
func captureErr(err error, reason string) string {
	if err != nil {
		return err.Error()
	}
	if reason != "" {
		return reason
	}
	return "ingest failed"
}
```

Adjust the `IngestResult` failure fields against the real struct (`internal/ingestion/pipeline.go:51`): if failures are reported via `error` only (no `Status == "failed"` without error), simplify the checks to `ierr != nil` — check how `Ingest` reports fetch errors before finalizing.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/automations/ -run TestCapture -v`
Expected: PASS. If `stubFetcher`'s `Document` doesn't satisfy the extract stage, copy the working fixture shape from `internal/ingestion/pipeline_test.go`.

- [ ] **Step 5: Full package + commit**

Run: `go test ./internal/automations/ ./internal/config/`
Expected: PASS.

```bash
git add internal/automations/
git commit -m "feat(automations): capture automation — URL + file ingest, archive, failure memory (FR-26, FR-81, FR-82)"
```

---

### Task 4: Registration — registry, catalog, triage regression test

**Files:**
- Modify: `internal/automations/registry.go` (Registry map, line 13-24)
- Modify: `internal/automations/catalog.go` (purposes map, line 13-24)
- Test: `internal/automations/capture_test.go` (extend)

**Interfaces:**
- Consumes: `Capture{}` from Task 3.
- Produces: `capture` resolvable via `Get`/`Names`/`Schedulables` → schedulable by `axon start`, runnable by `axon run capture`, listed by `axon automations`.

- [ ] **Step 1: Failing test** (append to `capture_test.go`):

```go
func TestCaptureRegisteredAndSchedulable(t *testing.T) {
	profile := config.Profile{
		Automations: map[string]config.Automation{
			"capture": {Enabled: true, Schedule: "*/5 * * * *", CatchUp: "run-once"},
		},
	}
	if _, err := Get(profile, "capture"); err != nil {
		t.Fatalf("capture not in registry: %v", err)
	}
	if Purpose("capture") == "(no description)" {
		t.Fatal("capture has no catalog purpose")
	}
	scheds := Schedulables(profile)
	if len(scheds) != 1 || scheds[0].Automation.Name() != "capture" {
		t.Fatalf("schedulables = %+v, want capture", scheds)
	}
}

// TestInboxTriageIgnoresDroppedFiles is the spec's triage regression: a PDF in
// the inbox must never be read as a note. vault.List is markdown-only, so this
// documents the guarantee.
func TestInboxTriageIgnoresDroppedFiles(t *testing.T) {
	rc, _ := newRC(t, nil)
	writeInbox(t, rc.Vault.Root(), map[string]string{"paper.pdf": "%PDF-not-markdown"})
	items := inboxItems(context.Background(), rc)
	if len(items) != 0 {
		t.Fatalf("triage items = %v, want none for a dropped PDF", items)
	}
}
```

(Add `"github.com/jandro-es/axon/internal/config"` to test imports.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/automations/ -run 'TestCaptureRegistered|TestInboxTriageIgnores' -v`
Expected: `TestCaptureRegisteredAndSchedulable` FAILS (unknown automation); the triage test passes already (List is markdown-only) — it stays as the regression guard.

- [ ] **Step 3: Register.** In `registry.go`'s `Registry` map add:

```go
		Capture{}.Name():          Capture{},
```

In `catalog.go`'s `purposes` map add:

```go
	"capture":           "Ingests own-line URLs from Inbox notes and files dropped into 00-Inbox, archiving originals. The FR-26 capture funnel; no model call (enrichment optional via capture.enrich).",
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/automations/ -v -run 'TestCapture|TestInboxTriage'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/
git commit -m "feat(automations): register capture in registry + catalog; triage regression test"
```

---

### Task 5: Vault docs — inbox README teaches the capture workflow

**Files:**
- Modify: `internal/scaffold/assets/readmes/inbox.md`

- [ ] **Step 1: Update the README.** Read the current `internal/scaffold/assets/readmes/inbox.md` and extend it (keep its existing tone/format, ~doubling it at most) with:

```markdown
## Capture

AXON watches this folder (the `capture` automation, every few minutes):

- **Paste a URL on its own line** in any note here — the page is fetched,
  cleaned and filed into `03-Resources/Knowledge/` with a summary. Your note
  is never modified.
- **Drop a file** (PDF, HTML, text) into this folder — it is ingested the
  same way and the original moves to `04-Archive/Capture/`.

Failures land in `.axon/review-queue.md`. Works from any device that syncs
the vault — share a URL into a note here from your phone and it's captured.
```

- [ ] **Step 2: Verify scaffold tests still pass**

Run: `go test ./internal/scaffold/ ./internal/core/`
Expected: PASS (the scaffold embeds this asset; core init tests exercise it).

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/assets/readmes/inbox.md
git commit -m "docs(scaffold): inbox README teaches the capture workflow"
```

---

### Task 6: Docs, example config, FR status flip, CHANGELOG

**Files:**
- Modify: `docs/03-requirements.md` (capture section header; FR-26 row; status banner ~line 15)
- Modify: `docs/02-architecture.md` (ADR-016 header)
- Modify: `docs/04-data-model-and-config.md` (config reference)
- Modify: `docs/05-component-knowledge-ingestion.md` (capture section)
- Modify: `axon.config.example.yaml` (capture block + automations entry)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: docs/03.** Flip the section header `### Universal capture *(planned — spec approved 2026-07-03, not yet built)*` → `*(built)*` and reword its intro to past tense ("implemented by the `capture` automation"). In the FR-26 row (ingestion table, ~line 52) append: `**Implemented** by the \`capture\` automation (ADR-016).` Update the status banner (~line 15) so FR-26 is no longer listed as post-v1/unbuilt.

- [ ] **Step 2: docs/02.** Flip ADR-016's header `*(accepted — spec approved, not yet built)*` → `*(built)*`.

- [ ] **Step 3: docs/04 + example config.** In docs/04's profile reference (near the `models:` block), add:

```yaml
    # capture: the FR-26 capture funnel (ADR-016). Optional; defaults shown.
    # The capture automation itself is scheduled via automations.capture.
    capture:  { enrich: heuristic, archive_dir: 04-Archive/Capture }
```

In `axon.config.example.yaml`, add next to the other optional blocks:

```yaml
    capture:                                  # inbox capture funnel (ADR-016)
      enrich: heuristic                       # heuristic (default, zero tokens) | claude (chokepoint, routine tier)
      archive_dir: "04-Archive/Capture"       # where ingested inbox originals are moved (never deleted)
```

and to its automations block:

```yaml
      capture:           { enabled: true,  schedule: "*/5 * * * *",     model: none,      budget_tokens: 0, catch_up: run-once }
```

- [ ] **Step 4: docs/05.** Add a short `## Capture (FR-26, ADR-016)` section: the `capture` automation is the funnel front-end to this pipeline — own-line URLs in `00-Inbox` notes and dropped files, change-gated ticks, archive-after-ingest, failure memory, `capture.enrich` toggle; reference the spec.

- [ ] **Step 5: CHANGELOG.** Add under `### Added`:

```markdown
- **Universal capture (ADR-016, FR-26 + FR-81…FR-83)** — the new `capture`
  automation turns `00-Inbox/` into a capture funnel: paste a URL on its own
  line in any inbox note, or drop a PDF/file into the folder, and AXON ingests
  it within minutes through the standard pipeline (egress-policied, deduped,
  ledgered), files the result under `03-Resources/Knowledge/`, and moves the
  original wikilink-safely to `04-Archive/Capture/YYYY-MM/` — nothing is ever
  deleted, and inbox notes are never modified. Ticks are change-gated on the
  inbox listing; failures are remembered (no retry spam) and surfaced once in
  the review queue. Mobile capture works with zero mobile code via vault sync.
  New optional config: `capture.enrich` (heuristic default | claude via the
  chokepoint) and `capture.archive_dir`.
```

- [ ] **Step 6: Verify + commit**

Run: `go test ./internal/config/` (example-config parse tests) and `go build ./...`
Expected: PASS.

```bash
git add docs/ axon.config.example.yaml CHANGELOG.md
git commit -m "docs: capture config reference, FR-26/FR-81..83 status, CHANGELOG"
```

---

### Task 7: Final gates + behavioral smoke

- [ ] **Step 1: Gates**

```bash
go build ./... && go vet ./... && golangci-lint run && go test ./...
```
Expected: all green.

- [ ] **Step 2: Behavioral smoke (scratch vault, no user data).** Build the binary and run a real capture tick against a scratch AXON_HOME + vault in the session scratchpad:
1. Generate a scratch config (`axon setup`-style or hand-written minimal config) with the vault in the scratchpad and `automations.capture` enabled.
2. `axon init` (scaffolds `00-Inbox`).
3. Drop a small `.txt` file into `00-Inbox/` and write a note containing an own-line URL to a locally served page (`python3 -m http.server` in the scratchpad or a file drop only, if no server is convenient).
4. `axon run capture` → expect: knowledge note under `03-Resources/Knowledge/`, original in `04-Archive/Capture/<month>/`, `## Capture` section in `.axon/review-queue.md`, run row visible in `axon automations`.
5. `axon run capture` again → "skipped"/no-op (change gate or known-skip).
6. `axon run capture --dry-run`-equivalent (`automations.capture.dry_run` or the engine's dry-run path via `axon run capture --dry-run` if the flag exists — check `axon run --help`) with a fresh drop → nothing written.

- [ ] **Step 3: Commit anything outstanding; report results.**

---

## Verification (definition of done)

1. `go test ./...`, `go vet`, `golangci-lint run` green.
2. FR trace: FR-26 (Tasks 2-4), FR-81 (Task 3: file ingest + archive + sandbox), FR-82 (Tasks 2-3: change gate, failure memory, review queue, events via Bus-wired pipeline), FR-83 (Task 1 + `capturePipeline`).
3. Cardinal rules: no direct agent import (enrichment via `tokens.Manager` only); no vault deletion anywhere; inbox notes never written; archive uses `vault.FS.Move`.
4. S8: with `automations.capture` absent/disabled, nothing changes for existing installs.
