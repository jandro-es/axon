---
name: run-daily-log
description: Synthesise today's daily note into a structured summary and roll tasks forward. Use at end of day, or when the user asks to "wrap up the day" / "write the daily log".
---

# Daily log

1. `vault_read` today's daily note (`Daily/YYYY-MM-DD.md`). If it doesn't exist or
   has no activity, say so and stop.
2. Produce 3–5 bullets capturing decisions, progress and open tasks; carry
   unfinished tasks forward.
3. Write the summary with `vault_patch` into the note's `axon:summary` block.
4. Link the day to relevant `[[01-Projects/...]]` / `[[MOCs/...]]` where clear.

This mirrors the headless `daily-log` automation — running it here is equivalent.
Never edit outside the managed block.
