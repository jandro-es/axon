# T5 — Stale-action sweep & weekly review — design

**Slice:** T5 (roadmap `docs/16-roadmap-1.2.5.md`) · **Date:** 2026-07-10
**FR:** FR-167, FR-168 · **ADR:** none (the `#someday` tag-edit rides ADR-034's
"user-initiated additive checkbox-line edit" principle; the sweep composes the
`merge-proposals` pattern + the ADR-020 review queue)
**Status:** design approved; ready for implementation plan.

> Current maxima before this slice: FR-166, ADR-034. After: FR-168, ADR-034.

## 1. Summary

The reflect step that keeps the action system trusted — GTD's weekly review, as a
zero-model nudge:

- **FR-167 — the sweep.** A new **zero-model** `actions-review` automation
  (weekly, **off by default**) proposes stale open actions to the review queue as
  a new `stalled` kind: `- [ ] stalled action "…" in [[note]] (Nd) — still
  relevant?`. "Stale" = an open, **undated** action whose **source note hasn't
  been updated in > `actions.stale_after_days`** (default 30). Deduped by proposal
  memory (propose once; a dismissal silences it — the `merge-proposals` model).
- **FR-168 — the disposition.** Accepting a `stalled` item **demotes the task to
  Someday/Maybe**: it tags the source checkbox line `#someday` via a new additive
  `vault.TagAction` (never completes, never deletes). The action then moves to the
  Someday bucket everywhere (T2 note, dashboard, `axon actions`).

No new ADR: the sweep is another `merge-proposals`-style zero-model proposer; the
tag-edit is the same class of user-initiated additive checkbox-line edit ADR-034
established for completion (documented as an ADR-034 extension).

## 2. Decisions (approved)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Stale signal | **Source note last-updated.** An open, undated action is stale when its source note's `updated` predates `today − stale_after_days` (via the existing `db.NotesUpdatedBefore`). The same signal `project-pulse` uses; `(Nd)` = days since the note's last update. Caveat accepted: touching a note resets its tasks' age. |
| 2 | Accept disposition | **Someday-only.** Accept tags the source line `#someday` (one clean GTD demotion); dismiss silences (proposal memory). No auto-complete/reschedule variants in v1 — added later if the queue shows demand. |

Folded in without a question:

- **Addressing:** the `stalled` line carries the action text + `[[note]]`;
  `Accept` re-scans the note and tags the **first open action whose `Text`
  matches** (via `vault.TagAction`), idempotently (skips if `#someday` already
  present). No hash in the human line.
- **Propose-once:** proposal memory keyed by the action hash; a dismissal is never
  re-proposed (the `merge-proposals` model, not a spaced ladder).
- **Cap + skip-pending:** capped at a Go const (`staleMaxProposals = 10`/run);
  actions already pending in the queue are skipped (the `merge`/resurfacer rule).
- **Config:** one new key — `actions.stale_after_days` (default 30) — in a new
  top-level `actions:` block.

## 3. The tag-edit — `vault.TagAction` (FR-168)

`internal/vault/actions.go` (beside `CompleteAction`):

```go
// TagAction appends " #<tag>" to the FIRST open checkbox line in the note whose
// T1 text matches actionText, if the tag isn't already present. Byte-precise +
// atomic; additive (never removes/reorders); returns ErrActionNotFound (nothing
// written) if no open line matches. Like CompleteAction (ADR-034) it edits a
// human-authored checkbox line, user-initiated via the review queue only — never
// model/agent-driven. `tag` is the bare word (e.g. "someday").
func (v *FS) TagAction(ctx context.Context, path, actionText, tag string) error {
	abs, err := v.safeAbs(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	fm, body := splitFrontmatter(string(data))
	lines := strings.Split(body, "\n")
	for _, a := range actions.Extract(path, body, false) {
		if a.State != actions.StateOpen || a.Text != actionText {
			continue
		}
		line := lines[a.LineNo]
		if strings.Contains(line, "#"+tag) {
			return nil // already tagged — idempotent no-op, nothing to write
		}
		lines[a.LineNo] = strings.TrimRight(line, " ") + " #" + tag
		return v.writeRaw(path, reassemble(fm, strings.Join(lines, "\n")))
	}
	return ErrActionNotFound
}
```

