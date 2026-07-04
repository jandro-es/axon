# Conditional GET for Subscription Feeds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The hourly feed poll sends `If-None-Match`/`If-Modified-Since` from stored per-feed validators and treats `304 Not Modified` as a free skip (FR-101, ADR-019 follow-up).

**Architecture:** `HTTPFetcher` gains a `FetchConditional` method behind a new `ConditionalFetcher` interface; `Document` carries the response's `ETag`/`Last-Modified`. The subscriptions automation stores `map[feedURL]Validators` in a new `subscriptions:http` state row, short-circuits on 304, and prunes validators to configured feeds on save. No config, no model calls, no vault involvement.

**Tech Stack:** Go 1.26, net/http conditional requests (RFC 9110 §13), existing `db.GetCursor/SetCursor` JSON-row pattern, httptest via the existing `plainFetcher()` seam.

## Global Constraints

- Go 1.26; `gofmt` clean, `go vet` and `golangci-lint run` green.
- Subscriptions-only scope; no config toggle (user-approved decisions 1–2).
- `Fetch`'s behavior stays byte-identical; the Confluence path is untouched.
- Dry-run persists nothing (no validator writes, as with seen-state today).
- Existing run summaries stay byte-identical when no 304 occurred.
- Verify suites with `env -u FORCE_COLOR go test ./... > /dev/null 2>&1; echo $?` (ambient FORCE_COLOR breaks TTY-detection tests; piping to `tail` masks exit codes).

---

### Task 1: `ConditionalFetcher` on `HTTPFetcher`

**Files:**
- Modify: `internal/ingestion/fetcher.go` (Document fields + new types)
- Modify: `internal/ingestion/fetch.go` (`Fetch` refactor, `FetchConditional`, `fetchWithRetry`, `fetchOnce` validators + 304)
- Test: `internal/ingestion/fetch_test.go` (append)

**Interfaces:**
- Consumes: existing `HTTPFetcher{client}`, `fetchOnce(ctx, url) (*Document, bool, error)` at `fetch.go:163`, `Fetch` retry loop at `fetch.go:132-160`, `plainFetcher()` test seam at `fetch_test.go:85`.
- Produces: `type Validators struct { ETag string; LastModified string }` (json tags `etag`/`last_modified`, omitempty); `type ConditionalFetcher interface { FetchConditional(ctx context.Context, url string, v Validators) (*Document, bool, error) }`; `Document.ETag`, `Document.LastModified`; `(*HTTPFetcher).FetchConditional` (implements it).

- [ ] **Step 1: Write the failing test** (append to `internal/ingestion/fetch_test.go`):

