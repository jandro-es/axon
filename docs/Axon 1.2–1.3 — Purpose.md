---
title: Axon 1.2–1.3 — Purpose
type: project
status: draft
tags: [axon, roadmap, purpose]
---

# AXON 1.2 & 1.3 — Purpose

> Planning only — nothing here is built; 1.1 is still closing. Written 2026-07-05 from the repo docs (`~/Projects/axon/docs/`, esp. `14-roadmap-1.1.md`) plus fresh market/technical research ([[Axon 1.2–1.3 — Research Notes]]). Companion notes: [[Axon 1.2–1.3 — PRD]], [[Axon 1.2–1.3 — Development Plan]]. When a release is adopted, its content graduates into the repo's design cycle (brainstorm → spec → ADR → FR rows) as `docs/15-roadmap-1.2.md` etc. — these vault notes are the thinking layer, not the contract.

## The arc

- **1.0 — it maintains.** The self-maintaining vault: capture, ingestion, search, fifteen automations, memory, budgets, observability. The complete v1 contract.
- **1.1 — it answers.** Grounded-or-silent `ask` with wikilink citations on three surfaces, standing research questions, ANN retrieval, capture endpoint, OCR, memory contradiction proposals.
- **1.2 — it remembers and reasons, cheaply.** A real memory architecture (temporal facts, supersedence, consolidation) instead of an append-only log; the intelligence to use it (entities, pulse, contradiction-aware answers); and validated local models doing the routine thinking so Claude is reserved for what deserves it.
- **1.3 — it perceives and researches.** The brain widens what it can take in: images and screenshots understood as searchable notes, long-form video/podcast URLs turned into cited knowledge, and bounded web research landing as cited vault notes.

One sentence each: **1.2 deepens what AXON knows; 1.3 widens what AXON can take in.** Both under the unchanged constitution: local-first, every model call through the chokepoint, every write wikilink-safe, everything toggleable, all-off still useful (S8).

> **Scope note (2026-07-10).** 1.3 originally scoped a wider "reach" theme (outward channels, voice/meeting capture, calendar). Those surfaces were **removed** to focus the release on perceiving richer inputs and researching them — the two slices below the line "1.3 — it perceives and researches" are what remains. The removed outward-channel and voice work is **not currently scheduled**.

## Why these two, why now

**The "Claude Code + Obsidian vault" pattern is commoditising.** Mid-2026 there are dozens of prompt packs and skill bundles doing triage/weekly-review/linking. AXON's moat was never the prompts — it's the daemon: the chokepoint, budgets, ledger, review queue, wikilink safety, reproducibility. 1.2/1.3 invest where prompt packs structurally can't follow: a memory index in SQLite, an eval-gated local model tier, a vision/multimodal ingestion pipeline, and bounded, budgeted research.

**Memory is the acknowledged 2026 bottleneck.** The serious frameworks (Zep/Graphiti, Letta, Mem0) converge on the same shape: episodic vs semantic separation, timestamped facts with validity intervals and supersedence, background "sleep-time" consolidation. AXON already took the first step (1.1's contradiction → reconcile proposals). No vault-native product has the full shape; AXON can have it while keeping the vault — not the DB — as the source of truth.

**Multimodal ingestion became table stakes.** Recall/NotebookLM made ingesting screenshots, YouTube and podcasts baseline expectations for a knowledge tool; a PKM that only reads text and PDFs now feels narrow. This maps directly onto seams AXON already has — the ingestion pipeline, the ADR-013 compiled-helper pattern, the ADR-026 OCR provider — so extending to images and captioned media is an incremental reach, not a new architecture, and it stays local-first (Ollama/Apple vision, no cloud).

**Bounded research is where the trust machinery pays off.** The hosted "research agents" of the cycle fetch the open web with little visibility into what left the machine or what it cost. AXON's egress policy engine, redaction, per-run budgets, and citation-into-the-vault discipline are precisely the guardrails those products lack — so graduating standing questions into bounded, budgeted, cited research runs is a differentiator only the daemon can offer, not a me-too feature.

**Token independence compounds everything above.** Ollama's MLX backend (~2× on Apple Silicon) and mid-2026 local models put routine synthesis within reach; ADR-015 already routes `classify` to Apple FM/Ollama and deliberately gates `synthesis` behind validation. 1.2 builds the eval harness that turns that gate from a "no" into a promotion procedure — every routine call moved local is budget headroom for 1.3's new spenders (vision descriptions, research).

## What stays true

No cloud/server dependencies; no native app (dismissed 2026-07-03); no agent-driven `vault_move`; no deletes, ever; Markdown only; the vault rebuilds the DB, never the reverse (S9); vision/perception runs locally and research egress is **opt-in, allow-listed, redacted, and off by default on the work profile**. AXON stays a daemon beside Obsidian, not a platform.
