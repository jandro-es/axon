# RSS/Feed Subscriptions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An hourly `subscriptions` automation that polls config-declared RSS/Atom feeds through the egress-policied fetcher and ingests new items through the standard pipeline, with subscribe-from-now, per-tick caps, and mark-seen-after-attempt.

**Architecture:** Capture's (ADR-016) poll pattern verbatim: registry automation, `automation_state` JSON row for the seen-state, per-call pipeline copy for the enrichment toggle, per-item failures isolated. New surface: the gofeed dependency (ADR-019), `SubscriptionsConfig`, and `internal/automations/subscriptions.go`. Spec: `docs/superpowers/specs/2026-07-04-subscriptions-design.md`; FR-91…FR-93.

**Tech Stack:** `github.com/mmcdole/gofeed` (the one new dep). Existing seams: `Pipeline.Fetcher` (egress/SSRF/auth), `pipeline.Ingest`, `db.GetSourceByURL`, `db.GetCursor/SetCursor`, the `newRC` test harness + `stubFetcher` pattern from `capture_test.go`.

## Global Constraints

- Enrichment is the only model path, and only when `subscriptions.enrich: claude` (routine tier through the chokepoint) — never a direct agent call.
- Subscribe-from-now: a feed's first tick marks current entries seen, ingests nothing.
- `max_per_tick` default 5; seen-state capped at 500 URLs/feed (keep the newest); items marked seen after the attempt, success or failure.
- Feed-level fetch/parse failure never aborts other feeds; dry-run may fetch/parse (read-only) but ingests nothing and persists nothing.
- Every task ends with `go test ./...` green and a commit on `feature/subscriptions`.

---

### Task 1: Dependency + config block

**Files:**
- Modify: `go.mod`/`go.sum` (`go get github.com/mmcdole/gofeed@latest`)
- Modify: `internal/config/types.go` (SubscriptionsConfig + Profile field)
- Modify: `internal/config/load.go` (validation in the profiles loop)
- Test: `internal/config/subscriptions_test.go` (new)

**Interfaces:**
- Produces: `config.SubscriptionsConfig{Enrich string; MaxPerTick int; Feeds []Feed}`, `config.Feed{URL string}`, methods `(SubscriptionsConfig) EnrichMode() string` (default `"heuristic"`) and `(SubscriptionsConfig) PerTick() int` (default 5); `Profile.Subscriptions SubscriptionsConfig`; `validateSubscriptions(c SubscriptionsConfig) error`.

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/mmcdole/gofeed@latest && go mod tidy`
Expected: go.mod gains `github.com/mmcdole/gofeed` (pure Go; check `go mod graph | grep gofeed` shows only pure-Go transitive deps — goquery/net/html etc.).

- [ ] **Step 2: Failing config test** — `internal/config/subscriptions_test.go`:

```go
package config

import "testing"

func TestSubscriptionsDefaults(t *testing.T) {
	var c SubscriptionsConfig
	if c.EnrichMode() != "heuristic" || c.PerTick() != 5 {
		t.Fatalf("defaults = %q/%d, want heuristic/5", c.EnrichMode(), c.PerTick())
	}
	c = SubscriptionsConfig{Enrich: "claude", MaxPerTick: 2}
	if c.EnrichMode() != "claude" || c.PerTick() != 2 {
		t.Fatalf("overrides not honored: %+v", c)
	}
}

