# Browser capture endpoint — design

Date: 2026-07-05
Status: approved
Roadmap: slice D1 of `docs/14-roadmap-1.1.md`
ADR: ADR-024 (`docs/02-architecture.md`)
FR IDs: FR-121 (`POST /api/capture`), FR-122 (served `/capture` page + recipes).

## Goal

Extend the capture funnel (ADR-016) to the browser and macOS Shortcuts: a
guarded localhost endpoint that drops a URL/selection into `00-Inbox/`, where
the existing capture automation ingests it — no new ingestion path, no
browser-triggered model spend.

## Decisions (user-approved)

- **Served same-origin `/capture` page** lets a cross-origin bookmarklet
  reach the guarded endpoint without relaxing the guard.
- **Write to `00-Inbox/`, automation ingests** — the endpoint is a thin,
  non-destructive inbox writer; no synchronous ingestion.
- **`dashboard.capture_enabled` toggle + ADR-024** documenting the served-page
  pattern and the write envelope.

## Background / constraints (verified in code)

- The capture automation ingests own-line URLs in inbox notes
  (`captureURLLine` regex, `internal/automations/capture.go:296`) and files
  dropped into `00-Inbox/` on each tick; `inboxDir = "00-Inbox"`.
- `vault.FS.Create(rel, content) (created bool, err error)`
  (`internal/vault/fs.go:303`) writes a note wikilink-safely and atomically;
  it does not overwrite (returns `created=false` if the path exists).
- The dashboard guard template is `handleReviewAction` / `handleAsk`:
  loopback bind + `guardHost` + `Content-Type: application/json` + a custom
  header (`internal/dashboard/server.go`). `Config` already carries `Vault`
  and `Bus`.
- The static handler serves the SPA with an index.html fallback for unknown
  routes (`internal/dashboard/static.go`), so `GET /capture` must be
  registered explicitly (before the catch-all) as its own handler.
- Toggle precedent: `DashboardConfig.AskEnabled *bool` + `AskAllowed()`
  (`internal/config/types.go`).

## Design

### FR-121 — `POST /api/capture`

New `handleCapture` (`internal/dashboard`):

1. 404 when `!s.cfg.CaptureEnabled || s.cfg.Vault == nil` (surface absent).
2. 403 unless `X-Axon-Capture: 1` and `Content-Type: application/json`.
3. Decode `{url, title, text string}` from a `MaxBytesReader` (16 KB — a
   selection can be a paragraph).
4. 400 if all three are empty (nothing to capture).
5. Build the note body: the URL on its own line (if present) so the capture
   automation ingests it, then the title as an `# H1` and any text as the
   body:

   ```
   <url on its own line, if any>

   # <title or "Captured note">

   <text, if any>

   <!-- captured <RFC3339> via /api/capture -->
   ```

6. Write to `00-Inbox/capture-<UTC: 20060102-150405.000>.md` via
   `vault.Create` (the millisecond stamp makes collisions practically
   impossible; on the vanishingly rare `created=false`, retry once with a
   suffix).
7. Emit `Bus.Publish(events.Event{Kind: "capture.received", …})`.
8. Return `{ok: true, path}` JSON.

No model call; the note is plain Markdown. The capture automation's egress
policy still gates the eventual fetch of any captured URL.

### FR-122 — served `/capture` page + recipes

**`GET /capture`** — a Go handler serving a small inline HTML page
(registered before the static catch-all; 404 when capture disabled). The page
JS reads `location.hash` (`u`, `t`, `s` params, URL-decoded), POSTs to
`/api/capture` **same-origin** with the `X-Axon-Capture` header, renders
"Captured ✓" or the error, and `window.close()`s on success after a beat.
Because the page is same-origin with the dashboard, the guarded POST
succeeds; a cross-origin page opening `/capture` only navigates to it (it
cannot read the response or forge the same-origin POST).

**Bookmarklet** (docs) — a one-liner the user drags to their bookmarks bar,
templated with their dashboard port:

```
javascript:(function(){var p=7777;window.open('http://127.0.0.1:'+p+'/capture#u='+encodeURIComponent(location.href)+'&t='+encodeURIComponent(document.title)+'&s='+encodeURIComponent((''+getSelection()).slice(0,2000)));})()
```

**macOS Shortcuts** (docs) — "Get Contents of URL" → `POST` to
`http://127.0.0.1:7777/api/capture`, headers `Content-Type: application/json`
and `X-Axon-Capture: 1`, JSON body `{"url": <Shortcut Input>}`. Also usable
from `curl`.

**Health** — `/health` gains `"capture_enabled": cfg.CaptureEnabled` (symmetry
with `ask_enabled` and future SPA hints).

### Config + wiring

- `DashboardConfig.CaptureEnabled *bool` (`yaml:"capture_enabled,omitempty"`)
  + `CaptureAllowed() bool` (default true).
- `dashboard.Config.CaptureEnabled bool`.
- `cmd/axon/start_cmd.go`: `CaptureEnabled: deps.profile.Dashboard.CaptureAllowed()`.
- Routes: `mux.HandleFunc("POST /api/capture", s.handleCapture)` and
  `mux.HandleFunc("GET /capture", s.handleCapturePage)`.

### Error handling

- Disabled / no vault → 404 (both routes).
- Missing header / wrong content type → 403.
- Empty payload → 400.
- Vault write error → 500.
- The served page shows any non-2xx body inline and does not auto-close.

### Testing

Go handler tests (mirroring `ask_api_test.go` / `review_api_test.go`):

- Guard: missing `X-Axon-Capture` → 403.
- Disabled (`CaptureEnabled=false`) → `POST /api/capture` and `GET /capture`
  both 404.
- URL capture → 200; a `00-Inbox/capture-*.md` note exists containing the URL
  on its own line; a `capture.received` event is published.
- Text-only capture → 200; note contains the text; no own-line URL.
- Empty `{}` → 400.
- `GET /capture` enabled → 200 HTML whose body references `/api/capture` and
  `X-Axon-Capture` (so the page will actually post correctly).

No SPA changes (capture is not a dashboard tab).

### Docs

- ADR-024 in `docs/02-architecture.md` (done).
- FR-121/122 in `docs/03-requirements.md` (done).
- `docs/04-data-model-and-config.md` + `axon.config.example.yaml`:
  `dashboard.capture_enabled`.
- `docs/GUIDE.md`: a "Capture from anywhere" section (bookmarklet + Shortcuts
  + curl), near the ingestion/capture material.
- `docs/14-roadmap-1.1.md`: mark D1 built.
- `CHANGELOG.md` entry.

## Trade-offs accepted

- Captured URLs ingest on the next capture tick, not instantly (funnel
  latency; tunable via the capture schedule).
- The served `/capture` page is inline Go HTML, thin and lightly tested
  (handler smoke only), like the dashboard's other thin surfaces.
- A local process past the preflight can append inbox notes up to disk
  limits — same trust level as any local process with the config; writes are
  confined to `00-Inbox/` and non-destructive.

## Out of scope

- A dashboard capture *tab* (capture is bookmarklet/Shortcuts/served-page).
- Synchronous ingestion / enrichment from the endpoint.
- Non-macOS share-sheet integrations (the endpoint is generic; only the
  recipes are macOS-specific).
