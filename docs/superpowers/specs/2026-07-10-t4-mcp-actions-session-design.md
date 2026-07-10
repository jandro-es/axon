# T4 — MCP action tools + SessionStart pointer — design

**Slice:** T4 (roadmap `docs/16-roadmap-1.2.5.md`) · **Date:** 2026-07-10
**FR:** FR-165, FR-166 · **ADR:** none (composition of ADR-017 MCP allowlists +
ADR-034 completion + the existing SessionStart injection)
**Status:** design approved; ready for implementation plan.

> Current maxima before this slice: FR-164, ADR-034. After: FR-166, ADR-034.

## 1. Summary

Actions where Claude and the session start already are — no new architecture:

- **FR-165 — two MCP tools.** `actions_list` (read-only, zero-spend, in the
  default set **and** the agentic **read** allowlist — the `vault_related`
  precedent) returns open actions + counts from the T1 index. `action_complete`
  (a vault mutation via T3's `vault.CompleteAction`, in the default set but
  **pinned out of both agentic allowlists** — the `vault_ask` precedent) lets an
  **interactive** session mark a task done.
- **FR-166 — SessionStart pointer.** One deterministic line — `- Actions: N open
  (M due today, K overdue) → [[01-Projects/Actions.md]]` — injected when open
  actions exist, from the `actions` table, **no model call**, mirroring the FR-89
  briefing pointer.

No new ADR: the tools compose ADR-017's dual allowlisting with ADR-034's
completion primitive; the pointer is another deterministic status line.

## 2. Decisions (approved)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Completion via MCP | **Both tools.** Ship `actions_list` (agentic-allowed) and `action_complete` (agentic-excluded) in the default interactive set. `action_complete` is user-in-the-loop and hash-addressed (completes only a specific known line) — no riskier than the existing interactive `vault_patch`/`vault_write`, and its exclusion from the agentic allowlists preserves ADR-034's "never headless-agent-driven" guarantee. |
| 2 | Pointer gating | **Operational status (ungated).** The pointer is treated like the inbox / review-queue / briefing pointers — always shown when open actions exist, one line, **not** gated by `memory.inject`. Task counts are operational, not personal identity, so they surface even on the stricter work profile (which already shows the sibling pointers). Matches the FR-89 precedent. |

Folded in without a question:

- `actions_list` input: optional `status` (a bucket or `open`, default `open`),
  `project`, `limit`; output rows carry `hash` so an interactive session can feed
  a row straight to `action_complete`.
- `action_complete` mirrors the dashboard: `vault.CompleteAction` +
  `db.MarkActionDone`; `DryRun`-aware (report-only) like `Write`/`Patch`;
  `vault.ErrActionNotFound` surfaced as the tool error on a stale hash.
- Count-assertion bumps: `filter_test` 16→18; `server_test` want-list +2.

## 3. `actions_list` MCP tool (FR-165)

`internal/mcp/tools.go` — I/O + handler (the `Related` model):

```go
type ActionsListIn struct {
	Status  string `json:"status,omitempty"  jsonschema:"filter: a bucket (overdue|today|scheduled|next|waiting|someday) or 'open' (default) — done/cancelled are excluded"`
	Project string `json:"project,omitempty" jsonschema:"filter by project (wikilink target or source-path substring)"`
	Limit   int    `json:"limit,omitempty"   jsonschema:"max rows (default 50)"`
}

type ActionView struct {
	Text    string `json:"text"`
	Source  string `json:"source"`             // vault-relative note
	Section string `json:"section,omitempty"`
	Bucket  string `json:"bucket"`             // read-time GTD bucket
	Due     string `json:"due,omitempty"`
	Priority string `json:"priority,omitempty"`
	Hash    string `json:"hash"`               // pass to action_complete
}

type ActionsListOut struct {
	Actions []ActionView   `json:"actions"`
	Counts  map[string]int `json:"counts"` // open/overdue/today/waiting/someday/done7
}

func (t *Tools) ActionsList(ctx context.Context, in ActionsListIn) (ActionsListOut, error)
```

