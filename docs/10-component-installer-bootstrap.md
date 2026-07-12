# 10 — Component: Installer & Bootstrap

**Owns:** FR-01…FR-07, NFR-03, NFR-10.
**Goal:** Clone → set a handful of values → one command → a running, healthy system, with clear output — reproducibly, per profile, idempotently.

## 1. The flow

**macOS / Linux (one command):**

```bash
git clone … && cd axon
make doctor       # check dependencies (prints how to install any that are missing)
make setup        # OS-aware installer (macOS launchd / Linux systemd --user):
                  # build, install, scaffold ~/.axon, Ollama + daemon at login (idempotent)
make update       # later: update an existing install (binary, DB, dashboard, daemon)
                  # Undo: make uninstall  (ARGS="--purge" also deletes ~/.axon)
```

**Any platform (manual):**

```bash
git clone … && cd axon
mkdir -p ~/.axon                                  # the AXON home dir ($AXON_HOME)
cp axon.config.example.yaml ~/.axon/config.yaml   # set vault_path, profile, budgets
cp .env.example ~/.axon/.env                       # set CLAUDE_CODE_OAUTH_TOKEN for the profile you run
make all                                            # build the SPA + the axon binary
make install                                        # install the binary to /usr/local/bin
axon init --env ~/.axon/.env                       # the bootstrap (idempotent, verbose)
axon start --env ~/.axon/.env                      # daemon + dashboard
```

The config is read from `~/.axon/config.yaml` by default (`$AXON_HOME/config.yaml`,
following an `AXON_HOME` override) — independent of the working directory. Pass
`--config <path>` to use a different file. Secrets default to `.env` in the
current directory; the lines above keep them beside the config and point `--env`
at them.

The install scripts handle **toolchain + OS wiring** (a shared `scripts/preflight.sh` verifies Go 1.22+, Node, and the runtime deps, printing package-manager-specific install commands for anything missing; build the dashboard SPA in `web/` with Node/Vite then `go build ./cmd/axon` with the assets embedded; install the `axon` binary into `/usr/local/bin`; on macOS install/start Ollama via `brew services` and pull the embedding model; install + load the auto-start service). `axon init` handles **environment convergence** for the active profile. The split keeps OS-level package wrangling out of the cross-platform Go code. Because the daemon ships as a single static binary with the SPA embedded, an alternative install path is to download a prebuilt release binary (`make release` cross-compiles them) and skip both build steps (no Go or Node needed). The companion uninstall scripts reverse everything (binary + service unit; `--purge` also removes `$AXON_HOME`), and **never touch the vault**. macOS uses launchd; Linux uses `systemd --user`; Windows uses `axon service install` (Task Scheduler).

**Updates.** `make update` (macOS/Linux) updates an existing installation in place: it rebuilds, swaps the binary and reports the version delta, re-runs `axon init` to converge the profile (DB migrations, vault scaffold, Claude Code wiring, dashboards), regenerates the service unit and restarts the daemon, and lists any newly shipped config settings — while preserving the config, secrets, and SQLite DB. Every build is version-stamped and checkable with `axon version`.

## 2. `axon init` — steps (each prints status: ✓ done / ↻ already / ⚠ fixed / ✗ failed + hint)

