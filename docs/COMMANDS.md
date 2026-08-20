# The `axon` command line

Every capability of the daemon is reachable from one binary. This is the
complete reference — 29 top-level commands, grouped the way you meet them.
`axon <command> --help` is always authoritative for flags.

**Persistent flags** (accepted by every command):

| Flag | Default | Meaning |
|------|---------|---------|
| `-c, --config <path>` | `~/.axon/config.yaml` | Config file to load |
| `-p, --profile <name>` | config's `active_profile` | Profile override (beats `AXON_PROFILE`) |
| `--env <path>` | `~/.axon/.env` | Secrets file |

`axon --version` prints the version; most commands also take `--json` for
machine-readable output.

---

## Setup & lifecycle

### `axon setup`
First-run provisioning in one step: writes config and secrets, wires the vault,
builds the first index. Flags: `--vault <path>`, `--profile-name` (default
`personal`), `--embeddings ollama|apple`, `--service` (also install the OS
service).

```sh
axon setup --vault ~/Documents/Brain
```

### `axon init`
Provision (or re-converge) the **active profile**: data dir, database, vault
scaffold, embedding model check, first index. Idempotent — re-running reports
"no changes" where nothing differs and never clobbers your notes. Flags:
`--json`, `--embeddings`.

### `axon onboard`
Create the personal identity & memory layer (USER/SOUL notes) through a short
interview. Flags: `--json`, `--non-interactive`, `--from <yaml|json>`.

### `axon doctor`
Health report: prerequisites, auth, service unit, database, ports, providers —
each check pass/warn/fail with the command that fixes it (`↳ fix:` in the
terminal, `remediation` in `--json`). Exit code is non-zero on a failing check.

```sh
axon doctor --json | jq '.checks[] | select(.status != "ok")'
```

### `axon update`
Self-update: verified download, atomic binary swap. Flags: `--check-only`,
`--json`. *Does not* restart a supervised daemon for you — it prints the
reload command (`make reload` / `axon service` hints) because a running
service keeps the old binary until reloaded.

### `axon uninstall`
Remove the daemon, service unit and binary. **Never touches the vault.**
`--purge` removes profile data (index, ledger) and requires
`--yes-purge-all-data`.

### `axon version`
Version and build metadata. Flags: `--short`, `--check` (compare against the
latest release).

---

## Config

### `axon config get <key>` / `set <key> <value>` / `validate`
Read or write a dotted, profile-relative key (`config set limits.daily 800000`)
— writes are comment-preserving and re-validated before saving. `validate`
checks the file and the active profile.

### `axon configure`
Interactive menu for the common knobs; prints guidance when there's no TTY.
Subcommands for scripting:

| Subcommand | Purpose |
|------------|---------|
| `configure embeddings <ollama\|apple>` | Switch provider (`--model`, `--dim`, `--reindex`) — persists, converges, re-embeds |
| `configure models <classify\|routine\|synthesis> <model>` | Set a tier's model: a Claude model string, `ollama:<model>`, or `apple` |
| `configure limits <daily\|weekly> <tokens>` | Set a budget window |
| `configure automations <name> <on\|off>` | Toggle an automation |
| `configure dashboard-port <port>` | Move the dashboard |

### `axon profiles`
List profiles with their fully-isolated resolved paths (data dir, DB, vault).
*Does not* print secrets. `--json`.

---

## Knowledge & query

### `axon ingest <url|path>`
Ingest a URL or a local text/Markdown/PDF/image file through the pipeline:
egress policy → extract → redact → chunk → embed → note. Flags: `--dry-run`,
`--no-apply-links`, `--enrich`, `--media` (force yt-dlp caption path),
`--header` (repeatable), `--json`. Local-file ingestion is **CLI-only** — the
MCP/agent path is URL-only by design.

```sh
axon ingest https://example.com/article --dry-run
```

### `axon search <query>`
Hybrid lexical + semantic search over the index. Flags: `--top-k` (8),
`--json`. *Does not* call Claude — retrieval is local.