Reuses T1 `actions.Extract` (same skip rules / body-relative line index) and the
T3 atomic-write primitives. **ADR-034 extension** (documented in docs/02): the
"byte-precise, user-initiated, never-agentic checkbox-line edit" now covers both
*completion* (`CompleteAction`) and *demotion* (`TagAction`); both are additive,
reversible by hand, and reachable only through the loopback dashboard / review
queue, never an agent/MCP path. `#someday` demotion is text-addressed (the review
line carries the text); a changed line → `ErrActionNotFound` → the Accept reports
"no longer present," never a wrong edit.

## 4. The review kind — `stalled` (FR-167/168)

`internal/review/review.go`:

- **Parse:** new regex
  `stalledRe = ^stalled action "(.+)" in \[\[([^\]]+)\]\] \(\d+d\)`; in `Load`'s
  switch, `it.Kind, it.Target, it.Note = "stalled", <text>, <note>` — `Target`
  holds the action text, `Note` the source note path (mirroring how `merge` stashes
  two strings in `Note`/`Target`).
- **Accept:** a new `case "stalled"` in the `Accept` switch:
  ```go
  case "stalled":
      if err := v.TagAction(ctx, it.Note+".md", it.Target, "someday"); err != nil {
          return Item{}, err
      }
      suffix = "✓ demoted to #someday"
  ```
  Dismiss is unchanged (the generic `mark` path). `stalled` is **not** an Outcome
  kind (accept is terminal; re-nagging is prevented by proposal memory, not a
  ladder).

## 5. The automation — `actions-review` (FR-167)

`internal/automations/actionsreview.go`, cloning `MergeProposals` (dedup.go):

```go
type ActionsReview struct{}
func (ActionsReview) Name() string    { return "actions-review" }
func (ActionsReview) Essential() bool  { return false }

const (
	actionsReviewState    = "actions-review/proposed"
	staleMaxProposals     = 10
)
```

**`Run`** (zero model calls):

1. `cutoff := rc.now().AddDate(0, 0, -staleAfterDays).Format("2006-01-02")` where
   `staleAfterDays = rc.Config.Actions.StaleAfterDaysOr()`.
2. `stale, _ := db.NotesUpdatedBefore(ctx, rc.DB, cutoff, 5000)` → a
   `path → updated` map of notes not touched since the cutoff.
3. `open, _ := db.ListActions(ctx, rc.DB, db.ListActionsOpts{State: "open"})`.
4. For each open action with **`Due == ""`** (undated) whose `SourcePath` is in
   the stale map and passes `scannableNote`: compute `age := daysBetween(updated,
   today)`; skip if its hash is in `loadProposalMemory(actionsReviewState)` or if
   an equivalent line is already pending (`review.Load`); else queue
   `- [ ] stalled action "<text>" in [[<stripExt(source)>]] (<age>d) — still
   relevant?` and mark the hash proposed. Cap at `staleMaxProposals`
   (oldest-first).
5. If any queued: `rc.Vault.Append(".axon/review-queue.md", header + …)` +
   `saveProposalMemory(actionsReviewState, proposed)`. DryRun returns the intended
   lines, writes nothing.

**`DetectChange`:** change-gate on `weekStart` + a hash of the candidate set
(stale-note paths + open-action hashes), so an unchanged week with nothing new
skips (FR-31). Off by default → `Schedulables` never runs it unless enabled.

**Config:** a new top-level `actions:` block:

```go
type ActionsConfig struct {
	StaleAfterDays int `yaml:"stale_after_days" validate:"omitempty,min=1"`
}
func (a ActionsConfig) StaleAfterDaysOr() int {
	if a.StaleAfterDays > 0 { return a.StaleAfterDays }
	return 30
}
```

