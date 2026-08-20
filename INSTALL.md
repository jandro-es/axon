# Installing AXON

This guide gets you from a clean machine to a running AXON in about **15
minutes**. The first part assumes no terminal experience: every step is one
action, says what it does, and shows what success looks like. Developers can
jump to the [fast path](#fast-path-for-developers) at the bottom.

> **Your notes are safe.** Your Obsidian vault is plain Markdown and is
> **never** modified by install, update, or uninstall. Everything AXON derives
> from it (its database, its index) can be rebuilt at any time.

## What you need

- A **Mac with Apple silicon** (any M-series chip), or a Linux machine.
  Windows works too — see the [fast path](#windows--other).
- About **15 minutes**.
- An **Obsidian vault** (a folder of Markdown notes) — or nothing at all:
  AXON will create and scaffold one for you.
- A **Claude subscription** (e.g. Claude Pro/Max) or a Claude enterprise
  login. AXON uses your login, never an API key.

**What gets installed:** one small program called `axon` that runs quietly in
the background beside your vault (this kind of background program is called a
*daemon*), plus two companions it relies on: the `claude` command-line tool
(how AXON talks to Claude on your account) and **Ollama** (a small local
service that lets AXON understand the *meaning* of your notes without sending
them anywhere).

---

## Part 1 — Install on a Mac, step by step

### Step 1 — Open Terminal

Terminal is the Mac app where you type commands instead of clicking. Press
`⌘ + Space`, type `Terminal`, press Return. A window appears with a prompt
waiting for you to type. Every command below gets pasted into that window,
followed by Return.

### Step 2 — Install Homebrew (skip if you have it)

Homebrew is the standard installer for developer tools on the Mac — it fetches
and updates programs for you. Check whether you already have it:

```
brew --version
```

**You should see:** a version number like `Homebrew 4.x`. If instead you see
`command not found`, install it by pasting the one command shown at
[brew.sh](https://brew.sh) and following its prompts (it explains everything
it will do and asks for your Mac password once).

### Step 3 — Install Ollama and its reading model

```
brew install ollama
```

**You should see:** several lines of progress ending without errors. Now start
Ollama's background service (this also makes it start automatically after a
reboot):

```
brew services start ollama
```

**You should see:** `Successfully started ollama`. Then download the model AXON
uses to index the meaning of notes (~270 MB, done once):

```
ollama pull nomic-embed-text
```

**You should see:** a progress bar reaching 100%, ending with `success`.

> **Not allowed to run Ollama** (e.g. a locked-down work Mac)? AXON can use
> Apple's built-in on-device models instead — finish the install, then run
> `axon configure embeddings apple --reindex`. Nothing is downloaded and no
> server runs. Details in the [Guide §4](docs/GUIDE.md#4-configuration).

### Step 4 — Install the claude tool and sign in

This is the official Claude command-line tool from Anthropic; AXON drives it
using your subscription:

```
npm install -g @anthropic-ai/claude-code
```

If `npm` is missing, first run `brew install node`, then repeat. (Or use the
installer at [claude.com/claude-code](https://claude.com/claude-code).) Then
sign in — a browser window opens for your normal Claude login:

```
claude login
```

**You should see:** the browser confirm the login, and the terminal print a
success message.

### Step 5 — Install AXON

This command downloads the latest AXON release (its integrity is verified
before anything runs) and starts the guided setup:

```
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
```

**You should see:** the download complete, then **`axon setup`** begin asking
questions right there in the terminal:

- **Vault path** — where your Obsidian vault folder is. If you don't have
  one, accept the suggested location and AXON creates and organises it.
- **Profile name** — accept `personal` unless this is a work machine (see
  [docs/PROFILES.md](docs/PROFILES.md)).
- **Embeddings provider** — accept `ollama` (or choose `apple` if you skipped
  Ollama in Step 3).

Setup then provisions everything — config, database, vault folders, Claude
wiring, and a service so AXON starts automatically when you log in — printing
each step as it goes. It is safe to re-run at any time; it never duplicates or
overwrites your content.

### Step 6 — Let AXON check itself

```
axon doctor
```

**You should see:** a list of checks, each marked ok / warn / fail, ending
with an overall status. It will look something like:

```
ok    claude       claude CLI found and authenticated
ok    ollama       reachable; nomic-embed-text present
warn  media        yt-dlp not found (podcast/YouTube captions disabled)
      ↳ fix: brew install yt-dlp
```

Two things to know:

- A **warn** means an *optional* capability is off — AXON still works. A
  **fail** means something AXON needs is broken.
- Every check you can act on prints a **`↳ fix:` line containing the exact
  command that fixes it**. Copy, paste, run, then run `axon doctor` again.

### Step 7 — See it running

Setup already started AXON in the background. Open your web browser at:

**<http://127.0.0.1:7777>**

**You should see:** the AXON dashboard — an Overview with a "Needs you" panel,
token budgets, and activity. This page is served from your own machine only;
nothing is exposed to the network. (If it doesn't load, run `axon start` in
the terminal and refresh.)

### Step 8 (optional but recommended) — the menu bar app

**Axon Companion** puts AXON's status, budgets and controls in your Mac's menu
bar, with a guided first-run tour.

1. Download `Axon-<version>.zip` from the
   [latest release](https://github.com/jandro-es/axon/releases/latest).
2. Double-click the zip, then drag **Axon.app** into your **Applications**
   folder.
3. Open it. The app is signed and notarised by Apple's process, so it opens
   normally — no security warnings to bypass. A brain icon appears in the menu
   bar.

Companion is strictly optional: everything it shows also lives in the
dashboard and CLI.

### Step 9 (recommended) — let scheduled automations use Claude

AXON's background automations call Claude without a terminal attached, which
needs a long-lived token created once:

```
claude setup-token
```

**You should see:** a browser confirmation, then a line beginning
`CLAUDE_CODE_OAUTH_TOKEN=`. Put that line into the file `~/.axon/.env` (Step 5
created it), or re-run `axon setup` which offers to store it for you. Without
this, interactive use works fine but scheduled automations will warn in
`axon doctor`.

---

## Your first 10 minutes

1. **Feed it one page.** Pick any article you'd like to keep and run:

   ```
   axon ingest https://en.wikipedia.org/wiki/Zettelkasten
   ```

   **You should see:** progress lines ending in the path of a new note. Open
   your vault in Obsidian — the note is there under `03-Resources/Knowledge/`,
   cleaned up, linked, and indexed.

2. **Ask your vault.**

   ```
   axon ask "What is a Zettelkasten?"
   ```

   **You should see:** an answer built *only* from your notes, ending with
   `[[wikilink]]` citations. If your vault doesn't contain the answer, AXON
   says so rather than making something up.

3. **Watch it on the dashboard.** Back at <http://127.0.0.1:7777>, the
   ingest and the question are both in the activity feed, with their token
   cost in the ledger. Nothing AXON does is invisible.

4. **Meet the review queue.** As automations run (triage, link suggestions,
   resurfacing), their proposals collect in the **Review** tab for you to
   accept or dismiss with one click. AXON proposes; you decide.

---

## When something goes wrong

- **Run `axon doctor` first.** It exists to answer "why isn't this working"
  and prints the fixing command for anything actionable.
- **The dashboard won't load** — run `axon start`. If it says the port is
  taken by something else, `axon doctor`'s `dashboard-port` check names the
  program holding it.
- **Automations fail with "claude not found"** even though `claude` works in
  your terminal: the background service needs its configuration *reloaded*,
  not just restarted — a subtle system behaviour AXON knows about. Run
  `axon doctor` and follow its `↳ fix:` line, which prints the correct reload
  command.
- **A warning about `ANTHROPIC_API_KEY`** — you have an API key set in your
  environment, which would silently switch Claude from your subscription to
  pay-per-use API billing. Remove it (the doctor message says how).
- **Still stuck?** The [Guide's troubleshooting chapter](docs/GUIDE.md#16-troubleshooting)
  goes deeper, and logs live under `~/.axon/<profile>/logs/`.

## Keeping it current / removing it

```
axon update       # checksum-verified self-update, when the dashboard says one exists
axon uninstall    # remove the service + binary; add --purge to also remove ~/.axon
```

Your vault is preserved in every case — uninstalling AXON leaves you with a
perfectly ordinary Obsidian vault.

---

## Part 2 — Install on Linux

The same sequence, with Linux commands (Debian/Ubuntu shown):

```bash
curl -fsSL https://ollama.com/install.sh | sh     # 1. Ollama
ollama pull nomic-embed-text                      # 2. the embedding model
npm install -g @anthropic-ai/claude-code          # 3. the claude CLI (needs Node 18+)
claude login                                      # 4. sign in
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash   # 5. AXON
axon doctor                                       # 6. verify
```

The service is installed as a `systemd --user` unit; `axon service status`
reports it, and `systemctl --user status axon-<profile>` is the native view.
Steps 6–9 and the first-10-minutes walkthrough above apply unchanged.

---

## Fast path for developers

```bash
# Release install — no Go/Node/repo needed (SHA-256 verified):
curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
#   --user      install to ~/.local/bin (no sudo)
#   --no-setup  binary only, skip axon setup

# From source (Go 1.26+, Node, make; run `make` alone for all targets):
git clone https://github.com/jandro-es/axon.git && cd axon
make doctor     # check build dependencies, with install commands
make setup      # build + install + provision + login service
make setup ARGS="--no-ollama"       # skip Ollama management
make setup ARGS="--no-service"      # no auto-start service
make setup PREFIX=$HOME/.local      # user-local, no sudo

# Later:
axon update     # release installs: checksum-verified self-update
make update     # source installs: rebuild + converge (migrations, scaffold, service) + reload
make uninstall  # remove daemon + binary (keeps ~/.axon); ARGS="--purge" removes ~/.axon too
make release    # cross-compiled, stripped binaries for macOS/Linux amd64+arm64 → dist/
```

Binaries are pure Go (no cgo), so cross-compilation needs no C toolchain. The
dashboard SPA is embedded at build time; without Node a fallback page is
served.

### Windows / other

```bash
make install            # build + install just the binary
axon init               # scaffold the profile + database
axon service install    # emit a Task Scheduler unit; register it as printed
```

### Service lifecycle — reload, don't restart

launchd and systemd keep the unit definition they parsed at load time, so
**rewriting a unit file changes nothing about a running daemon**. After
`axon service install` rewrites a unit, apply it with the reload command the
CLI prints (launchd: `bootout` + `bootstrap`; systemd:
`systemctl --user daemon-reload` then restart). `make reload` and every
`axon doctor` remediation already do this correctly.

### Companion: verifying and removing

The Companion app (macOS 26+) is Developer ID-signed, notarised, and stapled,
so it opens normally and works offline. Verify a download:

```bash
spctl -a -vv /Applications/Axon.app
# accepted
# source=Notarized Developer ID
# origin=Developer ID Application: Filtercode LTD (5R59WRDGLW)
```

Anything else means a damaged or tampered download — fetch a fresh copy rather
than bypassing Gatekeeper. To remove: quit from the popover, drag the app to
the Trash, and optionally `defaults delete com.axon.companion`. Companion
stores nothing outside its preferences and writes nothing into `~/.axon` or
your vault.
