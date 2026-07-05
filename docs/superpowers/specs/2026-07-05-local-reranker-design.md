# B2 — Optional local reranker (design)

**Date:** 2026-07-05
**Roadmap slice:** B2 (`docs/14-roadmap-1.1.md`, Phase B — Retrieval scale & quality)
**Requirements:** FR-126, FR-127
**ADR:** ADR-027 (local reranking as a retrieval primitive)

## Problem

Hybrid search fuses FTS5/bm25 lexical ranks with cosine vector ranks into one
reciprocal-rank score. That fused order is good but coarse — the true best
passage for a query is often just outside the top-k, or ranked below a
lexically-similar-but-less-relevant one. A **reranker** re-scores a wider
candidate pool against the query and reorders it, lifting the genuinely relevant
passages into the top-k.

B2 adds an **optional, local, on-device reranker**: fetch top-k×N candidates,
score each against the query with a local Ollama model, reorder, keep top-k.
Off by default; opt-in via `retrieval.rerank: off | "ollama:<model>"`.

## Decisions taken

1. **Boundary — retrieval primitive, outside the chokepoint.** Reranking is a
   scoring operation over already-retrieved candidates that produces an
   *ordering*, never vault- or Claude-bound content. Like embeddings (which call
   Ollama entirely outside the token manager), it lives in a leaf package called
   by `search`, calls Ollama directly, and is budget-exempt by construction.
2. **Scoring — pointwise.** Each candidate is scored independently (0–10) and
   sorted; a trivial per-call task small local models handle reliably.
3. **ADR — new ADR-027**, recording the chokepoint-boundary decision as a narrow
   amendment to cardinal-rule-1/ADR-015.

## Cardinal-rule & principle compliance

- **Chokepoint (rule 1):** the reranker makes **no Claude call** and never
  produces content that enters a Claude prompt or the vault — it emits an
  ordering of already-retrieved candidates. Per ADR-027 it is a retrieval
  primitive (like embeddings), so it sits outside the token manager. Cardinal
  rule 1 is unviolated: no *generative content* call bypasses the chokepoint;
  the reranker is a local scoring op, budget-exempt by construction.
- **Local-first (NFR-01):** scoring runs on the local Ollama server; no bytes
  leave the machine.
- **Best-effort / degrade gracefully:** any Ollama failure or parse error falls
  back to the original fused order — reranking never breaks search (mirrors how
  embeddings degrade to lexical-only).
- **Toggleable, off by default; no new Go dependency.**

## Components

### 1. `internal/rerank` package (new)

Mirrors `internal/embeddings`: a small interface + an Ollama implementation + a
fake, depending only on the stdlib.

```go
// Candidate is one passage to score, with its original fused score for
// tie-breaking.
type Candidate struct {
	Text  string
	Score float64
}

// Reranker reorders retrieval candidates by relevance to a query. It is a
// retrieval primitive (ADR-027): local, non-Claude, outside the chokepoint.
type Reranker interface {
	// Rerank returns candidate indices best-first. Best-effort: on failure it
	// returns an error and the caller keeps the original order.
	Rerank(ctx context.Context, query string, cands []Candidate) ([]int, error)
	// Name identifies the reranker for diagnostics.
	Name() string
}
```

**`OllamaReranker`** (`rerank_ollama.go`):
- Fields: `host`, `model`, `timeout`, `concurrency` (default 4), injectable
  `post func(ctx, url string, body []byte) (status int, resp []byte, err error)`
  so tests need no live server.
- `Rerank`: score each candidate via `POST <host>/api/generate`
  (`{"model":…,"prompt":…,"stream":false}`) with the prompt
  `"Query: <q>\nPassage: <text>\nOn a scale of 0-10, how relevant is the passage to the query? Answer with only a number.\n"`;
  parse the leading integer from `.response` (clamp to 0–10). Runs with bounded
  concurrency (a worker pool of `concurrency`).
- **Failure policy:** if the *first* scored call errors (Ollama unreachable),
  `Rerank` returns that error → whole-query fallback. Per-candidate parse
  failures or later errors → that candidate gets score 0; final sort is by
  `(score desc, original Candidate.Score desc)`, so an all-zero round preserves
  the original fused order.
- Returns the index permutation (stable sort).

**`Fake`** (`rerank_fake.go`, test-only build-safe): returns a caller-set
permutation (or reverses) so `search` integration tests can assert reordering
and the error path.

**`RerankerFor(rerank, host string) (Reranker, error)`** (`rerank.go`): `""` or
`"off"` → `nil, nil`; `"ollama:<model>"` → `NewOllamaReranker(host, model)`;
anything else → `nil, error` (wiring ignores the error → reranker off; `doctor`
surfaces the misconfiguration — same pattern as `ingestion.OCRFor`).

### 2. Search integration — `internal/search/search.go`

`Searcher` gains:
```go
Reranker  rerank.Reranker // nil ⇒ reranking off
Overfetch int             // candidate multiple; ≤0 ⇒ default 3
```
set at the composition root via a chain method (existing `New`/`Configure`
callers compile untouched):
```go
func (s *Searcher) WithReranker(r rerank.Reranker, overfetch int) *Searcher
```

