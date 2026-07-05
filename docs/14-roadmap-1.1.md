# 14 — Roadmap 1.1 *(planning only — nothing here is built)*

1.0 built the self-maintaining vault: capture, ingestion, search, fifteen
automations, memory, budgets, observability — the complete v1 contract
(FR-01…107, NFR-01…14). **1.1 makes it answer, connect, and anticipate**:
grounded intelligence on the substrate 1.0 built, under the same principles —
local-first, everything through the chokepoint, every write wikilink-safe, and
a system with all automations off still runs and is useful (S8).

This document is the 1.1 *plan*, in the style of the
[v1 build roadmap](11-build-roadmap.md). Every slice below still requires its
own design cycle (brainstorm → spec → **ADR** → FR rows) before any code; the
FR/ADR numbers here are **provisional** (current maxima: FR-107, ADR-022) and
are assigned for real in those cycles. A slice isn't "done" until its
acceptance gate passes.

## Phase A — Ask your vault *(the headline; build first)*

### A1 — `axon ask` (M) · FR-108…110 *(built)*
**Build:** retrieval-augmented answers with `[[wikilink]]` citations. Retrieve
via the existing hybrid `search` facade; build a bounded context from
`retrieval.top_k` / `max_context_tokens`; one synthesis-tier chokepoint call;
render the answer with the source notes cited. **Grounded-or-silent:** when
retrieval returns nothing relevant, `ask` says so and stops — it never answers
from the model's own knowledge.
**Gate:** every answer cites ≥1 vault source or refuses; the call is ledgered;
`--json` emits answer + citations; scratch-vault smoke.

### A2 — `vault_ask` MCP tool + dashboard Ask panel (M) · FR-111…112, ADR-023 *(built)*
**Build:** the same engine exposed through the service layer as an MCP tool
(read-only toward the vault) and as a dashboard panel. The dashboard half
needs **ADR-023**: it extends ADR-020's enumerated mutation surface with a
*token-spending* endpoint — vault-read-only but chokepoint-governed spend
triggered from the browser — behind the same loopback + custom-header
CORS-preflight guard as review actions.
**Gate:** Ask works from Claude Desktop (tool) and the dashboard (panel); the
spend appears in the ledger and the Tokens chart within 5s; the endpoint is
unreachable cross-origin.

### A3 — Standing research questions (S) · FR-116/117 (no ADR) *(built)*
**Build:** a managed vault note (`03-Resources/Research Questions.md`,
`axon:questions` block). The weekly knowledge-digest attempts answers from
that week's new material — citations and a confidence marker per answer;
unanswered questions persist to the next week.
**Gate:** a seeded question is answered (or explicitly not) in the next digest
run with sources; removing the note disables the feature cleanly.

## Phase B — Retrieval scale & quality

### B1 — ANN index behind the ADR-010 seam (M) · FR-113–115, ADR-025 *(built)*
**Build:** a pluggable vector index (`retrieval.index: brute | ann`) behind the
existing repository seam. Identical-results contract at small N (tested);
`doctor` suggests switching past a vault-size threshold. The single-file
SQLite promise holds: in-file or sidecar-rebuildable index; the "no
server-based vector DB" guardrail stands.
**Gate:** results parity on a small corpus; measurable latency win on a
synthetic 20k-note corpus; `axon reindex` fully rebuilds the index.

### B2 — Optional local reranker (S) · provisional FR-115
**Build:** `retrieval.rerank: off | "ollama:<model>"` — rerank top-k×3 → top-k
through an Ollama model, budget-exempt and ledgered per ADR-015.
**Gate:** rerank on/off changes ordering, never candidate safety; zero Claude
tokens; graceful pass-through when Ollama is down.

### B3 — Near-duplicate merge proposals (M) · provisional FR-116
**Build:** an embedding sweep reusing the resurfacer primitives
(`db.NoteMeanVectors`/`db.Cosine`) and the shared proposal-memory helpers,
proposing note merges to the review queue. **Accept semantics get their own
design pass** — merge is the closest thing to a destructive op AXON has;
direction: merged note + originals archived + inbound links rewritten, never
deletion.
**Gate:** duplicates surface once (proposal memory); an accepted merge leaves
zero broken links and both originals recoverable from the archive.

## Phase C — Memory & entity intelligence