func TestValidateSubscriptions(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SubscriptionsConfig
		wantErr bool
	}{
		{"zero ok", SubscriptionsConfig{}, false},
		{"good feed", SubscriptionsConfig{Feeds: []Feed{{URL: "https://example.com/feed.xml"}}}, false},
		{"bad scheme", SubscriptionsConfig{Feeds: []Feed{{URL: "ftp://x/feed"}}}, true},
		{"empty url", SubscriptionsConfig{Feeds: []Feed{{URL: ""}}}, true},
		{"bad enrich", SubscriptionsConfig{Enrich: "gpt"}, true},
		{"negative cap", SubscriptionsConfig{MaxPerTick: -1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateSubscriptions(tt.cfg); (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 3: Verify red** — `go test ./internal/config/ -run TestSubscriptions -v` → FAIL undefined.

- [ ] **Step 4: Implement.** `internal/config/types.go`, next to `CaptureConfig`:

```go
// SubscriptionsConfig declares the RSS/Atom feeds AXON polls (ADR-019).
// Optional: an absent block means no feeds and the automation skips.
type SubscriptionsConfig struct {
	// Enrich selects metadata enrichment for ingested items: "heuristic"
	// (default, zero tokens) or "claude" (chokepoint, routine tier).
	Enrich string `yaml:"enrich,omitempty"`
	// MaxPerTick caps new items ingested per feed per tick (default 5).
	MaxPerTick int `yaml:"max_per_tick,omitempty"`
	// Feeds are the subscribed feed URLs.
	Feeds []Feed `yaml:"feeds,omitempty"`
}

// Feed is one subscribed feed.
type Feed struct {
	URL string `yaml:"url"`
}

// EnrichMode returns the enrichment mode, defaulting to "heuristic".
func (c SubscriptionsConfig) EnrichMode() string {
	if c.Enrich == "" {
		return "heuristic"
	}
	return c.Enrich
}

// PerTick returns the per-feed per-tick ingestion cap, defaulting to 5.
func (c SubscriptionsConfig) PerTick() int {
	if c.MaxPerTick <= 0 {
		return 5
	}
	return c.MaxPerTick
}

// validateSubscriptions applies the subscriptions rules (ADR-019).
func validateSubscriptions(c SubscriptionsConfig) error {
	if c.Enrich != "" && c.Enrich != "heuristic" && c.Enrich != "claude" {
		return fmt.Errorf("subscriptions.enrich must be heuristic or claude (got %q)", c.Enrich)
	}
	if c.MaxPerTick < 0 {
		return fmt.Errorf("subscriptions.max_per_tick must be >= 0 (got %d)", c.MaxPerTick)
	}
	for _, f := range c.Feeds {
		u, err := url.Parse(f.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("subscriptions feed %q must be an http(s) URL", f.URL)
		}
	}
	return nil
}
```

(Add `"net/url"` import.) Add `Subscriptions SubscriptionsConfig \`yaml:"subscriptions"\`` to `Profile` (after `Capture`). In `load.go`'s profiles loop, add:

```go
		if err := validateSubscriptions(p.Subscriptions); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
```

- [ ] **Step 5: Run + commit**

Run: `go test ./internal/config/ && go build ./...`

```bash
git add go.mod go.sum internal/config/
git commit -m "feat(config): subscriptions block + gofeed dependency (ADR-019, FR-91)"
```

---

### Task 2: The `subscriptions` automation

**Files:**
- Create: `internal/automations/subscriptions.go`
- Modify: `internal/automations/capture.go` (extract `enrichedPipeline` shared helper)
- Test: `internal/automations/subscriptions_test.go` (new)

**Interfaces:**
- Consumes: `rc.Pipeline.Fetcher.Fetch(ctx, url) (*ingestion.Document, error)`; `gofeed.NewParser().Parse(io.Reader) (*gofeed.Feed, error)` (`feed.Items []*gofeed.Item` with `.Link`, `.PublishedParsed/.UpdatedParsed *time.Time`); `db.GetSourceByURL`, `db.GetCursor/SetCursor`; `pipeline.Ingest`.
- Produces: `Subscriptions{}` (`Name() == "subscriptions"`, not essential); shared `enrichedPipeline(rc RunCtx, mode string) *ingestion.Pipeline` (refactored out of `capturePipeline`, both callers updated); constants `subscriptionsSeenState = "subscriptions:seen"`, `subsSeenCapPerFeed = 500`.

- [ ] **Step 1: Failing tests** — `internal/automations/subscriptions_test.go`. The stub fetcher serves BOTH feed XML and item HTML, routed by URL (extends `capture_test.go`'s `stubFetcher` idea):

```go
package automations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/ingestion"
)

// urlFetcher serves canned documents by exact URL and counts fetches.
type urlFetcher struct {
	docs   map[string]*ingestion.Document
	calls  map[string]int
	failAll bool
}

func newURLFetcher() *urlFetcher {
	return &urlFetcher{docs: map[string]*ingestion.Document{}, calls: map[string]int{}}
}

func (u *urlFetcher) Fetch(ctx context.Context, url string) (*ingestion.Document, error) {
	u.calls[url]++
	if u.failAll {
		return nil, errors.New("connection refused")
	}
	d, ok := u.docs[url]
	if !ok {
		return nil, fmt.Errorf("404 for %s", url)
	}
	return d, nil
}

func (u *urlFetcher) addHTML(url, title string) {
	u.docs[url] = &ingestion.Document{URL: url, ContentType: "text/html",
		Body: []byte("<html><head><title>" + title + "</title></head><body><article><p>Substantial article content for " + title + " with enough words to extract meaningfully.</p></article></body></html>")}
}

func rssXML(items ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>Test Feed</title>`)
	for i, link := range items {
		fmt.Fprintf(&b, `<item><title>Item %d</title><link>%s</link><pubDate>Mon, 0%d Jun 2026 10:00:00 GMT</pubDate></item>`, i+1, link, i+1)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

const testFeedURL = "https://feeds.example.com/feed.xml"

func subsRC(t *testing.T, fetcher *urlFetcher, feedURLs ...string) RunCtx {
	t.Helper()
	rc, _ := newRC(t, nil)
	rc.Pipeline.Fetcher = fetcher
	feeds := make([]config.Feed, len(feedURLs))
	for i, u := range feedURLs {
		feeds[i] = config.Feed{URL: u}
	}
	rc.Config.Subscriptions = config.SubscriptionsConfig{Feeds: feeds}
	return rc
}

func TestSubscriptionsSubscribeFromNow(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL, ContentType: "application/rss+xml",
		Body: []byte(rssXML("https://example.com/a1", "https://example.com/a2"))}
	rc := subsRC(t, f, testFeedURL)
	ctx := context.Background()

	// First tick: marks seen, ingests nothing.
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "subscribed") {
		t.Fatalf("summary = %q, want subscribed marker", res.Summary)
	}
	if f.calls["https://example.com/a1"] != 0 {
		t.Fatal("first tick must not ingest existing entries")
	}

	// Second tick, new item appears: only IT is ingested.
	f.docs[testFeedURL].Body = []byte(rssXML("https://example.com/a1", "https://example.com/a2", "https://example.com/a3"))
	f.addHTML("https://example.com/a3", "Article Three")
	res2, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res2.Summary, "ingested 1") {
		t.Fatalf("summary = %q, want ingested 1", res2.Summary)
	}
	if f.calls["https://example.com/a1"] != 0 || f.calls["https://example.com/a3"] != 1 {
		t.Fatalf("fetch counts wrong: %v", f.calls)
	}
	// A knowledge note landed.
	paths, _ := rc.Vault.List(ctx)
	found := false
	for _, p := range paths {
		if strings.HasPrefix(p, "03-Resources/Knowledge/") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no knowledge note; vault: %v", paths)
	}

	// Third tick, nothing new: nothing ingested.
	res3, _ := (Subscriptions{}).Run(ctx, rc)
	if !strings.Contains(res3.Summary, "ingested 0") {
		t.Fatalf("summary = %q, want ingested 0", res3.Summary)
	}
}

func TestSubscriptionsPerTickCap(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL,
		Body: []byte(rssXML("https://example.com/seed"))}
	rc := subsRC(t, f, testFeedURL)
	rc.Config.Subscriptions.MaxPerTick = 2
	ctx := context.Background()
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil { // subscribe tick
		t.Fatal(err)
	}

	// Four new items appear; cap 2 per tick.
	links := []string{"https://example.com/seed", "https://example.com/n1", "https://example.com/n2", "https://example.com/n3", "https://example.com/n4"}
	f.docs[testFeedURL].Body = []byte(rssXML(links...))
	for _, l := range links[1:] {
		f.addHTML(l, l)
	}
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "ingested 2") {
		t.Fatalf("summary = %q, want ingested 2 (capped)", res.Summary)
	}
	// Next tick drains two more.
	res2, _ := (Subscriptions{}).Run(ctx, rc)
	if !strings.Contains(res2.Summary, "ingested 2") {
		t.Fatalf("drain summary = %q, want ingested 2", res2.Summary)
	}
}

