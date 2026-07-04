# Conditional GET for subscription feeds — design

**Date:** 2026-07-04
**Status:** approved (brainstormed + user-approved)
**Traces to:** new FR-101 (docs/03), ADR-019 (subscriptions — closes its
"no ETag/304" trade-off note), NFR-04 (egress policy unchanged)

## Goal

The hourly feed poll stops re-downloading unchanged feeds: AXON stores each
feed's `ETag`/`Last-Modified` validators and sends
`If-None-Match`/`If-Modified-Since` on the next poll; a `304 Not Modified`
is a free skip — no body, no parse, no seen-state churn (RFC 9110 §13).

## Decisions (user-approved)

1. **Subscriptions polling only.** One-shot ingests/captures don't recur;
   content-hash dedupe already makes re-ingests cheap. The shared fetch
   path used by capture/ingest/MCP is untouched.
2. **No config toggle.** A 304 only happens when the server itself asserts
   nothing changed; a stale validator degrades to today's full GET.

## Background / constraints

- `HTTPFetcher.fetchOnce` (`internal/ingestion/fetch.go`) is the single
  place request headers are set and status codes classified; `Fetch` wraps
  it in a bounded retry loop. `Document{URL, ContentType, Body, FetchedAt}`
  carries no caching metadata.
- The subscriptions automation fetches per feed via
  `rc.Pipeline.Fetcher.Fetch` (`internal/automations/subscriptions.go:57`);
  `Fetcher` is the one-method interface `Fetch(ctx, url)`.
- State rows: `subscriptions:seen` holds `map[feedURL][]itemURL` via
  `db.GetCursor/SetCursor` — the established JSON-row pattern.
- Fetch tests use `plainFetcher()` (`fetch_test.go`) — an `HTTPFetcher`
  with a default transport that skips the dial-time loopback block, so
  httptest servers work.
- Dry-run today: the subscriptions automation fetches and reports but
  mutates no state (no seen writes); conditional GET must keep that.

## Design

### Fetcher (`internal/ingestion`)

- `Document` gains `ETag string` and `LastModified string`, populated in
  `fetchOnce` from every 2xx response's `ETag`/`Last-Modified` headers.
  Non-feed callers ignore them.
- New types, defined where the capability lives:

  ```go
  // Validators are a document's HTTP cache validators (RFC 9110 §13).
  type Validators struct {
      ETag         string `json:"etag,omitempty"`
      LastModified string `json:"last_modified,omitempty"`
  }

  // ConditionalFetcher is implemented by fetchers that support HTTP
  // conditional requests. notModified reports a 304 (doc is nil).
  type ConditionalFetcher interface {
      FetchConditional(ctx context.Context, url string, v Validators) (doc *Document, notModified bool, err error)
  }
  ```

- `HTTPFetcher.FetchConditional`: same retry loop as `Fetch` (no
  Confluence special-case — it never applies to feeds), passing the
  validators into `fetchOnce`, which sets `If-None-Match` (when ETag) and
  `If-Modified-Since` (when LastModified) and classifies
  `304 → (nil, notModified=true, nil)` — success, never retried, body
  never read. `Fetch` delegates to the same path with empty validators;
  its behavior is byte-identical to today.
- `ingestion.Fake` implements `ConditionalFetcher`: new field
  `ETags map[string]string`; `FetchConditional` returns `notModified=true`
  when `v.ETag` matches `ETags[url]`, else falls through to `Fetch` and
  reports `ETags[url]` as the document's new ETag.

### Subscriptions automation (`internal/automations/subscriptions.go`)

- New state row `subscriptionsHTTPState = "subscriptions:http"`:
  `map[feedURL]ingestion.Validators`, loaded/saved beside the seen map.
- Per feed in `Run`: if `rc.Pipeline.Fetcher` type-asserts to
  `ingestion.ConditionalFetcher`, call `FetchConditional` with the feed's
  stored validators (zero value when none). On `notModified`: count
  `unchanged++`, append change line `"feed unchanged (304): <url>"`, and
  `continue` — no parse, seen-state untouched. On a full response: store
  `Validators{doc.ETag, doc.LastModified}` and proceed exactly as today.
  Fetchers without the interface take the existing `Fetch` path.
- Persistence: validators are saved only on a real (non-dry) run, in the
  same place the seen map is saved; at save time the map is pruned to
  currently-configured feed URLs, so removed feeds self-clean and
  `subscribe remove` needs no change.
- Summary: append `", N unchanged (304)"` only when `N > 0` — existing
  summaries stay byte-identical.
- Dry-run: the conditional fetch may run (a 304 makes dry-run cheaper
  too) and is reported, but no validator/seen/state writes happen.

### Error handling

A 304 for a feed AXON has never parsed cannot happen organically (no
stored validators → unconditional GET); if a server misbehaves and sends
one anyway, the feed is simply counted unchanged this tick. Fetch errors
keep today's semantics (feed-level failure surfaces, other feeds
continue). A feed that stops sending validators falls back to
unconditional GETs automatically (empty validators stored → none sent).

### Testing

- `internal/ingestion/fetch_test.go` via `plainFetcher()` + httptest:
  request carries `If-None-Match`/`If-Modified-Since` exactly when
  validators are non-empty; 304 → `(nil, true, nil)` with no retries
  (call-counting handler); 200 → document with captured `ETag`/
  `Last-Modified`; `Fetch` sends no conditional headers.
- `internal/automations/subscriptions_test.go` via the extended `Fake`:
  second tick with matching ETag → 304 path (nothing ingested, summary
  gains "1 unchanged (304)", seen map untouched); changed ETag → full
  path + validators updated in the state row; feed removed from config →
  its validator entry pruned on next run; dry-run leaves the http row
  absent/unchanged.

### Docs

- FR-101 (S) in the docs/03 subscriptions table.
- ADR-019 trade-off line in docs/02: drop "unconditional hourly GETs
  (no ETag/304 — cheap at personal scale, noted future optimization)";
  the remaining trade-offs stand.
- CHANGELOG under Added.

## Trade-offs accepted

- Validators live in SQLite (derived, disposable — a `reindex`/fresh DB
  just means one full GET per feed; correctness is unaffected).
- Weak vs strong ETag semantics are left to the server; AXON echoes the
  validator verbatim.
- Feeds behind CDNs that vary ETags per node degrade to full GETs — no
  worse than today.

## Out of scope

- Conditional GET for one-shot ingest/capture fetches.
- `Cache-Control`/`Age`-based freshness (AXON polls on its own schedule).
- Compression negotiation changes; body caching.
