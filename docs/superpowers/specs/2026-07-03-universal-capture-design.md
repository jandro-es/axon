# Universal capture — Inbox capture funnel (FR-26 grown) — design

**Date:** 2026-07-03
**Status:** approved (brainstormed + user-approved)
**Traces to:** FR-26 (capture-by-Inbox, C — implemented by this), new FR-81…FR-83 (docs/03), ADR-004 (external scheduler), ADR-006 (vault is source of truth), ADR-007 (chokepoint), new ADR-016

## Goal

Make knowledge enter AXON without a terminal: drop a URL into any inbox note
or drop a file into the inbox folder — from any device that syncs the vault —
and the daemon ingests it on the next capture tick through the existing
pipeline (egress-policied, redacted, deduped, ledgered). Mobile capture with
zero mobile code: Obsidian sync carries the vault; share a URL into an inbox
note on the phone → synced → captured.

## Background / constraints

- FR-26's documented design is **poll-based** ("queued for ingestion on the
  next ingestion tick"), not a filesystem watcher. fsnotify is not a
  dependency anywhere in the codebase today.
- The automation framework already provides everything a capture loop needs:
  scheduling (`robfig/cron` via `internal/scheduler`), enabled/schedule/
  catch-up/dry-run config (`automations.<name>`), panic-safety, run rows,
  events, and a content-hash change-gate convention.
- The dashboard HTTP server is deliberately **read-only** (never writes the
  vault, never calls Claude); it must not gain a capture endpoint.
- `ingestion.Pipeline.Ingest(ctx, arg, opts)` is the single entry point;
  `IngestOptions.AllowLocalFiles` gates local-file reads (SSRF/local-read
  guard); dedupe is by `sources.url` + content hash; results land in
  `03-Resources/Knowledge/`; link suggestions append to
  `.axon/review-queue.md`.
- Cardinal rule 2: no vault mutation that isn't wikilink-safe; there is no
  `vault.delete`. Human prose is never edited by AXON.
- The vault scaffold names the inbox `00-Inbox/`; `inbox-triage` currently
  treats **every** file under it as a markdown note.

## Decisions (user-approved)

1. **Poll-based folder capture only** (v1 surface). A `capture` automation on
   the existing scheduler (suggested `*/5 * * * *`, `catch_up: run-once`).
   *Rejected:* fsnotify (new dependency, debounce and partial-sync-write
   races, still needs a scan-on-start) and a `POST /capture` route on the
   dashboard (breaks its read-only invariant). A separate localhost capture
   listener remains a possible future extension, recorded in ADR-016.
2. **Archive after ingest.** A successfully ingested dropped file is moved
   wikilink-safely to `04-Archive/Capture/YYYY-MM/` (collision → `-2`
   suffix). Nothing is ever deleted. *Rejected:* leave-in-place (inbox
   clutter defeats the inbox).
3. **Enrichment is a config toggle, default heuristic.**
   `capture.enrich: heuristic | claude` — heuristic is deterministic and
   zero-token; `claude` routes enrichment through the token-manager
   chokepoint on the `routine` tier, where ADR-015 local routing and the
   fallback ladder apply.

## Design

### The `capture` automation (`internal/automations`)

A standard automation (registry + catalog entry, name `capture`), no model
call of its own:

- **DetectChange:** hash of the `00-Inbox/` listing (relative path + size +
  mtime per entry) compared against stored automation state. Unchanged inbox
  → the tick is free (token frugality: run on new material only).
- **Run:** scan `00-Inbox/` (excluding `README*`), partition into:
  - **URL captures** — `.md` notes are scanned for URLs standing on their own
    line: a bare `http(s)://…` or a single markdown link `[title](url)` alone
    on the line (surrounding whitespace ignored). Query the `sources` table
    first: a URL already known is skipped with **no network call**. New URLs
    go through `Pipeline.Ingest` (egress allowlist enforced in the fetcher).
    The inbox note itself is **never modified** — capture bookkeeping lives
    in SQLite, preserving cardinal rule 2. (After `axon reindex`, a re-seen
    URL re-fetches once and dedupes on content hash — documented behavior.)
  - **File captures** — non-`.md` entries (`.pdf` and anything
    `ExtractFile` handles; unknown extensions fail soft into failure memory).
    Ingested via `Pipeline.Ingest(ctx, absPath, {AllowLocalFiles: true})`.
    **Path sandbox (NFR-05):** only files physically enumerated in the
    `00-Inbox/` listing are ever ingested as files; paths written *inside*
    notes are never treated as file targets, so a synced note cannot point
    capture at `~/.ssh` or anything outside the vault.
- **Archive move:** on successful file ingest, move the original to
  `<archive_dir>/YYYY-MM/<name>` (default `04-Archive/Capture`) using the
  vault's wikilink-safe move (inbound links, if any, are rewritten). The new
  knowledge note's review-queue entry links both the note and the archived
  original.
