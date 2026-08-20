# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/).

## [Unreleased]

## [1.3.10] — 2026-08-20

**The sudo lockout, and an automation that could never run.** A patch: no
schema change (stays v7), no new MCP tool. Two config seeds added
(disabled-by-default automations — existing configs are unaffected; re-run
`axon setup`/copy the example block to pick them up).

### Fixed

- **`eval-drift` can now actually be scheduled.** It shipped in 1.1 (FR-143)
  in the automation registry and catalog — but with no entry in the starter
  config or the example config, and an automation with no config entry is
  never scheduled, so on every install to date it could not run at all. Both
  sources now seed it (`enabled: false`, weekly Monday 05:00, `model: none` —
  its eval calls run against the concrete local model only and spend no
  Claude tokens). A new invariant test asserts every registered automation is
  seeded in both config sources, which also caught and fixed
  `merge-proposals` missing from the example yaml's personal profile.
- **A daemon started with `sudo` no longer locks you out of your own vault.**
  Root writes never fail, so a stray `sudo axon start` quietly created every new
  note, review-queue file and Claude config entry owned by `root` and mode
  `0600` — after which the real daemon could not read them, and the dashboard's
  Review tab and the `heartbeat` automation both died on "permission denied".
  Three things allowed it, all now closed:
  - `axon start` refuses to run as root when the data dir or vault belongs to
    another user, naming the directory and its owner.
  - The single-instance guard no longer mistakes a daemon owned by another user
    for a dead one. Signal 0 returns `EPERM` for a live process that is not
    yours, and that was being read as "not running", so a second daemon started
    alongside the first.
  - A dashboard bind failure is now fatal instead of a warning. A taken port
    means another daemon already holds it; carrying on left an invisible second
    scheduler writing the vault with no dashboard to notice it by. Use
    `--no-dashboard` to run headless deliberately.
- **The Review tab reports what actually went wrong.** Any failure used to read
  "The daemon isn't answering", including a `500` carrying a precise, fixable
  message. The daemon's own text is now shown when it answers; the unreachable
  wording is kept for when it truly is.

## [1.3.9] — 2026-08-18

**Level cards.** A patch on 1.3.8: presentation only — no Go change, no schema
change, no config key.

### Fixed

- **Cards in a row now share a height.** 1.3.8's grid let every card keep its
  natural height, so side-by-side cards ended at different points and the page
  read as ragged — Vault against Token Budget, Token spend against Recent
  automations, Ingestion against Activity, and the same on Tokens, Actions,
  Knowledge and Graph. Cards in a row now stretch to match, and the spare height
  goes *into* the content rather than being left as a void: chart wells hand
  their height to the plot, the Activity feed and the graph fill their card, and
  a tile grid centres its rows instead of growing 240px-tall tiles.

## [1.3.8] — 2026-08-18

**The dashboard catches up with the app.** A minor release: no schema change
(stays v7), no new config key, no new MCP tool. One additive field on `/health`.

The macOS Companion shipped wearing Liquid Glass; the web dashboard was still
the dark-only skin it was born with in June. Two surfaces onto one daemon that
look unrelated read as two products — so the dashboard now uses the same
material, and gained the appearance control it should always have had. Opening
it up also made obvious how much of what the daemon already knows was never on
screen: half of `/health`, what is actually waiting on a human, which
automation is quietly failing, which notes nothing links to.

### Added

- **The dashboard wears the same glass as the Companion, and now has a light
  mode.** (ADR-037, FR-177…FR-183.) The web UI was a 2026-06 dark-only skin
  sitting next to a menu-bar app built on Liquid Glass; two surfaces onto one
  daemon looked like two products.
  - **Appearance: system (default), light, dark** — persisted, resolved in one
    place, and applied *before first paint* so a dark reload never flashes
    white. While set to system it follows the OS live.
  - **One material language.** Translucent layered surfaces over a tinted
    canvas, one corner radius, the teal→violet signal accent in both themes.
    Two rules copied verbatim from Companion's `Glass.swift`: charts never sit
    on glass (every plot gets an opaque well), and reduced transparency
    *replaces* glass with an opaque fill rather than dimming it. Reduced motion
    and increased contrast are honoured too.
  - **One time range** (24h/7d/30d/All) across every series, client-side, with
    exports still whole-series and saying so.
  - **`⌘K` command palette** plus `1`–`9`, `/`, `R`, `T`, `?` shortcuts — none
    of which is the only route to anything.
  - **Daemon status sheet**: version and available update, uptime, database,
    embeddings provider/model/dim, the daemon's resolved `claude` path, vault,
    guard state, and copy-diagnostics — all of it already in `/health` and none
    of it previously shown.
  - **A "Needs you" panel** on the Overview: pending proposals, failed runs, a
    paused guard, inbox backlog, embed queue, available update — each row
    deep-linking to the tab that resolves it.
  - **The knowledge graph is a map now**: a deterministic force layout (so a
    polled refresh doesn't reshuffle it), wheel zoom, drag pan, neighbourhood
    highlighting, click-to-open in Obsidian, and a **Hubs & orphans** panel for
    what a force map answers badly. Rendering is capped at the 400
    best-connected notes of the current filter, stated in the header.
  - **Panel upgrades**: per-automation reliability (runs, skips, mean duration,
    tokens, success rate, last run) and runs-per-day by status; a newest-first
    Activity feed with level/text filters and a pause that keeps the backlog
    count; Focus/All/Someday views and a filter on Actions; note-path
    autocomplete and walk-the-neighbourhood on Related; a cache-hit rate on
    Tokens; toasts for errors on the live stream; skeletons on first load; and
    empty states that name the command that fills them.
  - Built with **no new frontend dependency** — the graph, palette, toasts and
    icons are local, so the embedded SPA still needs no network.

### Changed

- **`GET /health` carries `vault`**, the vault folder's basename (never its
  path, absent when no vault is wired) — the same value `GET /api/actions` has
  carried since 1.2.5. It is what lets any panel that names a note link it into
  Obsidian.
- **`npm run dev` honours `AXON_DASHBOARD_PORT`**, so the dev server can be
  aimed at a scratch profile instead of the live daemon on :7777.
- **`docs/GUIDE.md` no longer teaches the command that caused the bug.** §13
  still said `launchctl load <plist>`, which exits 0 having done nothing when
  the label is already loaded. It now uses the `bootstrap` domain form, states
  outright that rewriting a unit does not change a daemon already running it,
  and carries the reload sequence for both supervisors. The `exec: "claude":
  executable file not found` and `dashboard-port` troubleshooting rows were
  brought in line with what those checks actually do now.

## [1.3.7] — 2026-08-17

**The rest of the same bug.** A patch: no schema change (stays v7), no config
change, no new MCP tool.

1.3.6 fixed the seam where it had actually bitten. Sweeping for the pattern
found it in the two commands a user runs *immediately after* a unit is
rewritten — which is the worst place for it, because that is the moment the
advice is most likely to be followed and least likely to work.