Flow (zero model calls, no ledger): `db.ListActions(ctx, t.deps.DB,
db.ListActionsOpts{IncludeAll: true})`; for each non-archived row compute
`actions.BucketFields(...)`; tally counts (open/overdue/today/waiting/someday +
done7 from `DoneDate ≥ today-7`); build `ActionView`s for the requested set
(default: open buckets only; `status`/`project` filter; `limit` default 50,
sorted by bucket engage-order then `Due`). Registered in `toolRegistry()` and
added to `agenticReadTools` (read-only, zero-spend — the `vault_related`
precedent). The count/bucket/filter logic reuses the same helpers the dashboard's
`buildActionsPayload` uses.

## 4. `action_complete` MCP tool (FR-165)

`internal/mcp/tools.go` — the write model (`Write`/`Patch` DryRun pattern):

```go
type ActionCompleteIn struct {
	Path string `json:"path" jsonschema:"vault-relative note containing the checkbox line"`
	Hash string `json:"hash" jsonschema:"the action's identity hash (from actions_list)"`
}
type ActionCompleteOut struct {
	Applied bool   `json:"applied"`
	Message string `json:"message"`
}

func (t *Tools) ActionComplete(ctx context.Context, in ActionCompleteIn) (ActionCompleteOut, error) {
	if in.Path == "" || in.Hash == "" {
		return ActionCompleteOut{}, fmt.Errorf("path and hash are required")
	}
	date := time.Now().Format("2006-01-02")
	if t.deps.DryRun {
		return ActionCompleteOut{Applied: false, Message: "would complete action " + in.Hash + " in " + in.Path}, nil
	}
	if err := t.deps.Vault.CompleteAction(ctx, in.Path, in.Hash, date); err != nil {
		return ActionCompleteOut{}, err // ErrActionNotFound → surfaced to the caller (stale/unknown hash)
	}
	if t.deps.DB != nil {
		_, _ = db.MarkActionDone(ctx, t.deps.DB, in.Hash, date)
	}
	return ActionCompleteOut{Applied: true, Message: "completed action in " + in.Path + " (✅ " + date + ")"}, nil
}
```

Registered in `toolRegistry()` (default set) but added to **neither**
`agenticReadTools` **nor** `agenticWriteTools`, so `validateAgenticTools` rejects
any automation that names it and `NewServer`'s filter never registers it in an
agentic subprocess — identical containment to `vault_ask` (ADR-034: completion is
user-initiated, never headless-agent-driven). The tool description states it edits
a human checkbox line and is interactive-only.

## 5. SessionStart pointer (FR-166)

`internal/hooks/hooks.go` — a helper mirroring `briefingPointer`, but reading the
`actions` table (the handler already has `deps.DB`):

```go
// openActionsPointer returns a one-line pointer to the consolidated action list
// when open actions exist, else "" (best-effort — any error yields no line, never
// a broken hook). Operational status, like the briefing pointer; no model call.
func openActionsPointer(ctx context.Context, d *sql.DB) string {
	if d == nil {
		return ""
	}
	rows, err := db.ListActions(ctx, d, db.ListActionsOpts{State: "open"})
	if err != nil || len(rows) == 0 {
		return ""
	}
	today := time.Now()
	open, todayN, overdue := 0, 0, 0
	for _, r := range rows {
		open++
		switch actions.BucketFields(r.State, r.Due, r.Scheduled, r.Start, r.Tags, today) {
		case "today":
			todayN++
		case "overdue":
			overdue++
		}
	}
	extra := ""
	if todayN > 0 || overdue > 0 {
		extra = fmt.Sprintf(" (%d due today, %d overdue)", todayN, overdue)
	}
	return fmt.Sprintf("- Actions: %d open%s → [[01-Projects/Actions.md]]\n", open, extra)
}
```

Slotted in `sessionStart` immediately after the briefing pointer (inside the
`if deps.Vault != nil` block), **ungated by `memory.inject`** (decision 2). No
change to the identity block or its token ceiling — this is a single status line,
like inbox/review/briefing.

## 6. Guardrails & invariants