func TestSubscriptionsMarkSeenAfterFailure(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL,
		Body: []byte(rssXML("https://example.com/seed"))}
	rc := subsRC(t, f, testFeedURL)
	ctx := context.Background()
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	// New item whose page 404s (not in f.docs).
	f.docs[testFeedURL].Body = []byte(rssXML("https://example.com/seed", "https://example.com/broken"))
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err) // per-item failure must not fail the run
	}
	if !strings.Contains(res.Summary, "1 failed") {
		t.Fatalf("summary = %q, want failure counted", res.Summary)
	}
	attempts := f.calls["https://example.com/broken"]

	// Next tick: NOT retried (mark-seen-after-attempt).
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	if f.calls["https://example.com/broken"] != attempts {
		t.Fatal("failed item was retried")
	}
}

func TestSubscriptionsFeedFailureIsolated(t *testing.T) {
	good, bad := "https://good.example.com/feed.xml", "https://bad.example.com/feed.xml"
	f := newURLFetcher()
	f.docs[good] = &ingestion.Document{URL: good, Body: []byte(rssXML("https://example.com/g1"))}
	// bad feed: no doc → fetch error
	rc := subsRC(t, f, bad, good)
	res, err := (Subscriptions{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("one bad feed must not fail the run: %v", err)
	}
	if !strings.Contains(res.Summary, "subscribed") {
		t.Fatalf("good feed should still subscribe: %q", res.Summary)
	}
	if !strings.Contains(strings.Join(res.Changes, "\n"), "bad.example.com") {
		t.Fatalf("bad feed failure not surfaced: %v", res.Changes)
	}
}

func TestSubscriptionsDryRunWritesNothing(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL,
		Body: []byte(rssXML("https://example.com/a1"))}
	rc := subsRC(t, f, testFeedURL)
	rc.DryRun = true
	res, err := (Subscriptions{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Summary, "would subscribe") {
		t.Fatalf("summary = %q", res.Summary)
	}
	// No state persisted: a real run afterwards still subscribes-from-now.
	rc.DryRun = false
	res2, _ := (Subscriptions{}).Run(context.Background(), rc)
	if !strings.Contains(res2.Summary, "subscribed") {
		t.Fatalf("dry-run leaked state: %q", res2.Summary)
	}
}

func TestSubscriptionsDetectChange(t *testing.T) {
	rc, _ := newRC(t, nil)
	ch, err := (Subscriptions{}).DetectChange(context.Background(), rc)
	if err != nil || ch.Changed {
		t.Fatalf("no feeds: %+v err=%v, want skip", ch, err)
	}
	rc.Config.Subscriptions = config.SubscriptionsConfig{Feeds: []config.Feed{{URL: "https://x.example.com/f"}}}
	ch, _ = (Subscriptions{}).DetectChange(context.Background(), rc)
	if !ch.Changed {
		t.Fatal("feeds configured: want changed")
	}
}
```

- [ ] **Step 2: Verify red** — `go test ./internal/automations/ -run TestSubscriptions -v` → FAIL undefined.

- [ ] **Step 3: Shared enrichment helper.** In `capture.go`, generalize:

```go
// enrichedPipeline returns a shallow copy of the shared pipeline with the
// enricher selected by mode ("claude" → chokepoint on the routine tier;
// anything else keeps the zero-token heuristic). Used by capture and
// subscriptions (ADR-016, ADR-019); the shared instance is never mutated.
func enrichedPipeline(rc RunCtx, mode string) *ingestion.Pipeline {
	pl := *rc.Pipeline
	if mode == "claude" {
		pl.Enricher = ingestion.ClaudeEnricher{Manager: rc.Manager, ModelKey: "routine"}
	}
	return &pl
}
```

and `capturePipeline` becomes `return enrichedPipeline(rc, rc.Config.Capture.EnrichMode())` (or replace its call site outright and delete it — prefer deletion; update capture.go's `Run`).

- [ ] **Step 4: Implement** — `internal/automations/subscriptions.go`:

```go
package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/ingestion"
)

