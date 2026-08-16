# Axon Companion — daemon contract

> **Frozen** 2026-08-16 against daemon `01b6259` (post-`v1.3.2`) on macOS 27.0.
> This file is the single source of truth for every byte Companion reads from or
> writes to the AXON daemon. AxonKit decodes **tolerantly**: unknown fields are
> ignored, and every field beyond the identity fields listed below is optional.
> If Companion needs something not documented here, the fix is an **upstream Go
> change** to the daemon (see "Seams grown for Companion"), never a workaround
> in Swift.

Captured samples live beside this file in
`Tests/AxonKitTests/Fixtures/`. Every AxonKit decoder has a test that decodes
its fixture; the fixtures are the regression net for daemon drift.

---

## 0. Compatibility rule (CFR-82)

- **Minimum daemon:** the release current at freeze time — `v1.3.2` /
  commit `01b6259`. Companion reads `/health.version`.
- **Older daemon:** Companion degrades **feature-by-feature**. A missing field
  decodes to `nil` and the surface that needs it hides itself with a one-line
  reason ("needs axon ≥ 1.3.3"). Companion never blocks startup on version.
- **Newer daemon:** unknown JSON fields and unknown SSE `kind`s are ignored by
  construction. A new `kind` must not break the event stream — there is a
  fixture frame (`future.kind.axon.does.not.emit.yet`) that asserts this.
- **Version string is not semver-clean.** A dev build reports
  `v1.3.1-1-gec42a3a` or `01b625917053-dirty` (recovered from VCS build info
  when `-ldflags` are absent). Parse leniently; never `require(version == x)`.

---

## 1. Transport & security

| | |
|---|---|
| Base URL | `http://127.0.0.1:7777` — host/port from `axon config get dashboard.host` / `dashboard.port` |
| Host guard | The daemon rejects any request whose `Host` header is not `localhost`, `127.0.0.1`, `::1`, `[::1]` → **403 `forbidden host`**. Anti-DNS-rebinding (FR-63). `URLSession` sets `Host` from the URL, so hitting `127.0.0.1` directly is correct; never use a custom hostname. |
| TLS | None. Loopback only. Companion must never widen this. |
| Auth | None beyond the host guard + per-endpoint guard headers below. |
| Timeouts | Daemon `ReadHeaderTimeout` is 5 s. Companion uses a 3 s request timeout for polling reads, unbounded for `/events`. |

### Guard headers

These force a CORS preflight that no cross-origin page can pass. Requests
missing them get **403 `forbidden`** (not 401).

| Endpoint | Required headers |
|---|---|
| `GET /api/actions` | `X-Axon-Actions: 1` |
| `POST /api/actions/complete` | `X-Axon-Actions: 1` + `Content-Type: application/json` |
| `POST /api/review/action` | `X-Axon-Review: 1` + `Content-Type: application/json` |
| `GET /api/related` | `X-Axon-Related: 1` |
| `POST /api/ask` | `X-Axon-Ask: 1` + `Content-Type: application/json` |
| `POST /api/capture` | `X-Axon-Capture: 1` + `Content-Type: application/json` |

Everything else (`/health`, `/events`, `/api/tokens|usage|runs|ingestion|vault|graph|activity|review|export`)
needs **no** guard header.

**Companion v1 sends only `X-Axon-Actions`** (for the Actions badge count).
It performs no review resolution, no ask, and no capture — those stay on the
web dashboard (PRD §7 non-goals).

---

## 2. `GET /health`

Fixture: `Fixtures/health.json`. Polled every 5 s (CFR-01).

```json
{
  "actions_enabled": true,
  "ask_enabled": true,
  "capture_enabled": true,
  "db": true,
  "embeddings_dim": 768,
  "embeddings_model": "nomic-embed-text",
  "embeddings_provider": "ollama",
  "latest_version": "1.3.2",
  "profile": "personal",
  "related_enabled": true,
  "started_at": "2026-08-16T16:41:02Z",
  "status": "ok",
  "update_available": true,
  "version": "v1.3.1-1-gec42a3a"
}
```