### Fixed

- **The remaining three places that restarted the daemon instead of reloading
  it.** 1.3.6 fixed `doctor` and the install/update scripts; a sweep for the
  same pattern found it still living in `make reload` — the command `axon
  update` points you at when a new binary needs to take effect — and in the
  lifecycle hints `axon service install` prints, which said `launchctl load
  <plist>`. That command exits 0 having done nothing when the label is already
  loaded, so the advice printed directly beneath a freshly rewritten unit was
  advice that could not apply it.

  `Unit.ReloadCmd` now carries the re-read command for each supervisor, so
  `internal/service` — which generates the units — owns the one true answer, and
  `doctor` stops hand-rolling `launchctl` strings for a file it did not write.
  The launchd lifecycle commands moved to the modern `bootstrap`/`bootout`
  domain form, and `axon service install` now says outright that a running
  daemon keeps the old unit until reloaded.

## [1.3.6] — 2026-08-17

**The daemon could not find `claude`, and every check said it could.** A patch:
no schema change (stays v7), no config change, no new MCP tool.

Two automations failed overnight with `exec: "claude": executable file not
found in $PATH` on a machine where `axon doctor` reported a clean bill of
health, the plist named the right directory, and `claude --version` answered
instantly in the shell. Everything you could inspect said it should work. The
cause was a seam none of those checks looked at, and 1.3.4's fix for the same
class of bug had been sitting on disk, correct and inert, for a day.

launchd and systemd keep the job definition they parsed at load time. Correcting
a unit file changes what the daemon *would* get on a fresh load and nothing
about what it currently has, so a machine can carry a right plist and a wrong
daemon indefinitely. The Claude Code native installer moving the CLI to
`~/.local/bin` was enough to open that gap.

### Fixed

- **`doctor`'s `service-path` check asks the running daemon instead of reading
  the unit file.** Reading the file made it structurally unable to see the
  failure it exists to catch: it reported `service unit PATH resolves claude`
  on the machine whose every automation was dying on exec. The daemon is the
  only party that knows the PATH it actually got, so when one is up, `doctor`
  asks it — and says so plainly when the file and the loaded job disagree:

  > the running daemon cannot resolve claude — every automation it runs will
  > fail with `exec: "claude": executable file not found`; …com.axon.personal.plist
  > already carries a PATH that would resolve it, so the loaded job is stale and
  > needs a reload (not just a restart)

  The unit file remains the fallback when there is no daemon to ask. A daemon
  older than the new `/health` field reports nothing rather than an empty
  string, so it falls back too instead of warning about a healthy machine.

- **The remediation for a stale unit now reloads it.** The command `doctor`
  offered was `launchctl kickstart -k`, which restarts the process from the
  definition already loaded — following it would rewrite an already-correct
  plist, restart the daemon into the same broken environment, and report
  success. Now `bootout` + `bootstrap`. The Linux path had the identical flaw: a
  bare `systemctl --user restart` does not re-read unit files either, so it
  gained the `daemon-reload` it always needed.

- **`install-macos.sh` and `update-macos.sh` replace the loaded launchd job
  rather than restarting it.** Both ran `launchctl unload … || true` followed by
  `launchctl load -w`, a pair whose failures are invisible in exactly the case
  that matters: the `|| true` swallows a failed unload, and `load` onto a
  still-loaded label exits 0 having done nothing. That is how a corrected plist
  came to sit beside a daemon still running the old one. They now share a
  `launchd_reload` helper in `_common.sh` that boots the old job out, waits for
  the label to actually go away (bootout is asynchronous — bootstrapping onto a
  dying job fails with EBUSY), bootstraps the file, and returns non-zero with
  launchd's own error when the daemon does not come up.

### Added

- **`claude_path` on `GET /health`** — the `claude` binary the daemon resolves
  on its own PATH, or `""` when it resolves none. This is the fact no external
  observer can determine: a service unit corrected on disk does not reach a job
  already loaded, so the daemon's own answer is the only reliable one. It is a
  single resolved path, never the surrounding environment.

## [1.3.5] — 2026-08-16

**One check that was always wrong.** A patch: no schema change (stays v7), no
config change, no new MCP tool.

### Fixed

- **`doctor`'s `dashboard-port` check no longer warns about the healthy state.**
  It reported `127.0.0.1:7777 is in use (a daemon may already be running)` on
  every machine where the daemon *was* running — warning about exactly the thing
  the user wants, and the only warning with nothing to act on. A busy port is
  only a problem when something other than AXON holds it, so the check now asks:
  an AXON daemon answers `/health`, and that is a pass naming the serving
  profile. A foreign listener still warns, and now carries the `lsof` command to
  find it.

  The probe is loopback-only with a 1.5s timeout and a capped read, so `doctor`
  cannot hang on a socket that accepts but never speaks HTTP, and cannot be fed
  an unbounded body by whatever is listening.

## [1.3.4] — 2026-08-16

**`axon doctor` now tells you how to fix things.** A patch: no schema change
(stays v7), no config change, no new MCP tool, no behaviour change to any
existing command.

### Added

- **Every actionable doctor check carries the command that fixes it.** `doctor`
  said what was wrong but rarely what to do about it — "ollama not found on
  PATH" left the reader to go and find the install command themselves. `Detail`
  now says what is wrong and a new `Check.Fix` says what to do: rendered by the
  CLI as a dim `↳ fix:` line under the check, and emitted as `remediation` by
  `--json` so a GUI can offer it as a copyable command rather than making the
  user parse prose.

  Populated for every warning a user can act on: a missing `claude`, `ollama`,
  `yt-dlp`, `tesseract`/`poppler` (with the right command per platform), an
  unreachable or unpulled Ollama model, an Apple helper needing `axon init`, an
  unusable vision provider, and a service unit whose PATH cannot resolve
  `claude`.

- **`path_env` on `axon service status --json`** — the PATH the installed unit
  hands the daemon, resolved from the installing shell at install time.

  This exists because of a real, reproducible bug. A GUI client is started by
  LaunchServices with `PATH=/usr/bin:/bin:/usr/sbin:/sbin` — no Homebrew, no
  `~/.local/bin` — so a child `axon doctor` reported `claude`, `ollama` and
  `yt-dlp` missing on a machine whose login shell finds all three. That is worse
  than no report: it sends the user to reinstall tools they already have. It is
  the same failure the *daemon* hit under launchd in 1.3.2, and the fix is the
  same PATH, now readable by anything that needs it.

### Changed

- The service-unit PATH parser moved from `internal/core` to `internal/service`,
  where the units are generated, so `doctor` and `service status` share one
  implementation instead of two regexes drifting apart.

## [1.3.3] — 2026-08-16

