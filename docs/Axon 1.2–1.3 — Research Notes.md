---
title: Axon 1.2–1.3 — Research Notes
type: resource
status: reference
tags: [axon, research, pkm, llm]
---

# AXON 1.2–1.3 — Research Notes (2026-07-05)

Condensed evidence behind [[Axon 1.2–1.3 — Purpose]] and [[Axon 1.2–1.3 — PRD]]. Two sweeps: the AI-PKM landscape (this session) and the hybrid local/cloud LLM stack (shared with [[Spinnaker — Market & Technical Research]]).

## AI-PKM landscape (mid-2026)

- **Obsidian ecosystem:** core still ships no native AI; investments are Bases, Web Clipper, and a new official **CLI** (early-2026, insiders) — a future automation surface for AXON. Copilot for Obsidian = best all-round chat plugin; **Smart Connections** owns ambient related-notes and moved to ~$20/mo (churn) — AXON already has the embeddings to give that away. The "**Claude Code as vault agent**" pattern is commoditising fast (dozens of prompt/skill packs), but they are prompts, not daemons — no chokepoint, budgets, review queue, or wikilink safety. ([obsidian.md/roadmap](https://obsidian.md/roadmap/), [plugin radar](https://wetheflywheel.com/en/radar/obsidian-ai-plugins/))
- **Standalone AI-PKM:** **NotebookLM** is the velocity leader (agentic Deep Research with cited reports, saved conversations, quizzes with progress). **Tana's meeting agent** (transcript → summary → action items auto-linked to person/project records in <7 min) is the most-raved feature of the cycle. **Recall** validates multimodal saves (YouTube/podcasts/PDFs) + AI spaced-repetition on your own content. **Khoj** is the closest OSS analogue (self-hosted, scheduled automations, deep research, channel clients). ([Workspace updates](https://workspaceupdates.googleblog.com/2026/03/new-ways-to-customize-and-interact-with-your-content-in-NotebookLM.html), [Tana review](https://aiproductivity.ai/tools/tana/), [recall.it](https://www.recall.it/), [khoj](https://github.com/khoj-ai/khoj))
- **Personal-agent daemons:** **OpenClaw** (100k stars in week one, Jan 2026) proved the shape AXON already has — long-lived daemon + cron heartbeat — plus the part AXON lacks: **delivery over channels you already use** (Telegram/WhatsApp). Google "CC" and Claude Orbit validated no-prompt daily briefings. Field lesson: data-grounded scheduled briefings retain; unsolicited AI chatter and unreviewed autonomy churn. AXON's review queue is the missing trust layer of the hosted players. ([openclaw.ai](https://openclaw.ai/), [gemini proactive](https://blog.google/innovation-and-ai/products/gemini-app/next-evolution-gemini-app/))
- **Memory tech SOTA:** "memory, not the model, is the 2026 bottleneck." Convergent shape across Mem0 / **Zep-Graphiti** / Letta / Cognee: episodic vs semantic separation, **timestamped facts with validity intervals + supersedence** (Graphiti leads LongMemEval 63.8% vs Mem0 49.0%), background "sleep-time" consolidation. Nothing vault-native does this. ([benchmark](https://particula.tech/blog/agent-memory-frameworks-tested-mem0-vs-zep-letta-cognee-2026), [Zep paper](https://arxiv.org/abs/2501.13956))
- **Capture:** voice memos with auto-transcription, share-sheet/mobile, messaging-channel capture, email-in and screenshot understanding are table stakes. **Meta acquired Limitless (Dec 2025) and shut the pendant/Rewind down** — stranded users make "local-first home for your recorded life" a wedge (Screenpipe is the OSS torch-bearer). ([wearable status](https://wearablexp.com/smart-wearables/limitless-ai-pendant-features-concerns/), [screenpipe](https://screenpipe.com/blog/best-ai-wearables-memory-2026))
- **Resurfacing:** FSRS is the modern SRS standard (20-40% fewer reviews at equal retention); AI-generated review items on your own notes proven by Recall/RemNote. Nobody does *agentic* resurfacing ("this contradicts what you wrote in March") — open ground for R9/R2. ([remnote](https://www.remnote.com/feature/spaced-repetition))

## Hybrid LLM stack (shared with Spinnaker research)

- **Ollama** v0.30.x with **MLX backend** on Apple Silicon (~2× throughput since v0.19); mature JSON-schema structured output; Qwen3/Gemma-class mid-size models credible for routine summarisation; **Qwen-VL class** for vision; nomic-embed-text still the embedding default. → R5 (eval-gated routine tier) and H3 (vision) are realistic now.
- **Apple Foundation Models:** on-device ~3B, 4k shared context, `@Generable` guided generation — already AXON's `classify` tier option (ADR-015). **AFM 3 with image input** ships ~fall 2026 (macOS 27) — H3's upgrade path behind the same seam. Requires Apple Intelligence on M1+; Ollama remains the portable fallback.
- **Claude subscription terms volatile through 2026:** Feb consumer-OAuth ban in third-party products → May credit-pool announcement → **June 15 pause**; current official status: Agent SDK / `claude -p` draw from subscription limits. AXON's single-user, own-subscription posture is the supported case; the volatility reinforces R5's token-independence direction. ([support article](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan))

## How the top field gaps map to slices

| Field gap (ranked by user value) | Slice |
|---|---|
| Temporal memory + contradiction handling | R1, R2 |
| Proactive channel delivery + capture-back | H1 |
| Meeting/voice pipeline | H2 |
| Multimodal ingestion | H3 |
| Agentic deep research into the vault | H4 |
| AI-scheduled resurfacing/review | R9 |
| Ambient related-notes surface | R8 |
| Obsidian CLI / Bases integration | H7 |
| Continuous-capture import wedge | H6 |
| Contradiction-aware ask | R2 |