1. **Resolve profile & config.** Load the config (`~/.axon/config.yaml` by default, or `--config <path>`) + `.env`; resolve `active_profile`; validate against the config schema (struct tags + validator); print the resolved (secret-redacted) config summary, including the config path in use.
2. **Prerequisite checks.** Go toolchain (if building from source); Ollama reachable; required embedding model present (offer to `ollama pull`); the `claude` CLI present **and authenticated for this `auth_mode`** (subscription/enterprise login, or a valid `CLAUDE_CODE_OAUTH_TOKEN` for headless); **warn loudly if `ANTHROPIC_API_KEY` is set** while `auth_mode` is subscription/enterprise (it would divert Claude Code to API billing); the installed OS service unit's PATH resolves `claude` (launchd/systemd default PATHs miss user-local installs); ports free; vault path writable. Each as pass/warn/fail with remediation. (Shared with `axon doctor`.)
3. **Data dir.** Create `$AXON_HOME/profiles/<name>/` (`db.sqlite`, `logs/`, `exports/`, `snapshots/`, `claude/`). Skip if present.
4. **Database.** Create/upgrade the SQLite DB; load `sqlite-vec`; create tables + `vec0`/FTS5; run migrations to current `schema_version`. Idempotent.
5. **Embedding model.** Pull/verify the model; assert configured `dim` matches the live model (fail with a clear message if not).
6. **Vault scaffold.** Create the PARA/Inbox/Daily/MOCs/Templates structure **only where missing**; seed folder READMEs and note templates; **never overwrite** existing user notes. If the vault already has content, detect and adapt (don't clobber); report what was added.
7. **Claude Code wiring.** Write `.claude/CLAUDE.md` (from template, profile-aware), `.claude/.mcp.json` (AXON MCP, profile-scoped), `.claude/settings.json` (hooks), and install the plugin into `.claude/plugins/axon/`. Set the profile's `CLAUDE_CONFIG_DIR`. If files exist, **merge** non-destructively and report diffs.
8. **In-vault dashboards.** Generate the Dataview/Bases dashboard notes.
9. **First index.** Run an initial `reindex` (build link graph; embed existing notes) with a progress bar; respects budget (embeddings are local/free, no Claude cost).
10. **Summary.** Print a final report: what was created vs already present, the dashboard URL, and next steps. Exit 0 only if all critical steps succeeded.

**Idempotency (FR-02):** a second `axon init` re-validates and converges; every step reports "already" where nothing changed; the final summary says "no changes". Achieved by content/feature detection, not by blindly rewriting.

## 3. Profiles & separate installations (FR-03, NFR-04)

- Personal and work are **separate installations** on separate machines, each running one active profile — they never co-run in one process. `AXON_PROFILE=work axon init` provisions the work profile's isolated data dir + vault + Claude config + policy on the work box.
- The example config ships **both** `personal` and `work` profiles so either installation is a one-flag operation and both templates live in version control.
- Separate accounts: each profile's `claude.config_dir` (`CLAUDE_CONFIG_DIR`) + `auth_mode` + its own `CLAUDE_CODE_OAUTH_TOKEN` ensure interactive and headless Claude use the right account; personal is a Max subscription, work is Enterprise SSO with no API.
- Restrictions: the profile `policy` block (egress allowlist, ingest domain allow/deny, redaction, `allowed_automations`, token limits) is applied everywhere — ingestion, automations, the token manager — so the work install is genuinely more constrained, not just nominally.

## 4. Clear output (NFR-10)

- Every long step streams human-readable progress (spinners/bars); on failure, the message names the cause and the fix (e.g. "Ollama not reachable at localhost:11434 — start it with `ollama serve`").
- `--json` emits machine-readable step results for scripting/CI.
- A run transcript is saved to `.axon/logs/init-<ts>.log`.

## 5. Lifecycle commands

The lifecycle is IN THE BINARY (operations-overhaul spec, Components 3–5);
`install.sh` at the repo root bootstraps a release binary and hands over to
`axon setup`. The shell scripts under `scripts/` remain the from-source path.

- `axon setup` — first-run provisioning: starter config + `.env` (kept if present), vault/DB/index via the init steps, optional service-at-login. Interactive (huh form) on a TTY; `--vault/--profile-name/--embeddings/--service` headless.
- `axon update` — checksum-verified self-update from GitHub Releases (`--check-only`, `--json`); availability is cached daily and surfaced by `doctor` and the dashboard `/health`.
- `axon uninstall [--purge]` — stop daemon, remove service unit + binary; `--purge` deletes `$AXON_HOME` behind a typed confirmation (`--yes-purge-all-data` headless). The vault is NEVER touched.
- `axon configure` — interactive settings menu + scriptable subcommands; `configure embeddings <p> --reindex` is the one-flow provider switch.
- `axon vault move <new-path>` — relocate the vault; updates `vault_path` + regenerates `.claude/` wiring; refuses under a running daemon (`--stop-daemon` for scripts).
- `axon start|stop|status` — daemon control; `start` also serves the dashboard; `status` shows health + budget + last runs.
- `axon doctor` — the prerequisite checks on demand (FR-05), plus cached update availability.
- `axon reindex [--embeddings]` — rebuild derived state from the vault (ADR-006); `--embeddings` forces full re-embed (after a model change).
- `axon export` — context-export bundle on demand.
- `axon service install|uninstall` — emit/remove OS service units (FR-06, optional).

## 6. Acceptance checks
- Fresh machine: clone → ≤6 values → `init` → `start` → `doctor` green in ≤15 min (S1/NFR-03).
- Second `axon init` reports no changes (S2/FR-02).
- An existing vault with notes is scaffolded **without** clobbering user content (FR-01).
- `AXON_PROFILE=work` produces a fully isolated instance (S7/NFR-04).
- A missing prerequisite yields a precise remediation message and a non-zero exit (NFR-10).
