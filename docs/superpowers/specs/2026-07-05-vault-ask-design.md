# `vault_ask` + dashboard Ask panel — design

Date: 2026-07-05
Status: approved
Roadmap: slice A2 of `docs/14-roadmap-1.1.md`
ADR: ADR-023 (`docs/02-architecture.md`) — the first token-spending
dashboard endpoint.
FR IDs: FR-111 (`vault_ask` MCP tool), FR-112 (dashboard Ask panel +
`POST /api/ask`).

## Goal

Put the A1 `ask` engine where people already work: inside Claude Code /
Desktop (an MCP tool) and on the dashboard (a panel). A2 is composition —
the engine (`internal/ask.Ask`) is unchanged. The one genuinely new thing is
**browser-initiated token spend**, which ADR-023 governs.

## Decisions (user-approved)

- **Both surfaces, full**: `vault_ask` MCP tool + `POST /api/ask` endpoint +
  a real (thin, untested) React Ask panel.
- **Standard guard + config toggle**: reuse the exact ADR-020 guard, plus a
  `dashboard.ask_enabled` kill-switch (default on).
- **Both clients**: `vault_ask` in the default tool set (Code + Desktop).

## Background / constraints (verified in code)

- `ask.Ask(ctx, ask.Deps{Searcher, Manager, Config}, question, topK)
  (ask.Answer, error)` — the A1 engine; refusals are values, spend goes
  through `tokens.Manager.Run` (`internal/ask/ask.go`).
- MCP: `mcp.Deps` already carries `Searcher *search.Searcher`,
  `Manager tokens.Manager`, `Config config.Profile`
  (`internal/mcp/tools.go:29`); tools are registered in `toolRegistry()`
  (`internal/mcp/server.go:29`) and each is a `Tools` method.
- Dashboard: `handleReviewAction` is the guard template — loopback bind +
  `guardHost` + `Content-Type: application/json` + `X-Axon-Review: 1`
  (`internal/dashboard/server.go:236-245`); `Config` carries `Manager`,
  `DB`, `Bus`, `Vault` (`server.go:28`). The SPA registers tabs in a `TABS`
  array and POSTs via `postReviewAction` with the guard headers
  (`web/src/App.jsx:611,688`).
- The fixed agentic allowlist `agenticAllowedTools`
  (`internal/automations/model.go`, ADR-022) already excludes `vault_ask`
  by omission — no automation can call it.
- Toggle pattern: pointer-default-ON with a resolver method, as
  `MemoryConfig.CaptureSessions *bool` / `SessionCaptureEnabled()`
  (`internal/config/types.go:204,213`).

## Design

### FR-111 — `vault_ask` MCP tool

New `Tools.Ask` method (`internal/mcp/tools.go`):

```go
type AskIn struct {
    Question string `json:"question" jsonschema:"the question to answer from the vault"`
    TopK     int    `json:"top_k,omitempty" jsonschema:"retrieval depth (default: retrieval.top_k)"`
}

func (t *Tools) Ask(ctx context.Context, in AskIn) (ask.Answer, error) {
    return ask.Ask(ctx, ask.Deps{
        Searcher: t.deps.Searcher, Manager: t.deps.Manager, Config: t.deps.Config,
    }, in.Question, in.TopK)
}
```

