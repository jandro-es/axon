# macOS 27 M5 — Companion 0.2.0 release, floor decision, QA handoff (design)

**Status:** approved + executed 2026-08-20. Decisions: **floor stays macOS 26**
(Jandro's choice, against the raise-to-27 recommendation — recorded: ISSUES #3
therefore stays open until a QA pass on real macOS 26 hardware; 0.2.0 uses no
27-only API, so the declared floor remains truthful in API terms even though
unverified in practice); **cut and publish companion-v0.2.0** (recommended,
picked). No FR/CFR — release mechanics + QA disposition only.

## What was done headlessly

Developer ID signing (Filtercode LTD) + notarization (spinnaker-notary
keychain profile) + staple + validate → `Axon-0.2.0.zip` (universal, 3.1 MB,
"Notarized Developer ID"), with `Metadata.appintents` verified inside the
shipped bundle; Sparkle appcast extended and EdDSA-signed (two entries:
0.1.0, 0.2.0; enclosure → the companion-v0.2.0 GitHub release asset);
`companion-v0.2.0` tag + GitHub release with the zip. 224 Swift tests green.

## The two human steps (the whole point of M5)

1. **Sparkle end-to-end (closes ISSUES #4):** in the running 0.1.0 Companion,
   Check for Updates → install 0.2.0 through Sparkle's UI. First real update
   through the published feed.
2. **Eyes-on QA (burns down QA.md):** the 0.2.0 checklist in
   `apps/companion/QA.md` — Siri/Shortcuts verb visibility and spoken flows,
   plus the standing MenuBarExtra items (glass, VoiceOver, contrast,
   reduce-transparency) that UI automation cannot reach.

## Out of scope

macOS 26 floor verification (owner keeps the floor; the debt is tracked, not
erased); daemon changes (none); M6.