`Search(ctx, query, topK)`:
- `Reranker == nil` → unchanged (fetch `topK`).
- else → fetch `topK × overfetch` candidates (overfetch defaulting to 3), build
  `[]rerank.Candidate{Text: hit.Snippet, Score: hit.Score}`, call
  `Reranker.Rerank`. **On error → return the first `topK` of the original
  hits** (graceful fallback). On success → reorder hits by the returned indices
  *defensively* (valid, unseen indices first; then any leftover hits in original
  order — robust to a partial/garbage permutation), then take the first `topK`.

`Retrieve` calls `Search`, so every retrieval path (ask, `vault_ask`,
automation retrieval) inherits reranking when enabled, with no per-caller change.

### 3. Config, wiring, doctor

- **Config** (`RetrievalConfig`):
  ```go
  Rerank          string `yaml:"rerank,omitempty"`
  RerankOverfetch int    `yaml:"rerank_overfetch,omitempty"`
  ```
  Accessors: `RerankMode() string` (returns `"off"` for `""`/`"off"`, else the
  raw value) and `RerankOverfetchOr() int` (default 3). The `off | ollama:<m>`
  format is validated by the doctor check (the struct validator can't express
  the prefix rule), consistent with how `ingestion.ocr` misconfig is surfaced.
- **Wiring** (`cmd/axon/deps.go` `buildServices`): build the reranker from
  `d.profile.Retrieval.Rerank` + the Ollama host (`d.profile.Embeddings.Host`
  or `embeddings.DefaultOllamaHost`), inject via `WithReranker`. The error is
  ignored at wiring (reranker → off); `doctor` reports it.
- **`axon doctor`** (`rerankCheck`): `off` → not shown; `ollama:<m>` → Ollama
  reachable + model present (warn-only, mirroring `embeddingsCheck`/`ocrCheck`);
  a malformed value → warn. Read-only and tolerant.
- **Config example + starter**: document `retrieval.rerank` (default off).

## Data flow

```
Search(query, topK)
  rerank off ─► hybrid fetch topK ─────────────────────────► hits
  rerank on  ─► hybrid fetch topK×overfetch ─► candidates
                   └─► rerank.Rerank(query, candidates) ─► Ollama /api/generate ×N (outside chokepoint)
                          success ─► reorder by returned indices ─► take topK
                          error   ─► first topK of original fused order (fallback)
Retrieve(...) ─► Search(...) ─► pack context   (reranking inherited by every caller)
```

## Error handling & edge cases

- **Ollama unreachable:** first scored call errors → `Rerank` errors →
  `Search` returns the original top-k. Search never fails because of reranking.
- **Unparseable model output** for a candidate: scored 0; ties broken by the
  original fused score, so a fully-garbage round == original order.
- **Malformed `retrieval.rerank`** (not `off`/`ollama:*`): `RerankerFor` errors;
  wiring leaves the reranker off; `doctor` warns.
- **Fewer candidates than topK×overfetch** (small corpus): rerank whatever
  came back; still returns ≤topK.
- **Partial/duplicate permutation** from a reranker: `Search` reorders
  defensively (dedupe, drop out-of-range, pad from leftover), never panics.
- **Concurrency:** the worker pool honours `ctx` cancellation; each request has
  its own timeout.

## Testing

- **`rerank` package:** `OllamaReranker` with a faked `post` — scores parsed and
  sorted into the right permutation; leading-number parsing (`"7"`, `"7/10"`,
  `"score: 8"`, `""`→0); first-call error → `Rerank` error; concurrency honoured
  (all candidates scored). `RerankerFor`: off→nil, `ollama:m`→reranker,
  garbage→error. `Fake` returns its configured permutation.
- **`search` integration:** a `Fake` reranker reversing order proves `Search`
  overfetches and reorders to top-k; a `Fake` returning an error proves the
  fallback to the original fused top-k; nil reranker → unchanged path.
- **Config:** `RerankMode`/`RerankOverfetchOr` defaults; a config with
  `rerank: "ollama:qwen2.5"` round-trips.
- **`doctor` rerankCheck:** off → absent; ollama-with-unreachable → warn.
- **Live smoke:** real Ollama with a small chat model — a query where reranking
  visibly changes the top-k order versus `rerank: off`.

## Non-goals

- No cross-encoder / dedicated rerank endpoint (Ollama has none); pointwise
  LLM-as-scorer only.
- No listwise scoring (unreliable permutations from small models).
- No reranking through the token-manager chokepoint (ADR-027: it is a retrieval
  primitive, budget-exempt by construction).
- No Apple/on-device-helper reranker in this slice (Ollama only; a future
  provider could join behind the same interface).
- No new Go dependency; no cloud.

## Requirements

- **FR-126** — When `retrieval.rerank` is enabled, `search.Search` fetches
  `top_k × rerank_overfetch` (default 3) hybrid candidates, scores them against
  the query with a local reranker, reorders, and returns the top-k; any reranker
  failure falls back to the original fused top-k (best-effort, never breaks
  search). Reranking is a retrieval primitive outside the token-manager
  chokepoint (ADR-027), local-only, budget-exempt, default off. Every retrieval
  caller inherits it via `Search`/`Retrieve` with no per-caller change.
- **FR-127** — The `ollama:<model>` reranker scores each candidate pointwise via
  Ollama `/api/generate` (bounded concurrency, per-call timeout), parsing a 0–10
  relevance number; `axon doctor` reports Ollama/model availability and warns on
  a malformed `retrieval.rerank` value. All processing is local (ADR-027).