- **Failure memory:** a failed item (unreachable URL, denied domain,
  unparseable file) is recorded in the automation-state store keyed by
  item identity (URL, or path + content hash) and skipped on later ticks
  until the underlying item changes. The failure is appended **once** to
  `.axon/review-queue.md` and emitted as an event — no per-tick spam. A
  per-item failure never aborts the rest of the run.
- **Dry-run:** reports what would be ingested/moved/skipped; writes nothing,
  fetches nothing.

### Enrichment

The automation builds its enricher per `capture.enrich`:
- `heuristic` (default): `ingestion.Heuristic{}` — zero tokens.
- `claude`: `ingestion.ClaudeEnricher{Manager, ModelKey: "routine"}` — every
  call through the chokepoint (cardinal rule 1); ADR-015 local routing,
  budget checks, and the fallback ladder apply unchanged; enrichment
  degrades to heuristic under budget denial exactly as `axon ingest --enrich`
  does.

### Config (`internal/config`)

New optional block on `Profile` (absent block = zero-value defaults, existing
configs untouched):

```yaml
capture:
  enrich: heuristic          # heuristic (default) | claude
  archive_dir: 04-Archive/Capture   # optional override, vault-relative
```

Validation: `enrich` `oneof=heuristic claude` when set; `archive_dir` must be
vault-relative (no `..`, no absolute paths). Scheduling/enabling stays in the
standard `automations.capture` entry; the starter config ships
`capture: {enabled: true, schedule: "*/5 * * * *", catch_up: run-once}`.

### Wiring

`capture` joins the automation registry, so `Schedulables()` schedules it in
`axon start` with no start-loop changes; `axon run capture` and
`axon configure automations capture on|off` work like every other
automation. The engine's Bus-wired pipeline means every capture ingest emits
the standard `ingest.done/skip/...` events to SSE, the events table, and the
dashboard's ingestion charts — no SPA changes.

### Targeted fix: triage vs non-markdown files

`inboxItems` (used by `inbox-triage`) is restricted to `.md` files so a
dropped PDF is never read as a note. Capture's archiving keeps the inbox
markdown-only over time anyway; this closes the window in between.

### Vault docs

The scaffolded `00-Inbox/README` gains the capture workflow ("paste a URL on
its own line in any note here, or drop a PDF/file into this folder — AXON
ingests it within minutes and archives originals to 04-Archive/Capture").
Scaffold is idempotent-update-safe (README is a scaffold-managed file).

### Testing

- Table-driven automation tests against a temp vault + fake pipeline
  recorder: URL-line extraction cases (bare URL, markdown link, mid-sentence
  URL NOT captured, README excluded), known-URL skip without fetch, file
  ingest + archive move (collision suffix), failure memory (second tick
  skips, review queue appended once), dry-run writes nothing, change gate
  (unchanged inbox → no work).
- One integration test: temp vault + real pipeline + `httptest` URL end to
  end (note created under `03-Resources/Knowledge/`, event emitted, second
  run skips).
- Triage regression: a `.pdf` in the inbox is not read as a note.

### Docs

- **ADR-016** in docs/02: "Poll-based inbox capture on the automation
  scheduler" — records the fsnotify and dashboard-endpoint rejections, the
  archive-move choice (no delete), the path sandbox, and the future-extension
  note (separate localhost capture listener).
- **docs/03:** FR-26 flipped to implemented; new FRs:
  - FR-81 (M): file-drop capture — non-markdown inbox files ingested
    (AllowLocalFiles, inbox-listing sandbox) and archived wikilink-safely.
  - FR-82 (M): capture bookkeeping — change-gated ticks, failure memory with
    single review-queue surfacing, standard events/run observability.
  - FR-83 (S): `capture.enrich` toggle (heuristic default; claude via the
    chokepoint on the routine tier).
- **docs/04:** `capture:` config reference + starter-config entry.
- **docs/05:** capture section referencing the automation as the FR-26
  implementation.

## Trade-offs accepted

- Minutes-level capture latency (poll tick) instead of seconds (watcher) —
  acceptable for a funnel fed mostly by device sync, and tunable via cron.
- Re-fetch-once after a full reindex for URLs whose knowledge notes were
  removed — the content-hash dedupe caps the cost at one fetch.
- Failed items stay in the inbox (visible, human-fixable) rather than being
  moved to a quarantine folder — one less convention to learn.

## Out of scope

- Any HTTP capture endpoint (bookmarklet/Shortcut target) — future ADR.
- fsnotify/watchman-style filesystem watching.
- Ingesting `.md` inbox notes as knowledge sources (that's triage's domain).
- Mobile apps or Obsidian plugins.
- Automatic re-ingestion of changed remote pages (explicit `axon ingest`
  remains the refresh path).