const (
	// subscriptionsSeenState is the automation_state key for the per-feed
	// seen-item memory (ADR-019).
	subscriptionsSeenState = "subscriptions:seen"
	// subsSeenCapPerFeed bounds each feed's seen list (keep the newest).
	subsSeenCapPerFeed = 500
)

// Subscriptions polls the config-declared RSS/Atom feeds and ingests new
// items through the standard pipeline (ADR-019, FR-91…FR-93). Feed XML is
// fetched by the same egress-policied fetcher as every ingest; gofeed
// tolerates the wild-feed chaos. Subscribe-from-now, per-tick caps and
// mark-seen-after-attempt keep the volume bounded.
type Subscriptions struct{}

func (Subscriptions) Name() string    { return "subscriptions" }
func (Subscriptions) Essential() bool { return false }

func (Subscriptions) DetectChange(ctx context.Context, rc RunCtx) (Change, error) {
	n := len(rc.Config.Subscriptions.Feeds)
	if n == 0 {
		return Change{Changed: false, Reason: "no feeds configured"}, nil
	}
	// Feeds change remotely; the poll IS the check. The schedule is the cadence.
	return Change{Changed: true, Reason: fmt.Sprintf("%d feed(s) to poll", n)}, nil
}

func (Subscriptions) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	cfg := rc.Config.Subscriptions
	seen := loadSubsSeen(ctx, rc)
	pl := enrichedPipeline(rc, cfg.EnrichMode())
	parser := gofeed.NewParser()

	var (
		changes                                []string
		ingested, failed, subscribed, feedErrs int
	)

	for _, feed := range cfg.Feeds {
		doc, err := rc.Pipeline.Fetcher.Fetch(ctx, feed.URL)
		if err != nil {
			feedErrs++
			changes = append(changes, fmt.Sprintf("feed FAILED: %s — %v", feed.URL, err))
			continue
		}
		parsed, err := parser.Parse(bytes.NewReader(doc.Body))
		if err != nil {
			feedErrs++
			changes = append(changes, fmt.Sprintf("feed FAILED: %s — parse: %v", feed.URL, err))
			continue
		}
		links := itemLinks(parsed)

		seenList, known := seen[feed.URL]
		if !known {
			// Subscribe-from-now (FR-92): record, ingest nothing.
			if rc.DryRun {
				changes = append(changes, fmt.Sprintf("would subscribe %s (%d existing entries)", feed.URL, len(links)))
				continue
			}
			seen[feed.URL] = capSeen(links)
			subscribed++
			changes = append(changes, fmt.Sprintf("subscribed %s (%d existing entries marked seen)", feed.URL, len(links)))
			continue
		}

		seenSet := map[string]bool{}
		for _, u := range seenList {
			seenSet[u] = true
		}
		budget := cfg.PerTick()
		for _, link := range links {
			if budget == 0 {
				break
			}
			if seenSet[link] {
				continue
			}
			if src, _ := db.GetSourceByURL(ctx, rc.DB, link); src != nil {
				seenList = append(seenList, link) // already ingested elsewhere: remember
				seenSet[link] = true
				continue
			}
			if rc.DryRun {
				changes = append(changes, "would ingest "+link)
				budget--
				continue
			}
			res, ierr := pl.Ingest(ctx, link, ingestion.IngestOptions{})
			// Mark seen after the attempt, success OR failure (FR-92):
			// one try per item; explicit `axon ingest` is the retry path.
			seenList = append(seenList, link)
			seenSet[link] = true
			budget--
			if ierr != nil {
				failed++
				changes = append(changes, fmt.Sprintf("FAILED: %s — %v", link, ierr))
				continue
			}
			ingested++
			changes = append(changes, fmt.Sprintf("%s → %s", link, res.NotePath))
		}
		seen[feed.URL] = capSeen(seenList)
	}

	if !rc.DryRun {
		saveSubsSeen(ctx, rc, seen)
	}

	summary := fmt.Sprintf("ingested %d item(s) from %d feed(s), %d failed", ingested, len(cfg.Feeds), failed)
	if subscribed > 0 {
		summary += fmt.Sprintf("; subscribed %d new feed(s)", subscribed)
	}
	if feedErrs > 0 {
		summary += fmt.Sprintf("; %d feed(s) unreachable", feedErrs)
	}
	if rc.DryRun {
		summary = "dry-run: " + summary + " (would subscribe/ingest as listed)"
	}
	return RunResult{Summary: summary, Changes: changes}, nil
}

