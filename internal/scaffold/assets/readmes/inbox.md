# 00 — Inbox

Frictionless capture. Drop any thought, link or snippet here without filing it.

AXON's **inbox-triage** automation later classifies each item and moves it into
the right PARA folder with wikilink-safe operations. Keep this folder shallow —
it is a queue, not a home.

## Capture

AXON watches this folder (the **capture** automation, every few minutes):

- **Paste a URL on its own line** in any note here — the page is fetched,
  cleaned and filed into `03-Resources/Knowledge/` with a summary. Your note
  is never modified.
- **Drop a file** (PDF, HTML, text) into this folder — it is ingested the
  same way and the original moves to `04-Archive/Capture/`.

Failures land in `.axon/review-queue.md`. Works from any device that syncs
the vault — share a URL into a note here from your phone and it's captured.
