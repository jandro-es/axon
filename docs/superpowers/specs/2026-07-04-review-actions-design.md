# Review-queue dashboard actions (+ FR-64 export) — design

**Date:** 2026-07-04
**Status:** approved (brainstormed + user-approved)
**Traces to:** FR-64 (implemented by this), new FR-94…FR-96 (docs/03), ADR-002 (vault source of truth), ADR-016…ADR-019 (the queue's producers), new ADR-020; renegotiates the dashboard read-only invariant (docs/09, FR-63 posture unchanged)

## Goal

Close the human-in-the-loop: every automation that proposes work — link
suggestions, inbox triage, capture records, resurfaced connections — feeds
`.axon/review-queue.md`, and today the only way to act is editing markdown.
The dashboard gains a Review tab with per-item Accept/Dismiss, where every
accept is applied through the same wikilink-safe ops as the rest of the
system. Plus FR-64 (chart data export), the last unbuilt v1 requirement.

## Background / constraints

- The dashboard server is documented "reads only; never calls Claude, never
  writes the vault" — the invariant that pushed capture away from a POST
  endpoint (ADR-016). This slice renegotiates it *narrowly*.
- The queue file has five writers with heterogeneous line formats (link
  suggestions per-note and pairwise, freeform triage, capture records,
  resurface proposals); items are `- [ ]` / `- [x]` checkbox lines under
  `## <kind> (<stamp>)` headers. Humans also check boxes in Obsidian.
- Cardinal rule 2: accepted links cannot be inserted into prose; managed
  blocks are the only safe write target. `vault.Patch` creates a block at
  the end of the body if absent. `vault.Move` is the only safe rename.
- `vault.FS` has no exported whole-file rewriter (only `Append` over
  unexported `writeRaw`); `List` excludes `.axon/` (system dir), but `Read`
  by explicit path works.
- The SPA is a single 703-line `App.jsx` with a TABS array, poll-based
  `useFetch`, an SSE hook with an explicit kinds list, vanilla CSS, no
  router, no JS tests, and **no POST anywhere**. `web/dist` is built by
  make/CI when npm exists; the Go binary falls back gracefully without it.
- Triage lines are freeform model text — displayable, not actionable.
- FR-64 (`docs/03`): "Export any chart's underlying data as CSV/JSON" — C,
  unbuilt; the chart datasets are clean JSON arrays already.

## Decisions (user-approved)

1. **Structured triage proposals + one-click move.** The classify prompt
   asks for JSON `{"folder": one of 01-Projects|02-Areas|03-Resources|
   04-Archive, "tags": [...]}`, validated at the chokepoint
   (`ValidateOutput` + `OutputSchema` — local models get JSON mode free);
   the queue line becomes `- [ ] triage [[note]] → <folder> (tags: a, b)`.
   Accept performs the wikilink-safe `Move` out of the inbox. Pre-upgrade
   freeform lines parse as `info` (dismiss-only).
2. **FR-64 included**: `GET /api/export` + per-card export links — closes
   the final v1 item.

## Design

### `internal/review` (new package)

Queue logic lives here — Go-testable, dashboard stays thin. Depends on
`vault` only (leaf-composing, like `search`).

- `type Item struct { ID, Kind, Section, Line string; Checked bool;
  Note, Target, Folder string; Tags []string }`
  - Kinds: `link` (`- [ ] link to [[X]]` under `## Link suggestions for
    [[note]] (...)` — Note from the header, Target from the line), `pair`
    (`- [ ] [[a]] ↔ [[b]]` — Note=a, Target=b), `triage` (structured form
    only — Note, Folder, Tags), `resurface` (`- [ ] resurface [[dormant]] —
    related to recent [[recent]] ...` — Note=recent, Target=dormant), `info`
    (everything else: capture records, freeform triage, unknown lines).
  - `ID` = short SHA-256 of `Section + "\x00" + Line` (stable across file
    rewrites; section headers carry timestamps so duplicates across sections
    hash apart; a duplicate line within one section acts on first match —
    accepted).
- `Load(ctx, v *vault.FS) ([]Item, error)` — parses the file (missing file →
  empty). Pending = `- [ ]`; `- [x]` items are returned `Checked: true` so
  the UI can show recent history, but only pending items are actionable.
- `Accept(ctx, v *vault.FS, id string) (Item, error)`:
  - `link`/`pair`/`resurface`: append `- [[Target]]` to the **`axon:links`
    managed block** of Note+".md" via `v.Patch` (read block, add line if not
    already present — idempotent), then mark the queue line
    `- [x] … ✓ applied <date>`.
  - `triage`: `v.Move(ctx, Note+".md", Folder+"/"+basename(Note)+".md")` —
    Note carries the full inbox path (e.g. `00-Inbox/idea`); destination
    collision or missing source returns the error unapplied.
  - `info`: error "not actionable".
- `Dismiss(ctx, v, id)` — any kind: mark `- [x] … ✗ dismissed <date>`.
- Line marking rewrites the whole file through the new
  **`vault.FS.RewriteSystemFile(rel, content string) error`**: atomic
  temp+rename (same guarantees as writeRaw), **refusing any rel not under
  `.axon/`** — the guard is code, not convention; general note rewriting
  remains impossible through this path.
- Concurrency: Load-then-rewrite races with an automation Append are
  possible but benign at personal scale (both go through atomic whole-file
  writes; a lost queue Append re-appears next tick via the producers'
  dedupe/memory). Accepting an already-checked line returns
  "already resolved".

### Triage upgrade (`internal/automations/model.go`)

Prompt asks for strict JSON; `AgentCall` gains
`OutputSchema: {"properties":{"folder":{"type":"string"},"tags":{"type":"array"}}}`
and `ValidateOutput` that unmarshals and checks `folder` against the allowed
set. Queue line: `fmt.Sprintf("- [ ] triage [[%s]] → %s (tags: %s)", stripExt(p),
out.Folder, strings.Join(out.Tags, ", "))`. Dry-run/degradation behavior
unchanged.

### Dashboard server (`internal/dashboard`)

- `Config` gains `Vault *vault.FS` (wired from `deps.vault` in
  `start_cmd.go`).
- Routes: `GET /api/review` → `{items: []Item, pending: int}`;
  `POST /api/review/action` body `{id, action: "accept"|"dismiss"}` →
  `{item: Item}` or `{error}` with 4xx.
- **Mutation guard (ADR-020):** POST handlers require
  `Content-Type: application/json` AND header `X-Axon-Review: 1`; both force
  a CORS preflight that fails (the server never sends CORS headers), so a
  malicious web page on the same machine cannot fire the action
  cross-origin. Host-guard and loopback bind unchanged (FR-63).
- Each action emits `review.accept` / `review.dismiss` on the bus (activity
  feed + events table); the SPA adds both to `SSE_KINDS`.
- The package doc's invariant is rewritten: "never calls Claude; never
  free-form writes — the only mutations are review-queue resolutions applied
  through the vault's wikilink-safe ops."

### FR-64 export

- `GET /api/export?dataset=tokens|runs|ingestion|vault|graph|activity&format=csv|json`
  — serializes the same rows the chart endpoints return (`encoding/csv`
  with flat per-dataset columns; `Content-Disposition: attachment;
  filename=axon-<dataset>-<date>.<ext>`). Unknown dataset/format → 400.
- SPA: a small ⤓ link on the Tokens, Automations (runs), Knowledge
  (ingestion), and Activity cards pointing at the endpoint (plain links —
  GET downloads need no fetch code).

### SPA (`web/src/App.jsx`, `web/src/styles.css`)

- `['review','Review']` added to TABS; nav badge with the pending count
  (from a lightweight poll of `/api/review`).
- `ReviewTab`: `useFetch('/api/review', 5000)`; items grouped by Kind with
  the section stamp; Accept/Dismiss buttons per pending item (`info` and
  checked items render without Accept); first mutation helper
  `postAction(id, action)` — `fetch POST` with the JSON body + `X-Axon-Review`
  header, then immediate refetch; row-level error text on failure (e.g.
  move collision). Recently resolved (checked) items render dimmed at the
  bottom. Existing `.card`/`.list`/`.li`/`.seg` classes; minimal new CSS.

### Testing

- `internal/review`: table-driven parser tests over a fixture file
  containing every producer's real format (copied verbatim from the five
  writers) + checked lines + unknown lines; Accept per kind against a temp
  vault (links block created/appended/idempotent; triage move + collision
  error; info not actionable; already-resolved); Dismiss; ID stability;
  marking rewrites only the target line.