// itemLinks extracts non-empty item links, newest first (published/updated
// date when present, feed order otherwise), deduplicated.
func itemLinks(f *gofeed.Feed) []string {
	items := make([]*gofeed.Item, 0, len(f.Items))
	items = append(items, f.Items...)
	sort.SliceStable(items, func(i, j int) bool {
		return itemTime(items[i]).After(itemTime(items[j]))
	})
	var links []string
	seen := map[string]bool{}
	for _, it := range items {
		l := strings.TrimSpace(it.Link)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		links = append(links, l)
	}
	return links
}

func itemTime(it *gofeed.Item) time.Time {
	if it.PublishedParsed != nil {
		return *it.PublishedParsed
	}
	if it.UpdatedParsed != nil {
		return *it.UpdatedParsed
	}
	return time.Time{}
}

// capSeen keeps the newest subsSeenCapPerFeed entries (append order = age).
func capSeen(list []string) []string {
	if len(list) > subsSeenCapPerFeed {
		return list[len(list)-subsSeenCapPerFeed:]
	}
	return list
}

func loadSubsSeen(ctx context.Context, rc RunCtx) map[string][]string {
	out := map[string][]string{}
	raw, err := db.GetCursor(ctx, rc.DB, subscriptionsSeenState)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func saveSubsSeen(ctx context.Context, rc RunCtx, seen map[string][]string) {
	raw, err := json.Marshal(seen)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, subscriptionsSeenState, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("subscriptions: persist seen-state", "err", err)
	}
}
```

- [ ] **Step 5: Run** — `go test ./internal/automations/ -run 'TestSubscriptions|TestCapture' -v && go test ./...` → PASS (capture tests confirm the `enrichedPipeline` refactor).

- [ ] **Step 6: Commit**

```bash
git add internal/automations/
git commit -m "feat(automations): subscriptions automation — feed polling with volume control (FR-91, FR-92, FR-93)"
```

---

### Task 3: Registration, starter/example config, docs, CHANGELOG

**Files:**
- Modify: `internal/automations/registry.go`, `catalog.go`, `registry_test.go` (13→14), `internal/mcp/tools_more_test.go` (13→14)
- Modify: `internal/config/starter.go`, `axon.config.example.yaml`
- Modify: `docs/02-architecture.md` (ADR-019 → built), `docs/03-requirements.md` (section → built), `docs/05-component-knowledge-ingestion.md` (subscriptions section), `CHANGELOG.md`
- Test: `internal/automations/subscriptions_test.go` (registration assertion)

- [ ] **Step 1: Registration test** (append to subscriptions_test.go):

```go
func TestSubscriptionsRegistered(t *testing.T) {
	p := config.Profile{Automations: map[string]config.Automation{
		"subscriptions": {Enabled: true, Schedule: "0 * * * *"},
	}}
	if _, err := Get(p, "subscriptions"); err != nil {
		t.Fatalf("not registered: %v", err)
	}
	if Purpose("subscriptions") == "(no description)" {
		t.Fatal("no catalog purpose")
	}
	if s := Schedulables(p); len(s) != 1 || s[0].Automation.Name() != "subscriptions" {
		t.Fatalf("schedulables = %+v", s)
	}
}
```

- [ ] **Step 2: Register.** `registry.go`: `Subscriptions{}.Name(): Subscriptions{},`. `catalog.go`:

```go
	"subscriptions":     "Polls configured RSS/Atom feeds hourly and ingests new items through the pipeline (subscribe-from-now, per-tick caps). Enrichment optional via subscriptions.enrich.",