**Machine-readable seams for GUI clients.** A patch by version number but
additive in content: four new read-only surfaces, no schema change (stays v7),
no config change, no new MCP tool, no behaviour change to any existing command.

Every one of these exists because the new macOS menu bar app
([Axon Companion](https://github.com/jandro-es/axon/releases/tag/companion-v0.1.0),
`apps/companion/`) needed to ask the daemon something it could not answer.
The rule was that the daemon grows the seam rather than the client working
around it — so each is a general capability, useful to any client, not a
Companion special case.

### Added

- **`axon doctor --json`.** Only the styled TTY renderer existed, so no program
  could read the report. Emits `{profile, status, checks[{name, status, detail}]}`
  on stdout and keeps the existing non-zero exit on a failing check. Note the
  daemon folds remediation advice into `detail` — there is no separate field;
  render `detail` verbatim.
- **`axon service status [--json]`.** `service` could install, uninstall and
  print a unit but not say whether one was installed, leaving a client to stat
  a plist or shell to `launchctl` — duplicating semantics the CLI owns. Reports
  `{profile, kind, path, installed, supported}`, including where the unit
  *would* go when absent.
- **`started_at` on `GET /health`.** Clients showing uptime had to keep their
  own "first seen" clock, which resets on client restart and lies across a
  daemon restart. Stamped at the top of `axon start`, so it measures the whole
  process lifetime rather than the time the dashboard bound.
- **Dashboard tabs are addressable.** The SPA had no routing at all — `tab` was
  plain `useState`, so no tab could be linked to. `#review`, `#actions`,
  `#tokens` and the rest now open that tab on load, are written on every tab
  change, and follow `hashchange`, making Back/Forward and copy-link work. An
  unknown fragment falls back to Overview.

### Fixed

- **Security advisories in the toolchain and dependencies.** Go directive
  1.26.5 → 1.26.6, clearing five stdlib advisories (`net/url` GO-2026-6218,
  `crypto/tls` GO-2026-6090, `net/http` GO-2026-6089, `encoding/xml`
  GO-2026-6088, `encoding/asn1` GO-2026-5972); `golang.org/x/text` 0.38.0 →
  0.39.0 (GO-2026-5970); and `postcss`/`nanoid` patch bumps under Vite. Both
  ecosystems report zero vulnerabilities.
- **`make release` and the rest of the Makefile.** A regression introduced while
  adding the Companion build targets replaced the root Makefile wholesale,
  removing the release cross-compile matrix, `doctor`/`setup`/`update`/`reload`/
  `uninstall`/`install` and the quality gates. Restored, with the Companion
  targets re-applied as their own section. `.github/workflows/release.yml` runs
  `make release`, so this was load-bearing for tagging any release at all.

## [1.3.2] — 2026-07-12

**Supervised daemon finds its tools; failed runs show why.** A service/observability
patch — no schema change (stays v7), no config change, no new MCP tool.

### Fixed

- **Every Claude-backed automation failed under launchd with
  `exec: "claude": executable file not found in $PATH`.** launchd (and systemd)
  start daemons with a minimal system PATH, so a `claude` installed in
  `~/.local/bin` is visible to the user's shell but not to the supervised daemon.
  Service units generated by `axon service install` now embed a `PATH` resolved at
  generation time: the directories of the daemon's external tools (`claude`,
  `yt-dlp`, `ollama`) as found on the installing shell's PATH, plus the standard
  system dirs (`service.DaemonPathEnv`).

### Added

- **`axon doctor` `service-path` check.** Parses the installed launchd/systemd
  unit and warns when its PATH cannot resolve `claude` — catching the
  "works in my shell, dead under the service" case with a remediation hint
  (re-run `axon service install` and reload). An absent unit passes: the daemon
  then runs manually with the shell's own PATH.
- **Failed runs surface their error on the dashboard.** `GET /api/runs` now
  carries `runs.error`; the Runs list renders the failure reason inline on failed
  rows (truncated, full text on hover), so a red badge is no longer a dead end.

## [1.3.1] — 2026-07-11

**Actions dashboard: fix, polish, and open-in-Obsidian.** A dashboard-only patch —
no schema change (stays v7), no config change, no new MCP tool.

### Fixed

- **Actions tab showed "0 shown" despite a non-zero open count.** `GET /api/actions`
  serialised the derived `db.Action` rows with Go's default PascalCase field names,
  but the SPA filtered and rendered on snake_case (`a.state`, `a.source_path`,
  `a.hash`), so every row was dropped from the "Open actions" list while the count
  tiles (a separate server-side map) still read correctly. Added snake_case JSON
  tags to `db.Action`; the dashboard is the only consumer of its raw JSON (the MCP
  path uses its own view type).

### Changed

- **Redesigned the Actions "Open actions" list** to match the console: sticky
  per-bucket headers with a semantic urgency dot and a count, hairline rows with a
  hover lift, wikilink/emphasis stripped from task text for scannability, source
  notes collapsed to their bare name (full path on hover), and a quiet `done`
  affordance that surfaces on row hover/focus.

### Added

- **Open-in-Obsidian links.** Each action's source-note chip is now an
  `obsidian://open?vault=…&file=…` deep link that opens the note holding the task.
  `GET /api/actions` carries the vault name (its folder basename) when a vault is
  wired; the link degrades to plain text otherwise.

## [1.3] — 2026-07-11

**"Perceive & research."** 1.2 deepened what AXON *knows*; 1.3 widens what it can
*take in and reason over*. Two slices land together: **images and screenshots**
become understood, searchable, tagged notes (read locally — OCR first, on-device
vision when the text is sparse), **long-form video and podcast URLs** become
linked transcript notes, and a question flagged for depth triggers **bounded,
budgeted, cited web research** that lands as a synthesised vault note. Same
constitution as always: local-first, every model call through the chokepoint,
every write wikilink-safe, everything toggleable, all-off still useful, the vault
rebuilds the DB and never the reverse. No migration (schema stays v7); no new MCP
tool. Both new input surfaces are opt-in, allow-listed, redacted, and off on the
work profile by default.

### Added

- **Multimodal ingestion (H1, FR-171…173, ADR-035).** Images/screenshots ingest
  via **OCR-first** (Apple on-device / tesseract, incl. a Swift `--image` helper
  mode) then a **local Vision seam** (`ollama:<model>` now, Apple image tier
  behind the same seam) when OCR text is sparse; the source image is archived to
  `03-Resources/Knowledge/attachments/<hash>.<ext>` and embedded `![[…]]`
  (archive-never-delete). Media URLs (YouTube family + `--media` + `media_hosts`)
  become transcript notes via a detected `yt-dlp`; caption-less URLs land as a
  flagged `00-Inbox` capture with zero model calls. Vision is a **local
  perception primitive** — budget-exempt, not chokepoint-routed (no Claude vision
  path). Images are CLI-only (the `AllowLocalFiles`/SSRF guard). Config:
  `ingestion.vision`/`media_hosts`/`caption_langs`; advisory `vision`+`media`
  doctor checks.
