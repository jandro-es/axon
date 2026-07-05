# B1 — Pluggable ANN vector index (IVF-flat) — Design

**Status:** approved (design), pending spec review
**Date:** 2026-07-05
**Roadmap:** `docs/14-roadmap-1.1.md` Phase B, slice B1
**New IDs:** ADR-025 (ADR-024 taken by capture); FR-113, FR-114, FR-115

## Goal

Make AXON's vector-search leg **pluggable** behind the existing `db` seam and add
an in-house **IVF-flat** approximate index, opt-in via `retrieval.index: ann`.
Small vaults keep exact brute-force results (auto-fallback); large vaults get a
measurable latency win. The single-file SQLite promise holds — the index is an
in-file table + column, fully rebuilt by `axon reindex`. No server, no new
runtime dependency.

## Non-goals

- No HNSW, no product quantization, no third-party ANN library.
- No change to the lexical (FTS5/bm25) leg or to RRF fusion.
- No new config knob for cluster count `k` (auto = √N); only `threshold` + `nprobe`.
- No behaviour change when `retrieval.index` is unset/`brute` (the default).

## The seam

`internal/db/search.go` already isolates the vector leg in
`vectorCandidates(ctx, q, query, limit) ([]int64, map[int64]float64, error)`,
called by `HybridSearch` and fused with the lexical leg by reciprocal rank
fusion. B1 promotes this to an interface.

```go
// internal/db/vindex.go
type VectorIndex interface {
    // Candidates returns up to `limit` chunk ids in descending cosine
    // similarity to query, plus the similarity for each returned id.
    Candidates(ctx context.Context, q DBTX, query []float32, limit int) ([]int64, map[int64]float64, error)
}
```

- **`BruteIndex{}`** — today's exact full scan, moved verbatim. Default (nil ⇒ brute).
- **`IVFIndex{Threshold, NProbe int}`** — probes; falls back to `BruteIndex` when
  centroids are absent or `CountVectors < Threshold`.

`HybridSearch` gains `SearchOpts.Index VectorIndex` (nil ⇒ `BruteIndex{}`). Lexical
candidates, fusion, hydration, and snippets are unchanged, so a query's hybrid
result differs only by which chunk ids the vector leg proposes.

**Dependency rule:** `db` stays a leaf. Config values (`Index`, `Threshold`,
`NProbe`) flow in through `SearchOpts` / `IVFIndex` fields — `db` never imports
`config`.

## Data model — migration `0004_vec_ivf.sql`

```sql
CREATE TABLE vec_centroids (
    id     INTEGER PRIMARY KEY,   -- centroid ordinal [0,k)
    dim    INTEGER NOT NULL,
    model  TEXT    NOT NULL,      -- embedding model of the space it clusters
    vector BLOB    NOT NULL       -- little-endian float32 (same codec as vec_chunks)
);

ALTER TABLE vec_chunks ADD COLUMN centroid INTEGER;   -- nullable; NULL = unassigned
CREATE INDEX idx_vec_chunks_centroid ON vec_chunks(centroid);
```

Both `vec_centroids` and the `centroid` column are 100% derivable from
`vec_chunks.embedding`. ADR-006 holds: this is *doubly* derived (vault → vec_chunks
→ centroids). `axon reindex` rebuilds it.

**No vector is ever lost.** The probe query includes an overflow clause:

```sql
SELECT chunk_id, embedding FROM vec_chunks
 WHERE centroid IN (/* nprobe nearest centroid ids */) OR centroid IS NULL;
```

A freshly-ingested, not-yet-assigned chunk (`centroid IS NULL`) is always scanned,
so recall never silently drops for new material. `UpsertChunkVector` assigns the
nearest centroid on insert **when centroids exist** (one pass over k), keeping the
overflow list small between reindexes; correctness does not depend on that pass
having run.

## Build — `db.BuildIVF(ctx, database, model string) error`

Called by `reindex` after the embeddings pass when `retrieval.index == "ann"`.

1. Load all embeddings for `model` from `vec_chunks`. If `N == 0`, clear centroids
   and return (nothing to index).
2. `k = clamp(round(sqrt(N)), 16, 4096)`.
3. **Spherical k-means:** L2-normalize each vector (cosine ≡ dot on the unit
   sphere), Lloyd's iteration re-normalizing centroids each round, bounded to 15
   iterations or until assignments stop changing.
4. **Deterministic — no RNG.** Initial centroids are the k evenly-strided vectors
   at indices `i*N/k` (`i in [0,k)`). Reindex is reproducible; parity tests are
   stable. `math/rand` is not used anywhere in the build.
5. In one transaction: replace `vec_centroids` with the new set and write every
   `vec_chunks.centroid`.

An empty-centroids state (fresh flip to `ann` before a reindex) is valid:
`IVFIndex` sees 0 centroids and delegates to brute — correct, never a crash.

## Probe — `IVFIndex.Candidates`