- **Cardinal rule 1 (no Claude bypass):** `actions_list` and the pointer make no
  model call (pure DB reads); `action_complete` makes no model call (a vault
  write). No ledger entries.
- **Cardinal rule 2 (wikilink-safe):** `action_complete` routes through T3's
  `vault.CompleteAction` (ADR-034 — byte-precise, hash-addressed, atomic, no
  delete). No other write.
- **ADR-017 dual allowlist:** `actions_list` in `agenticReadTools`;
  `action_complete` in neither map → structurally excluded from every agentic
  subprocess (validate-time reject + registration-time filter).
- **ADR-034 (completion is user-initiated):** the MCP write path is
  interactive-only; the agentic exclusion preserves "never headless-agent-driven."
- **S8 (all-off still useful):** both tools are read/optional; the SessionStart
  pointer only appears when open actions exist and degrades to nothing on any
  error. Disabling the MCP server or the hook removes them cleanly.
- **NFR-05 (content is data):** task text flows through the tools as data; no
  model interprets it as instructions (no model call at all).
- **S9:** `action_complete`'s `MarkActionDone` edits the derived index to what the
  next reindex reproduces from the now-`[x]` line; the vault is written first.

## 7. Testing strategy

- **`actions_list` (`internal/mcp`):** `TestActionsListTool` mirroring
  `TestRelatedTool` — `newTestTools`, seed `db.ReplaceActions`, call
  `tools.ActionsList(...)`; assert bucketed rows + counts, `status`/`project`
  filter, `limit`, and that each row carries a non-empty `hash`.
- **`action_complete` (`internal/mcp`):** seed a note (open checkbox) + its indexed
  row; call `tools.ActionComplete` → `Applied:true`, source line flipped, DB row
  done; stale hash → error (`errors.Is(err, vault.ErrActionNotFound)`); `DryRun:
  true` → `Applied:false`, file untouched. (The underlying flip is already covered
  by `internal/vault/actions_test.go`.)
- **Allowlist:** a test asserting `agenticReadTools["actions_list"]` is true and
  `agenticReadTools["action_complete"]` / `agenticWriteTools["action_complete"]`
  are both false (structural exclusion).
- **Count assertions:** `filter_test` 16→18; `server_test` want-list +2 (sorted:
  `action_complete`, `actions_list` lead the list).
- **SessionStart pointer (`internal/hooks`):** mirror
  `TestSessionStartBriefingPointer` — no open actions → no pointer; seed open +
  due-today + overdue rows → the `- Actions: N open (M due today, K overdue) →
  [[01-Projects/Actions.md]]` line; assert `fake.CallCount() == 0` (no model call);
  a case with `inject:false` still shows the pointer (ungated, decision 2).
- **Live smoke:** `axon mcp --tools actions_list,action_complete` handshake lists
  both; `axon hook session-start` output contains the actions pointer for a seeded
  vault. (MCP tool calls covered by unit tests; `env -u FORCE_COLOR`.)

## 8. Build order (for the implementation plan)

1. `actions_list`: I/O structs + `ActionsList` handler + `toolRegistry()` entry +
   `agenticReadTools` += `actions_list` + `TestActionsListTool`.
2. `action_complete`: I/O structs + `ActionComplete` handler + `toolRegistry()`
   entry (neither agentic map) + tests (apply/stale/dry-run + allowlist exclusion).
3. Count assertions: `filter_test` 16→18; `server_test` want-list +2.
4. SessionStart `openActionsPointer` + wiring in `sessionStart` + hooks tests.
5. Docs at build: `docs/03` FR-165/166; `docs/08` MCP tool table (both tools +
   agentic dispositions); `docs/12` SessionStart pointer; `docs/16` T4 built;
   CLAUDE.md FR range → FR-166; GUIDE tool rows.

## 9. Out of scope (this slice)

- Agent-driven completion (structurally excluded — the whole point).
- Un-complete / edit-text tools (completion is the only mutation).
- A `vault_merge`-style tool or any new mutation primitive.
- Model-generated action extraction (that's T6).
- Changing the identity block or its token ceiling.