| Field | Type | Companion use | Notes |
|---|---|---|---|
| `status` | `"ok"` \| `"degraded"` | **identity** — drives `.running` vs `.attention` | `degraded` today means only "DB ping failed" |
| `profile` | string | popover header | |
| `db` | bool | attention reason `.degraded("db")` | |
| `version` | string | **identity** — About pane, min-version gate | not semver-clean; see §0 |
| `latest_version` | string? | update row | **absent** until a release check has run |
| `update_available` | bool? | attention reason `.updateAvailable` | absent alongside `latest_version` |
| `started_at` | RFC3339 string? | uptime pill (CFR-02) | **new in this branch** — see §9 |
| `embeddings_provider` / `_model` / `_dim` | string/string/int | Settings → Daemon read-only row | |
| `ask_enabled`, `capture_enabled`, `related_enabled`, `actions_enabled` | bool | hide surfaces the profile disabled | Companion only reads `actions_enabled` |

**Absent by design** (do not expect them, despite `docs/09` §5 prose):
scheduler state, Ollama reachability, and last-successful-run-per-automation
are **not** on `/health`. Companion gets run state from
`axon automations --json` (§7) and `/api/runs` (§5).

There is **no** `uptime` field — compute `now - started_at`.

---

## 3. `GET /api/usage`

