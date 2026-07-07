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
- **1.3 — it reaches you.** The brain acquires senses and a voice: meeting/voice capture, images and screenshots understood, briefings delivered where you already are, bounded web research landing as cited vault notes.

One sentence each: **1.2 deepens what AXON knows; 1.3 widens what AXON touches.** Both under the unchanged constitution: local-first, every model call through the chokepoint, every write wikilink-safe, everything toggleable, all-off still useful (S8).

## Why these two, why now

**The "Claude Code + Obsidian vault" pattern is commoditising.** Mid-2026 there are dozens of prompt packs and skill bundles doing triage/weekly-review/linking. AXON's moat was never the prompts — it's the daemon: the chokepoint, budgets, ledger, review queue, wikilink safety, reproducibility. 1.2/1.3 invest where prompt packs structurally can't follow: a memory index in SQLite, an eval-gated local model tier, a scheduler with channel delivery, and audio/vision pipelines.

**Memory is the acknowledged 2026 bottleneck.** The serious frameworks (Zep/Graphiti, Letta, Mem0) converge on the same shape: episodic vs semantic separation, timestamped facts with validity intervals and supersedence, background "sleep-time" consolidation. AXON already took the first step (1.1's contradiction → reconcile proposals). No vault-native product has the full shape; AXON can have it while keeping the vault — not the DB — as the source of truth.

**Proactivity is proven, but only the trustworthy kind.** OpenClaw's heartbeat-plus-channels pattern (100k stars in a week) and Google/Anthropic's briefing agents validated one thing clearly: scheduled briefings tied to *your actual data*, delivered where you already are, retain users; unsolicited AI chatter and unreviewed autonomous action churn them. AXON's review queue and policy engine are precisely the trust machinery the hosted players lack — 1.3 exports them to a channel near you.

**The senses became table stakes.** Tana's meeting agent is the most-raved AI-PKM feature of the cycle; Recall/NotebookLM made multimodal ingestion (screenshots, YouTube, podcasts) baseline; Meta's acquisition-and-shutdown of Limitless stranded a user base and made "local-first home for your recorded life" a real wedge. All of it maps onto seams AXON already has (ingestion pipeline, ADR-013 compiled-helper, ADR-026 OCR provider).

**Token independence compounds everything above.** Ollama's MLX backend (~2× on Apple Silicon) and mid-2026 local models put routine synthesis within reach; ADR-015 already routes `classify` to Apple FM/Ollama and deliberately gates `synthesis` behind validation. 1.2 builds the eval harness that turns that gate from a "no" into a promotion procedure — every routine call moved local is budget headroom for 1.3's new spenders (research, meetings, vision).

## What stays true

No cloud/server dependencies; no native app (dismissed 2026-07-03); no agent-driven `vault_move`; no deletes, ever; Markdown only; the vault rebuilds the DB, never the reverse (S9); channels and research egress are **opt-in, allow-listed, redacted, and off by default on the work profile**. AXON stays a daemon beside Obsidian, not a platform.