```

Update `registry_test.go` want-list (+`"subscriptions"`) and the mcp automations count 13→14.

- [ ] **Step 3: Starter + example config.** Starter automations block:

```yaml
      subscriptions:     { enabled: true,  schedule: "0 * * * *",       model: routine,   budget_tokens: 60_000, catch_up: skip }
```

Example config: same automations row, plus next to the `capture:` block:

```yaml
    # subscriptions:                          # RSS/Atom feeds polled hourly (ADR-019)
    #   enrich: heuristic                     # heuristic (default, zero tokens) | claude (chokepoint, routine tier)
    #   max_per_tick: 5                       # new items ingested per feed per tick
    #   feeds:
    #     - url: "https://example.com/feed.xml"
```

- [ ] **Step 4: Docs.** ADR-019 header → `*(built)*`; docs/03 section → `*(built)*` past tense. docs/05: short `## Subscriptions (ADR-019)` section after the capture section (feeds → this pipeline; subscribe-from-now; caps; seen-state; enrich toggle; spec pointer). CHANGELOG under Added:

```markdown
- **RSS/feed subscriptions (ADR-019, FR-91…FR-93)** — declare feeds in
  `subscriptions.feeds` and AXON polls them hourly through the same
  egress-policied fetcher as every ingest, feeding new items into the
  standard pipeline (deduped, redacted, ledgered, optionally enriched on the
  routine tier). Volume is structural: subscribe-from-now (no backfill
  floods), at most `max_per_tick` items per feed per tick, one attempt per
  item. The agentic weekly digest now synthesizes across your subscriptions.
  New dependency: `mmcdole/gofeed` (feed parsing; ADR-justified).
```