Fixture: `Fixtures/usage.json`. Numerically identical to `axon status --json`
(enforced by the daemon's own `TestUsageMatchesManagerStatus`).

```json
{
  "day_cost_cap": 0, "day_cost_pct": 0, "day_cost_used": 0,
  "day_limit": 1500000, "day_pct": 0.2482666666666667, "day_used": 3724,
  "guard_paused": false, "guard_pct": 80, "guard_reason": "",
  "profile": "personal",
  "week_limit": 8000000, "week_pct": 0.38411249999999997, "week_used": 30729
}
```

- `*_pct` is **0–100**, not 0–1. SwiftUI `Gauge` needs `value/limit`, so divide.
- `guard_paused: true` + non-empty `guard_reason` → `.attention([.budgetGuard])`
  and the red guard chip. This pauses **automations only**, never interactive use.
- `day_cost_*` / `week_cost_*` are zero outside `auth_mode: api_key`; Companion
  shows tokens, not dollars, and hides cost rows when the cap is 0.
- `week_cost_cap` / `week_cost_pct` / `week_cost_used` are **absent** in this
  sample. Treat all cost fields as optional.

---

## 4. `GET /api/tokens`

Fixture: `Fixtures/tokens.json` (24 buckets across 3 models / several
operations, trimmed from a live 137-bucket response).

Query: `?days=N` (default **30**). This is the **only** range-aware read
besides `/api/runs`.

A **flat array** of per-day/operation/model buckets:

```json
[{ "day": "2026-08-09", "operation": "automation.briefing",
   "model": "claude-sonnet-5", "input": 4, "output": 118,
   "cache_read": 12041, "cache_write": 5445 }]
```

| Field | Type | Notes |
|---|---|---|
| `day` | `YYYY-MM-DD` | **identity** |
| `operation` | string | **identity**. Shape is `automation.<name>` or `ingest.enrich`. Strip the `automation.` prefix for the "by automation" legend. |
| `model` | string | **identity**. Full model id, e.g. `claude-sonnet-5`. |
| `input`, `output`, `cache_read`, `cache_write` | int64 | Charted total = `input + output`. Cache columns are informational; **do not** add them to the budget total — the budget counts what `axon status` counts. |

Both CFR-30 stackings (by automation, by model) come from this one response —
group client-side, do not re-fetch.

---

## 5. `GET /api/runs`

Fixture: `Fixtures/runs.json` (20 rows, deliberately including `failed`,
`ok` and `skipped`).

Query: `?limit=N` (default **100**). Newest first.

```json
[{ "id": 7003, "automation": "capture",
   "started_at": "2026-08-16T16:50:03Z", "finished_at": "2026-08-16T16:50:03Z",
   "status": "skipped", "skip_reason": "inbox unchanged since last capture",
   "tokens": 0, "error": "" }]
```

- `status` ∈ `ok` | `skipped` | `failed` (also transiently `running`).
- `skip_reason` and `error` are `""` (not absent) when not applicable.
- Timestamps are RFC3339 **UTC with `Z`**. Duration = `finished_at - started_at`;
  a still-running row has an empty `finished_at`.
- `limit` is a **row count, not a time range**. For the 24 h/7 d/30 d picker,
  fetch a generous `limit` and filter by `started_at` client-side.

---

## 6. `GET /api/ingestion` and `GET /api/vault`

Fixtures: `Fixtures/ingestion.json`, `Fixtures/vault.json`.
**Neither takes a range parameter** — both always return their full fixed
window. The Insights range picker filters these two client-side by `day`.

### `/api/ingestion`

```json
{ "embedding_queue": 0, "series": null }
```

⚠️ **`series` is `null`, not `[]`, when there are no sources.** That is the real
captured response on a vault with zero ingested sources — the fixture keeps it
that way on purpose. Decode as `[SourceBucket]?` and render an empty state.

When populated, each bucket is:
`{ "day": "YYYY-MM-DD", "status": "ok"|"failed"|"redacted"|…, "count": 3 }`
— counts of `sources` rows grouped by fetch day and status.

`embedding_queue` is the current count of chunks awaiting embedding (queue
depth), an **instantaneous scalar**, not a series.

### `/api/vault`

```json
{
  "growth": [{ "day": "2026-08-16", "notes": 165, "words": 183737 }],
  "stats": { "notes": 165, "links": 548, "words": 183737,
             "sources": 0, "inbox_backlog": 0 }
}
```

- `growth` points carry **`notes` and `words` only — there is no `links`
  series.** CFR-30's "notes, links, words over time" is only achievable for two
  of the three; `links` is charted as a current-value tile from `stats.links`.
- `growth` is **cumulative** by note-creation date and is derived from the
  current `notes` table, not snapshotted — it is rebuildable from Markdown
  (ADR-006) and therefore *changes retroactively* when notes are added with old
  `created` dates. Do not treat it as an append-only log.
- `stats.inbox_backlog` and the review-queue size are **instantaneous scalars**,
  not time series. CFR-30's "inbox backlog; review-queue size" render as tiles.

---

## 7. `GET /api/review` and `GET /api/actions` (badge counts)

### `/api/review` — no guard header

Fixture: `Fixtures/review.json` (trimmed to 5 of 272 items).

```json
{ "items": [{ "id": "89436b7356c8", "kind": "info",
              "section": "Memory reconciliation (2026-07-12 05:10)",
              "line": "- [x] reconcile: …", "checked": true }],
  "pending": 160 }
```

**Companion reads `pending` and nothing else.** `items` can be ~100 KB; the
badge needs one integer. Review *resolution* stays on the web dashboard
(PRD §7).

### `/api/actions` — requires `X-Axon-Actions: 1`

Fixture: `Fixtures/actions.json` (trimmed to 8 of ~1600 actions).

```json
{ "actions": [ … ],
  "counts": { "open": 521, "overdue": 0, "today": 3,
              "waiting": 12, "someday": 40, "done7": 9 },
  "trend": [{ "day": "2026-08-16", "done": 2 }],
  "vault": "Personal" }
```

**Companion reads `counts.open`** for the Actions badge. Returns **404** when
`actions_enabled` is false — hide the badge, do not show an error.

---

## 8. `GET /api/export` (CFR-31)

No guard header. Companion never serialises chart data itself.

```
GET /api/export?dataset=<tokens|runs|ingestion|vault|graph|activity>&format=<csv|json>
```

- `format` defaults to `csv`.
- Sets `Content-Disposition: attachment; filename=axon-<dataset>-<YYYY-MM-DD>.<ext>`.
  Companion parses this for the `NSSavePanel` default filename, falling back to
  the same pattern built locally.
- Unknown `dataset` → **400** with body
  `unknown dataset (tokens|runs|ingestion|vault|graph|activity)`.
- Bad `format` → **400** `format must be csv or json`.
- Nested datasets that have no CSV projection → **400**
  `dataset <x> is JSON-only (nested); use format=json`.
- **`dataset` does not accept range parameters.** Export always covers the
  endpoint's default window (tokens: 30 days, runs: 100 rows). The export menu
  labels this so the user is not misled by the on-screen range picker.

---

## 9. `GET /events` (SSE)

Fixture: `Fixtures/events.sse`.

### Framing

```
: connected

event: <kind>
data: <one-line JSON>

: ping

```

- Opens with the comment line `: connected` — Companion treats receipt of *any*
  byte as `.connected`.
- Keep-alive comment `: ping` every **20 s**. A silent stream past ~45 s is
  presumed dead → reconnect.
- Lines starting with `:` are comments — skip them.
- Frames are separated by a **blank line**. `event:` always precedes `data:`,
  and the daemon writes `data:` as a single line — but a compliant parser
  **must** concatenate consecutive `data:` lines (fixture includes a two-line
  case).
- A malformed/truncated `data:` payload must be **skipped, not fatal** (fixture
  includes a truncated frame).
- No `id:` field and no `Last-Event-ID` resume. On reconnect Companion refetches
  REST snapshots rather than replaying — the stream is a *change signal*, not a
  log of record.

### Event JSON

```json
{ "ts": "2026-08-16T17:55:00.414609+01:00", "level": "info",
  "kind": "automation.skip", "message": "capture: skipped",
  "data": { "…": "…" } }
```

| Field | Type | Notes |
|---|---|---|
| `ts` | RFC3339 | **local offset**, not `Z`, unlike the REST timestamps |
| `level` | `info` \| `warn` \| `error` | |
| `kind` | string | mirrors the SSE `event:` name |
| `message` | string | human-readable; safe to show verbatim in a notification body |
| `data` | object? | shape varies by kind; **never contains secrets** (NFR) |

### Kinds

Documented union (`docs/09` §3):

```
automation.run | automation.skip | automation.fail
ingest.done | ingest.skip | ingest.enrich
ingest.embed.fail | ingest.embed.skip | ingest.review_queue.fail
token.ledger | token.deny | token.defer | token.downgrade | token.error
review.accept | review.dismiss | action.done
```

⚠️ **The daemon emits kinds outside that union** — `ask.refused` is present in
the live event history and is **not** in `docs/09`. This is exactly why
tolerant decoding is mandatory: treat `kind` as an open string, never an
exhaustive Swift enum.

Companion reacts to:

| Kind | Reaction |
|---|---|
| `automation.fail` | notification (deduped 1/automation/hour, CFR-70); refresh runs |
| `token.deny`, `token.defer`, `token.downgrade` | guard notification (1/episode); refresh usage |
| `token.ledger` | refresh tokens + usage (coalesced) |
| `ingest.*` | refresh ingestion (coalesced) |
| `automation.run`, `automation.skip` | refresh runs (coalesced) |
| everything else | ignored |

`data` payload shapes Companion relies on:

- `automation.*` → `{"outcome": {"automation": "heartbeat", "status": "ok",
  "summary": "…", "tokens": 300, "run_id": 6931, "dry_run": false,
  "skip_reason": "…", "error": "…"}}` — Companion reads
  `outcome.automation` (for notification dedup keys) and `outcome.status`.
- `token.*` → `{"profile", "operation", "decision", "est_input", "reason"}` —
  Companion reads `reason` for the notification body.

**Fixture provenance.** Frames for `action.done`, `ask.refused`,
`automation.fail`, `automation.run`, `automation.skip`, `review.accept`,
`review.dismiss`, `token.error`, `token.ledger` are **real**, extracted from the
daemon's persisted `events` table (one latest row per kind) and re-framed
exactly as `sse.go` writes them. Frames for `ingest.done`, `ingest.skip`,
`ingest.embed.fail` and `token.deny` are **composed from the daemon's publish
sites** (`internal/ingestion/pipeline.go`, `internal/tokens/manager.go`) because
this vault has no persisted history for them; the last three frames
(multi-line `data:`, unknown kind, truncated payload) are **deliberate parser
edge cases**.

---

## 10. CLI contract

Located via `BinaryLocator`: explicit setting → `/usr/local/bin/axon` →
`/opt/homebrew/bin/axon` → `PATH` scan. **Never** through `$SHELL -c`.

**Error convention (uniform across all commands):** on failure the process
writes a styled `\n✗ Error: <message>\n` to **stderr only**, leaves **stdout
empty**, and exits **non-zero**. So: parse stdout, surface stderr, branch on
exit code. Never try to parse an error out of stdout.

Global flags Companion always passes through when configured:
`--config <path>`, `--profile <name>`, `--env <path>`.

### `axon status --json` → `Fixtures/status-cli.json`

⚠️ **PascalCase, untagged** — unlike every other `--json` command.

```json
{ "Profile": "personal",
  "Day":  { "Used": 3724, "Limit": 1500000, "Pct": 0.248, "CostUsed": 0, "CostCap": 0, "CostPct": 0 },
  "Week": { "Used": 30729, "Limit": 8000000, "Pct": 0.384, "CostUsed": 0, "CostCap": 0, "CostPct": 0 },
  "GuardPct": 80, "GuardPaused": false, "GuardReason": "" }
```

Used **only as the fallback** when port 7777 is dead — it proves the profile is
readable and therefore that the binary works, which distinguishes `.stopped`
from `.notInstalled`. When the daemon is up, `/api/usage` is authoritative.

### `axon doctor --json` → `Fixtures/doctor-cli.json`

**Added in this branch** (see §11). snake_case.

```json
{ "profile": "personal", "status": "ok",
  "checks": [{ "name": "claude-cli", "status": "ok",
               "detail": "Claude Code CLI found" }] }
```

- `status` ∈ `ok` | `fail`; check `status` ∈ `ok` | `warn` | `fail`.
- `error` (optional, top level) carries a config-load error while checks still run.
- **There is no separate `remediation` field.** The daemon folds remediation
  into `detail` (`"… — run \`axon update\`"`, `"… — install poppler + tesseract"`).
  CFR-60's "the daemon's own remediation text" **is** `detail`; render it
  verbatim as selectable monospaced text. Do not attempt to split it.
- Exits **non-zero** when `status == "fail"` while still writing the full JSON
  to stdout. Companion must parse stdout **regardless of exit code** here.
- `doctor` works with the daemon **stopped** — that is its job.

### `axon automations --json` → `Fixtures/automations-cli.json`

```json
[{ "name": "actions-consolidate", "purpose": "…",
   "essential": false, "enabled": true, "config_enabled": true,
   "allowed": true, "schedule": "0 7 * * *", "model": "none",
   "last_run": { "id": 6924, "automation": "actions-consolidate",
                 "started_at": "2026-08-16T08:09:02Z",
                 "finished_at": "2026-08-16T08:09:02Z",
                 "status": "skipped", "skip_reason": "actions unchanged",
                 "tokens": 0 } }]
```

- `enabled` is the **effective** state; `config_enabled` is what the config file
  says; `allowed` is whether the profile's policy permits it at all. A `work`
  profile with an allowlist reports `allowed: false` for everything outside it.
  The Automations pane shows the toggle **disabled with a reason** when
  `allowed == false` — flipping the config key would be a lie.
- `last_run` is **absent** for never-run automations.
- `schedule` is a 5-field cron expression.
- `model` ∈ `none` | `classify` | `routine` | `synthesis` — `none` means the
  automation spends zero tokens.

### `axon profiles --json` → `Fixtures/profiles-cli.json`

```json
[{ "name": "personal", "active": true, "auth_mode": "subscription",
   "vault_path": "/Users/jandro/Notes/Personal",
   "data_dir": "/Users/jandro/.axon/profiles/personal",
   "db_path": "…/db.sqlite", "config_dir": "…/claude",
   "oauth_token_ref": "env:CLAUDE_CODE_OAUTH_TOKEN_PERSONAL",
   "allowed_automations": ["*"] }]
```

**This is the only source of the vault path** (CFR-21) — there is no
`vault.path` config key (`axon config get vault.path` → "key not found").

`oauth_token_ref` is a *reference*, never a secret value. Companion neither
reads nor displays it.

Logs folder = `<data_dir>/logs/`.

### `axon config get <key> --json` → `Fixtures/config-get-cli.json`

```json
{ "key": "limits.daily_tokens", "value": 1500000 }
```

`value` is the **native JSON type** (number, string, bool, object).
`axon config get automations --json` returns the whole subtree as an object.

### `axon config set <key> <value> --json` → `Fixtures/config-set-cli.json`

```json
{ "key": "limits.daily_tokens", "ok": true, "value": "1500000" }
```

⚠️ **`value` comes back as a string on `set`, and as a native type on `get`.**
Round-trip verification must compare loosely (or re-`get`). Companion re-`get`s
after every `set` and renders reality, never an optimistic local value.

`set` only updates **existing** keys (comment-preserving, then re-validated).

### Resolved key paths (CFR-40/41)

| Setting | Key |
|---|---|
| Daily token budget | `limits.daily_tokens` |
| Weekly token budget | `limits.weekly_tokens` |
| Dashboard port | `dashboard.port` (read-only in Companion) |
| Dashboard host | `dashboard.host` (read-only) |
| Embeddings provider | `embeddings.provider` (read-only + "how to switch" link) |
| Automation enable | `automations.<name>.enabled` |
| Automation schedule | `automations.<name>.schedule` (read-only) |

### Lifecycle & service commands

| Command | Companion use | Notes |
|---|---|---|
| `axon start` | popover Start | **Runs in the foreground until interrupted.** Companion must *not* wait for exit — spawn detached and confirm via `/health` polling. |
| `axon stop` | popover Stop | SIGTERM via pidfile; returns promptly |
| `axon service install` / `uninstall` | "Start AXON at login" toggle | Owns launchd `com.axon.<profile>`. Companion **never** touches `launchctl` or plists (CFR-11). No `--json`. |
| `axon update` | update row | Confirmation-gated. No `--json`; Companion surfaces raw output. |
| `axon version --short` | About pane | plain string, e.g. `v1.3.1-1-gec42a3a` |

⚠️ On this machine the daemon is supervised by **launchd**, so `axon stop` is
followed by a launchd relaunch (KeepAlive). Companion's Stop must therefore be
honest about what it does: it stops the *process*; if a login service is
installed, launchd restarts it. The Stop confirmation says so.

---

## 11. Dashboard deep links (CFR-20)

`web/src/App.jsx` tab ids, in order:

```
overview  tokens  automations  review  actions
ask  related  knowledge  graph  activity
```

Deep link: `http://127.0.0.1:7777/#<tabId>` — e.g. `#review`, `#actions`.

**This routing did not exist before this branch** (see §12). The SPA now reads
the fragment on load, writes it on tab change, and follows `hashchange`. An
unknown fragment falls back to `overview`, so a Companion link can never land
the dashboard on a blank page.

Obsidian: `obsidian://open?path=<percent-encoded absolute vault path>`;
fall back to revealing the path in Finder when Obsidian is not installed.

---

## 12. Seams grown for Companion (upstream Go/web changes on this branch)

Per the plan's rule — *the daemon grows the seam first, never a Companion
workaround*.

| Commit | Change | Why |
|---|---|---|
| `554b4a8` | `feat(cli): add --json to doctor` | CFR-60 needs a machine-readable doctor; only the styled TTY renderer existed. |
| `01b6259` | `feat(dashboard): report daemon started_at on /health` | CFR-02's uptime pill. A client-side "first seen" clock resets on Companion restart and lies across a daemon restart. |
| *(this branch)* | `feat(dashboard): deep-link dashboard tabs via the URL fragment` | CFR-20 needs `#review` / `#actions`. The SPA had **no routing at all** — `tab` was plain `useState`, so no tab was addressable. |

## 13. Known contract gaps (accepted, not worked around)

These are documented limitations Companion renders honestly rather than faking:

1. **No per-automation health on `/health`.** Companion derives automation
   health from `axon automations --json` + `/api/runs`.
2. **No `links` growth series.** Charted as a current-value tile (§6).
3. **No range parameters on `/api/ingestion`, `/api/vault`, `/api/export`.**
   Filtered client-side; export labels its true window (§8).
4. **`docs/09` §5 overstates `/health`** (claims scheduler/Ollama/last-run
   detail). The implementation does not emit them. Contract follows the
   implementation.
5. **`ask.refused` is an undocumented event kind.** Treated as proof that the
   kind union is open (§9).
