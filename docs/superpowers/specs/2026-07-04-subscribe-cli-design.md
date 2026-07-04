# `axon subscribe` CLI — design

**Date:** 2026-07-04
**Status:** approved (brainstormed + user-approved)
**Traces to:** new FR-100 (docs/03), ADR-019 (subscriptions — this closes its
noted follow-up slice), NFR-04 (egress policy), Component 10 (CLI surface)

## Goal

Adding, listing, and removing feed subscriptions without hand-editing
`config.yaml`: `axon subscribe <url>` verifies the feed, checks the ingest
policy, and appends it to `profiles.<active>.subscriptions.feeds` through the
comment-preserving config editor.

## Background / constraints

- ADR-019 shipped feeds as config-declared only and explicitly deferred "an
  `axon subscribe` list-append CLI through the comment-preserving writer".
- The comment-preserving editor pattern exists in `cmd/axon`:
  `setConfigValue` (scalar replace at a `yaml.PathString`, re-`config.Parse`
  gate, `writeFileAtomic`) and `setAutomationEnabled` (rebuild one block from
  the parsed struct, `yaml.Marshal`, `path.ReplaceWithReader` — creates the
  block if missing, preserves comments everywhere else).
- `config.Feed` is `{URL string}` at `SubscriptionsConfig.Feeds`;
  `validateSubscriptions` requires http(s) + host.
- `ingestion.CheckIngestPolicy(policy, host)` is the authoritative
  domain-allow check; `ingestion.NewHTTPFetcher(policy)` is the
  egress-policied, SSRF-guarded fetcher; gofeed parses RSS/Atom/JSON Feed.
- The seen-state lives in `automation_state` under `subscriptions:seen` as
  `map[feedURL][]itemURL` (FR-92).
- The daemon loads config once at start (`loadProfileDeps`); the engine holds
  it in memory. Config edits reach a running daemon only on restart, but
  `axon run subscriptions` re-loads config per invocation.

## Decisions (user-approved)

1. **Full manage set**: `subscribe <url>`, `subscribe list`,
   `subscribe remove <url>` — not add-only.
2. **Verify at add time**: fetch through the egress-policied fetcher + gofeed
   parse; show the feed title. `--no-verify` skips (offline/flaky feeds).
3. **Policy refusal is explicit**: a feed whose host fails
   `CheckIngestPolicy` is refused with the exact config line to add;
   `--allow` opts in by appending the host to `policy.ingest_domains_allow`
   in the same comment-preserving way. Never silent.

## Design

### Command surface (`cmd/axon/subscribe_cmd.go`, registered on root)

- **`axon subscribe <url> [--no-verify] [--allow]`** — flow, cheapest first:
  1. URL shape check (http/https + non-empty host), mirroring
     `validateSubscriptions`.
  2. Duplicate check against the parsed profile's `Subscriptions.Feeds` →
     friendly no-op exit 0: "already subscribed".
  3. Policy: `ingestion.CheckIngestPolicy(profile.Policy, host)`. Refusal
     without `--allow` → non-zero exit, message shows the offending host,
     the `ingest_domains_allow` line to add, and the `--allow` hint. With
     `--allow` → append the host to `policy.ingest_domains_allow` first
     (same block-rebuild editor), then continue.
  4. Verify (unless `--no-verify`):
     `ingestion.NewHTTPFetcher(profile.Policy).Fetch(ctx, url)` +
     `gofeed.Parse`; success prints title + entry count; failure aborts
     with the error and the `--no-verify` hint.
  5. Append `{url}` to `subscriptions.feeds` via the block-rebuild editor;
     print next steps: polled hourly; first poll marks existing entries
     seen (FR-92); a running daemon applies it on restart, or run
     `axon run subscriptions` now.
- **`axon subscribe list`** — prints the configured feeds (config order) with
  seen-entry counts from the `subscriptions:seen` state row ("pending first
  poll" when the feed has no entry). Read-only; if the DB is missing or
  unreadable it degrades to config-only output, never errors.
- **`axon subscribe remove <url>`** — exact-URL match; removes the feed from
  config AND drops its `subscriptions:seen` entry so a later re-subscribe
  re-baselines (subscribe-from-now again). Unknown URL → non-zero exit
  listing current feeds. If the DB is unavailable the config edit still
  proceeds (stale seen entries are harmless and capped).

### Config-write mechanics

The `setAutomationEnabled` precedent, not surgical AST sequence-append:
read config → `config.Parse` → rebuild the profile's `SubscriptionsConfig`
(append/remove the feed; preserve `enrich`/`max_per_tick`) → `yaml.Marshal`
the block → `yaml.PathString(profiles.<name>.subscriptions)` +
`ReplaceWithReader` (creates the node if absent) → re-`config.Parse` to
refuse invalid writes → `writeFileAtomic`. Same mechanics for the `--allow`
policy append, rebuilding the `policy` block. Trade-off accepted: comments
*inside* the rewritten block are lost; comments everywhere else survive.
The `subscriptions` and `policy` blocks become CLI-managed.

### What this is not

No model calls, no vault writes, no ledger entries — pure config tooling.
The two cardinal rules are untouched; the only network I/O is the optional
verification fetch, which goes through the same egress-policied fetcher as
every ingest (NFR-04).

### Testing

CLI tests in `cmd/axon` mirroring `configure_cmd_test.go`, table-driven:
- add: creates the `subscriptions:` block when absent; appends when present;
  preserves a comment elsewhere in the file; dedupe no-op; invalid URL shape
  refused.
- policy: refused host → non-zero + guidance; `--allow` appends the domain
  and subscribes; `--allow` when the host already passes the policy check
  makes no policy edit (the flag only acts on refusal).
- verify: `httptest` server with real Atom XML → success prints title;
  HTML body → parse failure aborts; `--no-verify` skips the fetch entirely
  (asserted by a fetch-counting handler); fetcher policy failure surfaces.
- list: with seen state (counts), without DB (config-only), no feeds.
- remove: config row gone + seen entry dropped; unknown URL → non-zero.
- config re-validation refusal path (corrupt injected value cannot be
  written).

### Docs

- FR-100 (S) appended to the docs/03 subscriptions table (see commit).
- ADR-019 trade-off line in docs/02 updated: the `axon subscribe` follow-up
  is built; conditional GET remains the only open note.
- CHANGELOG under Added; `axon --help` self-documents the rest.

## Trade-offs accepted

- Comments inside the `subscriptions:` (and, with `--allow`, `policy:`)
  blocks are rewritten on first CLI edit.
- A running daemon needs a restart to poll a newly added feed; the CLI says
  so and offers `axon run subscriptions` as the immediate path.
- Exact-URL remove (no fuzzy/index matching) — YAGNI at personal scale.

## Out of scope

- Feed autodiscovery from HTML pages (`<link rel="alternate">`).
- Conditional GET / ETag caching (separate ADR-019 note).
- OPML import/export; per-feed overrides (enrich, caps).
- TUI/dashboard subscription management.