```go
// TestFetchConditional: first fetch captures validators without sending
// conditional headers; a conditional refetch sends them and maps 304 to
// (nil, notModified, nil) with no retries; plain Fetch never sends them.
func TestFetchConditional(t *testing.T) {
	const lastMod = "Fri, 04 Jul 2026 10:00:00 GMT"
	var calls int
	var gotINM, gotIMS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotINM = r.Header.Get("If-None-Match")
		gotIMS = r.Header.Get("If-Modified-Since")
		if gotINM == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", lastMod)
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>t</title></channel></rss>`))
	}))
	defer srv.Close()
	f := plainFetcher()
	ctx := context.Background()

	// Unconditional (empty validators): no conditional headers, validators captured.
	doc, notModified, err := f.FetchConditional(ctx, srv.URL, Validators{})
	if err != nil || notModified {
		t.Fatalf("first fetch: doc=%v notModified=%v err=%v", doc, notModified, err)
	}
	if gotINM != "" || gotIMS != "" {
		t.Errorf("empty validators must send no conditional headers: INM=%q IMS=%q", gotINM, gotIMS)
	}
	if doc.ETag != `"abc"` || doc.LastModified != lastMod {
		t.Errorf("validators not captured: %q %q", doc.ETag, doc.LastModified)
	}

	// Conditional: headers sent, 304 → notModified, success, NOT retried.
	doc2, nm2, err2 := f.FetchConditional(ctx, srv.URL, Validators{ETag: `"abc"`, LastModified: lastMod})
	if err2 != nil || !nm2 || doc2 != nil {
		t.Fatalf("304: doc=%v notModified=%v err=%v", doc2, nm2, err2)
	}
	if gotIMS != lastMod {
		t.Errorf("If-Modified-Since = %q, want %q", gotIMS, lastMod)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (304 must not retry)", calls)
	}

	// Plain Fetch: no conditional headers ever.
	if _, err := f.Fetch(ctx, srv.URL); err != nil {
		t.Fatal(err)
	}
	if gotINM != "" || gotIMS != "" {
		t.Errorf("plain Fetch sent conditional headers: INM=%q IMS=%q", gotINM, gotIMS)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/ingestion/ -run TestFetchConditional 2>&1 | tail -3`
Expected: FAIL (build error: `FetchConditional`, `Validators` undefined)

- [ ] **Step 3: Implement.** In `internal/ingestion/fetcher.go`, extend `Document` and add the types:

```go
type Document struct {
	URL         string
	ContentType string
	Body        []byte
	FetchedAt   time.Time
	// ETag / LastModified are the response's cache validators (RFC 9110
	// §13), captured so recurring fetchers (feed polling) can make the
	// next request conditional. One-shot callers ignore them.
	ETag         string
	LastModified string
}

// Validators are a document's HTTP cache validators, echoed back verbatim
// on the next conditional request for the same URL.
type Validators struct {
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
}

// ConditionalFetcher is implemented by fetchers that support HTTP
// conditional requests. notModified reports a 304 — success with no
// document; the caller's cached view still stands.
type ConditionalFetcher interface {
	FetchConditional(ctx context.Context, url string, v Validators) (doc *Document, notModified bool, err error)
}
```

In `internal/ingestion/fetch.go` (add `"errors"` to imports): declare the sentinel next to the retry constants:

```go
// errNotModified marks a 304 inside the retry loop: success, not failure.
var errNotModified = errors.New("not modified")
```

Replace the body of `Fetch`'s retry loop with a shared helper and add `FetchConditional`:

```go
func (f *HTTPFetcher) Fetch(ctx context.Context, url string) (*Document, error) {
	if apiURL, ok := confluenceAPIURL(url); ok {
		if doc, err := f.fetchConfluenceAPI(ctx, apiURL, url); err == nil {
			return doc, nil
		}
		// API refused or unavailable (anonymous instance, odd URL): fall back
		// to the regular page fetch below.
	}
	doc, _, err := f.fetchWithRetry(ctx, url, Validators{})
	return doc, err
}

// FetchConditional implements ConditionalFetcher. The Confluence API path
// is skipped — it never applies to feed URLs, the only recurring callers.
func (f *HTTPFetcher) FetchConditional(ctx context.Context, url string, v Validators) (*Document, bool, error) {
	return f.fetchWithRetry(ctx, url, v)
}

// fetchWithRetry is the bounded retry loop shared by Fetch and
// FetchConditional; a 304 short-circuits as (nil, true, nil).
func (f *HTTPFetcher) fetchWithRetry(ctx context.Context, url string, v Validators) (*Document, bool, error) {
	var lastErr error
	for attempt := 0; attempt <= fetchRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, false, ctx.Err()
			case <-time.After(time.Duration(attempt) * fetchBackoff):
			}
		}
		doc, retryable, err := f.fetchOnce(ctx, url, v)
		if err == nil {
			return doc, false, nil
		}
		if errors.Is(err, errNotModified) {
			return nil, true, nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return nil, false, lastErr
}
```

In `fetchOnce`: change the signature to `func (f *HTTPFetcher) fetchOnce(ctx context.Context, url string, v Validators) (*Document, bool, error)`; after the existing `Accept-Language` header line add:

```go
	if v.ETag != "" {
		req.Header.Set("If-None-Match", v.ETag)
	}
	if v.LastModified != "" {
		req.Header.Set("If-Modified-Since", v.LastModified)
	}
```

Immediately after `defer resp.Body.Close()` (before the status switch) add:

```go
	if resp.StatusCode == http.StatusNotModified {
		return nil, false, errNotModified
	}
