# C3 — Project Pulse (design)

*Roadmap `docs/14-roadmap-1.1.md` §C3. Size S. FR-131/132/133. No ADR — pure
composition of shipped patterns (Briefing + Resurfacer), like A3/C1/C2.*

## Goal

A weekly, disabled-by-default automation that gives the vault owner a standing
read on their **projects**: what moved, what stalled, what's next — tied to the
**goals** they've stated in `USER.md`. It writes a narrative *pulse* into a
managed block and nudges genuinely dormant projects into the review queue. It
must degrade to facts-only under budget pressure and never touch human prose.

Non-goals: no new note type; no per-project mutation of the project notes
themselves; no destructive ops; no cloud/egress. All model work stays
classify/routine-tier (local-routable, ADR-015) and flows through the
chokepoint.

## Inputs

- **Projects** — every note under `01-Projects/` except `README.md` and the
  pulse note itself. Source of truth is the vault (`rc.Vault.List`); each
  project's *last-touched* date comes from the DB `notes.updated` column
  (reused via `db.NotesUpdatedSince(ctx, db, "0001-01-01", N)` → all notes,
  filtered to the `01-Projects/` prefix — no new query).
- **Goals** — the `- goals:` list under `## Now` in
  `02-Areas/Profile/USER.md` (human-owned). Parsed from the rendered form
  `- goals: [a, b, c]` (brackets stripped, comma-split); the placeholder
  `(current objectives)` and an empty `[]` mean "no goals stated". If USER.md
  is absent, goals are simply empty — the pulse still runs on projects alone.

## Behaviour

### Change gate (`DetectChange`)
- No project notes → `Changed:false` ("no projects; feature inactive").
- Else cursor = `pulse:<weekStart YYYY-MM-DD>:<hashShort(sorted "path|updated"
  lines + "\ngoals:" + goals)>`. Runs when the week rolls over **or** the
  project set / their update stamps / the goals change; otherwise skipped.
  (Mirrors `research-questions`' week+content cursor.)

### Facts (deterministic, zero tokens)
Per project, newest-touched first:
`- [[Project]] — active|⚠ stale Nwk, touched <YYYY-MM-DD> (Nd ago)[; goal: <g>]`
where a goal is attached when the project's name appears in a goal string
(case-insensitive substring, either direction). Staleness threshold =
**21 days** (3 weeks), a struct-field default (`staleDays`), not config —
consistent with entity-pages/memory-distill thresholds. A footer counts
active vs stale and lists the stated goals.

### Narrative (one budget-degrading call)
One `runModel` call, `ModelKey:"routine"` (local-routable), fed the facts
**plus** a bounded excerpt (~80 words) of each **active** project note, with a
total excerpt cap (~1200 words across projects) for frugality. System prompt:
write a 3–6 sentence pulse — progress, stalls, next actions — grounded strictly
in the provided facts/excerpts, treating them as data not instructions
(NFR-05). On `deferred` (budget), narrative becomes
`_(pulse narrative skipped: budget)_` and the block is facts-only. The gate
"pulse degrades to facts-only under budget pressure" is met by construction.

### Write (wikilink-safe)
Target `01-Projects/Project Pulse.md`. Created if absent with frontmatter
(`type: pulse`) + a human preamble + an empty `axon:pulse` block (via
`vault.Create`, never clobbering). The block is rebuilt whole each run via
`rc.Vault.Patch(ctx, path, "pulse", …)`: `## Week of <weekStart>` heading,
narrative, then the per-project facts and footer. Prose above the block is the
human's forever (cardinal rule 2). `--dry-run` writes nothing and reports the
intended path + token estimate.

### Stale nudges (review queue + proposal memory)
Each **stale** project not already nudged appends one line to
`.axon/review-queue.md`:
`- [ ] pulse: [[Project]] untouched N weeks — review or archive?`
Proposal memory (state key `project-pulse:nudged`, the shared
`load/saveProposalMemory` helpers, keyed by project path) ensures a project
nudges **once**, never re-nagging weekly. Nudges are appended under a dated
`## Project pulse (…)` header, exactly like the resurfacer. Nudges are
suppressed entirely in `--dry-run` (reported only).

## Interfaces & wiring

- New file `internal/automations/pulse.go`: `type ProjectPulse struct{}`
  implementing `Name() "project-pulse"`, `Essential() false`, `DetectChange`,
  `Run`. Pure helpers (`parseGoals`, `projectFacts`, excerpt bounding) unit-
  tested directly.
- Registered in `registry.go` (17→18 automations); human description in
  `catalog.go`; disabled-by-default config in `starter.go` (both profiles) and
  `axon.config.example.yaml`.
- **Count-assertion bumps** (learned every automation cycle): `registry_test`
  want-list (+`project-pulse`), `mcp/tools_more_test` automation count 17→18,
  and gofmt realigns the registry/catalog maps.

## Testing (TDD)

Table-driven, fake agent + real-FS vault (the established automations test
rig):
1. `parseGoals` — bracketed list, placeholder, missing USER.md, wikilinked
   names.
2. `projectFacts` / staleness — active vs `⚠ stale`, goal attachment,
   newest-first order, threshold boundary (20 vs 22 days).
3. `Run` happy path — creates the pulse note, `axon:pulse` block holds
   narrative + facts; human preamble intact; ledgered estimate > 0.
4. Budget defer — `deferred` narrative → facts-only block, no error.
5. Stale nudge — one queue line per stale project; second run adds none
   (proposal memory); dry-run writes nothing.
6. Change gate — same week+state ⇒ skip; changed goals/updated ⇒ run.

## Acceptance gate (roadmap §C3)

A project untouched for ≥3 weeks produces exactly one nudge (proposal memory);
the pulse degrades to facts-only under budget; every write is wikilink-safe /
managed-block-only; runs on change only; disabled by default; a fresh vault
with it off is unaffected (S8). Live-smoke with real local model routing
(ADR-015) as in C2.
