# T3 — Dashboard Actions tab + completion mutation — design

**Slice:** T3 (roadmap `docs/16-roadmap-1.2.5.md`) · **Date:** 2026-07-10
**FR:** FR-162, FR-163, FR-164 · **ADR:** ADR-034
**Status:** design approved; ready for implementation plan.

> Current maxima before this slice: FR-161, ADR-033. After: FR-164, ADR-034.
> **This slice completes the T1+T2+T3 release criterion for 1.2.5.**

## 1. Summary

The at-a-glance state and the deal-with-it loop — and the one write in the whole
"act on it" theme. Three pieces:

- **FR-162 — `GET /api/actions`.** A read endpoint (clone of `handleRelated`)
  returning the filtered action list + a GTD counts summary + a 30-day completion
  trend, all derived from the T1 `actions` table (matching `axon actions`).
- **FR-163 — `POST /api/actions/complete` + `vault.CompleteAction`.** The **one
  new mutation** (ADR-034): a byte-precise, user-initiated, hash-addressed
  checkbox toggle `- [ ]`→`- [x]` + `✅ YYYY-MM-DD` in human prose. Stale/unknown
  hash → **409**, nothing written. Guarded like the ADR-020/023 mutations.
- **FR-164 — config kill-switch, health, SSE, Actions SPA tab.** A
  `dashboard.actions_enabled` pointer-default-ON kill-switch; `/health` flag; an
  `action.done` SSE event; and an **Actions** React tab (stat tiles + completion
  trend + filterable list with per-row complete buttons).

**ADR-034** records the narrow amendment to cardinal rule 2 that this mutation
represents. Everything else stays additive/managed-block.

## 2. Decisions (approved)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Post-complete refresh | **Automation re-renders on schedule.** The handler flips the source line (`vault.CompleteAction`), surgically marks the DB row done (`db.MarkActionDone` — so the dashboard list/counts refresh instantly), and emits `action.done`. `01-Projects/Actions.md` re-renders on the `actions-consolidate` automation's next run (its change-gate sees the new state). Matches the capture-endpoint precedent (thin browser-write handler; automation catches up); no dashboard→automations/core dependency. |
| 2 | Tab richness | **Tiles + trend + list.** Stat tiles (open/overdue/today/done-this-week) + a 30-day completion trend (Recharts `AreaChart`, computed from `done_date` — no new storage) + the filterable list with per-row complete buttons and source context. |

Folded in without a question (stated for the record):

- **Kill-switch defaults ON** (`ActionsEnabled *bool`, pointer-default-ON) —
  consistent with `ask`/`capture`/`related` and the roadmap. `capture` (also a
  browser vault-write) defaults ON, so this matches house style.
- **409 via a new exported sentinel** `vault.ErrActionNotFound` — no dashboard
  handler does `errors.Is`/409 today; this introduces the pattern (the only clean
  way to distinguish "stale/unknown hash" from a real 500).
- **`✅ date` = the server's local date** (`time.Now().Format("2006-01-02")`),
  matching the Obsidian Tasks convention (local, not the UTC block footer).
- **Server returns the full set; the SPA filters client-side** (personal-vault
  scale — one `useFetch`, filter in React, like the other tabs). No query params
  on `GET /api/actions` for v1.
- **SSE:** the genuinely-new event is `action.done` (a vault mutation the feed
  should show). Consolidation runs already surface via the engine's existing
  `automation.run`, so no separate `actions.consolidated` kind is added (the
  provisional name folds into `automation.run`).

## 3. The mutation — `vault.CompleteAction` + ADR-034 (FR-163)

New file `internal/vault/actions.go` (the vault package's only knowledge of the
task grammar; `vault` → `actions` is a clean one-way edge — `actions` is a pure
stdlib leaf, no cycle):

