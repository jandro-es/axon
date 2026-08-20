# Automation recipes — user-defined declarative automations (design)

**Status:** approved 2026-08-20 (all three decisions Jandro-picked-recommended:
`recipes:` in config.yaml; sinks = managed block + review proposal; one-shot
only with scheduling via `automations.<name>`). **FR-199…FR-201, ADR-039.**
Graduates docs/20 C1 ("the largest and most platform-defining slice").

## The idea

Today a new automation is a Go type, a registry entry, and three
count-assertion bumps. The 24 shipped automations decompose into a small
vocabulary — readers (`vault.Read`, `search.Searcher.Search`,
`db.NotesUpdatedSince`), zero-or-one one-shot `runModel` call, and two safe
sinks (`vault.Patch` of a managed block; a review-queue proposal). A recipe
expresses that vocabulary as YAML. Recipes materialize as ordinary
`Automation` values injected into `Registry(profile)`, so the entire engine
lifecycle — change-gate cursor, chokepoint, budgets, dry-run, ledger, SSE,
catalog, dashboard reliability table, budget-guard pause, `axon run` — is
inherited with **zero engine changes**.

**Security stance (ADR-039):** recipe definitions live in `config.yaml`,
which is outside every model write path — a model call can never author an
automation. Recipes are data, not programs: no template logic, no agentic
runs, no shell, no egress. Both cardinal rules hold by construction: the only
model path is `runModel` (chokepoint), and the only writers are `vault.Patch`
into an `axon:` block + the review-queue append.

## FR-199 — Recipe definition + validation (config)

A new `Profile.Recipes []Recipe` (`yaml:"recipes"`, optional):

```yaml
recipes:
  - name: reading-digest                # ^[a-z0-9][a-z0-9-]{1,40}$, unique
    purpose: "Weekly digest of new reading notes."
    inputs:                             # 1–8 named readers, all zero-Claude
      - name: recent
        recent_notes: {lookback_days: 7, limit: 20}   # "[[path]] (updated DATE)" lines
      - name: hits
        search: {query: "reading list", top_k: 5}     # "[[path]]: excerpt" lines
      - name: list
        note: {path: "03-Resources/Reading List.md"}  # the note body
    prompt: |                           # exactly ONE of prompt|render
      Summarise this week's reading ({{today}}).
      Recent: {{recent}}
      {{hits}} {{list}}
    output:                             # exactly one sink
      block: {note: "03-Resources/Reading Digest.md", block: "recipe"}
      # or: review: {}
automations:
  reading-digest: {enabled: true, schedule: "0 8 * * 1", model: routine, budget_tokens: 20000}
```

- **Readers:** `note {path}` (body of one note), `search {query, top_k ≤ 20}`
  (hybrid hits as `[[path]]: excerpt`), `recent_notes {lookback_days 1–90,
  limit ≤ 100}` (`[[path]] (updated DATE)` lines). Exactly one reader per
  input. Every reader is zero-Claude (the search query embedding is the usual
  budget-exempt local primitive).
- **Templating is substitution, not logic:** `{{<input-name>}}` and
  `{{today}}` via `strings.Replacer`. An unresolvable placeholder is a
  **validation error** (catches typos); an unused input is a validation error
  too (dead config is misconfig). No conditionals, loops, or functions.
- **`prompt` vs `render`:** `prompt` → one one-shot chokepoint call, sink
  receives the model output; `render` → zero-model, sink receives the
  substituted template. Exactly one of the two.
- **Sinks:** `block {note, block}` — vault-relative `.md` path, no `..`, not
  under `.axon/` or `.trash/`; block name `^[a-z0-9][a-z0-9-]*$`, not a
  reserved AXON block name (Go const list: heartbeat, briefing, actions,
  answers, memory, pulse, mentions, merged, tasks, summary, report, deep,
  links). `review {}` — no fields in v1. Exactly one sink.
- **Validation** lives in `internal/config` as `validateRecipes(p Profile)
  error` in the `Config.Validate` per-profile loop (the `validateVision`
  pattern): all of the above plus name uniqueness among recipes and purpose
  required. Built-in-name collisions cannot be checked here (dependency rule:
  `config` imports nothing) — that is FR-201's `ValidateRecipes`.
- **Caps (Go consts, not config):** rendered input clipped at ~32 KB with a
  truncation marker (bounds the zero-model render path; the model path is
  additionally bounded by the normal `budget_tokens` pre-flight); ≤ 8 inputs;
  review sink ≤ 10 proposals/run.

## FR-200 — Execution semantics (one generic Automation)

