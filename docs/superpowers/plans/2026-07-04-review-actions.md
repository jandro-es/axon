# Review-Queue Dashboard Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A dashboard Review tab that accepts/dismisses `.axon/review-queue.md` items through wikilink-safe ops, plus the FR-64 chart-data export.

**Architecture:** New `internal/review` package (vault-only dep) owns parsing + actions; `vault.RewriteSystemFile` (`.axon/`-guarded) resolves queue lines; inbox-triage upgrades to structured JSON proposals; the dashboard gains `Vault` in Config, its first POST route (preflight-forcing guard), `/api/review`, and `/api/export`; the SPA gains a Review tab and export links. Spec: `docs/superpowers/specs/2026-07-04-review-actions-design.md`; ADR-020; FR-64 + FR-94…FR-96.

**Tech Stack:** Go stdlib (`regexp`, `encoding/csv`); React (existing App.jsx patterns: TABS array, `useFetch`, `Card`, `.seg` buttons); no new dependencies.

## Global Constraints

- Accepts mutate ONLY via `vault.Patch` (`axon:links` block) and `vault.Move`; prose is never touched; queue-line resolution only via `RewriteSystemFile`, which refuses any path outside `.axon/`.
- POST `/api/review/action` requires `Content-Type: application/json` AND `X-Axon-Review: 1`; the server sends no CORS headers.
- Old freeform triage lines and capture records parse as kind `info` (dismiss-only).
- Every action emits `review.accept`/`review.dismiss` on the bus.
- Every task ends with `go test ./...` green and a commit on `feature/review-actions`.

---

### Task 1: `vault.RewriteSystemFile`

**Files:**
- Modify: `internal/vault/fs.go` (after `Append`, ~line 330)
- Test: `internal/vault/rewrite_test.go` (new)

**Interfaces:**
- Produces: `(v *FS) RewriteSystemFile(rel, content string) error` — atomic temp+rename via `writeRaw`; refuses any rel whose cleaned slash-path isn't under `.axon/`.

- [ ] **Step 1: Failing test** — `internal/vault/rewrite_test.go`:

```go
package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteSystemFile(t *testing.T) {
	v := NewFS(t.TempDir())
	if err := v.Append(".axon/review-queue.md", "- [ ] item one\n"); err != nil {
		t.Fatal(err)
	}
	if err := v.RewriteSystemFile(".axon/review-queue.md", "- [x] item one ✓\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if string(data) != "- [x] item one ✓\n" {
		t.Fatalf("content = %q", data)
	}

	// The guard is code, not convention.
	for _, rel := range []string{"01-Projects/x.md", "notes.md", "../escape.md", ".axonish/x.md"} {
		if err := v.RewriteSystemFile(rel, "x"); err == nil || !strings.Contains(err.Error(), ".axon") {
			t.Fatalf("rel %q: err = %v, want .axon-only refusal", rel, err)
		}
	}
}
```

- [ ] **Step 2: Verify red** — `go test ./internal/vault/ -run TestRewriteSystemFile -v` → FAIL undefined.

- [ ] **Step 3: Implement** (fs.go):

```go
// RewriteSystemFile atomically replaces the content of an AXON system file
// under .axon/ (temp+rename, NFR-06). It REFUSES any other path: general
// note rewriting must go through Write/Patch/Move (cardinal rule 2); this
// exists solely so review-queue resolutions can flip their own checkbox
// lines (ADR-020).
func (v *FS) RewriteSystemFile(rel, content string) error {
	clean := filepath.ToSlash(filepath.Clean(rel))
	if !strings.HasPrefix(clean, ".axon/") {
		return fmt.Errorf("rewrite %q: only .axon/ system files may be rewritten", rel)
	}
	return v.writeRaw(rel, content)
}
```

- [ ] **Step 4: Run + commit** — `go test ./internal/vault/`; `git add internal/vault/ && git commit -m "feat(vault): .axon/-guarded RewriteSystemFile (ADR-020, FR-95)"`

---

### Task 2: Structured triage proposals

**Files:**
- Modify: `internal/automations/model.go` (InboxTriage, ~lines 189-239)
- Test: `internal/automations/agentic_test.go` or a new `triage_test.go`; update `standard_test.go`'s triage test

**Interfaces:**
- Produces: `parseTriage(s string) (triageOut, error)` with `triageOut{Folder string; Tags []string}`; queue line format `- [ ] triage [[<note>]] → <folder> (tags: a, b)`; the call carries `OutputSchema` + `ValidateOutput`.

- [ ] **Step 1: Failing tests** — new `internal/automations/triage_test.go`:

```go
package automations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

func TestParseTriage(t *testing.T) {
	tests := []struct {
		in      string
		folder  string
		tags    int
		wantErr bool
	}{
		{`{"folder":"02-Areas","tags":["health","routine"]}`, "02-Areas", 2, false},
		{"Sure! Here you go: {\"folder\":\"01-Projects\",\"tags\":[]}", "01-Projects", 0, false},
		{`{"folder":"05-Nope","tags":[]}`, "", 0, true},
		{`not json at all`, "", 0, true},
	}
	for _, tt := range tests {
		out, err := parseTriage(tt.in)
		if (err != nil) != tt.wantErr {
			t.Fatalf("%q: err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err == nil && (out.Folder != tt.folder || len(out.Tags) != tt.tags) {
			t.Fatalf("%q: out = %+v", tt.in, out)
		}
	}
}

func TestInboxTriageStructuredLine(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/idea.md": "a captured thought\n"})
	fake.Reply = `{"folder":"02-Areas","tags":["thinking","ideas"]}`
	if _, err := (InboxTriage{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	q, _ := os.ReadFile(filepath.Join(rc.Vault.Root(), ".axon", "review-queue.md"))
	want := "- [ ] triage [[00-Inbox/idea]] → 02-Areas (tags: thinking, ideas)"
	if !strings.Contains(string(q), want) {
		t.Fatalf("queue missing %q:\n%s", want, q)
	}
}

func TestInboxTriageRejectsBadFolder(t *testing.T) {
	rc, fake := newRC(t, map[string]string{"00-Inbox/idea.md": "thought\n"})
	fake.Reply = `{"folder":"99-Bogus","tags":[]}`
	if _, err := (InboxTriage{}).Run(context.Background(), rc); err == nil {
		t.Fatal("invalid folder must fail validation at the chokepoint")
	}
	_ = agent.Usage{} // keep the import if unused otherwise
}
```