```go
// ErrActionNotFound is returned (nothing written) when no OPEN checkbox line in
// the note has the given identity hash — a stale/unknown hash. The dashboard
// maps it to 409.
var ErrActionNotFound = errors.New("no matching open action")

// CompleteAction toggles the single open checkbox line whose T1 identity hash
// equals lineHash: [ ]→[x] and appends " ✅ <date>". Byte-precise and atomic
// (temp+rename); human prose above/around the line is untouched. It is the ONE
// vault mutation that edits a human-authored line rather than a managed block
// (ADR-034) — user-initiated only, never model/agent-driven.
func (v *FS) CompleteAction(ctx context.Context, path, lineHash, date string) error {
	abs, err := v.safeAbs(path)          // security boundary — never skip
	if err != nil { return err }
	data, err := os.ReadFile(abs)
	if err != nil { return err }
	fm, body := splitFrontmatter(string(data))          // note.go — frontmatter-stripped body
	// Reuse T1's Extract so we match the EXACT line the index hashed (same
	// fenced-code / axon:actions-block skip rules, same body-relative LineNo).
	for _, a := range actions.Extract(path, body, false) {
		if a.State != actions.StateOpen || a.Hash() != lineHash {
			continue
		}
		lines := strings.Split(body, "\n")
		newLine, ok := actions.Complete(lines[a.LineNo], date)
		if !ok {
			return ErrActionNotFound
		}
		lines[a.LineNo] = newLine
		return v.writeRaw(path, reassemble(fm, strings.Join(lines, "\n")))
	}
	return ErrActionNotFound
}
```

New helper in `internal/actions` (grammar logic stays in one package):

```go
// Complete flips an open checkbox line's marker to 'x' and appends " ✅ <date>"
// (unless a ✅ is already present), preserving indentation, bullet char, and the
// rest of the line byte-for-byte. ok=false if the line is not an OPEN action.
func Complete(line, date string) (string, bool) {
	m := checkboxRe.FindStringSubmatch(line)
	if m == nil {
		return "", false
	}
	switch m[1] {
	case "x", "X", "-": // already done/cancelled
		return "", false
	}
	marker := "[" + m[1] + "]"
	idx := strings.Index(line, marker)
	if idx < 0 {
		return "", false
	}
	out := line[:idx] + "[x]" + line[idx+len(marker):]
	if !strings.Contains(out, "✅") {
		out = strings.TrimRight(out, " ") + " ✅ " + date
	}
	return out, true
}
```

