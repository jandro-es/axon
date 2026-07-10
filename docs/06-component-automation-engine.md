# 06 — Component: Automation Engine

**Owns:** FR-30…FR-35, NFR-12.
**Goal:** A portable, observable, budget-aware scheduler that runs the standard automations on new material — never burning tokens on a clock for its own sake.

## 1. Scheduler

- Cross-platform in-daemon scheduler (`gocron` or `robfig/cron/v3`). No OS cron dependency (ADR-008); `axon` can *emit* OS units optionally.
- Each automation declares: `enabled`, `schedule` (cron) and/or `triggers` (events), `model` (operation key or `none`), `budget_tokens`, `dry_run`, `catch_up` (`skip` | `run-once`).
- **Jitter** (small random delay) avoids thundering herds; **advisory locks** (FR-35) prevent overlap; a per-run **timeout** marks hung runs failed.
- Missed runs (daemon was down) follow `catch_up`.
- `policy.allowed_automations` gates everything: an automation not in the allow-list never schedules, regardless of `enabled`.

## 2. The runner contract

Every automation implements:

```go
type Automation interface {
    Name() string
    Essential() bool // budget-guard never pauses essential ones
    // DetectChange is cheap and makes no model call: decide if there is new
    // material worth processing. Returns whether to proceed, a reason, and an
    // opaque cursor persisted between runs.
    DetectChange(ctx context.Context, rc RunCtx) (Change, error) // {Changed, Reason, Cursor}
    // Run does the actual work. Must honour rc.DryRun and rc.Budget.
    Run(ctx context.Context, rc RunCtx) (RunResult, error)
}
```

`RunCtx` provides: profile, config, db repositories, vault (wikilink-safe ops), `Retrieve()` (token-bounded), the agent adapter (Claude Code via `claude -p`; the direct-API adapter only in `auth_mode: api_key`), the token manager, `DryRun`, and a structured logger.

**Standard run lifecycle** (enforced by the engine, not each automation):
1. Acquire lock; open a `runs` row (`status=running`).
2. `detectChange()`. If `!changed` → close run `status=skipped, skip_reason=reason`, **no model call**, emit event, release lock. (FR-31)
3. Budget pre-check via token manager; if the automation's `budget_tokens` or the profile window is exhausted → `status=skipped, skip_reason=budget`.
4. `run()` builds minimal context (retrieval, not vault dumps), pre-flights a token **estimate** via the token manager (exact `count_tokens` only in `api_key` mode), picks the model, does the work, and (unless `dryRun`) applies wikilink-safe writes.
5. Record tokens (and cost in `api_key` mode) to the ledger linked by `run_id`; write a diff summary into `runs.changes`; emit event; release lock.
6. On error: `status=failed`, capture message, never leave a half-edited note (atomic/staged writes).

## 3. The standard automations

Each is independently toggleable and configured per Component 04 §3.

### heartbeat (essential, cheap)
Periodic situational awareness. `detectChange`: always "changed" but does **zero model work** unless there is something to say. Gathers (from DB/vault, no model): inbox count, notes changed since last heartbeat, pending review-queue items, today's open tasks, budget status. If nothing noteworthy → writes/updates a compact status line on the dashboard + today's daily note, **no Claude call**. If noteworthy and `automations.heartbeat.model` is set (e.g. `classify`; unset by default) → one budget-checked, single-line synthesis through the chokepoint ("3 inbox items look project-related; budget 42% used"), degrading absolutely to the plain line on budget defer or any model error — the essential heartbeat never fails because of the optional call. Cheapest possible; the model is optional and off by default.

### daily-log (end of day)
Synthesises the day: reads today's daily note + the day's changed/created notes + completed tasks, produces a structured summary inside the daily note's `axon:summary` block, rolls unfinished tasks to tomorrow, and links the day to relevant projects/MOCs. Sonnet. Skips entirely on a day with no activity.

### inbox-triage
For each new Inbox item: classify (Haiku) into PARA + suggest tags + find related notes (hybrid search); then either (a) write a triage proposal to `.axon/review-queue.md` (default, human approves) or (b) if `auto_apply: true`, move (wikilink-safe) + tag + link directly. Detects auto-pasted URLs and hands them to the ingestion pipeline. Batches items to amortise tokens.

### compaction
Distillation pass (synthesis). Targets: stale session snapshots in `.axon/snapshots/`, oversized notes flagged by word-count threshold, and long daily notes older than N days. Produces durable summary notes (or fills `axon:summary` blocks), archives the raw material, and records an estimated **tokens-saved** figure (smaller future context). Weekly, off-peak. Never deletes; archives. (See Component 07 §compaction.)