- [ ] **Step 2: Verify red**, then **Step 3: Implement** in `model.go`. Replace the triage prompt/call/line:

```go
// triageOut is the structured triage proposal (ADR-020): parseable by the
// review queue so the dashboard can apply the move with one click.
type triageOut struct {
	Folder string   `json:"folder"`
	Tags   []string `json:"tags"`
}

var triageFolders = map[string]bool{
	"01-Projects": true, "02-Areas": true, "03-Resources": true, "04-Archive": true,
}

// parseTriage extracts and validates the model's JSON proposal (tolerating
// prose around the object, as parseEnrichment does).
func parseTriage(s string) (triageOut, error) {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return triageOut{}, fmt.Errorf("no JSON object in triage output")
	}
	var out triageOut
	if err := json.Unmarshal([]byte(s[start:end+1]), &out); err != nil {
		return triageOut{}, fmt.Errorf("triage JSON: %w", err)
	}
	if !triageFolders[out.Folder] {
		return triageOut{}, fmt.Errorf("triage folder %q not in the PARA set", out.Folder)
	}
	if len(out.Tags) > 3 {
		out.Tags = out.Tags[:3]
	}
	return out, nil
}
```

(add `"encoding/json"` import). The prompt becomes:

```go
		prompt := "Classify this captured note into one PARA folder and suggest up to 3 short tags. " +
			`Reply ONLY with JSON: {"folder": "01-Projects" | "02-Areas" | "03-Resources" | "04-Archive", "tags": ["..."]}` +
			"\n\nNOTE (data):\n<<<\n" + ingestion.NeutralizeDelimiters(firstWords(n.Body, 200)) + "\n>>>"
```

the AgentCall gains:

```go
			OutputSchema: json.RawMessage(`{"properties":{"folder":{"type":"string"},"tags":{"type":"array"}}}`),
			ValidateOutput: func(s string) error {
				_, perr := parseTriage(s)
				return perr
			},
```

(replacing the old empty-line validator), and the queue/changes lines become:

```go
		out, perr := parseTriage(text)
		if perr != nil {
			return RunResult{}, perr // unreachable in practice: validated at the chokepoint
		}
		line := fmt.Sprintf("triage [[%s]] → %s (tags: %s)", stripExt(p), out.Folder, strings.Join(out.Tags, ", "))
		// dry-run branch unchanged; then:
		fmt.Fprintf(&b, "- [ ] %s\n", line)
		changes = append(changes, fmt.Sprintf("%s → %s", p, line))
```

Update `standard_test.go`'s `TestInboxTriageProposesToReviewQueue`: set `fake.Reply = `{"folder":"03-Resources","tags":["x"]}`` (or RespondFn) and assert the new line shape. `TestInboxTriageEmptyReplyFails` in `capture_test.go`/`agentic_test.go` still passes (empty output fails `parseTriage`).

- [ ] **Step 4: Run + commit** — `go test ./internal/automations/ && go test ./...`; `git add internal/automations/ && git commit -m "feat(automations): structured triage proposals (ADR-020, FR-95)"`

---

### Task 3: `internal/review` package

**Files:**
- Create: `internal/review/review.go`
- Test: `internal/review/review_test.go`

**Interfaces:**
- Consumes: `vault.FS.{Root, Read, Patch, Move, RewriteSystemFile}`.
- Produces:
  - `type Item struct { ID, Kind, Section, Line string; Checked bool; Note, Target, Folder string; Tags []string }` (all JSON-tagged lowercase)
  - `Load(ctx context.Context, v *vault.FS) ([]Item, error)`
  - `Accept(ctx context.Context, v *vault.FS, id string) (Item, error)`
  - `Dismiss(ctx context.Context, v *vault.FS, id string) (Item, error)`
  - Kinds: `link`, `pair`, `triage`, `resurface`, `info`.

- [ ] **Step 1: Failing tests** — `internal/review/review_test.go`. The fixture reproduces every producer's REAL format (copied from the five writers):

```go
package review

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

const fixture = `
## Link suggestions for [[03-Resources/Knowledge/vectors]] (2026-07-01 10:00)
- [ ] link to [[01-Projects/search-upgrade]]

## Link suggestions (2026-07-02 01:00)
- [ ] [[01-Projects/search-upgrade]] ↔ [[03-Resources/Knowledge/embeddings]]

## Inbox triage (2026-07-03 12:30)
- [ ] triage [[00-Inbox/idea]] → 02-Areas (tags: thinking, ideas)
- [ ] triage [[00-Inbox/old-note]]: put it somewhere (freeform, pre-upgrade)

## Capture (2026-07-03 22:38)
- [x] captured meeting-notes.txt → [[03-Resources/Knowledge/meeting-notes]] (original: 04-Archive/Capture/2026-07/meeting-notes.txt)
- [ ] capture FAILED: https://127.0.0.1:1/x — refused