**Why hash-addressed + open-only:** the hash is state-independent but
content-sensitive (T1), so once a line is completed (`✅ date` appended) its hash
changes — a re-POST of the old hash finds no open match → 409 (idempotent-safe).
Duplicate identical open lines share a hash; `CompleteAction` completes the first
still-open one (T1's documented first-match rule).

**ADR-034** (docs/02) records: this is the sole exception to cardinal rule 2's
"AXON only edits inside managed blocks." It is justified because it is (1)
byte-precise (one line, one marker char + a dated suffix), (2) exactly what the
user would do by hand in Obsidian, (3) user-initiated through the loopback
dashboard only — **pinned out of every agent/MCP path** (T4's `action_complete`
tool is excluded from the agentic allowlists), and (4) refuse-on-stale (a changed
line → 409, never a blind overwrite). No deletes; `[x]` and the source line are
fully reversible by hand.

## 4. Read endpoint — `GET /api/actions` (FR-162)

`internal/dashboard/actions.go`, cloning `handleRelated`'s guard order:

```go
func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ActionsEnabled || s.cfg.DB == nil {
		http.Error(w, "actions are disabled for this profile", http.StatusNotFound)  // 404
		return
	}
	if r.Header.Get("X-Axon-Actions") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)                             // 403
		return
	}
	rows, err := db.ListActions(r.Context(), s.cfg.DB, db.ListActionsOpts{IncludeAll: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, buildActionsPayload(rows, time.Now()))
}
```

`buildActionsPayload(rows, today)` (pure, unit-tested) returns:

```json
{
  "actions": [ { "...db.Action fields...", "bucket": "overdue" }, ... ],
  "counts":  { "open": 12, "overdue": 3, "today": 2, "waiting": 1, "someday": 4, "done7": 5 },
  "trend":   [ { "day": "2026-06-11", "done": 0 }, ... 30 entries ... ]
}
```

- **bucket** per row via a new pure `actions.BucketFields(state, due, scheduled, start string, tags []string, today) string` (T1's `Bucket` is refactored to delegate to it — additive, all existing callers unaffected — so the dashboard needs no `db.Action`→`actions.Action` mapper).
- **counts** computed in Go over `rows` (open excludes done/cancelled/archived; `done7` = `state==done && DoneDate ≥ today-7`).
- **trend** = completions per day for the last 30 days, from `DoneDate` (a Go group-by; no new storage). Drives the `AreaChart`.
- `ListActions` is called with `IncludeAll: true` (so done rows are available for the Done-this-week section and the trend); `buildActionsPayload` then **drops `archived` rows** (archived tasks never surface in the dashboard) and computes bucket/counts/trend over the rest. The SPA filters the remaining set client-side.

## 5. Write endpoint — `POST /api/actions/complete` (FR-163)

`internal/dashboard/actions.go`, cloning `handleReviewAction` + the new 409:

```go
func (s *Server) handleActionComplete(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.ActionsEnabled || s.cfg.Vault == nil {
		http.Error(w, "actions are disabled for this profile", http.StatusNotFound)   // 404 (kill-switch)
		return
	}
	if r.Header.Get("X-Axon-Actions") != "1" ||
		!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "forbidden", http.StatusForbidden)                              // 403
		return
	}
	var in struct {
		Path string `json:"path"`
		Hash string `json:"hash"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)                           // 400
		return
	}
	if in.Path == "" || in.Hash == "" {
		http.Error(w, "path and hash required", http.StatusBadRequest)
		return
	}
	date := time.Now().Format("2006-01-02")
	err := s.cfg.Vault.CompleteAction(r.Context(), in.Path, in.Hash, date)
	if errors.Is(err, vault.ErrActionNotFound) {
		http.Error(w, "action not found (already done or changed) — refresh", http.StatusConflict) // 409
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if s.cfg.DB != nil { // keep the derived index fresh so the list refreshes now
		_, _ = db.MarkActionDone(r.Context(), s.cfg.DB, in.Hash, date)
	}
	if s.cfg.Bus != nil {
		s.cfg.Bus.Publish(events.Event{
			Level: events.LevelInfo, Kind: "action.done",
			Message: "completed action in " + in.Path,
			Data:    map[string]any{"profile": s.cfg.Profile, "path": in.Path, "date": date},
		})
	}
	writeJSON(w, map[string]any{"ok": true, "path": in.Path, "date": date})
}
```

Registered: `mux.HandleFunc("GET /api/actions", s.handleActions)` and
`mux.HandleFunc("POST /api/actions/complete", s.handleActionComplete)` in
`Handler()` (both inside the `guardHost` wrapper — loopback + Host).

New DB helper `internal/db/actions.go`:

```go
// MarkActionDone flips one derived row to done in place (the vault is already
// updated; this keeps the disposable index in step until the next reindex, which
// reproduces the same row from the now-[x] source line — S9-consistent).
func MarkActionDone(ctx context.Context, q Execer, hash, doneDate string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE actions SET state='done', checkbox='x', done_date=? WHERE hash=? AND state='open';`,
		doneDate, hash)
	if err != nil {
		return 0, fmt.Errorf("mark action done: %w", err)
	}
	return res.RowsAffected()
}
```

## 6. Config, health, SSE, wiring (FR-164)

- **Config** (`internal/config/types.go`): add `ActionsEnabled *bool
  \`yaml:"actions_enabled,omitempty"\`` to `DashboardConfig` + a
  `func (d DashboardConfig) ActionsAllowed() bool { return d.ActionsEnabled == nil || *d.ActionsEnabled }`
  (pointer-default-ON). Unit test mirrors `dashboard_test.go`'s
  `TestAskAllowedDefaultsOn`.
- **Server config** (`internal/dashboard/server.go`): add `ActionsEnabled bool`
  to `dashboard.Config`.
- **Wiring** (`cmd/axon/start_cmd.go`): `ActionsEnabled:
  deps.profile.Dashboard.ActionsAllowed(),` in the `dashboard.Config{...}` literal
  (Vault + DB are already wired).
- **Health** (`internal/dashboard/health.go`): `out["actions_enabled"] =
  s.cfg.ActionsEnabled`.
- **SSE** (`web/src/App.jsx`): add `'action.done'` to `SSE_KINDS`.

## 7. SPA — `ActionsTab` (FR-164)

`web/src/App.jsx`:

- **TABS**: add `['actions', 'Actions']`; extend the nav filter with
  `(id !== 'actions' || health?.actions_enabled !== false)`; render
  `{tab === 'actions' && <ActionsTab span="span-12" />}`.
- **Fetch**: `const { data } = useFetch('/api/actions?n=' + nonce, 6000)` (the
  `X-Axon-Actions` header is required — `useFetch` is extended, or a dedicated
  `getActions()` fetch with the header like `getRelated`). `ReviewTab`'s
  `nonce`/`busy` refresh-after-mutate scaffolding is the template.
- **Tiles**: a `<Card title="Actions"><div className="tiles">` of `<Tile>`s from
  `data.counts` (Open accent, Overdue, Today, Done (7d)).
- **Trend**: a `<Card title="Completions"><ResponsiveContainer><AreaChart
  data={data.trend}>` (add `Area` is already imported; `type="monotone"`,
  gradient fill, `SEMA.ok` green) — mirrors `TokenTrend`.
- **List**: grouped by bucket in engage order; each row shows text + `[[source]]`
  + due/priority, and open rows get a **complete** button:
  ```js
  function postComplete(path, hash) {
    return fetch('/api/actions/complete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Axon-Actions': '1' },
      body: JSON.stringify({ path, hash }),
    }).then(async (r) => { if (!r.ok) throw new Error(await r.text()); return r.json() })
  }
  ```
  On success bump `nonce` (re-fetch); on a 409 show a small inline "changed —
  refreshing" note and re-fetch. Per-row `busy` by hash.
- **Hidden** when `health.actions_enabled === false`.
- **Build**: `npm run build` in `web/` (re-touches `dist/.gitkeep`); embedded via
  `//go:embed all:dist`.

## 8. Guardrails & invariants

- **Cardinal rule 1 (no Claude bypass):** N/A — T3 makes no model call. No ledger
  entry.
- **Cardinal rule 2 (wikilink-safe):** `CompleteAction` is the ONE amendment
  (ADR-034) — a byte-precise, hash-addressed, open-only, refuse-on-stale toggle of
  a single line, user-initiated via the loopback dashboard only, never
  agent/model-driven, never a delete. All other writes stay managed-block. No
  `vault.Move`/`Merge`/`fs` write introduced.
- **Trust boundary (ADR-023):** both endpoints behind `guardHost` (loopback +
  Host); the write requires the `X-Axon-Actions` header + `application/json`
  (CORS-preflight-forcing) + the `actions_enabled` kill-switch (404 when off). The
  SPA hides the tab when disabled.
- **S8 (all-off still useful):** `actions_enabled: false` removes both endpoints +
  the tab; the index, `axon actions`, and the consolidation automation are
  unaffected.
- **S9 (vault rebuilds DB):** `MarkActionDone` edits the *derived* table to what
  the next reindex would produce from the now-`[x]` source line — consistent, not
  authoritative. The vault is updated first (source of truth).
- **NFR-06 (atomic writes):** `CompleteAction` writes via `writeRaw` (temp+rename);
  a failure leaves the note intact.
- **NFR-07 (≤5s dashboard):** `action.done` streams over SSE; the list polls every
  6s and refreshes immediately after a completion.

## 9. Testing strategy

- **`actions.Complete` (T1 pkg), table-driven:** `[ ]`→`[x] … ✅ date`;
  `[/]`→`[x]` (unknown-open marker); preserves indent/bullet (`* [ ]`, `  - [ ]`);
  `[x]`/`[-]`→`ok=false`; idempotent (existing `✅` not doubled); byte-preserves
  trailing content.
- **`actions.BucketFields`:** the T1 `Bucket` precedence table, now against the
  field-level function (Bucket delegates — existing tests still pass).
- **`vault.CompleteAction` (`internal/vault`):** seed a note (with frontmatter) via
  `newTempVault`; compute the target line's hash the T1 way; complete → assert
  exact file bytes show `- [x] … ✅ <date>`, frontmatter + other lines
  byte-identical; unknown hash → `errors.Is(err, ErrActionNotFound)` and file
  unchanged; already-`[x]` line → `ErrActionNotFound`; a line inside a fenced code
  block with a colliding text is NOT touched (Extract skip parity).
- **`db.MarkActionDone`:** seed open rows, mark by hash → state/done_date updated,
  rows-affected 1; unknown hash → 0.
- **`buildActionsPayload`:** counts/buckets/trend correctness; archived excluded;
  done-7d window.
- **Dashboard endpoints** (`internal/dashboard/actions_api_test.go`, mirroring
  `related_api_test.go` + `review_api_test.go`): `GET` guards (disabled→404,
  header-less→403, ok→JSON with actions/counts/trend); `POST` guards
  (disabled→404, header/CT→403, empty body→400, **stale hash→409**, ok→200 + file
  flipped + `action.done` emitted + DB row done).
- **Config:** `ActionsAllowed()` nil→true / `*false`→false.
- **Live smoke** (real binary, isolated `AXON_HOME`, never `:7777`): seed tasks,
  reindex, `curl` `GET /api/actions` (header on/off/kill-switch), `curl` a
  completion → the source note shows `- [x] … ✅ today` and the row leaves the open
  list; stale hash → 409; build the SPA and eyeball the tab. `env -u FORCE_COLOR`.

## 10. Build order (for the implementation plan)

1. `actions.Complete` + `actions.BucketFields` (+ `Bucket` delegates) + tests.
   *(Pure T1 additions; nothing else compiles against them yet.)*
2. `vault.CompleteAction` + `vault.ErrActionNotFound` + tests.
3. `db.MarkActionDone` + test.
4. `GET /api/actions` (`handleActions` + `buildActionsPayload`) + registration +
   config `ActionsEnabled`/`ActionsAllowed()` + server field + `start_cmd` wiring +
   health flag + `actions_api_test.go` (read guards).
5. `POST /api/actions/complete` (`handleActionComplete`) + the 409 mapping +
   `action.done` emit + write-guard tests.
6. SPA `ActionsTab` (tiles + trend + list + complete) + `TABS`/filter/`SSE_KINDS`
   + `npm run build`.
7. ADR-034 (docs/02) + docs at build: `docs/03` FR-162/163/164; `docs/04`
   `dashboard.actions_enabled`; `docs/09` the two endpoints + Actions tab; `docs/16`
   T3 built + **release criterion met**; CLAUDE.md FR/ADR ranges; GUIDE `axon`
   surface.

## 11. Out of scope (this slice)

- Un-completing / toggling `[x]`→`[ ]`, editing task **text**, changing dates —
  completion is the only mutation (roadmap non-goal).
- Any agent/MCP completion path — T4 adds `action_complete` as an MCP tool
  **pinned out of the agentic allowlists**; T3 is dashboard-only.
- Server-side filter query params on `GET /api/actions` (client-side for v1).
- A distinct `actions.consolidated` SSE kind (folded into `automation.run`).
- Editing the consolidated note from the dashboard (it's a projection; complete
  at the source).