- **Deep-research automation (H2, FR-174…176, ADR-036).** A new `deep-research`
  automation (off by default, personal-first): a `#deep`-tagged question in
  `03-Resources/Research Questions.md` carrying curated seed URLs fetches each
  through the existing `Pipeline.Ingest` (egress policy + pre-send redaction +
  dedup + chunk/embed) into `03-Resources/Knowledge/`, then one **closed-book
  `synthesis`-tier chokepoint call** (no web tools; sources are data, NFR-05)
  writes a wikilink-safe `axon:report` note under `03-Resources/Research/<slug>.md`
  with a deterministic **Sources** list, plus an `axon:deep` pointer block in the
  questions note. Bounded by `research.max_fetches` (8) + `research.budget_tokens`
  (120k); a current report + no fresh content + unchanged question ⇒ currency
  skip; a denied host is never fetched. Config: `research.enabled`/`max_fetches`/
  `budget_tokens` + the `deep-research` automation seed; advisory `research`
  doctor check.

### Changed

- **1.3 rescoped before build.** The release was narrowed from seven slices under
  a "reach" theme to the **two** shipped above under **"perceive & research"**.
  **Removed from 1.3** (not currently scheduled, may return on their own merits in
  a later roadmap): channel delivery & capture-back, the meeting & voice pipeline,
  calendar & email read-only context, continuous-capture import, and Obsidian
  CLI / Bases integration.

## [1.2.5] — 2026-07-10

**"Act on it."** 1.2 made the vault remember and reason; 1.2.5 makes it **act** —
every action ("task") scattered across the knowledge base is collected into one
trusted, always-current view, its state (open / due / overdue / waiting / someday
/ done) visible at a glance on the dashboard and inside the vault, with a
one-click way to deal with each. Simple to use, GTD-robust underneath: frictionless
capture, one trusted system, next-actions separated from someday/waiting, and a
weekly-review loop that keeps the lists honest. Same constitution: local-first,
every model call through the chokepoint (only T6 spends tokens, and it's off by
default), every write wikilink-safe, everything toggleable, all-off still useful
(S8), the vault rebuilds the DB and never the reverse (S9). Net-new slate: T1–T6
(FR-157…170, ADR-033/034, migration 0007).

### Added

- **Action index — grammar, derived table, CLI (FR-157…FR-159, ADR-033)** — a
  checkbox line anywhere in the vault, in the **Obsidian Tasks emoji grammar**
  (📅 due, ⏳ scheduled, 🛫 start, ✅ done, priority, `@context`, `#someday`/`#waiting`),
  is the single source of truth. A new pure `internal/actions` package is the one
  structured task parser; a **derived, disposable `actions` SQLite table**
  (migration 0007) is rebuilt inside the reindex transaction from Markdown
  byte-equivalently (S9); `axon actions` lists/filters/counts (`--status`,
  `--project`, `--context`, `--json`). The GTD **status bucket is computed at read
  time** (overdue/today/scheduled/next/waiting/someday), so nothing ages at
  midnight. Zero model calls.
- **Consolidation automation (FR-160/161)** — a zero-model `actions-consolidate`
  automation (daily, **enabled by default**) renders the whole index into the
  `axon:actions` managed block of `01-Projects/Actions.md` in GTD engage order
  (Overdue · Today · This week · Next by project → context · Waiting · Someday ·
  Done-this-week) as plain `[[source]]` references — never duplicate checkboxes.
  Change-gated on the rendered projection (an unchanged day writes nothing). The
  essential `heartbeat` gains a deterministic `tasks: N open (M overdue)` counter
  from the index.
- **Dashboard Actions tab + completion (FR-162…FR-164, ADR-034)** — `GET /api/actions`
  (list + GTD counts + a 30-day completion trend) and an **Actions** SPA tab
  (stat tiles, trend chart, filterable list with per-row **done** buttons). The
  one write in the theme: `POST /api/actions/complete` → **`vault.CompleteAction`**,
  a byte-precise, hash-addressed checkbox toggle (`[ ]`→`[x]` + `✅ date`) — a
  stale hash refuses with 409, nothing written. Guarded like the other browser
  mutations (loopback + Host + `X-Axon-Actions` header + `dashboard.actions_enabled`
  kill-switch); an `action.done` SSE event.