`internal/automations/recipe.go`: `type RecipeRun struct{ def config.Recipe }`
implementing `Automation`. `Registry(profile)` appends one per recipe whose
name does not collide with a built-in (**built-ins always win**; the
collision is surfaced by FR-201, never silently swapped).

- `Name()` = recipe name; `Essential()` = false (budget-guard may pause
  recipes).
- **DetectChange — the automatic change-gate:** resolve all inputs, render
  them canonically, cursor = `"recipe:" + hashShort(rendered)`. Unchanged
  inputs → skip with no model call (FR-31 generically — the hash every
  built-in hand-rolls). A missing `note:` input → not-changed, reason
  "input note absent" (the research-questions inactive pattern), so a recipe
  targeting a not-yet-created note idles for free.
- **Run:** re-resolve, substitute; model path via
  `runModel(AgentCall{Operation: "automation.<name>", ModelKey: tier,
  Essential: false})` where tier = the `automations.<name>.model` value
  (`classify`/`routine`/`synthesis` or a concrete ref; empty → `routine`) —
  the heartbeat precedent for config-driven tiers. Budget defer/deny →
  graceful `"deferred (budget)"` summary, not an error. `rc.BudgetTokens`
  applies via `runModel` unchanged.
- **Block sink:** target absent → `Create` a stub with a short human-editable
  preamble naming the recipe (ActionsConsolidate pattern), then `Patch` the
  `axon:<block>` managed block with the output. Human prose untouched —
  cardinal rule 2 by construction.
- **Review sink:** each non-empty output line (≤ 10) becomes
  `- [ ] <id> recipe "<line>" (from <name>)` appended to
  `.axon/review-queue.md`, deduped via the shared proposal-memory helpers
  (stateKey `<name>/proposed`, key = hash of the line).
- **DryRun:** report inputs resolved + would-write target; model path
  pre-flights only (`runModel`'s Authorize-only branch returns the estimate).

## FR-201 — Surfacing: registry, catalog, review kind, doctor

- **`automations.ValidateRecipes(profile) error`** — the check only the
  automations package can make: a recipe name colliding with a built-in is
  an **error**. Called from `axon start`, `axon run`, and doctor, so a
  colliding recipe is loud at every entry point while `Registry` stays
  total. A separate `automations.UnscheduledRecipes(profile) []string`
  (recipes with no `automations.<name>` entry) feeds doctor's advisory
  warning — unscheduled is legal (a recipe can exist for `axon run` only),
  just worth surfacing.
- **New review kind `recipe`** in `internal/review`: Load parses
  `recipe "<text>" (from <name>)`; **Accept = acknowledge only** (suffix
  "✓ noted" — the first purely informational kind, no mutation of any kind);
  dismiss archives as usual. Not an Outcomes kind.
- **Catalog:** recipes appear in `axon automations` with the recipe's own
  `purpose` (Catalog prefers it over the built-in `purposes` map) and
  `Info.Recipe bool` (`json:"recipe,omitempty"`) so CLI/dashboard can mark
  them.
- **doctor `recipesCheck`** (advisory): off (none defined) / N recipe(s)
  valid / collision or unscheduled warnings via `ValidateRecipes`.
- **No count-assertion bumps:** no new built-in, no new MCP tool; test
  profiles carry no recipes. `seeds_test` unaffected (recipes are
  user-authored by definition — starter stays recipe-free; the example
  config gains a commented-out sample).

## Out of scope (recorded in ADR-039)

Agentic recipes; vault-file or `~/.axon/recipes/` definitions; mutating
accept paths; multi-model chains; template logic; a sharing/marketplace
format; recipe-defined schedules (the `automations` map owns scheduling);
new sinks (daily-note append considered and deferred).

## Verification

Unit: config validation table (bad name, dup, collision-with-recipe, no
inputs, two readers in one input, both/neither prompt+render, unknown or
unused placeholder, reserved block, `.axon/` target, oversized caps); recipe
run with `agent.Fake` (tier assert via `fake.Calls[0].Model`, substitution,
block written, stub-create + preamble intact on re-run); zero-model render
path; automatic change-gate (second run skips, input edit re-arms); dry-run
(estimate, nothing written); budget defer degrade; review sink append +
dedup + accept-acknowledge + dismiss; `ValidateRecipes` collision +
unscheduled; catalog purpose/Recipe flag; doctor states. Live smoke
(isolated `AXON_HOME` :7788, never :7777): a real `render` recipe end-to-end
via `axon run` (block written, re-run change-gate-skips), the model path with
real Ollama routine (`ollama:codestral`, the C2/C3 pattern — Claude auth
absent in scratch), doctor + `axon automations` showing the recipe, config
validation rejecting a deliberately broken recipe.