```

And in the returned `Document` literal add:

```go
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
```

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/ingestion/ -run TestFetch -v 2>&1 | tail -10 && env -u FORCE_COLOR go test ./... > /dev/null 2>&1; echo $?`
Expected: all fetch tests PASS (existing retry/auth tests prove `Fetch` unchanged), suite exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/ingestion/fetcher.go internal/ingestion/fetch.go internal/ingestion/fetch_test.go
git commit -m "feat(ingestion): ConditionalFetcher — ETag/Last-Modified + 304 on HTTPFetcher (FR-101)"
```

---

### Task 2: Conditional polling in the subscriptions automation

**Files:**
- Modify: `internal/automations/subscriptions.go`
- Test: `internal/automations/subscriptions_test.go` (extend `urlFetcher`, append tests)

**Interfaces:**
- Consumes: `ingestion.ConditionalFetcher`, `ingestion.Validators{ETag, LastModified}`, `ingestion.Document.ETag/.LastModified` (Task 1); existing `loadSubsSeen/saveSubsSeen`, `db.GetCursor/SetCursor`, `subsRC(t, fetcher, feedURLs...)`, `urlFetcher`, `rssXML`, `testFeedURL`.
- Produces: state key `subscriptionsHTTPState = "subscriptions:http"` holding `map[string]ingestion.Validators`; `loadSubsHTTP(ctx, rc) map[string]ingestion.Validators`; `saveSubsHTTP(ctx, rc, m, feeds)` (prunes to configured feeds).

- [ ] **Step 1: Extend the test fetcher and write the failing tests.** In `internal/automations/subscriptions_test.go`, change `urlFetcher`/`newURLFetcher` to:

```go
// urlFetcher serves canned documents by exact URL and counts fetches. It
// implements ingestion.ConditionalFetcher: a stored etag matching the
// request's validator yields a 304 (hits304, no full-fetch count).
type urlFetcher struct {
	docs    map[string]*ingestion.Document
	calls   map[string]int
	etags   map[string]string
	hits304 int
	failAll bool
}

func newURLFetcher() *urlFetcher {
	return &urlFetcher{docs: map[string]*ingestion.Document{}, calls: map[string]int{}, etags: map[string]string{}}
}

func (u *urlFetcher) FetchConditional(ctx context.Context, url string, v ingestion.Validators) (*ingestion.Document, bool, error) {
	if v.ETag != "" && v.ETag == u.etags[url] {
		u.hits304++
		return nil, true, nil
	}
	d, err := u.Fetch(ctx, url)
	if err != nil {
		return nil, false, err
	}
	if et := u.etags[url]; et != "" {
		d.ETag = et
	}
	return d, false, nil
}
```

Append the tests:

```go
func TestSubscriptionsConditional304(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL, ContentType: "application/rss+xml",
		Body: []byte(rssXML("https://feeds.example.com/p1"))}
	f.etags[testFeedURL] = `W/"v1"`
	f.addHTML("https://feeds.example.com/p1", "Post One")
	rc := subsRC(t, f, testFeedURL)
	ctx := context.Background()

	// Tick 1: subscribe-from-now; validators stored.
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	raw, _ := db.GetCursor(ctx, rc.DB, "subscriptions:http")
	var vals map[string]ingestion.Validators
	if err := json.Unmarshal([]byte(raw), &vals); err != nil || vals[testFeedURL].ETag != `W/"v1"` {
		t.Fatalf("validators not stored: %q (%v)", raw, err)
	}

	// Tick 2: same etag → 304; nothing fetched in full, nothing ingested,
	// seen-state untouched, summary reports it.
	res, err := (Subscriptions{}).Run(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if f.hits304 != 1 || f.calls[testFeedURL] != 1 {
		t.Errorf("hits304=%d fullFetches=%d, want 1/1", f.hits304, f.calls[testFeedURL])
	}
	if !strings.Contains(res.Summary, "1 unchanged (304)") {
		t.Errorf("summary = %q", res.Summary)
	}
	seen := loadSubsSeen(ctx, rc)
	if len(seen[testFeedURL]) != 1 {
		t.Errorf("seen-state disturbed: %v", seen)
	}
}

func TestSubscriptionsValidatorsUpdateAndPrune(t *testing.T) {
	feedB := "https://feeds.example.com/b.xml"
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL, ContentType: "application/rss+xml",
		Body: []byte(rssXML("https://feeds.example.com/p1"))}
	f.docs[feedB] = &ingestion.Document{URL: feedB, ContentType: "application/rss+xml",
		Body: []byte(rssXML("https://feeds.example.com/p2"))}
	f.etags[testFeedURL] = `"v1"`
	f.etags[feedB] = `"v1"`
	rc := subsRC(t, f, testFeedURL, feedB)
	ctx := context.Background()

	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil { // both subscribed, validators v1
		t.Fatal(err)
	}
	// Server updated feed A: new etag → full fetch, validator replaced.
	f.etags[testFeedURL] = `"v2"`
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	raw, _ := db.GetCursor(ctx, rc.DB, "subscriptions:http")
	var vals map[string]ingestion.Validators
	_ = json.Unmarshal([]byte(raw), &vals)
	if vals[testFeedURL].ETag != `"v2"` || vals[feedB].ETag != `"v1"` {
		t.Fatalf("validators = %+v", vals)
	}

	// Feed B removed from config: its validator prunes on the next run.
	rc.Config.Subscriptions.Feeds = []config.Feed{{URL: testFeedURL}}
	if _, err := (Subscriptions{}).Run(ctx, rc); err != nil {
		t.Fatal(err)
	}
	raw, _ = db.GetCursor(ctx, rc.DB, "subscriptions:http")
	vals = nil
	_ = json.Unmarshal([]byte(raw), &vals)
	if _, ok := vals[feedB]; ok {
		t.Errorf("removed feed's validators must prune: %+v", vals)
	}
}