### `axon ask <question>`
Grounded answer with `[[wikilink]]` citations — or an explicit refusal when
the vault doesn't support an answer. One synthesis-tier call through the token
chokepoint. Flags: `--top-k`, `--json`.

### `axon related <note-path>`
Notes similar to the given one, by pure vector math. **No model call, no
tokens spent.** Flags: `--top-k`, `--json`.

### `axon actions`
Your GTD task list across the whole vault (from checkbox lines in notes).
Flags: `--status bucket|open|week`, `--project`, `--context`, `--all` (include
archive), `--json`. *Does not* call a model; the list is derived at read time.

### `axon subscribe <feed-url>` / `subscribe list` / `subscribe remove <url>`
Manage RSS/Atom subscriptions polled by the `subscriptions` automation. `--allow`
adds the feed's host to `policy.ingest_domains_allow`; `--no-verify` skips the
initial fetch. `remove` drops the feed's seen-state. Subscriptions are
subscribe-from-now — no backfill of old items.

### `axon reindex`
Rebuild the notes mirror and link graph from the vault. The vault is the
source of truth; the database is disposable, and this proves it. The
`--embeddings` flag is currently a **no-op with a notice** — embeddings are
refreshed by the daemon/ingestion, not this command.

### `axon export`
Portable snapshot bundle (manifest + Markdown + JSON) to `--out`.

### `axon vault move <new-path>`
Move the vault and rewrite every AXON-owned reference to it. Flags:
`--stop-daemon`, `--json`. *Does not* rewrite anything inside your notes'
prose — only AXON's own configuration and wiring.

---

## Runtime & ops

### `axon start`
Run the daemon: scheduler plus live dashboard (default `127.0.0.1:7777`).
Flags: `--once` (single scheduler pass), `--no-dashboard`. Refuses to run as
root when the data dir or vault belongs to another user; a dashboard bind
failure is fatal (a taken port means another daemon holds it).

### `axon stop`
SIGTERM the profile's daemon via its pidfile. Flag: `--timeout` (10s).

### `axon run <automation>`
Run one automation now, through exactly the scheduler's code path. Flags:
`--dry-run` (report-only writes — agentic runs get a server-enforced
report-only MCP), `--json`.

```sh
axon run knowledge-digest --dry-run
```

### `axon automations` (alias: `autos`)
List all automations: enabled, allowed-by-policy, purpose, last run. `--json`.

### `axon status`
Remaining token budget (day/week) and budget-guard state. `--json`.

### `axon health`
Score the second brain itself: index freshness, automation reliability, orphan
rate. `--json`.

### `axon eval`
Evaluate a model against the golden task sets — how AXON decides whether a
cheap local model is good enough for a tier. Flags: `--family
classify|routine|synthesis|all`, `--model`, `--min-pass`, `--no-save`,
`--json`.

### `axon service <install|uninstall|print|status>`
Manage the OS service unit (launchd / `systemd --user` / Task Scheduler).
Generated units embed a resolved `PATH` so the supervised daemon can find
`claude`, `ollama`, `yt-dlp`. `status --json` reports
`{profile, kind, path, installed, supported, path_env}`. Remember: a rewritten
unit does **not** reach a running daemon — reload it (`bootout`+`bootstrap` /
`daemon-reload`), don't just restart.

---

## Integration

### `axon mcp`
Run the AXON MCP server on stdio (for Claude Code / Claude Desktop). Flags:
`--tools <csv>` (server-side allowlist used for agentic automation runs),
`--dry-run` (report-only writes). You rarely run this by hand.

### `axon mcp install --client <code|desktop>`
Register the MCP server with a Claude client (`--print` to preview the JSON
instead of writing it).

### `axon hook <event>`
**Internal.** The Claude Code hook handler (`SessionStart`, `PreToolUse`,
`PostToolUse`, `Stop`, `SessionEnd`); reads the hook JSON payload on stdin.
Never makes a model call, never hard-fails a session. Wired by `axon init`;
not intended for manual use.