### C1 — Memory consolidation with contradiction handling (S) · FR-118/119/120 (no ADR) *(built)*
**Build:** memory-distill upgrade — a new entry that contradicts an existing
MEMORY entry becomes a review-queue `reconcile` proposal instead of silent
coexistence; accept supersedes (tombstones the old entry, prepends the new),
dismiss keeps the old and drops the new. Detection folds into the existing
single synthesis call (no extra spend). Spec in
`docs/superpowers/specs/2026-07-05-memory-consolidation-design.md`.
**Gate:** a seeded contradiction produces one proposal; accepting rewrites the
managed block only (tombstone + new entry); dismissing keeps the old entry.

### C2 — Entity pages (M) · provisional FR-118…119, ADR-025
**Build:** classify-tier (local-routable) extraction of people/projects from
new notes into auto-maintained entity notes with `axon:mentions` managed
blocks — the vault grows an index of *who* and *what*, not just notes.
**Gate:** mentions accrue wikilink-safely; human prose on entity pages is
never touched; extraction runs on new material only (content-hash gated).

### C3 — Project pulse (S) · provisional FR-120
**Build:** a weekly automation reading `01-Projects` + USER goals; writes a
pulse block (progress, stalls, next actions) and nudges stale projects via
the review queue.
**Gate:** a project untouched for N weeks produces one nudge (with proposal
memory); pulse degrades to facts-only under budget pressure.

## Phase D — Capture & ingestion reach

### D1 — Localhost capture endpoint + bookmarklet/Shortcuts (S) · FR-121…122, ADR-024 *(built)*
**Build:** `POST /api/capture` on the dashboard (same guard pattern as review
actions; covered by the ADR-023 surface extension from A2), dropping items
into `00-Inbox/` where the **existing capture automation** takes over —
minimal new machinery. Docs ship a bookmarklet and a macOS Shortcuts recipe.
**Gate:** a bookmarklet click lands a URL in the inbox and it is ingested on
the next capture tick; the endpoint is loopback-only and refuses cross-origin.

### D2 — OCR for scanned PDFs (M) · FR-123/124/125, ADR-026 *(built)*
**Build:** closes docs/05's explicitly deferred v1 item. A local-only OCR
provider seam — Apple Vision on-device via the ADR-013 compiled-helper
pattern, or tesseract when present (`ingestion.ocr: off | apple | tesseract`).
**Gate:** a scanned PDF yields a searchable note locally (no egress); `off`
preserves today's clean "empty extraction, reported" behaviour.

## Suggested build order & sizing

| Order | Slice | Size | Why here |
|-------|-------|------|----------|
| 1 | A1 `axon ask` | M | The headline; everything else compounds it. |
| 2 | A2 MCP tool + Ask panel | M | Same engine, two more surfaces; carries ADR-023. |
| 3 | D1 capture endpoint | S | Cheap, high leverage; reuses ADR-023 + existing funnel. |
| 4 | B1 ANN index | M | Keeps `ask`/search fast as the vault grows. |
| 5 | A3 research questions | S | Digest upgrade riding on A1's grounding. |
| 6 | C1 memory consolidation | S | Small, sharp intelligence win. |
| 7 | D2 OCR | M | Closes the last deferred v1 ingestion item. |
| 8 | B2 reranker | S | Quality knob once ANN lands. |
| 9 | C2 entity pages | M | Biggest new writer; benefits from all retrieval work. |
| 10 | C3 project pulse | S | Rides on C2's project entities. |
| 11 | B3 merge proposals | M | Needs its own destructive-op design pass; last. |

**Release criterion:** 1.1.0 ships when **Phase A is complete plus at least two
other phases**; remaining slices roll to 1.2 without renumbering.

## Cross-cutting rules

- Every model call through the chokepoint; classify/routine work stays
  local-routable (ADR-015); budget-exempt local calls remain fully ledgered.
- Every new writer is wikilink-safe (managed blocks or additive; no deletes).
- Every feature independently toggleable; all-off still runs and is useful (S8).
- Each slice: brainstorm → spec → ADR → FR rows → TDD plan → inline execution →
  live smoke → merge + push (the standing cycle).

## Explicit non-goals for 1.1

Cloud/server dependencies; agent-driven `vault_move`; a local synthesis tier
(the ADR-015 validation gate stands); a native macOS app (dismissed
2026-07-03); email/calendar sync.