## Resurfaced connections (2026-07-04 07:00)
- [ ] resurface [[03-Resources/ancient]] — related to recent [[01-Projects/current]] (sim 0.82, dormant since 2026-01-14)
`

func testVault(t *testing.T) *vault.FS {
	t.Helper()
	v := vault.NewFS(t.TempDir())
	if err := os.MkdirAll(filepath.Join(v.Root(), ".axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v.Root(), ".axon", "review-queue.md"), []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return v
}

func mustLoad(t *testing.T, v *vault.FS) []Item {
	t.Helper()
	items, err := Load(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	return items
}

func findKind(items []Item, kind string) *Item {
	for i := range items {
		if items[i].Kind == kind && !items[i].Checked {
			return &items[i]
		}
	}
	return nil
}

func TestLoadParsesEveryProducerFormat(t *testing.T) {
	items := mustLoad(t, testVault(t))
	kinds := map[string]int{}
	for _, it := range items {
		kinds[it.Kind]++
	}
	// link 1, pair 1, triage 1 structured, resurface 1, info 3
	// (freeform triage + captured record + capture FAILED).
	if kinds["link"] != 1 || kinds["pair"] != 1 || kinds["triage"] != 1 ||
		kinds["resurface"] != 1 || kinds["info"] != 3 {
		t.Fatalf("kinds = %v", kinds)
	}

	link := findKind(items, "link")
	if link.Note != "03-Resources/Knowledge/vectors" || link.Target != "01-Projects/search-upgrade" {
		t.Fatalf("link = %+v", link)
	}
	pair := findKind(items, "pair")
	if pair.Note != "01-Projects/search-upgrade" || pair.Target != "03-Resources/Knowledge/embeddings" {
		t.Fatalf("pair = %+v", pair)
	}
	tri := findKind(items, "triage")
	if tri.Note != "00-Inbox/idea" || tri.Folder != "02-Areas" || len(tri.Tags) != 2 {
		t.Fatalf("triage = %+v", tri)
	}
	res := findKind(items, "resurface")
	if res.Note != "01-Projects/current" || res.Target != "03-Resources/ancient" {
		t.Fatalf("resurface = %+v", res)
	}
	// The captured record is checked; the capture FAILED line is pending info.
	var checkedInfo bool
	for _, it := range items {
		if it.Kind == "info" && it.Checked {
			checkedInfo = true
		}
	}
	if !checkedInfo {
		t.Fatal("captured record should parse as checked info")
	}
	// IDs stable + unique.
	seen := map[string]bool{}
	for _, it := range items {
		if it.ID == "" || seen[it.ID] {
			t.Fatalf("bad/duplicate ID in %+v", it)
		}
		seen[it.ID] = true
	}
}

func TestAcceptLinkAppendsToLinksBlock(t *testing.T) {
	v := testVault(t)
	ctx := context.Background()
	// The note that receives the link must exist.
	if _, err := v.Create("03-Resources/Knowledge/vectors.md", "---\ntitle: vectors\n---\nprose stays untouched\n"); err != nil {
		t.Fatal(err)
	}
	link := findKind(mustLoad(t, v), "link")

	item, err := Accept(ctx, v, link.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !item.Checked {
		t.Fatal("accepted item should come back checked")
	}
	n, _ := v.Read(ctx, "03-Resources/Knowledge/vectors.md")
	if !strings.Contains(n.Body, "axon:links:start") || !strings.Contains(n.Body, "- [[01-Projects/search-upgrade]]") {
		t.Fatalf("links block missing:\n%s", n.Body)
	}
	if !strings.Contains(n.Body, "prose stays untouched") {
		t.Fatal("prose was touched")
	}
	// Queue line flipped.
	q, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if !strings.Contains(string(q), "- [x] link to [[01-Projects/search-upgrade]] — ✓ applied") {
		t.Fatalf("queue not resolved:\n%s", q)
	}
	// Idempotence: accepting again → already resolved.
	if _, err := Accept(ctx, v, link.ID); err == nil || !strings.Contains(err.Error(), "resolved") {
		t.Fatalf("re-accept err = %v", err)
	}
	// A second accept into the same block dedupes the link line.
	pair := findKind(mustLoad(t, v), "pair")
	if _, err := v.Create("01-Projects/search-upgrade.md", "---\ntitle: s\n---\nbody\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := Accept(ctx, v, pair.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptTriageMovesNote(t *testing.T) {
	v := testVault(t)
	ctx := context.Background()
	if _, err := v.Create("00-Inbox/idea.md", "---\ntitle: idea\n---\nthought\n"); err != nil {
		t.Fatal(err)
	}
	tri := findKind(mustLoad(t, v), "triage")
	if _, err := Accept(ctx, v, tri.ID); err != nil {
		t.Fatal(err)
	}
	if !v.Exists("02-Areas/idea.md") || v.Exists("00-Inbox/idea.md") {
		t.Fatal("note not moved")
	}
}

func TestAcceptInfoNotActionable(t *testing.T) {
	v := testVault(t)
	var infoID string
	for _, it := range mustLoad(t, v) {
		if it.Kind == "info" && !it.Checked {
			infoID = it.ID
		}
	}
	if _, err := Accept(context.Background(), v, infoID); err == nil || !strings.Contains(err.Error(), "not actionable") {
		t.Fatalf("err = %v", err)
	}
	// But dismissable.
	if _, err := Dismiss(context.Background(), v, infoID); err != nil {
		t.Fatal(err)
	}
	q, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if !strings.Contains(string(q), "✗ dismissed") {
		t.Fatalf("dismiss not recorded:\n%s", q)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	v := vault.NewFS(t.TempDir())
	items, err := Load(context.Background(), v)
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%v err=%v", items, err)
	}
}
```

- [ ] **Step 2: Verify red**, then **Step 3: Implement** — `internal/review/review.go`:

```go
// Package review parses AXON's review queue (.axon/review-queue.md) into
// typed items and resolves them through the vault's wikilink-safe ops
// (ADR-020): accepted links land in axon:links managed blocks, accepted
// triage proposals perform the wikilink-safe move, and the queue line flips
// via the .axon/-guarded RewriteSystemFile. Prose is never touched.
package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/vault"
)

const queuePath = ".axon/review-queue.md"

// Item is one review-queue entry.
type Item struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"` // link | pair | triage | resurface | info
	Section string   `json:"section"`
	Line    string   `json:"line"`
	Checked bool     `json:"checked"`
	Note    string   `json:"note,omitempty"`   // the note an accept writes to / moves
	Target  string   `json:"target,omitempty"` // the note an accept links to
	Folder  string   `json:"folder,omitempty"` // triage destination
	Tags    []string `json:"tags,omitempty"`
}

var (
	sectionRe    = regexp.MustCompile(`^## (.+)$`)
	sectionForRe = regexp.MustCompile(`^## Link suggestions for \[\[([^\]]+)\]\]`)
	lineRe       = regexp.MustCompile(`^- \[([ x])\] (.*)$`)
	linkToRe     = regexp.MustCompile(`^link to \[\[([^\]]+)\]\]`)
	pairRe       = regexp.MustCompile(`^\[\[([^\]]+)\]\] ↔ \[\[([^\]]+)\]\]`)
	triageRe     = regexp.MustCompile(`^triage \[\[([^\]]+)\]\] → (\S+) \(tags: ([^)]*)\)`)
	resurfaceRe  = regexp.MustCompile(`^resurface \[\[([^\]]+)\]\] — related to recent \[\[([^\]]+)\]\]`)
)

// Load parses the queue file. A missing file is an empty queue.
func Load(ctx context.Context, v *vault.FS) ([]Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(v.Root(), filepath.FromSlash(queuePath)))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read review queue: %w", err)
	}

	var items []Item
	section, sectionNote := "", ""
	for _, raw := range strings.Split(string(data), "\n") {
		if m := sectionRe.FindStringSubmatch(raw); m != nil {
			section = m[1]
			sectionNote = ""
			if fm := sectionForRe.FindStringSubmatch(raw); fm != nil {
				sectionNote = fm[1]
			}
			continue
		}
		m := lineRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		it := Item{
			Section: section,
			Line:    raw,
			Checked: m[1] == "x",
			Kind:    "info",
		}
		body := m[2]
		switch {
		case linkToRe.MatchString(body):
			lm := linkToRe.FindStringSubmatch(body)
			if sectionNote != "" {
				it.Kind, it.Note, it.Target = "link", sectionNote, lm[1]
			}
		case pairRe.MatchString(body):
			pm := pairRe.FindStringSubmatch(body)
			it.Kind, it.Note, it.Target = "pair", pm[1], pm[2]
		case triageRe.MatchString(body):
			tm := triageRe.FindStringSubmatch(body)
			it.Kind, it.Note, it.Folder = "triage", tm[1], tm[2]
			for _, tag := range strings.Split(tm[3], ",") {
				if tag = strings.TrimSpace(tag); tag != "" {
					it.Tags = append(it.Tags, tag)
				}
			}
		case resurfaceRe.MatchString(body):
			rm := resurfaceRe.FindStringSubmatch(body)
			it.Kind, it.Target, it.Note = "resurface", rm[1], rm[2]
		}
		sum := sha256.Sum256([]byte(it.Section + "\x00" + it.Line))
		it.ID = hex.EncodeToString(sum[:])[:12]
		items = append(items, it)
	}
	return items, nil
}

// Accept applies a pending item through wikilink-safe ops and resolves its
// queue line.
func Accept(ctx context.Context, v *vault.FS, id string) (Item, error) {
	it, err := find(ctx, v, id)
	if err != nil {
		return Item{}, err
	}
	switch it.Kind {
	case "link", "pair", "resurface":
		if err := appendToLinksBlock(ctx, v, it.Note, it.Target); err != nil {
			return Item{}, err
		}
	case "triage":
		dest := it.Folder + "/" + path.Base(it.Note) + ".md"
		if err := v.Move(ctx, it.Note+".md", dest); err != nil {
			return Item{}, err
		}
	default:
		return Item{}, fmt.Errorf("item %s (%s) is not actionable — dismiss it instead", id, it.Kind)
	}
	return mark(ctx, v, it, "✓ applied")
}

// Dismiss resolves a pending item without applying it.
func Dismiss(ctx context.Context, v *vault.FS, id string) (Item, error) {
	it, err := find(ctx, v, id)
	if err != nil {
		return Item{}, err
	}
	return mark(ctx, v, it, "✗ dismissed")
}

func find(ctx context.Context, v *vault.FS, id string) (Item, error) {
	items, err := Load(ctx, v)
	if err != nil {
		return Item{}, err
	}
	for _, it := range items {
		if it.ID == id {
			if it.Checked {
				return Item{}, fmt.Errorf("item %s is already resolved", id)
			}
			return it, nil
		}
	}
	return Item{}, fmt.Errorf("item %s not found", id)
}

// appendToLinksBlock adds "- [[target]]" to the note's axon:links managed
// block (created if absent; idempotent). Prose is never touched (cardinal
// rule 2): Patch replaces only the managed region.
func appendToLinksBlock(ctx context.Context, v *vault.FS, note, target string) error {
	notePath := note + ".md"
	n, err := v.Read(ctx, notePath)
	if err != nil {
		return fmt.Errorf("accept target note: %w", err)
	}
	existing := extractBlock(n.Body, "links")
	linkLine := "- [[" + target + "]]"
	if strings.Contains(existing, linkLine) {
		return nil // already linked: accept is idempotent
	}
	content := linkLine
	if strings.TrimSpace(existing) != "" {
		content = strings.TrimSpace(existing) + "\n" + linkLine
	}
	return v.Patch(ctx, notePath, "links", content)
}