### context-export (no model)
Produces a portable snapshot bundle in `.axon/exports/<timestamp>/`: a manifest (JSON), a curated set of "core context" Markdown (active projects, key MOCs, recent decisions), and a token-budget profile. Pure data assembly; zero Claude cost. Useful for backup, sharing, or seeding a fresh profile.

### knowledge-reindex (no model)
Walks the vault, recomputes note/chunk hashes, re-embeds changed chunks, rebuilds the link graph and FTS index, retries any `pending` embeddings. Keeps the derived DB consistent with the vault (ADR-006). Daily, cheap, no Claude.

### knowledge-digest (weekly)
Surfaces what was ingested this week and the connections it created: clusters new sources, proposes MOC additions and cross-links, writes a digest note. Synthesis model, gated on "any new sources this week".

### link-suggester
Vector-similarity sweep (no model needed for candidate generation; optional Haiku to phrase the rationale) proposing Zettelkasten links between notes that are semantically close but not yet linked. Writes ranked suggestions to `.axon/review-queue.md`. The core "AI surfaces connections you'd miss" feature, kept cheap by doing the heavy lifting in vector space.

### budget-guard (essential, no model)
Runs frequently; reads `budget_state`; when usage crosses `guard_pause_at_pct`, **pauses non-essential automations** for the rest of the window and emits a prominent dashboard event; resumes at window reset. Never blocks interactive use or essential automations silently — it surfaces, it doesn't hide.

### briefing (daily, ADR-018)
Writes the morning `axon:briefing` block into `Daily/<date>.md`, at most once per day: deterministic facts (notes changed, new sources, automation activity, review-queue pending, budget) always, plus a 2–4 sentence narrative from **one one-shot routine-tier call** (local-routable per ADR-015; degrades to facts-only on budget defer). SessionStart injects a one-line pointer when today's block exists (FR-89).

### resurfacer (weekly, zero-model base — ADR-018; scheduling R9)
Proposes review-queue connections between recently-touched notes (≤7 days) and dormant ones (≥90 days) by mean-chunk-vector cosine (≥0.75; primitives shared with the dashboard graph). **R9 (FR-151/152/153)** replaces "propose once, silence forever" with a light FSRS-flavoured schedule: per-pair `{rung, due, last}` in `automation_state` (`resurfacer:schedule`; interval ladder `resurfacing.intervals_weeks`, default `[1,2,4,8,16]`), fed by the pair's own review-queue+archive outcomes (`review.Outcomes` — dismiss advances +1 rung, accept +2), so a declined item returns later, not next week. Base path stays zero-model. **Opt-in** (`budget_tokens > 0`): the top-N most-similar pairs get one routine-tier contradiction check each, reclassifying genuine clashes into a `contradicts` review kind (Accept links the pair). The spaced-serendipity half of the proactive layer.

### research-questions (weekly, 1.1 A3 — off by default)
Answers user-authored standing questions from `03-Resources/Research Questions.md` (top-level `?` items in the human region) — one grounded `ask` per question through the chokepoint — into an `axon:answers` block with `[[wikilink]]` citations and a confidence marker. Change-gated on the question hash ∨ new sources this week; unanswered questions persist.

### entity-pages (weekly, classify-tier, 1.1 C2 — off by default)
Extracts named people/projects from notes updated in the lookback window (one structured classify call each, local-routable) and maintains `Entities/People|Projects/` index pages, appending `- [[note]] (date)` lines to an `axon:mentions` block once an entity clears the ≥2-note threshold. Pending mentions live in `automation_state`; human prose is never touched.

### project-pulse (weekly, 1.1 C3 — off by default)
Reads `01-Projects/` + the USER goals and writes an `axon:pulse` block in `01-Projects/Project Pulse.md`: deterministic per-project facts (last-touched, active/stale, linked goal) plus one budget-degrading routine-tier narrative (progress/stalls/next). Nudges projects untouched ≥3 weeks to the review queue once (proposal memory). Composes the briefing + resurfacer patterns.

