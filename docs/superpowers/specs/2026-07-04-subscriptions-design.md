# RSS/feed subscriptions — design

**Date:** 2026-07-04
**Status:** approved (brainstormed + user-approved)
**Traces to:** new FR-91…FR-93 (docs/03), ADR-004 (scheduler), ADR-015 (local routing — enrichment rides it), ADR-016 (capture is the architectural template), new ADR-019

## Goal

Config-declared RSS/Atom feeds polled on a schedule, with new items ingested
through the existing pipeline — egress-policied, deduped, ledgered, optionally
enriched — so the agentic knowledge-digest synthesizes across subscribed
material and AXON becomes a personal research assistant rather than a
bookmark processor.

## Background / constraints

- `ingestion.HTTPFetcher` already enforces the egress allowlist, refuses
  loopback/private IPs at dial time (SSRF), applies per-domain auth on every
  redirect hop, and retries transient failures — reusable as-is for fetching
  feed XML.
- Item URLs are ordinary web pages: `pipeline.Ingest(url)` handles fetch,
  extract, redact, hash-dedupe, enrich, persist, embed, events. Nothing new
  downstream.
- Capture (ADR-016) established the poll-automation pattern: registry entry,
  scheduler-driven, `automation_state` JSON row for bookkeeping, per-item
  failures never abort the run, per-call pipeline copy for the enrichment
  toggle.
- Go's stdlib has no feed parser, and real-world feeds are non-conformant
  (multiple RSS versions, Atom, chaotic dates, encodings).
- Volume is the risk: a busy feed with enrichment on could burn budget; a
  fresh subscription exposes dozens of historical entries.

## Decisions (user-approved)

1. **Feed parsing via `github.com/mmcdole/gofeed`** — the de-facto Go feed
   parser (RSS 0.9–2.0, Atom, JSON Feed, tolerant date/encoding handling).
   Pure Go, leaf dependency; the guardrail-mandated justification lives in
   ADR-019. *Rejected:* a hand-rolled minimal RSS2/Atom parser (malformed
   feeds — common — would silently yield nothing; RSS date parsing alone is
   a permanent bug farm).
2. **Subscribe-from-now.** A feed's first tick marks all current entries as
   seen and ingests nothing; only entries appearing after subscription are
   ingested. Historical items are an explicit `axon ingest <url>` away.
   *Rejected:* full backfill (guaranteed flood), newest-3 taste (arbitrary).
3. **Mark-seen-after-attempt.** An item is recorded seen whether its ingest
   succeeded or failed — one attempt per item, failure surfaced in the run
   summary/events, explicit re-ingest as the retry path. No hourly retry
   spam, no unbounded failure memory.

## Design

### Config (`internal/config`)

Optional `subscriptions:` block on `Profile` (absent = zero feeds, automation
skips):

```yaml
subscriptions:
  enrich: heuristic        # heuristic (default, zero tokens) | claude (chokepoint, routine tier)
  max_per_tick: 5          # new items ingested per feed per tick (default 5)
  feeds:
    - url: https://example.com/feed.xml
```

`SubscriptionsConfig{Enrich string; MaxPerTick int; Feeds []Feed}` with
`Feed{URL string}`; accessors `EnrichMode()` (default `heuristic`) and
`PerTick()` (default 5) following `CaptureConfig`'s pattern. Validation:
`enrich` `oneof` when set; every feed URL must parse as http(s);
`max_per_tick` ≥ 0 (0 = default).

### The `subscriptions` automation (`internal/automations/subscriptions.go`)

- Registry name `subscriptions`; `Essential() == false`; **feed-item
  enrichment is the only model path**, and only when `enrich: claude`
  (through the chokepoint on the routine tier, exactly like capture).
- **DetectChange:** no feeds configured → not changed ("no feeds
  configured"); otherwise changed (feeds change remotely; the poll is the
  check). Cursor: `subs:<n-feeds>:<tick-hour>` is unnecessary — return no
  cursor; the schedule is the cadence. (`catch_up: skip`.)
