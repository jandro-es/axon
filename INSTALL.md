# Installing AXON

AXON ships as a single self-contained binary (the dashboard is embedded — no
Node needed at runtime). Everything below is driven by `make`; run `make` with
no arguments for a categorised list of targets.

> Your Obsidian vault is the source of truth and is **never** modified by
> install, update, or uninstall. The SQLite database is derived and rebuildable.

## TL;DR

```bash
make doctor     # check dependencies (and how to install any that are missing)
make setup      # full install: binary + config + Ollama + daemon at login
# … later …
make update     # update an existing install (binary, DB schema, dashboard, daemon)
make uninstall  # remove the daemon + binary (keeps ~/.axon)
```

## Requirements

| Tool | Required | Purpose |
| --- | --- | --- |
| **Go 1.26+** | yes (to build) | compiles the binary (matches the `go` directive in `go.mod`) |
| **Node + npm** | optional | builds the dashboard SPA (a fallback page is served without it) |
| **claude CLI** | recommended | automations + interactive use ([Claude Code](https://claude.com/claude-code)) |
| **Ollama** | recommended | local embeddings + hybrid search |
| **git / make** | recommended | version stamping + the build shortcuts |

`make doctor` inspects all of these and prints the exact install command for
your OS/package manager for anything missing. It never changes your system.

## Install

### macOS / Linux — one command

```bash
make setup
```

This builds the binary and dashboard, installs the binary to `/usr/local/bin`
(override with `PREFIX=…`), scaffolds `~/.axon/config.yaml` + `~/.axon/.env`,
provisions the active profile (`axon init`), prepares the embedding model, and
installs a login service (launchd on macOS, `systemd --user` on Linux) so the
daemon starts automatically. It is idempotent — safe to re-run.

Pass installer flags through `ARGS`:

```bash
make setup ARGS="--no-ollama"        # skip Ollama management
make setup ARGS="--no-service"       # don't install the auto-start service
make setup PREFIX=$HOME/.local       # user-local install (no sudo)
```

Then set your `vault_path` (and, for headless automations, a
`CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token`) in `~/.axon/`. Verify with
`axon version` (which build you're running), `axon doctor` (prerequisites), and
`axon status` (daemon + budget).

### Windows / other

```bash
make install            # build + install just the binary
axon init               # scaffold the profile + database
axon service install    # emit a Task Scheduler unit; register it as printed
```

## Update

From an updated source tree (e.g. after `git pull`):

```bash
make update
```

This rebuilds the binary + dashboard, replaces the installed binary (reporting
the version delta), converges the profile with `axon init` (applies **database
migrations**, refreshes the vault scaffold, Claude Code wiring and dashboards),
regenerates the service unit, and restarts the daemon so the new build goes
live. Your `config.yaml`, `.env` and SQLite DB are preserved; any **new config
settings** shipped since your install are listed for you to adopt (never applied
silently).

## Uninstall

```bash
make uninstall                 # stop + remove the daemon and binary; keep ~/.axon
make uninstall ARGS="--purge"  # also delete ~/.axon (config, secrets, DB) — confirmed
```

Your vault is preserved in every case.

## Building for distribution

```bash
make release     # cross-compiled, stripped binaries for macOS + Linux (amd64/arm64) → dist/
make version     # show the version/commit/date that will be stamped in
```

Binaries are pure-Go (no cgo), so cross-compilation needs no C toolchain.

## Troubleshooting

- **A build fails** — run `make doctor`; it names the missing tool and the exact
  install command for your system. Every script prints the step it failed on and
  a recovery hint; steps are idempotent, so fixing the cause and re-running
  resumes safely.
- **`PREFIX/bin` not on PATH** — the installer warns; add it to your shell
  profile.
- **Daemon didn't come up** — check `axon status`, then the logs under
  `<data_dir>/logs/` (macOS) or `systemctl --user status axon-<profile>` (Linux).
- **Stray `ANTHROPIC_API_KEY`** — unset it on subscription/enterprise installs;
  `axon doctor` flags it (it would divert Claude Code onto API billing).