- `vault`: `RewriteSystemFile` (atomic, `.axon/`-only guard rejects
  `01-Projects/x.md` and `../escape`).
- `automations`: triage JSON validation (good folder, bad folder rejected
  at chokepoint → retry/fallback), new line format.
- `dashboard`: httptest — GET /api/review shape; POST happy path (item
  applied, event emitted); POST without the custom header → 403; bad
  id/action → 4xx; export endpoint per dataset (csv header row + row count,
  json passthrough), unknown dataset → 400.
- Live smoke: scratch vault with seeded queue entries (run link-suggester or
  hand-seed) → `axon start` → curl GET/POST against the real server →
  verify the note gained the `axon:links` block, the triage note moved, the
  queue lines flipped, events appeared; open the dashboard and click through
  (manual); export a CSV via curl.

### Docs

- **ADR-020**: "Human-in-the-loop review actions on the dashboard" — the
  narrow invariant renegotiation and why it's safe (wikilink-safe ops only,
  `.axon/`-guarded rewriter, CSRF-by-preflight, loopback+Host-guard),
  the review package boundary, and the structured-triage upgrade.
- **FR-94…FR-96** in docs/03 (+ flip FR-64's row and the status banner):
  - FR-94 (M): review API + tab — parse the queue into typed items; accept/
    dismiss via localhost POST with the preflight-forcing guard; events.
  - FR-95 (M): wikilink-safe accepts — links/pairs/resurfaces into
    `axon:links` managed blocks; structured triage → `vault.Move`; the
    `.axon/`-only `RewriteSystemFile`; prose never touched.
  - FR-96 (S): FR-64 delivered — `/api/export` CSV/JSON + per-card links.
- docs/09 (dashboard component): Review tab, export, revised invariant.

## Trade-offs accepted

- The dashboard is no longer purely read-only — the renegotiation is
  narrow, structural, and ADR'd; the alternative (a second actions server)
  duplicates surface for no security gain on the same loopback.
- Queue-file rewrite races with producer appends can momentarily lose an
  append at pathological timing; producers' own dedupe/memory re-propose,
  and personal-scale frequency makes this acceptable.
- Old freeform triage lines are dismiss-only forever (no migration).

## Out of scope

- Bulk accept/dismiss, undo, editing proposals before accepting.
- Queue compaction/archival of resolved sections (future slice).
- Auth beyond loopback + Host-guard + preflight-forcing headers.
- JS test harness (logic concentrated in Go-tested `internal/review`).
