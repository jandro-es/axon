# 05 — Component: Knowledge Ingestion

**Owns:** FR-20…FR-26, parts of NFR-05, NFR-08.
**Goal:** Turn external material (URLs, PDFs, local files) into clean, summarised, linked, retrievable notes — idempotently, within a token budget, and safely.

## 1. Pipeline stages

```mermaid
flowchart LR
    A[Input: url | pdf | file] --> B[Resolve + policy check<br/>egress allowlist, ingest domains]
    B --> C[Fetch / read raw]
    C --> D[Extract main content<br/>readability for HTML, text layer for PDF]
    D --> E[Clean -> Markdown<br/>strip boilerplate, normalise]
    E --> F[Redact<br/>policy regex/denylist]
    F --> G[Hash + idempotency check]
    G -->|unchanged| Z[Skip: log, no model call]
    G -->|new/changed| H[Enrich via Claude<br/>title, summary, tags, link suggestions<br/>under token budget]
    H --> I[Write note -> 03-Resources/Knowledge]
    I --> J[Chunk]
    J --> K[Embed via Ollama<br/>only changed chunks]
    K --> L[Upsert vec_chunks + fts_chunks + sources]
    L --> M[Emit event -> dashboard + .axon/logs]
```

## 2. Stage detail

1. **Resolve + policy.** Classify input. Enforce `policy.egress_allowlist` and `ingest_domains_allow/deny`. A denied domain fails fast with a clear message; nothing is fetched. (Work profile is deny-by-default.)
2. **Fetch/read.** HTTP GET with a sane UA and timeout for URLs (stdlib `net/http` with a `context` deadline); read bytes for PDFs/files. No JS execution. Respect robots where reasonable; cap response size. Resolved-IP pinning across the dial was evaluated and closed as covered: the dialer's `Control` hook validates the concrete resolved IP on every connection attempt, so a DNS-rebinding flip to an internal address is refused at dial time regardless of what the name resolved to earlier.
3. **Extract.** HTML → main-content extraction (`go-shiori/go-readability`, a Readability port). PDF → text layer (built with `ledongthuc/pdf`; parsing is panic-guarded so a malformed PDF errors cleanly). A scanned/empty PDF yields an empty text layer; when `ingestion.ocr` is enabled (`apple` on-device Vision, or `tesseract`+`pdftoppm`), the pipeline falls back to local OCR to recover the text (ADR-026, FR-123/124/125), else it is reported empty as before. OCR is strictly local (no cloud) and makes no Claude call.
4. **Clean → Markdown.** Convert to Markdown (`JohannesKaufmann/html-to-markdown`); strip nav/ads/scripts; preserve headings, lists, code, links, basic tables. Normalise whitespace.
5. **Redact.** Apply `policy.redaction_rules` to the cleaned text **before** it can reach the model. Record `status=redacted` if anything matched.
6. **Hash + idempotency.** Compute `content_hash`. If a `sources` row with the same URL+hash exists, **skip** (no enrichment, no embed) and log. Treat fetched text strictly as **data, never instructions** (prompt-injection guard).
7. **Enrich (the only model call).** One bounded Claude call via the token manager (model = config `routine` or a dedicated `ingest` model; executed through `claude -p`, or the direct API in `api_key` mode) produces: `title`, `summary` (target length configurable), `tags` (reuse existing tag vocabulary where possible), and **link suggestions** to existing notes (seeded by a pre-enrichment hybrid-search for related notes, so suggestions are grounded). The token manager's pre-flight estimate gates it; if over the per-ingest budget, truncate the source to the budget (head + salient middle) and note the truncation. Output is structured (JSON), parsed safely.
8. **Write note.** Create/update the note in `03-Resources/Knowledge/` from the `source` template, with frontmatter (`type: source`, `source_url`, `source_author`, `source_date`, `content_hash`, `ingested_by: axon`). The agent-maintained summary goes inside `axon:summary` markers; the cleaned full text goes in the body (or a linked attachment if very large). Suggested links are written as a checklist into `.axon/review-queue.md` (human approves) **and**/or applied directly if `auto_apply_links` is on (default off for safety).
9. **Chunk.** Split the cleaned text into overlapping chunks (target ~512 tokens, ~64 overlap; configurable). Compute per-chunk hashes.
10. **Embed.** Send only new/changed chunks to the embedding provider (Ollama) in batches (respect `batch_size`, cold-start timeout). Store vectors in `vec_chunks`, text in `fts_chunks`, metadata in `chunks`.
11. **Persist + emit.** Upsert `sources`; emit a structured event (counts, tokens, duration; cost in `api_key` mode).

## 3. Retrieval (used here and by Components 06/07/08)