1. If `CountCentroids == 0` **or** `CountVectors < Threshold` → delegate to
   `BruteIndex{}.Candidates` (exact). This is the auto-fallback.
2. Else: `Cosine(query, centroid)` for each centroid; take the `NProbe` nearest
   centroid ids.
3. Run the `centroid IN (…) OR centroid IS NULL` scan, `Cosine` each blob, sort
   descending (stable id tie-break, matching brute), truncate to `limit`.

**Locked invariant:** at `NProbe >= k` the probe visits every list, returning
bit-identical ordered ids to brute. The parity contract is provable, not hoped.

## Config — `internal/config/types.go`

```go
type RetrievalConfig struct {
    TopK             int       `yaml:"top_k" validate:"required,min=1"`
    MaxContextTokens FlexInt   `yaml:"max_context_tokens" validate:"required"`
    Index            string    `yaml:"index,omitempty" validate:"omitempty,oneof=brute ann"` // default brute
    ANN              ANNConfig `yaml:"ann,omitempty"`
}

type ANNConfig struct {
    Threshold int `yaml:"threshold,omitempty"` // min vectors before ann engages; 0 → default 10000
    NProbe    int `yaml:"nprobe,omitempty"`    // clusters probed per query; 0 → default 8
}
```

Helpers (mirroring the pointer-default pattern): `RetrievalConfig.IndexMode()`
returns `"brute"` when empty; `ANNConfig.ThresholdOr()` → 10000 when 0;
`ANNConfig.NProbeOr()` → 8 when 0. Default `index: brute` keeps existing vaults
unchanged and satisfies S8 (fresh clone, everything off, still works).

## Wiring

- **`internal/search`** — `Searcher` gains `Index string`, `Threshold`, `NProbe`
  fields set at construction from `RetrievalConfig`. `Search` builds the concrete
  `db.VectorIndex` (`BruteIndex{}` or `IVFIndex{…}`) and passes it via
  `SearchOpts.Index`. `search.New` signature extends to accept the retrieval
  config; `cmd/axon` and `internal/mcp` call sites updated.
- **`internal/core/reindex.go`** — after the embeddings pass, if `index == ann`,
  call `db.BuildIVF`. Fulfils the gate "axon reindex fully rebuilds the index."
- **`internal/core/doctor.go`** — new `annIndexCheck`:
  - `index: brute` and `CountVectors > threshold` → **Warn**: "N vectors indexed;
    set `retrieval.index: ann` and run `axon reindex` for faster search."
  - `index: ann` and centroids missing/empty → **Warn**: "ANN enabled but index not
    built; run `axon reindex`."
  - otherwise → **OK**.

## Testing

- **Parity (small N):** corpus below threshold, `ann` mode → ordered ids identical
  to brute. (`internal/db`)
- **Parity (nprobe=k):** corpus above threshold, `nprobe >= k` → ids identical to
  brute — proves the visit-every-list invariant.
- **Recall:** synthetic corpus with planted nearest neighbours; `ann` (default
  nprobe) returns the true top-1 for each planted query.
- **Overflow safety:** build index, then `UpsertChunkVector` a new near-duplicate
  vector; probe finds it even though a rebuild has not run.
- **Rebuild:** `BuildIVF` → `CountCentroids > 0`; `reindex` (ann mode) leaves a
  populated `vec_centroids`.
- **Determinism:** `BuildIVF` twice on the same corpus → identical centroids.
- **Config:** `IndexMode()`/`ThresholdOr()`/`NProbeOr()` defaults; validator
  rejects `index: bogus`.
- **Doctor:** the three branches above.

All test suites run with `env -u FORCE_COLOR go test ...`.

## FR mapping

- **FR-113** — Pluggable vector-index seam: `retrieval.index: brute | ann`,
  `BruteIndex` default, no behaviour change when unset.
- **FR-114** — In-house IVF-flat index (build + probe) with auto-fallback below
  `threshold` and the identical-results-at-small-N / nprobe=k contract; rebuilt by
  `axon reindex`; single-file (in-DB) with no server dependency.
- **FR-115** — `doctor` suggests enabling `ann` past the vault-size threshold and
  warns when `ann` is enabled but the index is unbuilt.

## ADR-025 (summary to add to docs/02)

**Decision:** Extend ADR-010's vector seam with a pluggable `VectorIndex`
interface and an in-house IVF-flat approximate index (`retrieval.index: ann`),
persisted in-file (`vec_centroids` + a `centroid` column), rebuilt by `reindex`.
**Why:** brute-force cosine is O(N) per query and degrades past ~10^5 vectors; IVF
gives a sub-linear candidate set with tunable recall while keeping the single
pure-Go SQLite file. **Alternatives rejected:** HNSW / a third-party ANN library
(younger deps, graph serialization, heavier than the 10^4–10^5 range needs); a
server vector DB (violates the standing guardrail). **Trade-off:** approximate
recall above the threshold, bounded by `nprobe` and made exact at `nprobe >= k`;
`brute` remains the default and the source-of-truth fallback.