- **Run:** load seen-state; for each configured feed:
  1. `Fetcher.Fetch(ctx, feed.URL)` via the pipeline's fetcher (egress/SSRF/
     auth enforced). Fetch or parse failure → count the feed as failed,
     continue with the others.
  2. `gofeed.Parser.Parse(bytes.NewReader(doc.Body))`; items sorted newest
     first (published/updated date, tolerating absent dates by feed order).
  3. **First tick for this feed** (no seen entry): mark every current item
     link seen; report "subscribed (N existing entries marked seen)"; ingest
     nothing.
  4. Otherwise: for each item link not seen and not already in `sources`
     (`db.GetSourceByURL`), up to `max_per_tick`: `pipeline.Ingest(ctx,
     link, IngestOptions{})` on a pipeline copy carrying the configured
     enricher; mark seen after the attempt regardless of outcome; count
     ingested/failed.
  - Dry-run: fetch + parse (read-only network) but ingest nothing and write
    no state; report what would be ingested per feed.
  - Persist seen-state (non-dry): `automation_state` row
    `subscriptions:seen`, JSON `map[string][]string` feed URL → seen item
    URLs, each list capped at the 500 newest.
  - `RunResult.Summary`: `"ingested X item(s) from Y feed(s), Z failed"` (or
    the subscribe/skip variants); `Changes`: one line per ingested item
    (`feed → notePath`) and per failure.
- Starter config: `subscriptions: {enabled: true, schedule: "0 * * * *",
  model: routine, budget_tokens: 60_000, catch_up: skip}` plus a commented
  `subscriptions:` block with an example feed.

### Downstream (no changes)

Feed items become ordinary knowledge notes under `03-Resources/Knowledge/`:
the agentic knowledge-digest reads them, the resurfacer sees their vectors,
ingestion charts show the volume, and `ingest.done/skip` events stream as
usual.

### Testing

- Config: defaults/validation table (bad scheme rejected, enrich enum).
- Automation, table-driven against a stub `Fetcher` serving fixture XML
  (one RSS 2.0 fixture, one Atom fixture, one malformed):
  first-tick marks-seen-without-ingest; second tick with a new item ingests
  it (fixture item URLs point at the stub fetcher too, serving HTML);
  per-tick cap; already-in-sources skip; mark-seen-after-failure (broken
  item attempted once across two runs); feed-level failure isolates; dry-run
  fetches but writes nothing; no-feeds DetectChange skip; seen-state cap.
- Registration tests (registry count 14, catalog, schedulables); mcp count
  bump.
- Live smoke: scratch env + a real public feed (or a `python3 -m
  http.server` fixture) → subscribe tick, then add an item to the fixture →
  ingest tick.

### Docs

- **ADR-019**: "Feed subscriptions on the capture pattern, parsed by gofeed"
  — records the dependency justification (stdlib gap + wild-feed tolerance;
  pure-Go leaf), subscribe-from-now, mark-seen-after-attempt, fetcher reuse,
  and the deferred `axon subscribe` CLI + conditional-GET notes.
- **FR-91…FR-93** in docs/03:
  - FR-91 (M): scheduled feed polling — config-declared feeds fetched
    through the egress-policied fetcher, parsed (RSS/Atom/JSON Feed), new
    items ingested through the standard pipeline.
  - FR-92 (M): volume control — subscribe-from-now first tick, per-feed
    per-tick cap, seen-state in `automation_state` (capped), items attempted
    exactly once, feed failures isolated.
  - FR-93 (S): `subscriptions.enrich` toggle (heuristic default; claude via
    the chokepoint on the routine tier).
- docs/05 subscriptions section; config reference in docs/04 + example
  config; CHANGELOG.

## Trade-offs accepted

- One new dependency (gofeed) — the first since the Charm TUI; pure Go,
  leaf, ADR'd.
- Hourly unconditional feed GETs (no ETag/304) — bytes are cheap at
  personal-feed counts; conditional GET is a noted future optimization.
- Mark-seen-after-attempt means a transient failure on an item loses it
  unless re-ingested explicitly — chosen over hourly retry loops.

## Out of scope

- `axon subscribe <url>` CLI (config-edit is the v1 path; list-append via
  the comment-preserving writer is its own slice — ADR-noted).
- Conditional GET (ETag/If-Modified-Since), OPML import, per-feed enrich
  overrides, podcast/YouTube media handling, feed autodiscovery from page
  URLs.