added as `Actions ActionsConfig \`yaml:"actions"\`` on `Profile`. Seeds
(`starter.go` + `axon.config.example.yaml`): `actions-review: { enabled: false,
schedule: "0 8 * * 6", model: none, budget_tokens: 0 }` (Saturday 08:00) and an
`actions: { stale_after_days: 30 }` block. Registry += `ActionsReview`; catalog
`purposes` += an entry; advisory `doctor` `actions-review` check (config-only,
`StatusOK`, off/active + threshold).

## 6. Guardrails & invariants

- **Cardinal rule 1:** zero model calls (`model: none`, no `runModel`); no ledger.
- **Cardinal rule 2:** the only write is `rc.Vault.Append` to
  `.axon/review-queue.md` (a system file) during the sweep, and — on Accept —
  `vault.TagAction` (the ADR-034-class additive checkbox-line edit). No delete, no
  managed-block clobber, no `Move`/`Merge`.
- **User-approved only:** the sweep only *proposes*; the `#someday` edit happens
  solely on a human Accept through the review queue. No agent/MCP path tags actions.
- **S8:** off by default — a fresh install never runs it; enabling adds only
  review-queue nudges.
- **S9:** reads the derived index + notes table; writes only the review queue and
  (on accept) the source line. The `actions` row's move to the Someday bucket is
  reproduced by the next reindex from the now-`#someday` line.
- **FR-31:** change-gated; a week with no new stale actions is a skip.

## 7. Testing strategy

- **`vault.TagAction` (`internal/vault`):** seed a note with an open task; tag it
  → line gains ` #someday`, other lines byte-identical, frontmatter preserved;
  second call idempotent (no double tag, no error); already-`[x]`/absent text →
  `ErrActionNotFound`, file unchanged.
- **`stalled` review kind (`internal/review`):** a queued `stalled` line parses to
  `Kind:"stalled"` with the text/note; `Accept` tags the source line `#someday`
  and marks `✓ demoted to #someday`; dismiss marks dismissed without editing the
  source.
- **`ActionsReview` (`internal/automations`):** real vault + DB; seed a stale note
  (old `updated`) with an open undated action + a fresh note with one → only the
  stale one is proposed; a dated action is never proposed; re-run is proposal-memory
  silenced; a pending queue line is skipped; DryRun writes nothing; cap honored.
- **Config:** `StaleAfterDaysOr()` default 30 / override; example config validates.
- **Registry/catalog:** want-list 21→22; catalog purpose present; mcp
  automations-count 21→22.
- **Live smoke:** seed a vault with an old-note undated task, `axon reindex`,
  `axon run actions-review` → the `stalled` line appears in `.axon/review-queue.md`;
  accept it via `POST /api/review/action` (or the CLI) → the source line gains
  `#someday` and `axon actions --status someday` shows it. `env -u FORCE_COLOR`.

## 8. Build order (for the implementation plan)

1. `vault.TagAction` + tests.
2. `stalled` review kind: regex + `Load` case + `Accept` case + tests.
3. Config `ActionsConfig` + `StaleAfterDaysOr` + `Profile.Actions` + example/starter
   seeds + config test.
4. `ActionsReview` automation + registry + `registry_test` (21→22) + catalog
   purpose + `mcp/tools_more_test` (21→22) + automation tests.
5. `doctor` `actions-review` advisory check + test.
6. Docs: `docs/02` ADR-034 extension note (TagAction); `docs/03` FR-167/168;
   `docs/04` `actions:` block; `docs/06` automation entry; `docs/16` T5 built;
   CLAUDE.md FR range → FR-168; README automation count 21→22.

## 9. Out of scope (this slice)

- Complete / reschedule accept dispositions (someday-only v1).
- A spaced re-ask ladder (propose-once; that's R9's resurfacer job).
- Sweeping dated/overdue actions (only open **undated** stale ones).
- Per-action created dates or new grammar (rides note `updated`).
- Any agent/MCP tagging path (review-queue accept only).