### merge-proposals (weekly, zero-model — 1.2 R7, off by default)
Sweeps note mean-vectors for **near-duplicates**: all-pairs `db.Cosine` over `db.NoteMeanVectors`, proposing any pair with `sim ≥ merge.threshold` (default 0.92) as a `merge [[a]] + [[b]] (sim …)` line in the review queue (deduped against pending items + dismissed-pair proposal memory, capped at `merge.max_proposals`). **No model call** — the cosine is the rationale. Accepting a proposal runs the wikilink-safe `vault.Merge` (ADR-032): the more inbound-linked note survives, keeps its prose and gains the loser's body in an `axon:merged` block, all inbound links retarget to the survivor, and the loser is archived intact to `.trash/merged/` — **never deleted** (zero broken links, both originals recoverable). No MCP tool; user-approved via the review queue only.

### actions-consolidate (daily, zero-model — 1.2.5 T2, enabled by default)
Renders the derived action index (T1) into the `axon:actions` managed block of `01-Projects/Actions.md` in GTD engage order: **🔴 Overdue · 📅 Today · ⏳ This week · ▶ Next actions** (grouped by project then context) **· 🕓 Waiting for · 💭 Someday/Maybe · ✅ Done this week**. Every line is a plain `- text — [[source]] · 📅 due` **reference, never a checkbox** — the source note stays the one place a task lives (constitution §3). **No model call.** Change-gated on the rendered projection hash, so a day with no visible change (same tasks, same buckets) writes nothing; the write is a wikilink-safe `Patch` (human preamble above the block is never touched). Enabled by default (like `knowledge-reindex`/`context-export` — zero-token utility). The heartbeat's `tasks: N open (M overdue)` counter reads the same index (FR-161).

> This is an illustrative tour, not the full catalog — the authoritative, always-current list (including `capture`, `subscriptions`, `session-distill`, `memory-distill`) is the `purposes` map surfaced by `axon automations`.

## 4. Agent adapter — Claude Code (`claude -p`) is the default path

Automations reach Claude by shelling out to Claude Code, authenticated by the profile's subscription/enterprise login:
```
CLAUDE_CONFIG_DIR=<profile>  CLAUDE_CODE_OAUTH_TOKEN=<from `claude setup-token`> \
  claude -p "<task prompt>" --agent <subagent> --model <preferred> --output-format json
```
It parses the JSON result for the final output and usage, feeds usage into the ledger, and applies any vault changes through AXON's wikilink-safe ops (the subagent is instructed to *propose* changes that AXON applies, or to use the AXON MCP tools which are already safe). On subscription/enterprise this draws on the plan's Agent SDK credit rather than per-token billing. The optional `auth_mode: api_key` install can instead use the in-process direct-API adapter for small single-shot tasks; the choice is per-automation config (`runner: claude_code | inprocess`), with `claude_code` the default and the only path available without API access.

**Agentic mode (ADR-017, built):** an automation may declare AXON MCP tools + a turn cap in code (v1: knowledge-digest — `knowledge_search`/`vault_read`/`vault_links`, 8 turns; compaction — `vault_read`/`vault_links`, 4 turns/note). Config `automations.<name>.agentic: false` opts back into the one-shot path, which also remains the automatic degradation path when a run is budget-killed or deferred. **`budget_tokens` is enforced at runtime** (previously display-only): the pre-flight input cap for one-shot calls, the per-run total cap for agentic runs — enforced live by the adapter's streaming kill-switch, with real accumulated usage ledgered on every path. Dry-run stays Authorize-only for one-shot and read-only agentic calls; for a **write-capable** agentic run it spawns the agent with server-enforced report-only write tools (real preview, real token cost — see docs/08 and ADR-022). **Agentic writes (ADR-022):** compaction's agentic path now writes the distilled summary into the note's `axon:summary` block via the `vault_patch` tool (`vault_read`/`vault_links`/`vault_patch`, 4 turns/note); the original is still archived first (FR-44) and Go verifies the block, falling back to a deterministic write if the agent skipped the tool. The `agentic: false` one-shot + deterministic write is unchanged.

> No `ANTHROPIC_API_KEY` in subscription/enterprise mode — it would divert Claude Code onto API billing. Verify current `claude -p` flags/JSON shape against the Claude Code docs at build time; isolate them in this adapter so a CLI change is a one-file fix.

## 5. Extensibility (NFR-12)
A new automation = a module in `automations/` implementing `Automation` + a config block. The engine discovers it; no core wiring edits. Same for a new ingestion source (a `Fetcher`).

## 6. Acceptance checks
- With nothing changed, each automation logs a skip and makes no Claude call (FR-31; success criterion S3).
- `axon run daily-log --dry-run` prints intended edits + token estimate, writes nothing (FR-33/FR-34).
- Crossing the budget threshold pauses non-essential automations and shows it on the dashboard (FR-43).
- A failed run leaves no half-edited note (NFR-06).