- **MCP action tools + SessionStart pointer (FR-165/166)** — `actions_list` (read,
  zero-spend, in the agentic read allowlist) and `action_complete` (interactive
  completion, **structurally excluded from every agentic subprocess** — the same
  containment as `vault_ask`, so ADR-034's "never headless-agent-driven" holds).
  SessionStart injects one deterministic pointer line — `Actions: N open (M due
  today, K overdue) → [[Actions.md]]` — from the index, no model call.
- **Stale-action sweep & Someday demotion (FR-167/168)** — a zero-model
  `actions-review` automation (weekly, off by default) proposes open, undated
  actions in notes untouched for > `actions.stale_after_days` (default 30) to the
  review queue; **accepting demotes the task to Someday/Maybe** — `vault.TagAction`
  appends `#someday` to the source line (additive, never completes, never deletes).
- **Implicit action extraction (FR-169/170)** — the only 1.2.5 token spender: an
  opt-in routine-tier `action-extract` automation (off by default, chokepoint,
  local-routable per ADR-015, change-gated, budget-bounded, NFR-05) extracts
  implicit commitments ("I should email John…") from recent notes to the review
  queue; **accepting appends a real checkbox** to the source note's `axon:tasks`
  managed block, which the parser indexes as a genuine, completable action.

### Notes

- **ADR-033** (checkbox lines as the source of truth + the derived, disposable
  index) and **ADR-034** (task completion — and, by extension, `#someday`
  demotion — as a byte-precise, user-initiated, never-agentic additive
  checkbox-line edit) are the two new architecture decisions. Migration 0007 adds
  the `actions` table. Twenty-three standard automations.

## [1.2.0] — 2026-07-10

**"Remember & reason, cheaply."** 1.0 built the self-maintaining vault; 1.1 made
it answer; 1.2 gives it a real memory architecture (temporal facts with validity
intervals and supersedence), the intelligence that exploits it
(contradiction-aware answers, scheduled resurfacing, near-duplicate merges), an
ambient related-notes surface off the embeddings already present, and an
eval-gated local-model tier so Claude is reserved for what deserves it. Same
constitution: local-first, every model call through the chokepoint, every write
wikilink-safe, all-off still useful (S8), the vault rebuilds the DB and never the
reverse (S9). Net-new slate: R1, R2, R5, R7, R8, R9 (FR-134…156, ADR-028…032).

### Added

- **Temporal memory layer (FR-134…FR-137, ADR-028)** — memory evolves from an
  append-only dated log into episodic entries + **semantic facts with validity
  intervals**. `MEMORY.md`'s `axon:memory` block gains a backward-compatible
  interval grammar (`valid_from`, tombstoned supersedence — nothing deleted); a
  derived `memory_facts` SQLite table (migration 0005) is rebuilt byte-equivalently
  by `axon reindex` (S9). Consolidation extends `memory-distill`; a life-change
  ("moved London → Tokyo") flows through the C1 reconcile review-queue flow, closing
  the old fact's interval on accept. SessionStart injection prefers currently-valid
  facts.
- **Local synthesis validation & routine-tier promotion + `axon eval` (FR-140…FR-145,
  ADR-029/030/031)** — an eval harness (`axon eval`) with golden sets drawn from
  AXON's own tasks, graded pass/fail by rubric against any `(provider, model)` pair;
  an eval-gated admission gate so `models.routine: ollama:<model>` is supported only
  once it passes thresholds on this machine (`doctor` reports status; an `eval-drift`
  automation re-runs evals when a model's Ollama digest changes); and a per-call
  verification cascade — a schema-valid local `routine` answer is scored by a cheap
  local judge (`models.verify`) and escalates to Claude below `models.verify_min_score`,
  all ledgered. Default off.
- **Contradiction-aware ask (FR-146/FR-147)** — when retrieval surfaces sources that
  disagree, the grounded answer flags it: both claims cited with their dates,
  newest-valid preferred, no silent averaging. One clause on the synthesis prompt
  emits a leading `CONFLICT` sentinel, stripped into an additive `Answer.Conflicted`
  flag (grounding gate, `NOT_FOUND`, and citation contract unchanged). Surfaced on
  `vault_ask` (`⚠ Sources conflict`) and the dashboard `/api/ask` response. One
  chokepoint call, no extra tokens.
- **Ambient related-notes surface (FR-148/FR-149/FR-150)** — the embeddings already
  present exposed as a live "related to what I'm looking at" surface: `axon related
  <note>`, a `vault_related` MCP tool (default set + agentic read allowlist —
  zero-spend), and a dashboard **Related** tab backed by `GET /api/related` (gated by
  `dashboard.related_enabled`). Pure vector math over the ANN seam — **no model
  call**, <100 ms warm.
- **Resurfacing with review scheduling (FR-151/FR-152/FR-153)** — the resurfacer
  upgrades from "propose once, silence forever" to a light FSRS-flavoured review
  queue: per-pair `{rung, due, last}` schedule (interval ladder
  `resurfacing.intervals_weeks`, default `[1,2,4,8,16]`) persisted in
  `automation_state`, fed by the pair's own queue+archive outcomes (dismiss +1 rung,
  accept +2 — intervals lengthen on acceptance). An opt-in routine-tier contradiction
  check (gated on `budget_tokens > 0`) reclassifies genuine clashes into a new
  `contradicts` review kind.
- **Near-duplicate merge proposals (FR-154/FR-155/FR-156, ADR-032)** — a weekly
  zero-model `merge-proposals` sweep proposes near-duplicate note pairs (mean-vector
  cosine ≥ `merge.threshold`, default 0.92) to the review queue. Accepting runs the
  wikilink-safe `vault.Merge` (the destructive-op design pass): the more inbound-linked
  note survives, keeps its prose and gains the loser's body in an additive
  `axon:merged` block, all inbound links retarget to the survivor, and the loser is
  archived intact to `.trash/merged/` — **never deleted** (zero broken links, both
  originals recoverable). No MCP tool, no agent-driven merge; disabled by default.

## [1.1.0] — 2026-07-06

**"Now it answers."** Grounded-or-silent `ask` on three surfaces with wikilink
citations, ANN retrieval, a local reranker, standing research questions, entity
pages, project pulse, a capture endpoint, OCR, and contradiction-aware memory
distillation (FR-108…FR-133, ADR-023…ADR-027).

### Added

- **`axon ask` (FR-108…FR-110)** — grounded-or-silent answers from the vault:
  hybrid retrieval builds a bounded context, a deterministic gate refuses
  unanswerable questions for free, one synthesis-tier call answers with
  `[[wikilink]]` citations, and a code-enforced contract guarantees every citation
  resolves to a retrieved note — hallucinated or missing citations surface as
  refusals listing the sources.
- **`vault_ask` + dashboard Ask panel (FR-111/FR-112, ADR-023)** — the grounded
  `ask` engine on two more surfaces: a `vault_ask` MCP tool (Claude Code + Desktop)
  and a dashboard **Ask** panel backed by `POST /api/ask` (the dashboard's first
  token-spending action), guarded identically to review actions and gated by a
  `dashboard.ask_enabled` kill-switch. `vault_ask` is excluded from the agentic
  allowlist by construction.
- **Pluggable ANN vector index (FR-113…FR-115, ADR-025)** — a `db.VectorIndex` seam
  with an in-house IVF-flat approximate index behind `retrieval.index: ann` (default
  `brute`). Deterministic spherical k-means into an in-file `vec_centroids` table;
  auto-falls back to exact brute below `retrieval.ann.threshold` (default 10 000) and
  is bit-identical to brute at `nprobe ≥ k`. Single-file SQLite promise intact — no
  server, no new dependency.
- **Standing research questions (FR-116/FR-117)** — a weekly `research-questions`
  automation answers questions in `03-Resources/Research Questions.md` from your vault
  (grounded `ask` per question) into an `axon:answers` block with citations and a
  confidence marker. Never edits your prose; disabled by default.
- **Contradiction-aware memory distillation (FR-118…FR-120)** — `memory-distill`
  detects when a new memory entry conflicts with an existing fact and proposes a
  `reconcile` review-queue item; accepting tombstones the old fact (never deletes)
  and holds the new one until confirmed.
- **Browser capture endpoint (FR-121/FR-122, ADR-024)** — `POST /api/capture` and a
  served same-origin `/capture` page drop a URL/selection into `00-Inbox/` for the
  `capture` automation, guarded like review/ask actions and gated by
  `dashboard.capture_enabled`. Ships a bookmarklet + macOS Shortcuts recipe.
- **OCR for scanned PDFs (FR-123…FR-125, ADR-026)** — fallback OCR when a PDF's text
  layer is too thin: an on-device Apple Vision helper (macOS) or a tesseract pipeline,
  wired into the ingestion extract path; `axon init`/`doctor` provision and report it.
- **Local reranker (FR-126/FR-127, ADR-027)** — an optional Ollama pointwise reranker
  behind `retrieval.rerank` that re-scores an overfetched candidate pool, best-effort
  (falls back to the fused order on error). A retrieval primitive outside the
  chokepoint (like embeddings).
- **Entity pages (FR-128…FR-130)** — an `entity-pages` automation extracts named
  people/projects from new notes into an auto-maintained `Entities/` index with
  wikilink-safe mention lists. Disabled by default.
- **Project pulse (FR-131…FR-133)** — a weekly `project-pulse` automation reads
  `01-Projects/` + USER goals into an `axon:pulse` block (progress, stalls, next
  actions) and nudges stale projects to the review queue. Disabled by default.

## [1.0.0] — 2026-07-04

The v1 contract is complete: every requirement in FR-01…FR-107 / NFR-01…NFR-14
is implemented, audited (see `docs/PRODUCTION-READINESS.md`), and documented.
Cardinal rule 1 is generalized by ADR-015 (no generative call — Claude or
local — bypasses the token manager); the vault contract is unchanged.

### Added

- **Agentic write tools (ADR-022, FR-105…FR-107)** — opted-in agentic
  automations may now call the managed-block-safe write tools (`vault_patch`,
  `vault_write`, `daily_append`, `memory_remember`; never `vault_move`),
  enforced by the same dual allowlist as reads (client `--allowedTools` +
  server `axon mcp --tools`). `axon run <name> --dry-run` spawns the agent
  with **server-enforced report-only** write tools — each validates and
  reports what it would change without mutating (a real preview at real token
  cost). A mid-run budget kill leaves a prefix of per-tool-atomic, idempotent
  writes — never a half-edited note; a re-run converges. Compaction is the
  first user: its agentic path writes `axon:summary` via `vault_patch`
  (archive-first and the `agentic:false` deterministic write unchanged).
  Closes ADR-017's two reasons for deferring write tools.
- **Heartbeat synthesis (opt-in)** — setting `automations.heartbeat.model`
  (e.g. `classify`, local-routable per ADR-015) adds one budget-checked,
  single-line synthesis to the heartbeat block when something is noteworthy
  (inbox items, pending review proposals, or an active budget guard);
  budget defer or model error degrades absolutely to the plain status line.
  Default remains zero model work.
- **ADR follow-up slices (FR-102…FR-104)** — the link-suggester now remembers
  what it proposed (`link-suggester:proposed`, shared proposal-memory helpers
  with the resurfacer): a dismissed suggestion stays dismissed and embedding
  growth stops re-queuing the same pairs. Resolved review-queue lines older
  than 7 days compact into `.axon/review-queue-archive.md` whenever a
  resolution rewrites the queue (archive-append before rewrite; emptied
  section headers dropped; pending lines untouched). And the generated hook
  settings wire `SessionEnd`: cleanly-ended sessions distill on the next
  tick via a sticky `ended` flag instead of waiting out the 30-minute idle
  heuristic, which stays as the crash fallback. Closes the last ADR-noted
  follow-ups (ADR-018/020/021).
- **Conditional feed polling (FR-101)** — the subscriptions automation now
  stores each feed's `ETag`/`Last-Modified` and polls with
  `If-None-Match`/`If-Modified-Since`; a `304 Not Modified` is a free skip
  (no download, no parse, no state churn), reported as "N unchanged (304)"
  in the run summary. Validators live in `automation_state` and prune
  automatically when feeds are removed. Closes ADR-019's remaining
  optimization note.
- **`axon subscribe` CLI (FR-100)** — manage feed subscriptions without
  hand-editing config: `axon subscribe <url>` fetches the feed through the
  egress-policied fetcher, parses it (gofeed), and appends it to
  `subscriptions.feeds` via the comment-preserving editor with re-validation
  and an atomic write (`--no-verify` skips the fetch); a host outside the
  ingest policy is refused with guidance unless `--allow` explicitly opts it
  into `ingest_domains_allow`. `subscribe list` shows each feed's seen-state;
  `subscribe remove <url>` drops the feed and its seen entry so
  re-subscribing re-baselines (subscribe-from-now). Closes ADR-019's noted
  follow-up slice.
- **Session memory capture (ADR-021, FR-97…FR-99)** — AXON now remembers
  what your sessions decided. The Stop hook records finished vault sessions
  (paths only, silently, gated by `memory.capture_sessions` — on by default,
  off for stricter profiles); the new `session-distill` automation distills
  each idle session once with a single classify-tier call (local-routable)
  into decision/lesson/preference entries in MEMORY.md (`source: session`),
  where the SessionStart injection already surfaces them to every future
  session and memory-distill's compaction curates them over time. Redaction
  applies before the model sees any transcript text (NFR-14).
- **Review-queue actions on the dashboard (ADR-020, FR-94…FR-96)** — a new
  Review tab lists every pending proposal (link suggestions, structured
  inbox-triage moves, resurfaced connections, capture records) with one-click
  accept/dismiss. Accepts are wikilink-safe by construction: links land in
  the note's `axon:links` managed block, triage moves go through the
  link-rewriting `vault.Move`, and the queue file itself is only touched by
  the new `.axon/`-guarded rewriter. The dashboard's mutation surface is
  exactly these resolutions (JSON + custom-header guard forcing a CORS
  preflight; loopback + Host-guard unchanged). Inbox-triage now emits
  structured JSON proposals so its accepts actually move notes. Also ships
  **FR-64** — every chart's data exports as CSV/JSON — closing the final
  open requirement of the original v1 contract.
- **RSS/feed subscriptions (ADR-019, FR-91…FR-93)** — declare feeds in
  `subscriptions.feeds` and AXON polls them hourly through the same
  egress-policied fetcher as every ingest, feeding new items into the
  standard pipeline (deduped, redacted, ledgered, optionally enriched on the
  routine tier). Volume is structural: subscribe-from-now (no backfill
  floods), at most `max_per_tick` items per feed per tick, one attempt per
  item. The agentic weekly digest now synthesizes across your subscriptions.
  New dependency: `mmcdole/gofeed` (feed parsing; ADR-justified).
- **Proactive layer (ADR-018, FR-88…FR-90)** — AXON now comes to you. A daily
  `briefing` automation writes an `axon:briefing` block into the daily note
  (notes changed, new sources, automation activity, review queue, budget)
  plus a short narrative on the routine tier — local-routable, budget-capped,
  degrading to facts-only under pressure — and every Claude session opens
  with a one-line pointer to it. A weekly `resurfacer` proposes review-queue
  connections between what you're working on now and notes dormant for 90+
  days, by mean-chunk-vector similarity (shared with the graph view), with
  persistent proposal memory so nothing is suggested twice. Zero model calls.
- **Agentic automations (ADR-017, FR-84…FR-87)** — knowledge-digest and
  compaction now run Claude headlessly **with AXON's read-only MCP tools**
  (vault/knowledge search, note reads, backlinks): the digest actually reads
  the week's sources instead of being told a count, and compaction checks
  backlinks before distilling. Enforcement is structural: no built-in tools,
  a per-call `--allowedTools` list **and** a server-side `axon mcp --tools`
  filter, bounded turns, and a streaming kill-switch that terminates a run
  the moment `automations.<name>.budget_tokens` is exceeded — with the real
  accumulated usage ledgered on every path, including kills
  (`token.run_budget_kill`). `agentic: false` per automation restores the
  one-shot behavior, which also remains the automatic degradation path.
  Note: `budget_tokens` was previously display-only and is now enforced for
  all automations (one-shot calls defer when the estimated input exceeds it).
- **Universal capture (ADR-016, FR-26 + FR-81…FR-83)** — the new `capture`
  automation turns `00-Inbox/` into a capture funnel: paste a URL on its own
  line in any inbox note, or drop a PDF/file into the folder, and AXON ingests
  it within minutes through the standard pipeline (egress-policied, deduped,
  ledgered), files the result under `03-Resources/Knowledge/`, and moves the
  original wikilink-safely to `04-Archive/Capture/YYYY-MM/` — nothing is ever
  deleted, and inbox notes are never modified. Ticks are change-gated on the
  inbox listing; failures are remembered (no retry spam) and surfaced once in
  the review queue. Mobile capture works with zero mobile code via vault sync.
  New optional config: `capture.enrich` (heuristic default | claude via the
  chokepoint) and `capture.archive_dir`.
- **Local model routing (ADR-015, FR-77…FR-80)** — the `classify` and
  `routine` tiers can now be served by local providers via provider-prefixed
  model strings: `models.classify: "ollama:qwen3:8b"` (local Ollama chat) or
  `models.classify: "apple"` (Apple Foundation Models on-device model;
  macOS 26+, Apple Silicon, Apple Intelligence, classify tier only — delivered
  with the same compiled-at-init Swift helper pattern as Apple embeddings).
  Local calls run through the token-manager chokepoint and are fully ledgered
  (provider-prefixed model strings, `cost_usd` null) but **budget-exempt**:
  they never consume the day/week windows or trigger defer/deny/downgrade —
  budgets keep meaning Claude quota. On local failure or schema-invalid
  output, `models.local_fallback` (default `claude`) retries locally once and
  then falls forward to Claude through the normal budget path, or fails
  visibly when set to `fail`. `axon configure models` gained a provider step
  with convergence probes, and `axon doctor`/`axon init` report and converge
  the configured local providers. New optional config: `models.ollama_host`,
  `models.local_fallback`, `models.apple_helper`.

- **Build versioning you can check** — `axon version` reports the version,
  commit, build date, and Go/OS/arch, with `--short` for scripts; `axon --version`
  works too. Every build is stamped: `make build`/`make release` inject the exact
  `git describe` version, commit, and date via `-ldflags`, and a plain
  `go build`/`go install` falls back to Go's embedded VCS commit — so no build is
  ever an anonymous `0.0.0-dev`.
- **`axon automations` [--json]** — list every automation with its enabled state
  (config + policy), purpose, schedule, and last run (status, when, tokens, and
  the skip/error reason).
- **`axon health` [--json]** — a 0–100 vault health score with a letter grade and
  per-dimension breakdown: index & link integrity, automation reliability, and
  knowledge freshness. Read-only; no model call.
- **`axon ingest --enrich`** — opt into Claude-backed metadata enrichment routed
  through the token-manager chokepoint; the ingest result now reports how the
  metadata was produced and the tokens it cost (deterministic heuristic remains
  the default, at zero tokens).
- **Professional install/update system** — a self-documenting `Makefile`
  (`make` lists everything): `doctor`, `install`, `setup`, `update`, `reload`,
  `uninstall`, and `release` (cross-compiled macOS/Linux × amd64/arm64 binaries).
  A cross-platform dependency **preflight** (`scripts/preflight.sh`, `make doctor`)
  checks the build + runtime toolchain and prints the exact install command for
  your package manager. New Linux (`systemd --user`) install/update/uninstall
  scripts sit alongside the macOS ones, and an `update` flow rebuilds, swaps the
  binary (reporting the version delta), converges the profile (`axon init` — DB
  migrations, scaffold, wiring, dashboards), restarts the daemon, and lists newly
  shipped config settings. See [INSTALL.md](INSTALL.md).

### Changed

- **Clearer console output** — a shared `internal/ui` styler gives commands
  consistent colour + status glyphs, auto-disabled for pipes/non-TTY and honouring
  `NO_COLOR`/`FORCE_COLOR`. Errors now render as a clear block with an actionable
  fix hint (e.g. a missing config points you at `axon init`).
- **Descriptive budget-guard messaging** — when the token guard pauses a
  non-essential automation, the skip reason now names the window and threshold
  (e.g. "budget guard active — daily 82% ≥ 80% …") instead of a bare "budget",
  and `axon status` shows the same reason.
- `make uninstall` replaces `make uninstall-macos` (now OS-aware); `make setup`
  works on Linux as well as macOS.

## [0.10.0] — 2026-06-28

Completed the remaining deferred requirements, so every M/S requirement in the
contract (`docs/03`) is now implemented.

### Added

- **PDF ingestion (FR-21)** — PDFs go through the same fetch→extract→enrich→
  chunk→embed pipeline as URLs and text files (`internal/ingestion`, via
  `ledongthuc/pdf`); malformed PDFs surface a clear error, never a crash.
- **`config get` / `config set` (FR-04)** — read and update config values by
  dotted key (resolved relative to the active profile). `set` preserves comments
  and formatting and re-validates before writing; invalid changes are refused.
- **`stop` (FR-04)** — gracefully stops the daemon for the active profile via a
  per-profile pidfile (`start` now writes one); stale pidfiles are cleaned up.
- **`metrics_query` MCP tool (FR-50)** — token-ledger aggregates (by day/
  operation/model) plus current budget windows, for dashboards and agents.
- **Obsidian MCP interop (FR-54)** — `profiles.<p>.interop.obsidian_mcp` registers
  a community Obsidian MCP server alongside AXON's own when running
  `axon mcp install`; AXON's server stays the default vault contract.
- **`api_key` direct-API adapter (FR-33/FR-40/FR-41)** — in `auth_mode: api_key`
  AXON calls the Anthropic API directly (`anthropic-sdk-go`) with **exact
  `count_tokens`** pre-flight and per-token cost; subscription/enterprise still
  use Claude Code. Still mediated by the token-manager chokepoint.
- **Keychain secrets** — `keychain:NAME` references resolve from the OS keychain
  (`zalando/go-keyring`), alongside `env:NAME`.

### Notes / optional future polish (not contract requirements)

- ~~Heartbeat one-line model synthesis~~ — built (opt-in via
  `automations.heartbeat.model`; see Unreleased).
- ~~Resolved-IP pinning across the dial~~ — closed as covered: the dialer's
  `Control` hook validates the concrete resolved IP on every connection
  attempt, so DNS-rebinding to internal ranges is already refused at dial
  time; pinning adds no security value (evaluated 2026-07-04).

## [0.9.0] — 2026-06-28

Phase 9 — multi-client integration (Claude Desktop) (FR-74…FR-76, ADR-012,
Component 13). With this, the full spec pack (`docs/00`–`13`) is implemented.

### Added

- **`axon mcp install --client code|desktop`** — registers the AXON MCP server
  with a Claude client. `desktop` merges a profile-scoped entry into
  `claude_desktop_config.json` **non-destructively** (other servers preserved;
  an unparseable existing file is refused, not clobbered); `code` (re)generates
  the project `.claude/` wiring. `--print` previews the registration JSON.
- **`internal/clients`** — OS-specific Claude Desktop config-path resolution,
  the non-destructive merge, and registration detection.
- **Per-client `doctor` checks** — `client:claude-code` and
  `client:claude-desktop` report whether AXON is registered (and for which
  profile) and state Claude Desktop's reduced guarantees honestly: tools only,
  no hooks/skills/profile injection.

### Notes

- Claude Desktop receives AXON's **tools** but not hooks, skills, subagents or
  headless automations (those remain Claude Code). AXON's own tools stay
  wikilink-safe and path-sandboxed **in the server**, so vault safety does not
  depend on the client.

## [0.8.0] — 2026-06-28

Phase 8 — the personal memory & identity layer (FR-70…FR-73, NFR-14, ADR-011,
Component 12).

### Added

- **Identity layer** (`internal/identity`) — a first-class set of vault notes
  under `02-Areas/Profile/`: `USER.md` (profile), `SOUL.md` (assistant persona &
  boundaries) and `MEMORY.md` (durable entries in an `axon:memory` managed
  block). Generated wikilink-safely and never clobbering human edits.
- **`axon onboard`** — an interactive, idempotent wizard (no model call) that
  interviews the user, writes the identity layer, and (re)ensures the Claude Code
  wiring. Supports `--non-interactive`, `--from <file>` (YAML/JSON answers) and
  `--json` (secret-free report). `axon init` now nudges to run it.
- **SessionStart identity injection** — the hook injects a token-bounded snapshot
  of USER + SOUL + recent `MEMORY` into each Claude Code session with **no model
  call**; governed by `profiles.<p>.memory` (`inject`, `session_tokens`,
  `recent_entries`) and disablable per profile.
- **`memory_remember`** MCP tool — appends a dated durable entry to the
  `axon:memory` block, wikilink-safe, never touching human prose.
- **`memory-distill`** automation — distils recent daily-note activity into new
  memory entries and compacts an over-long block, through the token manager,
  change-gated and dry-run aware.

### Security

- **Personal-data privacy (NFR-14)** — the identity layer never reaches logs,
  events, the token ledger or exports; redaction (`policy.redaction_rules`) is
  applied to the injected block before any egress.

## [0.7.0] — 2026-06-28

The initial feature-complete build, implemented in phases against
[`docs/11-build-roadmap.md`](docs/11-build-roadmap.md).

### Added

- **CLI & bootstrap** — `axon init` (idempotent, verbose), `config validate`,
  `doctor`, profile resolution and a single self-contained binary.
- **Vault core** — wikilink-safe filesystem (`read`/`write`/`patch`/`move`,
  atomic, sandboxed), frontmatter parsing, `axon:*` managed blocks, link-graph
  builder, vault scaffold + note templates, and `reindex`.
- **Knowledge ingestion & search** — fetch → extract → clean → redact → hash →
  enrich → write → chunk → embed (Ollama) → index; hybrid FTS5 + vector search;
  `ingest`/`search` commands. (Vectors use a brute-force cosine store behind a
  repository seam — see ADR-010.)
- **Token & context manager** — the mandatory chokepoint (`Authorize`/`Run`/
  `BuildContext`/`Status`): local pre-flight estimate, day/week token windows,
  model selection + downgrade, ledger, and `status`.
- **Automation engine** — portable scheduler (cron + jitter + locks + catch-up),
  the run lifecycle (change-gate → budget pre-check → dry-run → record), the real
  `claude -p` adapter, the nine standard automations, and `run`/`start`.
- **Agent bridge** — the AXON MCP server (wikilink-safe vault tools, hybrid
  search, token/automation tools), Claude Code hooks (SessionStart/PreToolUse/
  PostToolUse/Stop), and a plugin (skills + subagents + `CLAUDE.md`).
- **Dashboard & observability** — a localhost HTTP API + SSE, an embedded
  Vite/React/Recharts SPA (tokens, usage, runs, ingestion, vault growth,
  knowledge graph, activity feed), `/health`, and in-vault Dataview dashboards.
- **Multi-profile, policy & hardening** — full profile isolation, policy
  enforcement everywhere, OS service units (`service`), portable `export`,
  `profiles` inspection, and docs.

### Security

- Vault path-traversal sandbox; SSRF protection (per-redirect egress
  re-validation, link-local/metadata IP block); agent-path local-file ingestion
  refused; provenance-field redaction; dashboard `Host`-header (anti
  DNS-rebinding) guard; hardened `PreToolUse` denylist.

### Notes / not yet implemented (at 0.7.0)

- PDF ingestion, the optional `auth_mode: api_key` in-process adapter, heartbeat
  model synthesis, richer `/health`, DNS-rebinding IP pinning on ingest, and
  `config get/set`. *(PDF ingestion, the api_key adapter and `config get/set`
  were implemented in 0.10.0.)*

[Unreleased]: https://github.com/jandro-es/axon/compare/v1.3.9...HEAD
[1.3.10]: https://github.com/jandro-es/axon/releases/tag/v1.3.10
[1.3.9]: https://github.com/jandro-es/axon/releases/tag/v1.3.9
[1.3.8]: https://github.com/jandro-es/axon/releases/tag/v1.3.8
[1.3.7]: https://github.com/jandro-es/axon/releases/tag/v1.3.7
[1.3.6]: https://github.com/jandro-es/axon/releases/tag/v1.3.6
[1.3.5]: https://github.com/jandro-es/axon/releases/tag/v1.3.5
[1.3.4]: https://github.com/jandro-es/axon/releases/tag/v1.3.4
[1.3.3]: https://github.com/jandro-es/axon/releases/tag/v1.3.3
[1.3.2]: https://github.com/jandro-es/axon/releases/tag/v1.3.2
[1.3.1]: https://github.com/jandro-es/axon/releases/tag/v1.3.1
[1.2.5]: https://github.com/jandro-es/axon/releases/tag/v1.2.5
[1.2.0]: https://github.com/jandro-es/axon/releases/tag/v1.2.0
[1.1.0]: https://github.com/jandro-es/axon/releases/tag/v1.1.0
[1.0.0]: https://github.com/jandro-es/axon/releases/tag/v1.0.0
[0.10.0]: https://github.com/jandro-es/axon/releases/tag/v0.10.0
[0.9.0]: https://github.com/jandro-es/axon/releases/tag/v0.9.0
[0.8.0]: https://github.com/jandro-es/axon/releases/tag/v0.8.0
[0.7.0]: https://github.com/jandro-es/axon/releases/tag/v0.7.0