func TestSubscriptionsDryRunStoresNoValidators(t *testing.T) {
	f := newURLFetcher()
	f.docs[testFeedURL] = &ingestion.Document{URL: testFeedURL, ContentType: "application/rss+xml",
		Body: []byte(rssXML("https://feeds.example.com/p1"))}
	f.etags[testFeedURL] = `"v1"`
	rc := subsRC(t, f, testFeedURL)
	rc.DryRun = true
	if _, err := (Subscriptions{}).Run(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	if raw, _ := db.GetCursor(context.Background(), rc.DB, "subscriptions:http"); raw != "" {
		t.Errorf("dry-run stored validators: %q", raw)
	}
}
```

(`encoding/json` is already imported by the test file's package peers; add to this file's imports if the compiler asks.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/automations/ -run 'TestSubscriptionsConditional304|TestSubscriptionsValidators|TestSubscriptionsDryRunStores' 2>&1 | tail -3`
Expected: FAIL — no `subscriptions:http` row is written, summary lacks "unchanged (304)".

- [ ] **Step 3: Implement.** In `internal/automations/subscriptions.go`:

Add to the const block:

```go
	// subscriptionsHTTPState stores per-feed HTTP cache validators for
	// conditional polling (FR-101): map[feedURL]ingestion.Validators.
	subscriptionsHTTPState = "subscriptions:http"
```

In `Run`, before the feed loop (right after `parser := gofeed.NewParser()`):

```go
	cond, _ := rc.Pipeline.Fetcher.(ingestion.ConditionalFetcher)
	validators := loadSubsHTTP(ctx, rc)
	unchanged := 0
```

Replace the fetch call at the top of the feed loop (`doc, err := rc.Pipeline.Fetcher.Fetch(ctx, feed.URL)`) with:

```go
		var doc *ingestion.Document
		var err error
		if cond != nil {
			var notModified bool
			doc, notModified, err = cond.FetchConditional(ctx, feed.URL, validators[feed.URL])
			if err == nil && notModified {
				// The server asserts nothing changed (304): free skip —
				// no body, no parse, seen-state untouched.
				unchanged++
				changes = append(changes, "feed unchanged (304): "+feed.URL)
				continue
			}
		} else {
			doc, err = rc.Pipeline.Fetcher.Fetch(ctx, feed.URL)
		}
```

Immediately after the feed-level parse succeeds (right after the `links := itemLinks(parsed)` line), record the fresh validators:

```go
		if v := (ingestion.Validators{ETag: doc.ETag, LastModified: doc.LastModified}); v != (ingestion.Validators{}) {
			validators[feed.URL] = v
		} else {
			delete(validators, feed.URL) // feed stopped sending validators
		}
```

Extend the save block:

```go
	if !rc.DryRun {
		saveSubsSeen(ctx, rc, seen)
		saveSubsHTTP(ctx, rc, validators, cfg.Feeds)
	}
```

Extend the summary (after the `feedErrs` append, before the dry-run prefix):

```go
	if unchanged > 0 {
		summary += fmt.Sprintf("; %d unchanged (304)", unchanged)
	}
```

Append beside `loadSubsSeen`/`saveSubsSeen`:

```go
// loadSubsHTTP reads the per-feed HTTP validators (empty on any problem —
// worst case one unconditional GET per feed).
func loadSubsHTTP(ctx context.Context, rc RunCtx) map[string]ingestion.Validators {
	out := map[string]ingestion.Validators{}
	raw, err := db.GetCursor(ctx, rc.DB, subscriptionsHTTPState)
	if err != nil || raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// saveSubsHTTP persists the validators, pruned to currently-configured
// feeds so removed subscriptions self-clean.
func saveSubsHTTP(ctx context.Context, rc RunCtx, m map[string]ingestion.Validators, feeds []config.Feed) {
	keep := map[string]ingestion.Validators{}
	for _, f := range feeds {
		if v, ok := m[f.URL]; ok {
			keep[f.URL] = v
		}
	}
	raw, err := json.Marshal(keep)
	if err != nil {
		return
	}
	if err := db.SetCursor(ctx, rc.DB, subscriptionsHTTPState, string(raw), rc.now().UTC().Format(time.RFC3339)); err != nil {
		rc.Log.Warn("subscriptions: persist http validators", "err", err)
	}
}
```

Add `"github.com/jandro-es/axon/internal/config"` to the imports if not present (for `[]config.Feed`).

- [ ] **Step 4: Run to verify pass**

Run: `go test ./internal/automations/ -run TestSubscriptions -v 2>&1 | tail -20 && env -u FORCE_COLOR go test ./... > /dev/null 2>&1; echo $?`
Expected: all subscriptions tests PASS (existing ones prove unchanged-path parity), suite exit 0.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/subscriptions.go internal/automations/subscriptions_test.go
git commit -m "feat(automations): conditional feed polling — 304 skips, validator store + prune (FR-101)"
```

---

### Task 3: Docs, gates, live smoke

**Files:**
- Modify: `docs/03-requirements.md` (FR-101 row: drop the planned marker), `docs/02-architecture.md` (ADR-019 line: drop "spec approved" wording), `docs/superpowers/specs/2026-07-04-feed-conditional-get-design.md` (Fake note), `CHANGELOG.md`

- [ ] **Step 1: Docs.** In `docs/03-requirements.md` FR-101 row: `**Conditional feed polling** *(planned — spec approved 2026-07-04, not yet built)*.` → `**Conditional feed polling.**`. In `docs/02-architecture.md` line 246: `Polling is conditional (ETag/304, FR-101 — spec approved 2026-07-04).` → `Polling is conditional (ETag/304, FR-101).`. In the spec's Fetcher section, replace the `ingestion.Fake` bullet with: `- The automations test fetcher (subscriptions_test.go's urlFetcher) implements ConditionalFetcher; ingestion.Fake is unchanged (no consumer needs a conditional fake).` In `CHANGELOG.md` under `### Added`, above the subscribe-CLI entry:

```markdown
- **Conditional feed polling (FR-101)** — the subscriptions automation now
  stores each feed's `ETag`/`Last-Modified` and polls with
  `If-None-Match`/`If-Modified-Since`; a `304 Not Modified` is a free skip
  (no download, no parse, no state churn), reported as "N unchanged (304)"
  in the run summary. Validators live in `automation_state` and prune
  automatically when feeds are removed. Closes ADR-019's remaining
  optimization note.
```

- [ ] **Step 2: Gates.**

Run: `go build ./... && go vet ./... && golangci-lint run && env -u FORCE_COLOR go test ./... > /dev/null 2>&1; echo $?`
Expected: `0 issues.`, exit 0.

- [ ] **Step 3: Live smoke** (scratch env; go.dev's feed is already subscribed there):

```bash
SMOKE=/private/tmp/claude-501/-Users-jandro-Projects-axon/ee1556a6-d9b5-4a70-8538-3b43e2143ed6/scratchpad/capture-smoke
curl -sI https://go.dev/blog/feed.atom | grep -i 'etag\|last-modified'   # confirm the feed sends validators
go build -o "$SMOKE/axon" ./cmd/axon
"$SMOKE/axon" run subscriptions --config "$SMOKE/home/config.yaml"        # full GET, validators stored
sqlite3 "$SMOKE/home/data/db.sqlite" "SELECT cursor FROM automation_state WHERE automation='subscriptions:http';"
"$SMOKE/axon" run subscriptions --config "$SMOKE/home/config.yaml"        # expect "; 1 unchanged (304)"
```

Expected: the state row holds the feed's etag/last-modified; the second run's summary ends with `; 1 unchanged (304)`. If go.dev stops sending validators, pick any feed from the curl check that does (e.g. `https://xkcd.com/atom.xml`) via `axon subscribe` first.

- [ ] **Step 4: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: FR-101 built — conditional feed polling docs + CHANGELOG"
```

---

## Verification (definition of done)

1. Gates green: build, vet, golangci-lint, full suite (with `env -u FORCE_COLOR`).
2. FR-101 covered: validators sent/captured (Task 1), 304 skip + store/prune/dry-run (Task 2).
3. `Fetch` parity proven by the existing fetch/auth/retry tests passing untouched.
4. Live smoke shows a real 304 round-trip against a public feed.
