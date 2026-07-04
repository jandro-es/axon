# Agentic write tools — design

Date: 2026-07-04
Status: approved
ADR: ADR-022 (docs/02-architecture.md)
FR IDs: FR-105 (write-tool allowlist), FR-106 (report-only dry-run),
FR-107 (compaction demonstrator)

## Goal

Turn ADR-017's agentic automation path from read-only to read-write,
resolving the two concerns ADR-017 named when it deferred write tools:

1. *A killed mid-run agent could half-finish multi-note work.*
2. *MCP tools have no dry-run story yet.*

Deliver the capability (write-tool allowlist, server-enforced report-only
dry-run, both-sides enforcement, convergence model) plus **one** automation
wired to prove it end-to-end (compaction).

## Decisions (user-approved)

- **Write surface:** managed-block-safe tools only — `vault_patch`,
  `vault_write`, `daily_append`, `memory_remember`. **`vault_move` excluded**
  (restructuring stays human-approved via the review queue, ADR-020).
- **Dry-run:** `--dry-run` spawns the agent with **server-enforced
  report-only** write tools — a real preview at real token cost.
- **Kill model:** per-tool atomicity + idempotent convergence; no
  transactional buffer.
- **Wiring:** capability + one demonstrator (compaction); knowledge-digest
  and everything else untouched.

## Background / constraints (verified in code)

- The write tools already exist, are atomic (NFR-06) and wikilink-safe, and
  serve the interactive Claude Code path:
  `internal/mcp/tools.go` — `Write` (:125), `Patch` (:184),
  `DailyAppend` (:267), `Remember` (:302); each has a single vault-mutation
  call site (`vault.Write/Patch/Append/Create`).
- The agentic path already passes a per-run tool allowlist:
  `runAgentic(ctx, rc, call, toolsAllow []string, maxTurns int)`
  (`internal/automations/model.go:50`) sets `call.Tools`; the adapter
  (`internal/agent/claudecode.go`) spawns the subprocess MCP server as
  `axon mcp --tools <csv>` (buildMCPConfig, :213) and gates the client with
  `--allowedTools mcp__axon__<tool>…` (:195).
- Server-side filtering already exists: `mcp.Deps.ToolFilter`
  (`internal/mcp/tools.go:38`) + `NewServer` registers only filtered tools
  (`internal/mcp/server.go:150`). `cmd/axon/mcp_cmd.go` sets it from
  `--tools`.
- `runModel` under `rc.DryRun` short-circuits to `Authorize` only and never
  spawns the agent (`internal/automations/model.go:26`). This is the
  behaviour FR-106 changes for write-capable agentic runs.
- Cardinal rules: rule 1 (every model call through the chokepoint —
  `tokens.Manager.Run`); rule 2 (wikilink-safe; managed blocks; no
  `vault.delete`).

## Design

### FR-105 — Write-tool allowlist

No change to enforcement mechanics — only the *contents* of an automation's
`toolsAllow` may now include write tools. An automation that wants writes
passes, e.g., `[]string{"vault_read", "vault_links", "vault_patch"}`. Then:

- **Server side:** the subprocess is `axon mcp --tools vault_read,vault_links,vault_patch`
  — it physically registers only those tools (`NewServer` +
  `Deps.ToolFilter`), so an unlisted tool cannot be called even if the model
  asks.
- **Client side:** `--allowedTools mcp__axon__vault_read,mcp__axon__vault_links,mcp__axon__vault_patch`.

The allowable write set is fixed in code (a small allowlist the automation
package validates its `toolsAllow` against, so a typo or a
non-managed-block-safe tool like `vault_move` in an automation's list is a
build/test error, not a silent capability): **`vault_patch`, `vault_write`,
`daily_append`, `memory_remember`**. Read tools remain unrestricted.

### FR-106 — Report-only dry-run

**New field:** `mcp.Deps.DryRun bool`. When true, every write method returns
its computed change with `applied: false` and does **not** call the vault
mutation:

- `Write` → validates path/managed rules, returns `{would: "create <path> (N bytes)", applied:false}` (or the managed-clobber refusal it already raises).
- `Patch` → validates the note exists and the marker, computes the new block, returns `{would: "patch axon:<marker> in <path>", applied:false}`.
- `DailyAppend` → returns `{would: "append N line(s) to <daily>", applied:false}`.
- `Remember` → returns `{would: "remember <kind>: <text>", applied:false}`.

Read tools are unaffected by `DryRun`.

**Threading:** `axon run <automation> --dry-run` sets `rc.DryRun`. For an
agentic call whose `toolsAllow` includes any write tool, `runAgentic` must
**actually run the agent** (not short-circuit to Authorize) with the
subprocess spawned as `axon mcp --tools <csv> --dry-run`:

- `cmd/axon/mcp_cmd.go` gains a `--dry-run` bool flag → `mcpDeps.DryRun = true`.
- The adapter appends `--dry-run` to the `buildMCPConfig` args **iff** the
  call carries a dry-run marker. `tokens.AgentCall` gains `DryRunTools bool`
  (distinct from the automation-level `rc.DryRun`, which today means
  "estimate only"): the automation sets `call.DryRunTools = rc.DryRun` on a
  write-capable agentic call, and `runModel` for such a call runs the agent
  (spending tokens, fully ledgered) instead of Authorize-only.
- Read-only agentic dry-runs keep today's Authorize-only behaviour (cheap;
  nothing would be written anyway).

**Honesty:** a write-capable agentic dry-run spends tokens. The run summary
says so (`"agentic dry-run: previewed N write(s), spent ~T tokens"`), and it
is chokepoint-governed and ledgered like any run.

### Kill / convergence model (resolves ADR-017 blocker 1)

No new machinery. Each write tool call is atomic (NFR-06 atomic file write)
and idempotent (managed-block patch replaces a region; `vault_write` refuses
to clobber prose; `daily_append`/`memory_remember` are append-converging).
A mid-run budget kill (ADR-017's existing kill-switch) leaves a **prefix of
completed writes**, each internally consistent; re-running the automation
converges (the same patches reapply identically). The ledger records
accumulated real usage on kill exactly as ADR-017 already does. Documented
explicitly so operators understand "some notes updated, some not, none
broken" is the contract — not transactional all-or-nothing.

### FR-107 — Compaction demonstrator

Compaction is already agentic (reads backlinks before distilling,
`internal/automations/model.go` compaction path, ~:388). Change: on the
agentic path, add `vault_patch` to its `toolsAllow` and instruct the model
to write its distilled summary into the target note's `axon:summary`
managed block via the tool, rather than returning text for deterministic Go
to `Patch`. Its `agentic: false` one-shot path (deterministic-Go write)
stays the fallback and the kill/defer degradation target, byte-for-byte
identical to today — so a fresh clone with agentic off is unchanged (S8).
Knowledge-digest keeps its current read-then-Go-write shape (untouched).

### Error handling

- A write tool not in the fixed allowlist appearing in an automation's
  `toolsAllow` is a validation error surfaced in tests (never shipped).
- Report-only write methods never mutate; a validation failure in dry-run
  returns the same error the live path would (so the preview is truthful
  about what would fail).
- Kill/defer on the agentic write path degrades to the `agentic:false`
  fallback exactly as ADR-017 (FR-85) already does.
- The subprocess physically lacking a tool means a model attempt to call it
  fails at the MCP layer, not by trusting client-side gating alone.

### Testing

- **Enforcement:** extend the `registeredToolNames`/`NewServer` filter tests —
  a write tool is registered iff in the CSV; the fixed write-allowlist
  validator rejects `vault_move` and unknown names.
- **Report-only:** table over `Write/Patch/DailyAppend/Remember` with
  `Deps.DryRun = true` → each returns `applied:false` and the vault is
  byte-identical before/after (hash the temp vault).
- **Convergence:** apply the same `Patch` twice → identical note; a
  `vault_write` over an existing prose note still refuses in dry-run
  (preview is truthful).
- **Adapter:** `--dry-run` appears in the subprocess args iff
  `call.DryRunTools`; write tools appear in `--tools`/`--allowedTools` iff
  the automation requested them; read-only dry-run stays Authorize-only.
- **Compaction:** agentic path drives a `vault_patch` (fake agent) that
  lands in `axon:summary`; `agentic:false` path byte-identical to today;
  `--dry-run` previews without mutating.

### Docs

- ADR-022 in `docs/02-architecture.md` (done in this cycle) — supersedes
  ADR-017's "write tools rejected" for opted-in automations.
- FR-105…FR-107 in `docs/03-requirements.md` (done).
- `docs/06-component-automation-engine.md`: compaction paragraph — agentic
  path writes via `vault_patch`; `agentic:false` fallback unchanged.
- `docs/08-component-agent-bridge-mcp.md`: the agentic allowlist may include
  the managed-block-safe write tools; report-only server mode; both-sides
  enforcement extended to writes.
- `CHANGELOG.md` entry.

## Trade-offs accepted

- An agentic-write automation is non-deterministic where its deterministic-Go
  predecessor was exact — mitigated by the always-available `agentic:false`
  fallback and managed-block scoping.
- A write-capable agentic dry-run costs tokens (documented; chokepoint- and
  budget_tokens-governed).
- A budget kill can leave some target notes updated and others not —
  recoverable by re-run, never internally inconsistent.
- The write allowlist is fixed in code, not per-automation config —
  constants-over-config, and it keeps the security surface auditable in one
  place.

## Out of scope

- `vault_move` / any vault restructuring from an agent (human-approved via
  the review queue).
- Migrating knowledge-digest (or any second automation) to agentic writes.
- A transactional staging buffer / rollback journal.
- Dashboard changes (agentic runs already stream via existing events).