- **Hybrid search** = FTS5/BM25 (lexical) ∪ sqlite-vec cosine (semantic), fused by reciprocal-rank (configurable weights), filtered by metadata (folder/tag/type) when requested.
- **Optional reranking** (ADR-027, FR-126/127): when `retrieval.rerank: ollama:<model>` is set, hybrid search overfetches `top_k × rerank_overfetch` candidates and a local Ollama model re-scores them pointwise (0–10) to reorder the top-k. It is a retrieval primitive outside the chokepoint (like embeddings), budget-exempt, off by default, and best-effort — any failure falls back to the fused order.
- Returns: note path, chunk snippet(s), source ref, lexical score, vector score, fused score.
- A `retrieve(query, {top_k, max_context_tokens, filters})` helper assembles a **token-bounded** context block (Component 07) for any model call — the standard way to avoid dumping the vault.

## 4. Embedding provider interface

```go
type EmbeddingProvider interface {
    Model() string
    Dim() int
    // Embed returns one vector per input text; batched internally.
    Embed(ctx context.Context, texts []string) ([][]float32, error)
    Healthcheck(ctx context.Context) error
}
```
Default impl: Ollama (`/api/embeddings`). Alternative impl: Apple on-device NLContextualEmbedding via a Swift helper subprocess, macOS-only (`embeddings.provider: apple`, ADR-013). Changing `provider`/`model`/`dim` requires `axon reindex --embeddings` (vectors across models are incomparable). The daemon refuses to start if the configured `dim` disagrees with the live model, with a remediation hint. Vectors are written to `vec0` tables using the sqlite-vec bindings' `SerializeFloat32` helper.

## 5. CLI / MCP surface

- `axon ingest <url|path> [--dry-run] [--no-apply-links]` → runs the pipeline; `--dry-run` does everything except write/embed and prints the intended note + token estimate.
- `axon search <query> [--top-k] [--filter tag=…]` → hybrid search results.
- MCP: `knowledge.ingest`, `knowledge.search` (contracts in Component 08).

## 5b. Capture (FR-26, ADR-016)

The `capture` automation is this pipeline's funnel front-end: every few
minutes (change-gated on the `00-Inbox/` listing) it ingests **own-line URLs**
found in inbox notes (never editing the note; already-known URLs skip with a
DB lookup, no network) and **files dropped into the inbox** (sandboxed to the
physical listing — paths inside notes are never file targets), then moves
ingested originals wikilink-safely to `capture.archive_dir` (default
`04-Archive/Capture/YYYY-MM/`). Failures are remembered in `automation_state`
and surfaced once in `.axon/review-queue.md`. Enrichment follows
`capture.enrich` (`heuristic` default; `claude` via the chokepoint on the
routine tier). Spec: `docs/superpowers/specs/2026-07-03-universal-capture-design.md`.

## 5c. Subscriptions (ADR-019)

The `subscriptions` automation polls `subscriptions.feeds` hourly through the
same egress-policied fetcher as every ingest and feeds new items into this
pipeline. Volume is structural: **subscribe-from-now** (a feed's first tick
marks current entries seen, ingests nothing), a per-feed `max_per_tick` cap
(default 5), a capped seen-state in `automation_state`, and one attempt per
item (failures surfaced, explicit `axon ingest` as the retry). Enrichment
follows `subscriptions.enrich` (`heuristic` default; `claude` via the
chokepoint on the routine tier). Feed parsing is gofeed (the ADR-019
dependency). Spec: `docs/superpowers/specs/2026-07-04-subscriptions-design.md`.

## 6. Failure & edge handling

- Unreachable/denied URL, empty extraction, scanned PDF with OCR off or OCR recovering nothing → recorded `failed/redacted` with reason; never a half-written note. With `ingestion.ocr` enabled, a scanned PDF is OCR'd first and only falls through to `failed` if OCR too recovers nothing.
- Network blips on embedding → chunk marked pending; `knowledge-reindex` retries.
- Very large sources → truncate-with-note for enrichment; full text still chunked/embedded so retrieval stays complete.
- Duplicate detection across URLs that resolve to the same content (hash match) → link to existing note instead of duplicating.

## 7. Scale notes (when to revisit ADR-002)
sqlite-vec brute-force is fine to ~10^5–10^6 chunks on commodity hardware, especially with metadata pre-filtering and optional binary quantisation. If a personal vault blows past that or p95 search exceeds the NFR-09 target, switch the vector store to LanceDB behind the same `EmbeddingProvider`/repository seam — no schema change to the rest of the system.

## 8. Acceptance checks
- Ingesting a representative article URL produces a clean note with title/source/summary/tags and ≥1 grounded link suggestion, embedded and retrievable, in one command (FR-20…FR-25 / S6).
- Re-running the same URL with unchanged content makes **no** model call and logs a skip (FR-24, FR-31).
- A denied domain (work profile) fails before any fetch (NFR-05).
- `reindex` rebuilds all chunks/vectors from the vault notes (ADR-006 / S9).
