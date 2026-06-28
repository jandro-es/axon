# 07 — Component: Context & Token Manager

**Owns:** FR-40…FR-46, NFR-08. This is the **mandatory chokepoint** (ADR-007): no code path calls Claude without going through it.

**Goal:** Make "token-aware, not wasting tokens" structural — every call measured, budgeted, justified; context assembled by retrieval, not by dumping; stale context distilled into durable notes.

## 1. The chokepoint API

```go
type AgentCall struct {
    Operation    string   // ledger label, e.g. "automation.daily-log"
    ModelKey     string   // "classify" | "routine" | "synthesis" | concrete model; resolved via config
    System       string   // optional
    Messages     []Message // already assembled (callers use BuildContext)
    BudgetTokens int      // per-call cap (0 = use automation/profile default)
    Essential    bool     // bypass guard-pause but still ledgered
}

type TokenManager interface {
    // Assemble a token-bounded context from retrieval results.
    BuildContext(ctx context.Context, query string, opts RetrieveOpts) (Context, error) // {Messages, Tokens, Sources}
    // Pre-flight: CountTokens, check budgets, decide proceed/downgrade/defer.
    Authorize(ctx context.Context, call AgentCall) (Authorization, error) // {Decision, Model, EstInput, Reason}
    // Execute through the agent adapter, then record usage. Calls Authorize internally.
    Run(ctx context.Context, call AgentCall) (AgentResult, error)
    // Read-only view: remaining day/week tokens + cost, guard state.
    Status(ctx context.Context, profile string) (BudgetStatus, error)
}
```

Callers **never** construct a raw Claude request; they call `Run()` (or `Authorize()` then `Run()`), which guarantees pre-flight + ledger + budget enforcement.

## 2. Pre-flight counting (FR-40)

- **subscription / enterprise (default):** there is no `count_tokens` API endpoint available, so estimate input tokens **locally** before sending — a tokeniser approximation over the assembled prompt + injected files (e.g. a byte/heuristic estimator, or a bundled BPE table). The estimate need not be exact; it bounds context and guards against rate-limit / Agent-SDK-credit burn. Cache estimates by content hash.
- **api_key (optional):** use the Messages API token-count endpoint (`POST /v1/messages/count_tokens`) via `anthropic-sdk-go`'s `client.Messages.CountTokens(ctx, …)` for an exact count.
- Either way the estimate feeds the same gate. `Authorize()` returns:
  - `proceed` — within the active token window/credit at the requested model.
  - `downgrade` — over budget at the requested model but fits a cheaper one (e.g. synthesis→routine); caller may accept.
  - `defer` — essential window exhausted; queue for next window (automations) or surface to user (interactive).
  - `deny` — hard cap breached and non-essential.

## 3. Post-hoc accounting (FR-41)

- Record whatever usage the execution path reports. `claude -p --output-format json` returns token usage (and an estimated cost); the direct-API path returns `usage` (`input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`). If the headless path reports nothing, fall back to the pre-flight estimate for input and a measured output-length estimate.
- Write a `token_ledger` row (operation, model, counts, `est_input` from pre-flight, `run_id`). `cost_usd` is populated **only in `api_key` mode**; on subscription/enterprise it stays null and the row counts toward the token/credit window instead.
- Update `budget_state` for day+week windows.
- Emit a live event → dashboard within ≤5s (NFR-07/S4).

## 4. Budgets & guard (FR-42, FR-43)

- Windows: rolling **day** and **week** per profile (in **tokens**, estimated), plus per-automation `budget_tokens`. On `api_key` mode a profile `daily_cost_usd` cap also applies; on subscription/enterprise the token windows stand in for "don't burn the plan's rate limit / Agent SDK credit".
- `guard_pause_at_pct` (e.g. 80) triggers **budget-guard** (Component 06) to pause non-essential automations until the window resets — and is the right lever when Claude Code starts reporting rate-limit pressure.
- `essential` operations (budget-guard, heartbeat status, interactive sessions) are **surfaced not silently blocked** — the user/dashboard always sees when the system is near/at cap.
- `Status()` powers `axon status`, the `tokens.status` MCP tool, and the dashboard gauges.

## 5. Retrieval-first context (FR-46)

- `BuildContext` runs hybrid search (Component 05 §3), takes top-k, packs chunks until `max_context_tokens`, and returns the assembled messages plus source refs (for citation/links).
- Hard rule: **no automation or tool sends the whole vault**. If a task seems to need everything, it needs compaction first, or a narrower query.
- Prompt-caching: stable preambles (the `CLAUDE.md`-style schema, long reference blocks) are placed first and marked cacheable so repeat calls read cache instead of re-paying. Record cache hits in the ledger.

## 6. Model selection (FR-45)

- Per-operation model from config (`models.classify|routine|synthesis`), overridable per automation and per MCP tool. On the Claude Code path it is passed as `claude -p --model`; **actual availability follows the plan tier** (e.g. Opus access and rate limits differ by Max tier), so treat it as a preference and degrade gracefully if a model isn't available.
- Defaults: **Haiku** for classification/triage/short labels; **Sonnet** for routine edits/daily logs; **Opus** for weekly synthesis/distillation. The work (enterprise) profile may pin synthesis→Sonnet to conserve the plan's limits.
- `Authorize()`'s `downgrade` path can pick a cheaper/lighter model automatically when configured to.

## 7. Compaction as a token strategy (FR-44)

Compaction is both a knowledge-hygiene and a cost mechanism:
- **Targets:** session snapshots in `.axon/snapshots/`, oversized notes, long stale daily notes.
- **Process:** retrieve the raw material, summarise (synthesis model) into a durable note or `axon:summary` block, link it, and archive the raw source. Record `tokens_saved_est` = (raw context tokens that future calls would have carried) − (compacted tokens). This estimate is approximate but trends are what matter; show it on the dashboard.
- **Why it pays:** future retrieval pulls the compact summary instead of sprawling raw logs, shrinking every downstream call's input.
- Interactive parallel: hooks suggest `/compact` when a Claude Code session's context grows large (Component 08), and the daily-log/compaction automations persist anything worth keeping into the vault so context can be safely cleared.

## 8. "Not wasting tokens" — the consolidated rules

1. **Change-gate first.** No new material ⇒ no call (Component 06).
2. **Estimate before you send.** Local token estimate pre-flight (exact `count_tokens` in `api_key` mode); refuse/downgrade/defer.
3. **Retrieve, don't dump.** Top-k bounded context only.
4. **Right-size the model.** Haiku/Sonnet/Opus by task.
5. **Cache.** Embeddings, summaries, prompt prefixes, count results.
6. **Budget and guard.** Day/week/per-automation caps with auto-pause.
7. **Compact.** Shrink future context by distilling the past.
8. **Measure everything.** If it isn't in the ledger, it didn't happen.

## 9. Acceptance checks
- Every Claude call across the system appears in `token_ledger` with model/operation/counts (and `cost_usd` in `api_key` mode) (FR-41/S4).
- A call that would breach the token window/credit is downgraded or deferred per policy, with the decision logged (FR-40/FR-42).
- `axon status --json` reports remaining day/week tokens (and cost in `api_key` mode) and guard state (FR-42).
- A compaction run records a non-zero `tokens_saved_est` and leaves a durable summary + archived raw (FR-44).