- [ ] **Step 5: Run + commit** — `go test ./... && git add -A && git commit -m "feat: register subscriptions; starter config, docs, CHANGELOG (FR-91..93)"`

---

### Task 4: Final gates + live smoke

- [ ] **Step 1: Gates** — `go build ./... && go vet ./... && golangci-lint run && go test ./...` → green.
- [ ] **Step 2: Live smoke** (scratch env): serve a local fixture feed via `python3 -m http.server` in the scratchpad (feed.xml + one article HTML page)… note the SSRF guard refuses loopback for ingest — so instead use a small **real public feed** (e.g. a low-traffic blog's RSS) added to the scratch config: tick 1 → "subscribed (N existing entries marked seen)"; verify `subscriptions:seen` row exists (sqlite query); tick 2 → "ingested 0" (nothing new). The new-item path is covered by unit tests; a real new-item live event isn't waitable. Alternatively run tick 1, delete ONE URL from the seen row via sqlite, tick 2 → that item ingests through the real pipeline end to end (real fetch, extract, embed, note under `03-Resources/Knowledge/`).
- [ ] **Step 3: Commit anything outstanding; report.**

---

## Verification (definition of done)

1. Gates green; capture tests pass post-refactor (`enrichedPipeline`).
2. FR trace: FR-91 (Tasks 1-2), FR-92 (Task 2), FR-93 (Tasks 1-2 via `enrichedPipeline`).
3. Frugality: no feeds → skip; no new items → zero model calls; enrichment only on `enrich: claude`.
4. Live smoke proves the real loop: subscribe tick, seen-state row, forced new-item ingest producing a real knowledge note.
