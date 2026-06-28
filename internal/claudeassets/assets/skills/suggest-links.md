---
name: suggest-links
description: Run a semantic-similarity sweep to propose Zettelkasten links between related but unlinked notes. Use when the user asks to "find connections" or "suggest links".
---

# Suggest links

1. Run `automations_run` with `name: link-suggester` (or, for a single note, use
   `vault_search` on that note's content and read `vault_links` to see what's
   already linked).
2. Propose links only between notes that are semantically close but not yet
   connected. Write ranked suggestions to `.axon/review-queue.md`.
3. On approval, add `[[links]]` by editing the relevant notes (managed blocks via
   `vault_patch`, or with the user's confirmation for prose).

The heavy lifting is done in vector space (no/low token cost); keep it cheap.
