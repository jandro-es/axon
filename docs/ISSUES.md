# Known issues & triage *(living — last swept 2026-08-20)*

Issues found by audit, kept here until each graduates into a fix slice through
the normal cycle (brainstorm → spec where warranted → TDD → merge). **This file
is triage, not a backlog promise** — an entry records that we know, not that we
will. Severity is impact-based: HIGH = a shipped requirement is silently unmet
or unreleased-but-needed; MED = a real gap with a workaround; LOW = polish or
consistency; NOTE = recorded so nobody "fixes" a deliberate choice.

| # | Sev | Issue |
|---|-----|-------|
| 1 | ~~HIGH~~ | **SHIPPED in v1.3.10.** `eval-drift` was registered but unschedulable (no seed in `starter.go` nor the example yaml; `Schedulables()` only iterates configured automations). Fixed with disabled-by-default seeds in both sources, plus a new invariant test (`internal/automations/seeds_test.go`) that every registered automation is seeded in both — which also caught `merge-proposals` missing from the example yaml. |
| 2 | ~~HIGH~~ | **SHIPPED in v1.3.10.** The sudo-lockout closure (FR-189) and Review-tab error reporting (FR-190) were sitting in `[Unreleased]`; v1.3.10 (2026-08-20) tagged them together with the eval-drift fix (#1). |
| 3 | MED | **The Companion has never been verified on its declared macOS 26 floor.** `apps/companion/QA.md`: all verification happened on macOS 27; `MenuBarExtra(.window)` defeats UI automation, so popover layout, glass rendering, keyboard traversal, VoiceOver, Reduce-Transparency fallback and WCAG contrast are open human-verification items. **Fix:** the floor decision + QA pass is `docs/21-roadmap-macos27.md` **M5**. Size: S (discipline, not code). |
| 4 | MED | **A Sparkle auto-update has never been exercised end-to-end.** The appcast is signed and published, but no 0.1.0 → next update has ever been installed through it; the first real update is the riskiest possible moment to find out. **Fix:** part of docs/21 M5 — ship a trivial 0.1.1 and update through the UI before any update that matters. Size: S. |
| 5 | ~~LOW~~ | **FIXED on `main` (2026-08-20).** `inbox-triage` seeded `routine` in `starter.go` but `classify` in the example yaml. Resolved to `classify` in both (cheaper, matches the automation's purpose), and `seeds_test.go` now also asserts model/schedule consistency between the starter and the example's personal profile so the two files cannot drift again. |
| 6 | ~~LOW~~ | **FIXED on `main` (2026-08-20) — the flag was real, the docs were stale.** Investigation showed `axon reindex --embeddings` already performs a full forced re-embed (`core.ReembedPending(…, forceAll=true)` + vector-index refresh + memory-fact embeddings) in both TTY and plain flows; only the flag's help text ("currently a no-op with a notice") and the docs echoing it were out of date. Help text, COMMANDS.md and GUIDE §15 corrected — no behaviour change. |
| 7 | LOW | **CFR namespace was un-reconciled with FR numbering.** The Companion PRD's CFR-01…91 grew beside FR-01…190 with no stated relationship. **Status: resolved by `docs/18-component-companion.md`** (CFRs bind the client, FRs the daemon; Companion-driven daemon behaviour has FRs — FR-184…188). Remaining check: no doc may cite a CFR as a daemon requirement. Size: done, spot-check only. |
| 8 | LOW | **Diagrams predate the Companion.** `docs/diagrams/` (regenerated 2026-07-10) shows no Companion client in the architecture or multi-client views. Nothing is wrong, but the picture is incomplete. **Fix:** underway in the 2026-08 docs-refresh cycle (`generate.mjs` is the deterministic source — edit it, never the SVGs). Size: S. |
| 9 | NOTE | **`docs/04`'s `automations:` map is a deliberately abbreviated subset.** It omits many automations by design; `axon automations` is authoritative and `internal/config/starter.go` + `axon.config.example.yaml` are the load-bearing seeds. Recorded so nobody "completes" the docs/04 map and creates a fourth thing to keep in sync. |

## Process

- New findings append here with the same fields; sweeps update the header date.
- An issue leaves this file only by shipping (link the release) or by being
  explicitly declined (record why, as #7/#9 do).
- HIGH items should be dispositioned before the next feature slice starts;
  they are small on purpose.
