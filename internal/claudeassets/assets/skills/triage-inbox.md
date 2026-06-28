---
name: triage-inbox
description: Classify 00-Inbox items into PARA and propose tags + links. Use when the user asks to "triage the inbox" or process captures.
---

# Triage the inbox

1. List `00-Inbox/` items with `vault_search` or by reading the folder.
2. For each item (or dispatch the `triager` subagent for a batch): decide the PARA
   destination, suggest tags, and find related notes via `vault_search`.
3. Write proposals to `.axon/review-queue.md` for the human to approve. Do **not**
   auto-move unless the user explicitly approves.
4. When approved, relocate notes with `vault_move` (wikilink-safe) — never `mv`.

Detected URLs in captures should be handed to `knowledge_ingest`.
