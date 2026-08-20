# Known issues & triage *(living — last swept 2026-08-20)*

Issues found by audit, kept here until each graduates into a fix slice through
the normal cycle (brainstorm → spec where warranted → TDD → merge). **This file
is triage, not a backlog promise** — an entry records that we know, not that we
will. Severity is impact-based: HIGH = a shipped requirement is silently unmet
or unreleased-but-needed; MED = a real gap with a workaround; LOW = polish or
consistency; NOTE = recorded so nobody "fixes" a deliberate choice.

| # | Sev | Issue |
|---|-----|-------|
| 1 | HIGH | **`eval-drift` is registered but unschedulable.** It exists in the automation registry and catalog (FR-143), but has **no entry in `internal/config/starter.go` nor `axon.config.example.yaml`** — and `Schedulables()` only iterates automations present in the profile config, so it can never run on any default install. FR-143 (re-run evals when a gated local model's digest changes) is silently dead. **Fix:** add a disabled-by-default `eval-drift` seed to both config sources (remember the three count-assertions that bump) + a GUIDE/AUTOMATIONS row. Size: S. |
| 2 | HIGH | **Released-quality fixes sitting unreleased on `main`.** The sudo-lockout closure (root-start refusal, `EPERM` single-instance fix, fatal dashboard bind — FR-189) and the Review-tab error reporting fix (FR-190) are in `[Unreleased]` in the CHANGELOG. The lockout is a data-ownership footgun on every install until tagged. **Fix:** cut **v1.3.10**. Size: S (tag + CHANGELOG roll). |
| 3 | MED | **The Companion has never been verified on its declared macOS 26 floor.** `apps/companion/QA.md`: all verification happened on macOS 27; `MenuBarExtra(.window)` defeats UI automation, so popover layout, glass rendering, keyboard traversal, VoiceOver, Reduce-Transparency fallback and WCAG contrast are open human-verification items. **Fix:** the floor decision + QA pass is `docs/21-roadmap-macos27.md` **M5**. Size: S (discipline, not code). |
| 4 | MED | **A Sparkle auto-update has never been exercised end-to-end.** The appcast is signed and published, but no 0.1.0 → next update has ever been installed through it; the first real update is the riskiest possible moment to find out. **Fix:** part of docs/21 M5 — ship a trivial 0.1.1 and update through the UI before any update that matters. Size: S. |
| 5 | LOW | **`inbox-triage` tier mismatch between config sources.** `internal/config/starter.go:87` seeds `model: routine`; `axon.config.example.yaml:146` documents `model: classify`. Fresh installs therefore spend routine-tier tokens on a task the example (and the automation's classify-shaped purpose) says is classify-tier. **Fix:** pick one — recommendation: `classify` in both (cheaper, matches purpose) — and add a seed⇄example consistency test so the two files cannot drift again. Size: S. |
| 6 | LOW | **`axon reindex --embeddings` is a documented no-op.** The flag exists and prints a notice that embedding happens via the daemon/ingestion instead. A flag that explains why it does nothing should either do the thing or not exist. **Fix:** either implement synchronous re-embedding (it already exists as `core` machinery for `configure embeddings --reindex`) or remove the flag and point the error at the real path. Size: S. |
| 7 | LOW | **CFR namespace was un-reconciled with FR numbering.** The Companion PRD's CFR-01…91 grew beside FR-01…190 with no stated relationship. **Status: resolved by `docs/18-component-companion.md`** (CFRs bind the client, FRs the daemon; Companion-driven daemon behaviour has FRs — FR-184…188). Remaining check: no doc may cite a CFR as a daemon requirement. Size: done, spot-check only. |
| 8 | LOW | **Diagrams predate the Companion.** `docs/diagrams/` (regenerated 2026-07-10) shows no Companion client in the architecture or multi-client views. Nothing is wrong, but the picture is incomplete. **Fix:** underway in the 2026-08 docs-refresh cycle (`generate.mjs` is the deterministic source — edit it, never the SVGs). Size: S. |
| 9 | NOTE | **`docs/04`'s `automations:` map is a deliberately abbreviated subset.** It omits many automations by design; `axon automations` is authoritative and `internal/config/starter.go` + `axon.config.example.yaml` are the load-bearing seeds. Recorded so nobody "completes" the docs/04 map and creates a fourth thing to keep in sync. |

## Process

- New findings append here with the same fields; sweeps update the header date.
- An issue leaves this file only by shipping (link the release) or by being
  explicitly declined (record why, as #7/#9 do).
- HIGH items should be dispositioned before the next feature slice starts;
  they are small on purpose.
