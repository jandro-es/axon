# macOS 27 M2 — the fm tier (`apple:system` / `apple:pcc`) (design)

**Status:** approved 2026-08-20 (all three decisions Jandro-picked-recommended:
`fm serve` on a unix socket; extend the `apple:` family; PCC personal-only
opt-in best-effort). **FR-193…FR-195, ADR-038.** Graduates docs/21 M2.

## Live findings this design is built on (real macOS 27.0)

- `fm serve` works: `/health`, `/v1/models` (`system`, `pcc`), OpenAI-shaped
  `POST /v1/chat/completions` with **real usage** (`prompt_tokens: 60,
  completion_tokens: 3`, cached + reasoning details) and a `--socket` mode.
- On-device `fm respond` answers from a restricted (non-Terminal) context —
  a daemon can use it.
- **PCC is context-gated**: "Private Cloud Compute is not available in this
  context. Please use the Terminal app." — with exit code 0. Whether a
  launchd daemon context reaches PCC is unknown; the design treats PCC as
  best-effort and degrades.
- `fm quota-usage` exists ("System: Not applicable…; PCC: unavailable (…)").
- The existing Swift-helper `apple` tier reports **no usage** (heuristics).

## Components

1. **`agent.FMServe`** (`internal/agent/fmserve.go`) — unix-socket HTTP
   client (custom `DialContext`), `POST /v1/chat/completions`
   `{model, messages([system]+user), stream:false}`; maps usage
   (prompt→Input, completion→Output, cached→CacheRead); `Response.Model` =
   the concrete `apple:<variant>` ref for the ledger; `AuthMode() = "local"`.
   `JSONOutput`/`OutputSchema` are not sent (fm serve `response_format`
   unverified) — `ValidateOutput` + the chokepoint retry own correctness.
2. **`agent.FMSupervisor`** (`internal/agent/fmserve_proc.go`) — owns the
   child: `fm serve --socket <data_dir>/fm.sock`; `Ensure(ctx)` lazily starts
   (mutex, one per process), waits for `/health` (bounded), restarts a dead
   child on next Ensure; `Stop()` SIGTERM + WaitDelay kill; socket dir 0700.
   The binary path comes from `exec.LookPath("fm")` at construction; a
   missing binary makes Ensure error → adapter error → FR-79 ladder.
3. **Config** — `ParseModelRef`: `apple:system`/`apple:pcc` →
   `ModelRef{Provider: ProviderAppleFM("apple-fm"), Model: variant}`; any
   other `apple:<x>` rejected by validation with "unknown apple variant";
   `apple-fm:*` stays reserved-rejected. `ModelsConfig.PCCEnabled bool
   yaml:"pcc_enabled"`; validation: `apple:pcc` requires it true; routine may
   be `apple:pcc` (the one loosening of validateLocalRouting's apple rule);
   synthesis unchanged Claude-only.
4. **Chokepoint** — `Router.AppleFM` + Resolve case; `runLocal` pre-flight
   caps: `apple:system` shares `appleInputCapTokens`; `apple:pcc` gets
   `pccInputCapTokens` (28_000 of the 32K window, leaving response room).
   PCC ledger rows carry `apple:pcc`; no Claude budget-window accounting;
   budget-guard untouched (standing scope choice). Verify cascade stays
   ollama-only (recorded, not extended).
5. **Wiring** (`cmd/axon` deps) — on darwin with `fm` on PATH, build the
   supervisor + adapter and hand them to the router (mirrors how the Ollama
   and AppleFM adapters are wired); non-darwin or no `fm` → router field nil
   → actionable resolve error → ladder.
6. **eval-drift** — the cursor gains fm-backed gated tiers keyed on
   `sw_vers -productVersion` (reuses M1's helper); an OS update re-evals.
7. **Doctor** — the FR-191 `apple-fm` check gains tier-awareness: when a
   configured tier resolves to the fm provider, probe the socket/serve
   health; when PCC is enabled, append `fm quota-usage` output (advisory,
   ANSI-stripped, capped).

## Out of scope

Streaming; `response_format`; vision through fm (M3); Siri/App Intents (M4);
any change to bare `apple`, the Swift helper, or the verify cascade; Claude
budget-window accounting for PCC.

## Verification

Unit: adapter vs `httptest` unix-socket server replaying the captured real
response; supervisor vs a fake `fm` shell script (start/health/dead-restart/
stop); ParseModelRef + validation tables; runLocal cap + ladder tests with
`agent.Fake`. Live smoke (this machine): real socket serve + one classify
call through the chokepoint with measured usage in the ledger; doctor states;
`apple:pcc` exercising the real context-unavailable degrade path.