// extractBlock returns the inner content of an axon:<name> managed block.
func extractBlock(body, name string) string {
	start := "<!-- axon:" + name + ":start -->"
	end := "<!-- axon:" + name + ":end -->"
	i := strings.Index(body, start)
	if i < 0 {
		return ""
	}
	rest := body[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// mark flips the item's queue line to checked with a resolution suffix.
func mark(ctx context.Context, v *vault.FS, it Item, suffix string) (Item, error) {
	if err := ctx.Err(); err != nil {
		return Item{}, err
	}
	abs := filepath.Join(v.Root(), filepath.FromSlash(queuePath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return Item{}, err
	}
	newLine := strings.Replace(it.Line, "- [ ]", "- [x]", 1) +
		" — " + suffix + " " + time.Now().UTC().Format("2006-01-02")
	content := strings.Replace(string(data), it.Line, newLine, 1)
	if content == string(data) {
		return Item{}, fmt.Errorf("item %s: queue line changed underneath — reload", it.ID)
	}
	if err := v.RewriteSystemFile(queuePath, content); err != nil {
		return Item{}, err
	}
	it.Checked = true
	it.Line = newLine
	return it, nil
}
```

- [ ] **Step 4: Run + commit** — `go test ./internal/review/ -v && go test ./...`; `git add internal/review/ && git commit -m "feat(review): typed queue parser + wikilink-safe accept/dismiss (FR-94, FR-95)"`

---

### Task 4: Dashboard endpoints — review + export

**Files:**
- Modify: `internal/dashboard/server.go` (Config, Handler, new handlers)
- Create: `internal/dashboard/export.go` (CSV serialization)
- Test: `internal/dashboard/review_api_test.go` (new; follow `dashboard_test.go`'s server-construction pattern)

**Interfaces:**
- Consumes: `review.Load/Accept/Dismiss`, `vault.FS`.
- Produces: `Config.Vault *vault.FS`; routes `GET /api/review`, `POST /api/review/action`, `GET /api/export`; package-doc invariant rewritten.

- [ ] **Step 1: Failing tests** — `internal/dashboard/review_api_test.go` (mirror `dashboard_test.go`'s setup: in-memory DB + `New(Config{...})` + `httptest.NewServer(s.Handler())`; check that file for the exact helper shapes and reuse them):

```go
package dashboard

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/events"
	"github.com/jandro-es/axon/internal/vault"
)

func reviewTestServer(t *testing.T) (*httptest.Server, *vault.FS, *events.Bus) {
	t.Helper()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	v := vault.NewFS(t.TempDir())
	_ = os.MkdirAll(filepath.Join(v.Root(), ".axon"), 0o755)
	queue := "## Link suggestions for [[notes/a]] (2026-07-04 10:00)\n- [ ] link to [[notes/b]]\n"
	_ = os.WriteFile(filepath.Join(v.Root(), ".axon", "review-queue.md"), []byte(queue), 0o644)
	_, _ = v.Create("notes/a.md", "---\ntitle: a\n---\nbody\n")
	bus := events.NewBus()
	s := New(Config{Profile: "test", DB: d, Vault: v, Bus: bus})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, v, bus
}

func TestReviewGetAndAccept(t *testing.T) {
	srv, v, _ := reviewTestServer(t)

	res, err := http.Get(srv.URL + "/api/review")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Items   []map[string]any `json:"items"`
		Pending int              `json:"pending"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Pending != 1 || len(out.Items) != 1 {
		t.Fatalf("review = %+v", out)
	}
	id := out.Items[0]["id"].(string)

	body, _ := json.Marshal(map[string]string{"id": id, "action": "accept"})
	req, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Axon-Review", "1")
	res2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res2.StatusCode != 200 {
		t.Fatalf("accept status = %d", res2.StatusCode)
	}
	n, _ := v.Read(t.Context(), "notes/a.md")
	if !strings.Contains(n.Body, "- [[notes/b]]") {
		t.Fatalf("link not applied:\n%s", n.Body)
	}
}

func TestReviewActionGuards(t *testing.T) {
	srv, _, _ := reviewTestServer(t)
	body := []byte(`{"id":"x","action":"accept"}`)

	// Missing the custom header → 403.
	req, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	res, _ := http.DefaultClient.Do(req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("no-header status = %d, want 403", res.StatusCode)
	}

	// Wrong content type → 403.
	req2, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req2.Header.Set("Content-Type", "text/plain")
	req2.Header.Set("X-Axon-Review", "1")
	res2, _ := http.DefaultClient.Do(req2)
	if res2.StatusCode != http.StatusForbidden {
		t.Fatalf("bad-ct status = %d, want 403", res2.StatusCode)
	}

	// Unknown id → 404-ish (400/404 accepted).
	req3, _ := http.NewRequest("POST", srv.URL+"/api/review/action", bytes.NewReader(body))
	req3.Header.Set("Content-Type", "application/json")
	req3.Header.Set("X-Axon-Review", "1")
	res3, _ := http.DefaultClient.Do(req3)
	if res3.StatusCode < 400 || res3.StatusCode >= 500 {
		t.Fatalf("unknown-id status = %d, want 4xx", res3.StatusCode)
	}
}

func TestExportCSVAndJSON(t *testing.T) {
	srv, _, _ := reviewTestServer(t)
	res, err := http.Get(srv.URL + "/api/export?dataset=runs&format=csv")
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Type"), "text/csv") {
		t.Fatalf("csv export: status %d ct %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if cd := res.Header.Get("Content-Disposition"); !strings.Contains(cd, "axon-runs-") {
		t.Fatalf("disposition = %q", cd)
	}

	res2, _ := http.Get(srv.URL + "/api/export?dataset=tokens&format=json")
	if res2.StatusCode != 200 {
		t.Fatalf("json export status = %d", res2.StatusCode)
	}
	res3, _ := http.Get(srv.URL + "/api/export?dataset=bogus&format=csv")
	if res3.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus dataset status = %d, want 400", res3.StatusCode)
	}
}
```

- [ ] **Step 2: Verify red**, then **Step 3: Implement.**

3a. `server.go`: package doc line 4-5 becomes *"…It holds no secrets and binds to loopback only (FR-63). It never calls Claude and never free-form writes; the only mutations are review-queue resolutions applied through the vault's wikilink-safe ops (ADR-020)."* `Config` gains:

```go
	// Vault enables the Review tab's accept/dismiss actions (ADR-020). nil
	// disables the review endpoints (read-only deployments).
	Vault *vault.FS
```

Routes added in `Handler()`:

```go
	mux.HandleFunc("GET /api/review", s.jsonHandler(s.dataReview))
	mux.HandleFunc("POST /api/review/action", s.handleReviewAction)
	mux.HandleFunc("GET /api/export", s.handleExport)
```

Handlers (server.go):

```go
func (s *Server) dataReview(ctx context.Context, _ *http.Request) (any, error) {
	if s.cfg.Vault == nil {
		return map[string]any{"items": []review.Item{}, "pending": 0}, nil
	}
	items, err := review.Load(ctx, s.cfg.Vault)
	if err != nil {
		return nil, err
	}
	pending := 0
	for _, it := range items {
		if !it.Checked {
			pending++
		}
	}
	if items == nil {
		items = []review.Item{}
	}
	return map[string]any{"items": items, "pending": pending}, nil
}

// handleReviewAction is the dashboard's only mutating endpoint (ADR-020).
// The JSON content type + custom header force a CORS preflight that no
// cross-origin page can pass (this server sends no CORS headers), on top of
// the loopback bind and Host guard.
func (s *Server) handleReviewAction(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Vault == nil {
		http.Error(w, "review actions unavailable (no vault wired)", http.StatusServiceUnavailable)
		return
	}
	if r.Header.Get("X-Axon-Review") != "1" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var in struct {
		ID     string `json:"id"`
		Action string `json:"action"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var item review.Item
	var err error
	switch in.Action {
	case "accept":
		item, err = review.Accept(r.Context(), s.cfg.Vault, in.ID)
	case "dismiss":
		item, err = review.Dismiss(r.Context(), s.cfg.Vault, in.ID)
	default:
		http.Error(w, "action must be accept or dismiss", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(events.Event{
			Level: events.LevelInfo, Kind: "review." + in.Action,
			Message: in.Action + ": " + item.Line,
			Data:    map[string]any{"profile": s.cfg.Profile, "id": item.ID, "kind": item.Kind},
		})
	}
	writeJSON(w, map[string]any{"item": item})
}
```

(add `"strings"`, review + vault imports).

3b. `internal/dashboard/export.go` — the FR-64 serializer:

```go
package dashboard

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jandro-es/axon/internal/db"
)

// handleExport serves any chart dataset as CSV or JSON (FR-64). JSON reuses
// the same data functions the charts poll; CSV flattens with explicit
// columns. The graph dataset is JSON-only (nested nodes/edges).
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	dataset := r.URL.Query().Get("dataset")
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "csv"
	}

	fetch := map[string]func() (any, error){
		"tokens":    func() (any, error) { return s.dataTokens(r.Context(), r) },
		"runs":      func() (any, error) { return s.dataRuns(r.Context(), r) },
		"ingestion": func() (any, error) { return s.dataIngestion(r.Context(), r) },
		"vault":     func() (any, error) { return s.dataVault(r.Context(), r) },
		"graph":     func() (any, error) { return s.dataGraph(r.Context(), r) },
		"activity":  func() (any, error) { return s.dataActivity(r.Context(), r) },
	}
	fn, ok := fetch[dataset]
	if !ok {
		http.Error(w, "unknown dataset (tokens|runs|ingestion|vault|graph|activity)", http.StatusBadRequest)
		return
	}
	data, err := fn()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("axon-%s-%s", dataset, time.Now().UTC().Format("2006-01-02"))
	switch format {
	case "json":
		w.Header().Set("Content-Disposition", `attachment; filename=`+name+`.json`)
		writeJSON(w, data)
	case "csv":
		rows, header, ok := csvRows(dataset, data)
		if !ok {
			http.Error(w, "dataset "+dataset+" is JSON-only (nested); use format=json", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename=`+name+`.csv`)
		cw := csv.NewWriter(w)
		_ = cw.Write(header)
		for _, row := range rows {
			_ = cw.Write(row)
		}
		cw.Flush()
	default:
		http.Error(w, "format must be csv or json", http.StatusBadRequest)
	}
}

// csvRows flattens the flat datasets; ok=false for nested ones (graph).
func csvRows(dataset string, data any) ([][]string, []string, bool) {
	i := strconv.Itoa
	i64 := func(n int64) string { return strconv.FormatInt(n, 10) }
	switch dataset {
	case "tokens":
		rows := data.([]db.TokenBucket)
		out := make([][]string, 0, len(rows))
		for _, b := range rows {
			out = append(out, []string{b.Day, b.Operation, b.Model, i64(b.Input), i64(b.Output), i64(b.CacheRead), i64(b.CacheWrite)})
		}
		return out, []string{"day", "operation", "model", "input", "output", "cache_read", "cache_write"}, true
	case "runs":
		rows := data.([]db.RunRow)
		out := make([][]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, []string{i64(r.ID), r.Automation, r.StartedAt, r.FinishedAt, r.Status, r.SkipReason, i64(r.Tokens)})
		}
		return out, []string{"id", "automation", "started_at", "finished_at", "status", "skip_reason", "tokens"}, true
	case "activity":
		rows := data.([]db.EventRow)
		out := make([][]string, 0, len(rows))
		for _, e := range rows {
			out = append(out, []string{i64(e.ID), e.TS, e.Level, e.Kind, e.Message})
		}
		return out, []string{"id", "ts", "level", "kind", "message"}, true
	case "ingestion":
		m := data.(map[string]any)
		rows := m["series"].([]db.SourceBucket)
		out := make([][]string, 0, len(rows))
		for _, b := range rows {
			out = append(out, []string{b.Day, b.Status, i(b.Count)})
		}
		return out, []string{"day", "status", "count"}, true
	case "vault":
		m := data.(map[string]any)
		rows := m["growth"].([]db.GrowthPoint)
		out := make([][]string, 0, len(rows))
		for _, g := range rows {
			out = append(out, []string{g.Day, i(g.Notes), i(g.Words)})
		}
		return out, []string{"day", "notes", "words"}, true
	default:
		return nil, nil, false
	}
}
```

(Type-assert shapes against the actual data functions — `dataRuns` returns `[]db.RunRow` etc.; if a helper returns `(any, error)` from a different concrete type, adjust the assertion, not the shape.)

- [ ] **Step 4: Run + commit** — `go test ./internal/dashboard/ -v && go test ./...`; `git add internal/dashboard/ && git commit -m "feat(dashboard): review endpoints + FR-64 export (FR-94, FR-96)"`

---

### Task 5: SPA — Review tab + export links

**Files:**
- Modify: `web/src/App.jsx` (TABS, SSE_KINDS, new components, tab block)
- Modify: `web/src/styles.css` (a few rules)

- [ ] **Step 1: SSE kinds + TABS.** Add to `SSE_KINDS`: `'review.accept', 'review.dismiss',` and to `TABS`: `['review', 'Review'],` (after `'automations'`).

- [ ] **Step 2: Components** (add near ActivityCard):

```jsx
function postReviewAction(id, action) {
  return fetch('/api/review/action', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Axon-Review': '1' },
    body: JSON.stringify({ id, action }),
  }).then(async (r) => {
    if (!r.ok) throw new Error(await r.text())
    return r.json()
  })
}

function ExportLinks({ dataset }) {
  return (
    <span className="export-links">
      <a href={`/api/export?dataset=${dataset}&format=csv`}>⤓ csv</a>
      <a href={`/api/export?dataset=${dataset}&format=json`}>⤓ json</a>
    </span>
  )
}

const KIND_LABEL = { link: 'Link suggestion', pair: 'Link suggestion', triage: 'Inbox triage', resurface: 'Resurfaced', info: 'Record' }

function ReviewTab({ span }) {
  const { data, error } = useFetch('/api/review', 5000)
  const [busy, setBusy] = useState(null)      // item id in flight
  const [errs, setErrs] = useState({})        // id -> error text
  const [nonce, setNonce] = useState(0)       // bump to refetch after action
  const { data: fresh } = useFetch(`/api/review?n=${nonce}`, 3600_000)
  const view = fresh || data
  const items = view?.items || []
  const pending = items.filter((it) => !it.checked)
  const resolved = items.filter((it) => it.checked).slice(-15)

  const act = (id, action) => {
    setBusy(id)
    postReviewAction(id, action)
      .then(() => setErrs((e) => ({ ...e, [id]: null })))
      .catch((err) => setErrs((e) => ({ ...e, [id]: String(err.message || err) })))
      .finally(() => { setBusy(null); setNonce((n) => n + 1) })
  }

  const describe = (it) => {
    if (it.kind === 'triage') return <>move <b>[[{it.note}]]</b> → <b>{it.folder}</b>{it.tags?.length ? ` (${it.tags.join(', ')})` : ''}</>
    if (it.kind === 'link' || it.kind === 'pair') return <>link <b>[[{it.note}]]</b> → <b>[[{it.target}]]</b></>
    if (it.kind === 'resurface') return <>resurface <b>[[{it.target}]]</b> for <b>[[{it.note}]]</b></>
    return it.line.replace(/^- \[.\] /, '')
  }

  return (
    <Card title="Review queue" meta={`${pending.length} pending`} span={span}>
      {error && <Empty>daemon unreachable</Empty>}
      <div className="list">
        {pending.map((it) => (
          <div className="li review-item" key={it.id}>
            <span className={`kind kind-${it.kind}`}>{KIND_LABEL[it.kind] || it.kind}</span>
            <span className="msg">{describe(it)}</span>
            <span className="review-actions">
              {it.kind !== 'info' && (
                <button disabled={busy === it.id} onClick={() => act(it.id, 'accept')}>accept</button>
              )}
              <button className="ghost" disabled={busy === it.id} onClick={() => act(it.id, 'dismiss')}>dismiss</button>
            </span>
            {errs[it.id] && <span className="review-err">{errs[it.id]}</span>}
          </div>
        ))}
        {pending.length === 0 && <Empty>Queue is clear. Automations append proposals here for your review.</Empty>}
      </div>
      {resolved.length > 0 && (
        <div className="list resolved">
          {resolved.map((it) => (
            <div className="li dim" key={it.id}><span className="msg">{it.line.replace(/^- \[.\] /, '')}</span></div>
          ))}
        </div>
      )}
    </Card>
  )
}
```

Tab block (with the other `{tab === …}` blocks): `{tab === 'review' && <ReviewTab span="span-12" />}`. Nav badge: change the Review button label render to append the pending count — simplest: hoist `const { data: reviewMeta } = useFetch('/api/review', 15000)` in `App()` and render `Review{reviewMeta?.pending ? ` · ${reviewMeta.pending}` : ''}` for that tab's button (special-case in the TABS map loop or give TABS a render label function — keep it a one-line special case).

Export links: in the Tokens tab block add `<ExportLinks dataset="tokens" />` beside the existing cards (a `span-12` slim row or inside a Card meta), and similarly `runs` on Automations, `ingestion` + `vault` on Knowledge, `activity` on Activity. Placement is visual judgment — a small `.export-row` div per tab is fine.

- [ ] **Step 3: CSS** (`styles.css`):

```css
.review-item { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.review-item .kind { font-size: 11px; color: var(--faint, #616d83); min-width: 110px; }
.review-actions { margin-left: auto; display: flex; gap: 6px; }
.review-actions button { cursor: pointer; }
.review-actions .ghost { opacity: .6; }
.review-err { flex-basis: 100%; color: #fb6f6f; font-size: 12px; }
.li.dim { opacity: .45; }
.export-links { display: inline-flex; gap: 10px; font-size: 12px; }
.export-row { grid-column: span 12; display: flex; justify-content: flex-end; gap: 12px; }
```

(match the file's existing variable/color conventions when editing).

- [ ] **Step 4: Build + verify** — `cd web && npm install && npm run build` → dist builds clean; `go build ./...` embeds it. Open with the dev proxy (`npm run dev` against a running scratch daemon) or rely on Task 7's smoke.

- [ ] **Step 5: Commit** — `git add web/ && git commit -m "feat(web): Review tab with accept/dismiss + chart export links (FR-94, FR-96)"`

---

### Task 6: Wiring + docs

**Files:**
- Modify: `cmd/axon/start_cmd.go` (dashboard.Config gains `Vault: deps.vault`)
- Modify: `docs/02-architecture.md` (ADR-020 → built), `docs/03-requirements.md` (section → built; FR-64 row + status banner flipped), `docs/09-component-dashboard-observability.md` (Review tab, export, revised invariant), `CLAUDE.md` (dashboard dependency line), `CHANGELOG.md`

- [ ] **Step 1: Wiring** — in `start_cmd.go`'s `dashboard.Config{...}` literal add `Vault: deps.vault,`.

- [ ] **Step 2: Docs.** Flip ADR-020 + the docs/03 section headers to `*(built)*` (past-tense intros). FR-64 row (docs/03 ~line 96): append `**Implemented** (FR-96/ADR-020): `/api/export` + per-card links.` Status banner (~line 15): the "remaining C item (FR-64…)" clause becomes "every C item except FR-76 (documented caveat) is now implemented". docs/09: add the Review tab + export to the surface list and update the read-only phrasing to the ADR-020 form. CLAUDE.md repo-structure dashboard line: `dashboard/ # dashboard HTTP + SSE handlers (Go) serving the SPA, streaming events, and resolving review-queue items (ADR-020)`.

- [ ] **Step 3: CHANGELOG** under Added:

```markdown
- **Review-queue actions on the dashboard (ADR-020, FR-94…FR-96)** — a new
  Review tab lists every pending proposal (link suggestions, structured
  inbox-triage moves, resurfaced connections, capture records) with one-click
  accept/dismiss. Accepts are wikilink-safe by construction: links land in
  the note's `axon:links` managed block, triage moves go through the
  link-rewriting `vault.Move`, and the queue file itself is only touched by
  the new `.axon/`-guarded rewriter. The dashboard's mutation surface is
  exactly these resolutions (JSON + custom-header guard forcing a CORS
  preflight; loopback + Host-guard unchanged). Inbox-triage now emits
  structured JSON proposals so its accepts actually move notes. Also ships
  **FR-64** — every chart's data exports as CSV/JSON — closing the final
  open requirement of the original v1 contract.
```

- [ ] **Step 4: Run + commit** — `go build ./... && go test ./...`; `git add -A && git commit -m "feat: wire review actions into the daemon; docs, CHANGELOG (FR-94..96, FR-64)"`

---

### Task 7: Final gates + live smoke

- [ ] **Step 1: Gates** — `go build ./... && go vet ./... && golangci-lint run && go test ./...` → green; `cd web && npm run build` → clean.
- [ ] **Step 2: Live smoke** (scratch env): rebuild binary with the fresh dist; seed the scratch vault's queue (`.axon/review-queue.md`) with one line of each actionable format + create the referenced notes; start the daemon (`axon start` backgrounded or `--once`? — the dashboard needs the server: run `axon start` in the background with the scratch config); then via curl: `GET /api/review` (items parsed), `POST /api/review/action` accept a link (verify `axon:links` block in the note), accept the triage (verify the move), dismiss one, `GET /api/export?dataset=runs&format=csv` (CSV downloads); confirm `review.accept` rows in `/api/activity`. Kill the daemon. Optionally open the dashboard in a browser for a visual pass.
- [ ] **Step 3: Commit anything outstanding; report.**

---

## Verification (definition of done)

1. Gates green; SPA builds; one-shot argv etc. untouched (no agent/tokens changes in this slice).
2. FR trace: FR-94 (Tasks 3-5), FR-95 (Tasks 1-3), FR-96/FR-64 (Tasks 4-5).
3. Cardinal rule 2 audit: the only new write paths are `Patch`(axon:links), `Move`, and `RewriteSystemFile` (`.axon/`-guarded, tested).
4. Live smoke proves the loop: queue → dashboard → accept → wikilink-safe application → resolved line → event.