Register `vault_ask` in `toolRegistry()` with a description making the
grounded-or-silent contract explicit. It joins the default set, so both
clients get it and the `axon mcp --tools` server-side filter can still scope
it out for agentic runs (it is never in an automation's allowlist anyway).
No engine change. Read-only toward the vault; the synthesis spend is
chokepoint-governed and ledgered.

### FR-112 — `POST /api/ask` + Ask panel (ADR-023)

**Config additions** (`internal/dashboard/server.go`):

```go
// on dashboard.Config:
Searcher  *search.Searcher       // enables /api/ask (nil disables the endpoint)
Retrieval config.RetrievalConfig // top_k / max_context_tokens for ask
AskEnabled bool                  // resolved from dashboard.ask_enabled (default true)
```

**Handler** `handleAsk` mirrors `handleReviewAction`:

- 404 when `!AskEnabled` or `Searcher == nil` (surface absent, not just
  forbidden — a disabled feature shouldn't advertise itself).
- 403 unless `X-Axon-Ask: 1` and `Content-Type: application/json`.
- Decode `{question string, top_k int}` from a `MaxBytesReader` (4 KB).
- `a := ask.Ask(ctx, ask.Deps{Searcher, Manager, Config: {Retrieval}}, q, topK)`.
  (The engine reads only `Config.Retrieval`; build a
  `config.Profile{Retrieval: cfg.Retrieval}`.)
- On a Go error → 400 for the empty-question case, 500 otherwise. Otherwise
  write the `ask.Answer` JSON.
- Emit `Bus.Publish(events.Event{Kind: "ask.answer" | "ask.refused", …})`
  so the activity feed shows the spend (the ledger already records tokens).

Registered as `mux.HandleFunc("POST /api/ask", s.handleAsk)`.

**Health** (`/api/health`): add `"ask_enabled": cfg.AskEnabled` so the SPA
can hide the tab.

**SPA** (`web/src/App.jsx` + `styles.css`): an `AskTab` component (input +
submit → `postAsk(question)` with `X-Axon-Ask: 1`), rendering the three
states (answer + cited `[[sources]]`; grounded refusal + retrieved-uncited
sources; error). An `['ask', 'Ask']` entry in `TABS`, shown only when the
health payload reports `ask_enabled`. Thin and untested (ADR-020 precedent).

**Config + wiring:**

- `DashboardConfig.AskEnabled *bool` (`internal/config/types.go`) +
  `func (d DashboardConfig) AskAllowed() bool { return d.AskEnabled == nil || *d.AskEnabled }`.
- `cmd/axon/start_cmd.go`: pass `Searcher: svc.searcher`,
  `Retrieval: deps.profile.Retrieval`,
  `AskEnabled: deps.profile.Dashboard.AskAllowed()` into `dashboard.Config`.

### Error handling

- Empty question → `ask.Ask` returns a Go error → 400.
- No Searcher / disabled → 404 (feature absent).
- Cross-origin / missing header → 403 (preflight fails before the handler
  runs; the header check is defence in depth).
- Budget defer/deny and citation-failure are `ask.Answer` refusals (200 with
  `refused: true`), not HTTP errors — the panel renders them.

### Testing

- **MCP** (`internal/mcp/tools_test.go`): `Tools.Ask` returns a grounded
  answer over the existing fake-agent harness (reuse the A1 pattern: seed a
  note, `core.Reindex`, fake reply with a valid citation); `registeredToolNames`
  includes `vault_ask`.
- **Allowlist guard** (`internal/automations`): assert `vault_ask` is not in
  `agenticAllowedTools` — a pin so a future edit can't silently grant it to
  automations.
- **Dashboard** (`internal/dashboard/*_test.go`, mirroring the review-action
  handler tests): missing header → 403; `AskEnabled=false` → 404; happy path
  over a fake manager → 200 with the answer JSON; `ask.*` event published.
- **SPA**: none.

### Docs

- ADR-023 in `docs/02-architecture.md` (done).
- FR-111/112 in `docs/03-requirements.md` (done).
- `docs/04-data-model-and-config.md` + `axon.config.example.yaml`:
  `dashboard.ask_enabled`.
- `docs/GUIDE.md`: Ask panel in §10 (dashboard) + `vault_ask` in §11 (MCP
  tools list).
- `docs/14-roadmap-1.1.md`: mark A2 built.
- `CHANGELOG.md` entry.

## Trade-offs accepted

- The dashboard now spends tokens on human action (bounded by budgets, the
  guard, and the toggle; fully ledgered) — no longer strictly zero-spend.
- The SPA Ask panel is untested (ADR-020 precedent).
- `Retrieval`-only `config.Profile` inside the dashboard handler is a small
  shim; cleaner than threading the whole profile into `dashboard.Config`.

## Out of scope (A3 / later)

- Standing research questions (A3).
- Conversation history / multi-turn ask.
- Any retrieval changes (ANN, rerank — Phase B).
