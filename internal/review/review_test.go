package review

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestCompact(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -10).Format("2006-01-02")  // archivable
	fresh := now.AddDate(0, 0, -2).Format("2006-01-02") // kept
	content := fmt.Sprintf(`
## Link suggestions (2026-06-20 01:00)
- [x] [[a]] ↔ [[b]] — ✓ applied %s
- [x] [[c]] ↔ [[d]] — ✗ dismissed %s

## Inbox triage (2026-06-25 12:30)
- [x] triage [[00-Inbox/idea]] → 02-Areas (tags: t) — ✓ applied %s
- [ ] triage [[00-Inbox/next]] → 02-Areas (tags: t)

## Capture (2026-07-03 22:38)
- [x] captured meeting-notes.txt → [[03-Resources/Knowledge/meeting-notes]] (original: x.txt)
`, old, fresh, old)

	kept, archived := compact(content, now)

	// The Link suggestions header stays: its fresh dismissal is still kept.
	for _, want := range []string{"✗ dismissed " + fresh, "- [ ] triage [[00-Inbox/next]]", "captured meeting-notes.txt", "## Link suggestions", "## Inbox triage", "## Capture"} {
		if !strings.Contains(kept, want) {
			t.Errorf("kept missing %q\nkept:\n%s", want, kept)
		}
	}
	if strings.Contains(kept, "✓ applied "+old) {
		t.Errorf("kept still contains old resolution\nkept:\n%s", kept)
	}
	// Archived lines carry their section headers.
	for _, want := range []string{"## Link suggestions (2026-06-20 01:00)", "[[a]] ↔ [[b]] — ✓ applied " + old, "## Inbox triage (2026-06-25 12:30)", "triage [[00-Inbox/idea]]"} {
		if !strings.Contains(archived, want) {
			t.Errorf("archived missing %q\narchived:\n%s", want, archived)
		}
	}
	// The resolved-but-dateless capture line is never archived (no guesswork).
	if strings.Contains(archived, "captured meeting-notes.txt") {
		t.Error("dateless resolved line must not be archived")
	}
}

func TestCompactNothingToArchive(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	content := "\n## Link suggestions (2026-07-04 01:00)\n- [ ] [[a]] ↔ [[b]]\n"
	_, archived := compact(content, now)
	if archived != "" {
		t.Fatalf("archived should be empty, got %q", archived)
	}
}

// TestDismissCompactsOldResolved: resolving an item also archives resolved
// lines past the threshold, drops their emptied section, and leaves the
// remaining items' IDs stable (FR-103).
func TestDismissCompactsOldResolved(t *testing.T) {
	v := vault.NewFS(t.TempDir())
	if err := os.MkdirAll(filepath.Join(v.Root(), ".axon"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().AddDate(0, 0, -10).Format("2006-01-02")
	queue := fmt.Sprintf(`
## Link suggestions (2026-06-20 01:00)
- [x] [[old-a]] ↔ [[old-b]] — ✓ applied %s

## Resurfaced connections (2026-07-04 07:00)
- [ ] resurface [[dormant]] — related to recent [[current]] (sim 0.82, dormant since 2026-01-14)
- [ ] resurface [[other]] — related to recent [[current]] (sim 0.80, dormant since 2026-02-01)
`, old)
	if err := os.WriteFile(filepath.Join(v.Root(), ".axon", "review-queue.md"), []byte(queue), 0o644); err != nil {
		t.Fatal(err)
	}

	items := mustLoad(t, v)
	target := findKind(items, "resurface")
	if target == nil {
		t.Fatal("no pending resurface item")
	}
	other := ""
	for _, it := range items {
		if it.Kind == "resurface" && it.ID != target.ID {
			other = it.ID
		}
	}

	if _, err := Dismiss(context.Background(), v, target.ID); err != nil {
		t.Fatal(err)
	}

	// Old resolved line + its section moved to the archive.
	arch, err := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue-archive.md"))
	if err != nil {
		t.Fatalf("archive not written: %v", err)
	}
	if !strings.Contains(string(arch), "[[old-a]] ↔ [[old-b]]") || !strings.Contains(string(arch), "## Link suggestions (2026-06-20 01:00)") {
		t.Fatalf("archive content wrong:\n%s", arch)
	}
	qdata, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if strings.Contains(string(qdata), "old-a") || strings.Contains(string(qdata), "## Link suggestions (2026-06-20 01:00)") {
		t.Fatalf("queue not compacted:\n%s", qdata)
	}
	// The just-dismissed line (today's date, < 7 days) is still visible.
	if !strings.Contains(string(qdata), "✗ dismissed") {
		t.Fatalf("fresh resolution vanished:\n%s", qdata)
	}

	// The surviving pending item keeps its identity across the compaction.
	after := mustLoad(t, v)
	found := false
	for _, it := range after {
		if it.ID == other && !it.Checked {
			found = true
		}
	}
	if !found {
		t.Fatalf("pending item lost or ID changed after compaction: %+v", after)
	}
}
