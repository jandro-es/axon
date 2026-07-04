# Installing AXON

AXON ships as a single self-contained binary (the dashboard is embedded — no
Node needed at runtime). There are two paths: the **one-line release install**
(no toolchain at all) and **building from source** (driven by `make`; run
`make` with no arguments for a categorised list of targets).

> Your Obsidian vault is the source of truth and is **never** modified by
> install, update, or uninstall. The SQLite database is derived and rebuildable.

## TL;DR

```bash
# Release install — no Go/Node/repo needed:
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash

# From source:
make doctor     # check dependencies (and how to install any that are missing)
make setup      # full install: binary + config + Ollama + daemon at login
# … later …
axon update     # release installs: checksum-verified self-update
make update     # from-source installs: rebuild + converge + restart
make uninstall  # remove the daemon + binary (keeps ~/.axon)
```

## Preparing a fresh machine

AXON needs two runtime companions — the `claude` CLI (the brain) and Ollama
(local embeddings, optional local models) — plus a build toolchain only if you
build from source. On a machine that has none of it:

**macOS**

```bash
# Homebrew (if missing): https://brew.sh
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

brew install ollama                       # local embeddings + optional local models
ollama pull nomic-embed-text              # the default embedding model (768-dim)
npm install -g @anthropic-ai/claude-code  # the claude CLI (or the installer at claude.com/claude-code)
claude login                              # authenticate with your Claude subscription/enterprise account

# Only if building from source:
brew install go node git make
```

**Linux (Debian/Ubuntu shown)**

```bash
curl -fsSL https://ollama.com/install.sh | sh   # Ollama
ollama pull nomic-embed-text
npm install -g @anthropic-ai/claude-code        # the claude CLI (needs Node 18+)
claude login

# Only if building from source:
sudo apt-get install -y golang-go nodejs npm git make   # Go must satisfy go.mod (1.26+)
```

For **headless automations** (the scheduler calling Claude with no terminal
attached), also create a long-lived token once and put it in `~/.axon/.env`:

```bash
claude setup-token        # → CLAUDE_CODE_OAUTH_TOKEN=...
```

Everything is verifiable before and after: `make doctor` (from source) or
`axon doctor` (any install) names anything missing **with the exact install
command for your OS/package manager**, and never changes your system itself.
AXON degrades gracefully — without Ollama, notes are still written and
lexically searchable; vectors back-fill via `axon reindex --embeddings` later.

## Requirements

| Tool | Required | Purpose |
| --- | --- | --- |
| **claude CLI** | recommended | automations + interactive use ([Claude Code](https://claude.com/claude-code)) |
| **Ollama** | recommended | local embeddings + hybrid search; optional local models for cheap tiers |
| **Go 1.26+** | source builds only | compiles the binary (matches the `go` directive in `go.mod`) |
| **Node + npm** | optional | builds the dashboard SPA (a fallback page is served without it) |
| **git / make** | source builds only | version stamping + the build shortcuts |

`make doctor` inspects all of these and prints the exact install command for
your OS/package manager for anything missing. It never changes your system.

## Install

### Release binary — one line, no toolchain

```bash
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
```

Downloads the latest release binary (SHA-256 verified) and hands over to the
interactive **`axon setup`**, which asks for your vault path, profile and
embeddings provider, then provisions everything. `--user` installs to
`~/.local/bin` without sudo; `--no-setup` installs the binary only. `axon
setup` is idempotent — re-run it any time; existing config and secrets are
kept. Update later with **`axon update`** (checksum-verified self-update;
`axon doctor` and the dashboard tell you when one is available); remove with
**`axon uninstall`** (`--purge` also deletes `~/.axon`; the vault is never
touched).

### From source (macOS / Linux) — one command

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
