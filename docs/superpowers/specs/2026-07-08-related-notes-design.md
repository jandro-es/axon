# R8 — Ambient related-notes surface — design

**Slice:** R8 (roadmap `docs/15-roadmap-1.2.md`) · **Date:** 2026-07-08
**FR:** FR-148, FR-149, FR-150 · **ADR:** none (rides ADR-025 + ADR-023)
**Status:** design approved; ready for implementation plan.

> Provisional roadmap numbers for R8 were FR-144/145; those were consumed by R5,
> so R8 is (re)assigned FR-148…150. Current maxima before this slice: FR-147,
> ADR-031. After: FR-150, ADR-031 (no new ADR).

## 1. Summary

Expose the embeddings AXON already computes as a live **"related to what I'm
looking at"** surface: given a note, return the most similar *other* notes by
pure vector math — **zero model calls, no vault mutation**. Three surfaces plus a
documented loopback endpoint an Obsidian sidebar plugin can consume:

- `axon related <path>` — CLI
- `vault_related` — MCP tool (read-only, agentic-safe)
- Dashboard **Related** panel + `GET /api/related?path=…` (also the documented
  loopback endpoint)

This is a **composition slice**: it reuses the ANN VectorIndex seam (B1/ADR-025),
the per-note mean-vector primitive (`db.NoteMeanVectors` + `db.Cosine`, the same
math powering the graph's similarity edges — FR-61), and the dashboard trust
boundary (ADR-023). No new architecture → **no new ADR**.

## 2. Decisions (approved)

| # | Decision | Choice |
|---|----------|--------|
| 1 | Similarity engine / seam | **ANN seam, chunk→note.** Feed the target note's mean vector through the `VectorIndex` (IVF/ANN, auto-brute-fallback), dedup chunk hits to notes. "Related" = a note owning a chunk near the target's centroid. Honors the gate's "respects the ANN seam" clause and scales sub-linearly. |
| 2 | Note argument | **Exact vault-relative path** on all surfaces, mirroring `vault_read`. No title fuzzy-matching (no title index exists). Unknown path → clear error. |
| 3 | Exposure | **Agentic + kill-switch.** `vault_related` joins `agenticReadTools` (zero-spend, read-only — unlike `vault_ask`, which is excluded for spending tokens). Dashboard endpoint gated by `dashboard.related_enabled` (`*bool`, default ON), consistent with ask/capture (ADR-023). |

## 3. The engine — `Searcher.Related` (FR-148)

New method + result type in `internal/search/search.go`:

```go
type RelatedNote struct {
    Path       string  `json:"path"`
    Similarity float64 `json:"similarity"` // raw cosine in [-1,1], typically (0,1]
}

func (s *Searcher) Related(ctx context.Context, notePath string, topK int) ([]RelatedNote, error)
```

Flow — **zero model calls, zero vault writes**:

1. `if topK <= 0 { topK = relatedDefaultTopK }`.
2. Resolve `notePath` → `noteID` via `db.GetNoteIDByPath(ctx, s.DB, notePath)`.
   Unknown path → return a wrapped error (`%w`) the callers surface.
3. Fetch the target's representative vector:
   `means, _ := db.NoteMeanVectors(ctx, s.DB, map[int64]bool{noteID: true})`.
   If `means[noteID]` is absent (note has no embedded chunks) → return
   `nil, nil` (empty, **not** an error); callers render "no related notes
   (note not embedded?)".
4. Fetch ANN candidates through the seam:
   `db.HybridSearch(ctx, s.DB, db.SearchOpts{QueryVector: mean, Query: "",
   TopK: (topK+1)*relatedChunkOverfetch, Index: s.vindex()})`.
   Empty `Query` ⇒ no lexical rows; `s.vindex()` picks `IVFIndex` when
   `retrieval.index: ann` (auto-falls back to brute below
   `retrieval.ann.threshold`, so small vaults stay exact). **No embedder call** —
   we already hold the vector, so nothing reaches Ollama or Claude.
5. Collapse chunk hits → notes: group `ChunkHit` by `*NoteID`, `similarity =
   max(hit.Vector)` per note. Skip hits with nil `NoteID`. **Exclude the target
   `noteID`** (its own chunks score ~1.0).
6. Drop notes below `relatedMinSimilarity`, sort by similarity desc, truncate to
   `topK`. Return `[]RelatedNote`.

Tunables as unexported Go consts in `search` (mirroring `resurfaceThreshold`,
not config — keeps the config surface minimal):

```go
const (
    relatedDefaultTopK    = 10
    relatedMinSimilarity  = 0.3 // floor; tune at live smoke
    relatedChunkOverfetch = 8   // chunk headroom so chunk→note dedup still fills topK
)
```

**Title is out of scope for v1** — results carry `Path` + `Similarity` only
(consistent with the graph, which is path-keyed). A title lookup is an easy
additive follow-up if wanted later.

## 4. Surfaces

### 4.1 CLI — `axon related <path>` (FR-149)

`cmd/axon/related_cmd.go`, structured like `search_cmd.go`:

- `Args: cobra.ExactArgs(1)` — a vault-relative path.
- Flags: `--top-k int` (0 ⇒ engine default), `--json bool`.
- Wiring: `loadProfileDeps(gf, true)` → `deps.buildSearcher()` →
  `searcher.Related(ctx, args[0], topK)`.
- Output: on a TTY, a `tui.Table` (columns: Similarity %, Path); non-TTY plain
  list; `--json` emits the `[]RelatedNote` via `json.NewEncoder`. Empty result →
  a friendly "no related notes found" line (styled via `ui.For`).
- Registered in `cmd/axon/root.go` on the `root.AddCommand(...)` data-command line.

### 4.2 MCP — `vault_related` (FR-149)

`internal/mcp/tools.go`:

```go
type RelatedIn struct {
    Path string `json:"path"    jsonschema:"vault-relative path of the note to find neighbours for"`
    TopK int    `json:"top_k,omitempty" jsonschema:"max number of related notes (default 10)"`
}
type RelatedOut struct {
    Related []search.RelatedNote `json:"related"`
}
func (t *Tools) Related(ctx context.Context, in RelatedIn) (RelatedOut, error)
```

Handler delegates to `t.deps.Searcher.Related`. Registered in
`server.go`'s `toolRegistry()` (name `vault_related`, underscore convention),
part of the **default MCP set** (both clients). **Added to `agenticReadTools`**
in `internal/automations/model.go` (read-only, zero token spend). Not in
`agenticWriteTools`.

**Known count-assertion bumps (all three must change together):**
- `internal/mcp/filter_test.go` — `len(all) != 15` → `16`.
- `internal/mcp/server_test.go` — add `"vault_related"` to the sorted `want` list.
- `internal/mcp/tools_more_test.go` — add `TestRelatedTool` (behaviour test).

### 4.3 Dashboard — panel + endpoint (FR-150)

- **Endpoint:** `GET /api/related?path=…` registered in `dashboard/server.go`
  `Handler()`. Guard order mirroring `handleAsk` (ADR-023), adapted for a
  read-only GET:
  1. `!s.cfg.RelatedEnabled || s.cfg.Searcher == nil` → **404**.
  2. `r.Header.Get("X-Axon-Related") != "1"` → **403** (a custom header forces a
     CORS preflight no cross-origin page passes).
  3. Missing/empty `path` query param → **400**.
  4. Else `Searcher.Related(...)` → `writeJSON({"related":[...]})`.
  Whole mux already wrapped in `s.guardHost` (loopback + Host allowlist). No
  request body (GET) → no `MaxBytesReader`. **No SSE event** — it's a read-only,
  high-frequency pull; emitting per-query events would be feed noise. No
  `SSE_KINDS` change.
- **Config:** `dashboard.related_enabled *bool` (default ON) with
  `RelatedAllowed()` helper in `internal/config/types.go` (pointer-bool pattern,
  like `AskAllowed`/`CaptureAllowed`). New `dashboard.Config.RelatedEnabled`
  field, wired in `cmd/axon/start_cmd.go` from `deps.profile.Dashboard.RelatedAllowed()`.
  `Searcher` is already on `dashboard.Config`.
- **health:** expose `related_enabled` in `dashboard/health.go` (like
  `ask_enabled`/`capture_enabled`) to drive conditional UI.
- **SPA:** `web/src/App.jsx` — a `RelatedTab` (`<Card>`, a path input + button,
  on-demand `GET /api/related?path=` with the `X-Axon-Related:1` header, renders
  the list). Add to `TABS`; hide when `health.related_enabled === false`
  (mirroring the ask-tab conditional). No new event kind.

## 5. Doctor

Add a light advisory `relatedCheck` in `internal/core` doctor (guarded by
`RelatedAllowed()`): reports enabled/disabled and the embedded-note count so the
cross-cutting "doctor reports each new subsystem" rule is honored. The underlying
ANN seam's health is already covered by the existing `annIndexCheck`; this check
does **not** duplicate it — it only confirms the surface is wired and has vectors
to work with. Warn-only, never fails the build.

## 6. Guardrails & invariants

- **Cardinal rule 1 (no Claude bypass):** N/A — the whole path is pure vector
  math over SQLite; nothing reaches Claude or Ollama. No token-ledger entry
  because there is no model call (contrast `vault_ask`).
- **Cardinal rule 2 (wikilink-safe):** read-only; no `vault.write`/`patch`/`move`,
  no `fs` writes. No mutation at all.
- **S8 (all-off still useful):** additive surface; `related_enabled: false`
  removes the dashboard endpoint + tab and the feature loses nothing essential.
  The CLI command and MCP tool have no individual kill-switch (read-only,
  zero-spend); they stay in the default set. The whole MCP server / dashboard can
  still be disabled wholesale as today.
- **S9 (vault rebuilds DB, never reverse):** no new persisted knowledge; results
  are derived live from existing `vec_chunks`. `reindex` unaffected.
- **NFR-05 (content is data):** the note path is used only to look up an id; note
  content is never interpreted as instructions.
- **Performance gate:** related list returns <100 ms warm via the ANN seam;
  covered by the brute↔ann parity test + live smoke.

## 7. Testing strategy

- **Engine (`internal/search`):** real in-memory SQLite + `embeddings.NewFake`,
  seed a few topical notes, `core.Reindex`, then assert `Related`:
  neighbours sorted desc, **target excluded**, `topK` honored, empty slice for an
  unembedded note, unknown path → error. **Brute↔ANN parity:** identical top
  results with `retrieval.index: brute` vs `ann` at `nprobe ≥ k` (the B1/ADR-025
  parity contract).
- **CLI:** `run(t, "related", "<path>", "--json")` in `related_cmd_test.go`
  (mirrors `ask_cmd_test.go` / `cli_test.go` helpers).
- **MCP:** `TestRelatedTool` in `tools_more_test.go` + the three count-assertion
  updates (§4.2).
- **Dashboard:** `related_api_test.go` mirroring `ask_api_test.go` — guard states
  (disabled→404, header-less→403, missing-path→400, ok→JSON with results).
- **Live smoke (real Ollama nomic; no Claude needed):** seed topical notes,
  `axon related <path>` shows sensible neighbours <100 ms warm; `curl` the
  endpoint with and without `X-Axon-Related:1`; kill-switch off → 404; dashboard
  panel renders. Run suites with `env -u FORCE_COLOR`.

## 8. Build order (for the implementation plan)

1. Engine: `RelatedNote` + `Searcher.Related` + consts + unit tests (incl. ANN
   parity). *(Nothing downstream compiles against a missing method.)*
2. CLI `axon related` + test + `root.go` registration.
3. MCP `vault_related`: tool + registry + `agenticReadTools` + the 3 count bumps.
4. Config `related_enabled` + `RelatedAllowed()`; dashboard endpoint + guard +
   health field + `start_cmd.go` wiring + `related_api_test.go`.
5. SPA `RelatedTab`; `web` build.
6. Doctor `relatedCheck`.
7. Docs at build: `docs/03` FR-148/149/150 rows; `docs/08`/`docs/09` tool +
   dashboard entries; `docs/15` R8 line marked built; README command/tool counts;
   CLAUDE.md if a count changes.

## 9. Out of scope (this slice)

- Result titles (path + similarity only for v1).
- A packaged Obsidian plugin (we ship the *documented endpoint*; the plugin
  itself is not in R8).
- Any `retrieval.*` config knobs (thresholds are consts).
- Caching / precomputed neighbour tables (the ANN seam meets the <100 ms gate
  live).
