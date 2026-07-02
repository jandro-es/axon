# Operations overhaul — Charm TUI, self-update lifecycle, vault migration

**Date:** 2026-07-02
**Status:** approved (brainstormed + user-approved)
**Traces to:** FR-01/FR-02 (init/bootstrap), FR-05 (doctor), S1–S9 (PRD success criteria: smooth, verbose, idempotent ops), NFR-05 (no TTY-hang in headless paths)
**Delivery:** one spec, two sequential implementation plans (A: TUI core; B: lifecycle + vault move + docs)

## Goal

Make installing, updating, configuring, and removing AXON — and moving the
vault — smooth one-command experiences with simple interactive choices,
while preserving every headless/scripted contract (`--json`, non-TTY, exit
codes) exactly as they are.

## Decisions

1. **Charm stack (ADR-014):** `bubbletea` (TUI runtime), `huh` (forms/menus),
   `lipgloss` (styling). User-directed adoption; recorded as an ADR because it
   crosses the "heavyweight framework" guardrail.
2. **Everything as TUI programs:** all commands render live views on a TTY;
   plain renderers remain the single source of truth for non-TTY and `--json`.
3. **Distribution: GitHub Releases self-update.** Tagged releases built by CI;
   `axon update` self-updates with checksum verification; source/`make` path
   remains for development.
4. **Vault migration:** `axon vault move <new-path>` — one command, updates
   every reference AXON owns.

## Component 1 — TUI foundation (`internal/tui`)

- `tui.Run(model, plain)` helper: TTY → bubbletea program; non-TTY or
  `--json` → the existing plain renderer. The plain path is canonical: JSON
  shape, exit codes, and greppable strings never change. CI/tests keep passing
  against plain output.
- Reusable components: **steplist** (init/doctor/update progress: pending ▸
  running ▸ ✓/⚠/✗ + detail), **spinner-with-log** (long ops: helper compile,
  model pull, re-embed), **table** (status/automations), **confirm** and
  **typed-confirm** (destructive ops), plus `huh` select/multiselect/input
  forms.
- `internal/ui` is reimplemented on lipgloss with the same exported API
  (glyphs, colors, Header/Divider/Hint), so static and live output share one
  visual language; call sites do not change.
- TTY detection in one place (`tui.Interactive(w)`): respects `--json`,
  `NO_COLOR`, `CI`, and non-terminal stdout.

## Component 2 — command migration

`init`, `doctor`, `reindex`, `status`, `run`, `automations`, `health`,
`ingest`, `search` render as live views (steplist/spinner/table as fits);
`onboard`'s bufio prompts become `huh` forms. Zero semantic change: flags,
JSON output, exit codes, and the underlying core functions stay untouched —
commands feed the same StepResult/Outcome/Check values to either renderer.

## Component 3 — `axon configure`

- **Interactive (TTY, no args):** huh menu over: embeddings provider
  (Apple ↔ Ollama), models per class, budget limits, automations on/off,
  dashboard port, redaction rules. Each edit goes through the existing
  comment-preserving `setConfigValue` and re-validation.
- **Non-interactive:** `axon configure embeddings apple|ollama`,
  `axon configure models <class> <model>`, `axon configure automations
  <name> on|off`, etc. — scriptable equivalents of every menu item.
- **Provider switch is the full chain in one flow:** persist provider (+
  apple model/dim defaults when switching to apple; prompt for the Ollama
  model/dim when switching back) → converge (init's embeddings probe with
  live progress) → show "re-embed required (~N chunks)" → on confirm run
  `reindex --embeddings` → done. Non-interactive: `--reindex` flag opts into
  the re-embed, otherwise the command prints the pending step loudly.

## Component 4 — lifecycle

- **Releases:** GitHub Actions workflow on tag push runs `make release`
  (darwin/linux × arm64/amd64) and uploads tarballs + a SHA-256 checksums
  file. `axon version --check` reports the latest release vs the running
  build.
- **`axon update`:** GitHub API → latest release; if newer: download the
  matching artifact, verify checksum, atomic swap (write sidecar + rename;
  keep `axon.old` until success), restart the service if installed, run
  `init` to converge (DB migrations, helper rebuild). Live TUI progress;
  `--check-only` and `--json` for scripts. Doctor + dashboard `/health`
  surface "vX.Y available" (checked at most daily, cached in the data dir,
  never blocking).
- **Install:** thin `install.sh` one-liner (detect OS/arch → download latest
  release → install binary → exec `axon setup`). **`axon setup`** is the
  in-binary interactive provisioning TUI: config creation (from embedded
  example), vault path, profile, embeddings choice, service-at-login,
  token guidance (`claude setup-token`), then init. Absorbs the logic of
  `install-macos.sh`/`install-linux.sh`; the scripts shrink to bootstrap +
  source-build paths; `make setup` remains for developers.
- **`axon uninstall`:** stop daemon → remove service unit → remove binary
  (self-delete last, or print the one `rm` if not writable); `--purge` also
  removes `$AXON_HOME` behind a typed-confirm. The vault is NEVER touched.

## Component 5 — `axon vault move <new-path>`

1. Preflight: destination must not exist (or be an empty dir); refuse while
   the daemon runs — interactively offer stop → move → restart.
2. Move: `os.Rename`; on cross-device link error fall back to copy → verify
   (file count + content hash sample) → remove source.
3. Update references AXON owns: `vault_path` in config (comment-preserving
   setter); regenerate `.claude/` wiring in the new location (re-bakes
   absolute binary/config paths); nothing needed in SQLite (vault-relative
   paths) — a doctor run verifies.
4. Print what AXON cannot update: Obsidian's own vault bookmark (open the
   vault at its new location once).
5. `--json` + non-interactive behavior: refuses to stop the daemon unless
   `--stop-daemon` is passed.

## Component 6 — docs

GUIDE.md (install/update/uninstall/configure/vault-move sections), docs/10
(bootstrap component: axon setup/update/uninstall are now the product),
docs/09 (health payload gains update availability), README quick-start
(one-liner install), ADR-014 in docs/02, config example cross-references.
CLAUDE.md repo-structure line gains `tui/`.

## Constraints

- Cardinal rules untouched: no new Claude paths; vault mutations stay inside
  wikilink-safe helpers — `vault move` relocates the tree wholesale (no
  per-note rewriting needed; wikilinks are vault-relative).
- Headless safety: nothing may ever block on a TTY prompt when stdout is not
  a terminal — enforced centrally in `tui.Run`/`tui.Interactive`.
- Idempotency: `setup`, `update`, `uninstall`, `vault move` all re-runnable;
  each step reports done/already/warn/failed like init does today.
- Self-update trust: HTTPS + SHA-256 checksums from the release; no
  auto-update — always explicit user action.

## Out of scope

- Homebrew tap / OS packages (future; releases make it easy later).
- Windows service lifecycle (unit generation exists; setup/update target
  darwin/linux).
- Signature verification beyond checksums (future hardening).
- Migrating the vault BETWEEN machines (this is a local path move).
