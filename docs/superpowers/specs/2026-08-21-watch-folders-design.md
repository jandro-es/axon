# Watch-folders — drop a file outside the vault, have it flow in (design)

**Status:** approved 2026-08-21 (all three decisions Jandro-picked-recommended:
**move** into `00-Inbox` rather than copy-with-a-seen-ledger; **no** per-folder
kind hints; path validation is structural **plus** a sensitive-root deny-list).
**FR-208, FR-209; ADR-040.** Graduates `docs/20` **E1**.

**No migration.** Schema stays `0007`. No new automation — `capture` grows a
pre-step, so the built-in count stays **26**.

## The idea

Everything AXON ingests today is either already in the vault (`00-Inbox`,
swept by `capture` every five minutes) or a URL the owner handed it. E1 asks
for a third path: drop a PDF in `~/Downloads/axon`, screenshot into a watched
folder, export from any app, and have it arrive without opening Obsidian.

The shape falls out of what already exists. `capture` runs on a five-minute
tick, ingests `00-Inbox` files through `Pipeline.Ingest` with
`AllowLocalFiles: true`, archives the originals wikilink-safely, remembers
failures, and reports them to the review queue. If a watched-folder file can
be *moved into `00-Inbox`* before that sweep runs, every one of those
behaviours is inherited with no new code downstream.

**Why this one gets an ADR when the last three did not.** The preceding three
slices moved no boundary: no new sink, no new model path, no new egress, no
schema change. Those criteria say nothing about **ingress**, and this is the
first time the daemon reads host directories outside the vault on a schedule.
ADR-040 records the shape and the refusals.

## FR-208 — Config, validation, and the doctor check

### The config surface

```yaml
capture:
  enrich: heuristic
  archive_dir: "04-Archive/Capture"
  watch_folders:                      # absent or empty ⇒ no new behaviour
    - "/Users/me/Downloads/axon"
    - "/Users/me/Pictures/Screenshots"
```

`CaptureConfig.WatchFolders []string` (`yaml:"watch_folders,omitempty"`).
Absent or empty is the default on **both** profiles, so the feature "ships
disabled" without a separate toggle — there is nothing to turn off.

### Validation splits by what can be known when

**At config load** — `validateWatchFolders(p Profile)`, a profile-level check
in the `Config.Validate` loop beside `validateRecipes` and `validateVision`
(the vault-containment rule needs `p.Paths().VaultPath`, which the
capture-block-only `validateCapture` cannot see). It refuses:

| refusal | why |
|---|---|
| relative path, or containing `..` | a watched folder is a fixed location, not a computed one |
| a path inside the vault | `capture` already owns `00-Inbox`; a watched folder inside the vault loops against itself |
| `$HOME` itself, `/`, `/etc`, `~/.ssh`, `~/.aws`, `~/.config`, `~/Library` | "no home-dir scanning, ever" (docs/20 E1) — these must never be bulk-ingested |
| duplicate entries | a file would be moved twice; the second attempt is a confusing error |

**At runtime** — a folder that is absent or unreadable is **skipped with a
doctor warning**, not a load error. A validator that failed on an unmounted
volume would break `axon config validate` in CI and on any machine where an
external disk happens to be detached.

The deny-list compares cleaned absolute paths, and additionally the
symlink-resolved path **when the folder exists** — so `/Users/me/../me/.ssh`
is caught lexically and a symlink pointing at `~/.ssh` is caught on
resolution. When the path does not exist yet, `filepath.EvalSymlinks` cannot
resolve it, so only the lexical comparison applies; the folder is then also
unreadable at runtime and the doctor check surfaces it. This is deliberate:
refusing to validate a config because a volume is detached would be worse than
deferring one of two checks.

### The doctor check

A `watch-folders` check in the same style as the rest: `off` when the list is
empty; OK naming the count when every folder reads; **warn** naming the first
unreadable folder with `Fix: "create the folder or remove it from
capture.watch_folders"`.

Because it carries a `Fix`, `self-check` (FR-207) files it to the review queue
automatically. The two slices compose without either knowing about the other,
which is the payoff of FR-207's "any check with a Fix" rule.

## FR-209 — The sweep and the change-gate

### The sweep

`internal/automations/watch.go`, called at the top of `Capture.Run` before the
inbox listing. Per configured folder, **top-level only** (no recursion,
matching the inbox):

**Skipped, each for its own reason:**

- **Directories** and **dotfiles** — the inbox rules, unchanged.
- **Symlinks** — new, and the most important. `os.ReadDir` reports a symlink
  as not-a-directory, so it would pass the inbox filter, be moved in, and then
  `Ingest` would *follow* it: a link to `~/.ssh/id_rsa` becomes a vault note
  and model context. The inbox never carried this exposure because it holds
  what the owner put there; a watched folder may hold links the owner never
  made.
- **Files modified within the settle window** (`watchSettleSeconds = 30`) —
  new. A browser writing a download in place would otherwise be caught
  mid-write and ingested truncated. `capture` has no settle check because
  dragging a file into `00-Inbox` is atomic from the owner's side; a watched
  folder is not.

**Moved, with two mechanical guards:**

- **Name collisions** reuse the `-N` suffixing `archiveInboxFile` already
  performs, so `report.pdf` arriving twice becomes `report.pdf` and
  `report-2.pdf` rather than one clobbering the other.
- **`os.Rename` falls back to copy-then-remove on `EXDEV`**, so a watched
  folder on an external volume or network mount works. The copy is
  `create → io.Copy → sync → close → remove source`, and the source is removed
  only after the destination is durably written.

**Capped** at `watchMaxPerTick = 20` moves per tick, so a folder holding
thousands of files cannot stall a five-minute run; the remainder arrives on
following ticks. When the cap bites, the run summary says so — a silent
truncation would read as "everything was captured".

Per-file errors are non-fatal: the file is left in place, the error is
recorded in capture's existing failure memory, and it surfaces in the review
queue exactly like an inbox capture failure.

### The change-gate — the bit that would silently break this

`inboxFingerprint` hashes only the `00-Inbox` listing. `DetectChange` runs
**before** `Run`, so a new file appearing in a watched folder would leave the
inbox unchanged, `capture` would report "no change", `Run` would never
execute, and the sweep would never happen. The feature would look correct in
review and do nothing in production.

The fingerprint therefore covers the watched folders too: name, size and
mtime of each eligible top-level file, folder by folder, in configured order.
It stays content-free, so a tick over an unchanged set remains near-free —
the property the existing comment on `inboxFingerprint` is careful to
preserve. An unreadable folder contributes nothing to the hash rather than
erroring, so a detached volume does not wedge the gate.

## Out of scope

- **`fsnotify` or any OS watcher.** Polling reuses the schedule, change-gate,
  failure memory and reporting that already exist. The cost is minutes of
  latency, which is right for a drop-box. ADR-040 does not foreclose it.
- **Recursion into subdirectories.** Top-level only, matching `00-Inbox`.
- **Per-folder kind hints.** Rejected as a second source of truth for
  classification that can disagree with extension sniffing, which is shipped
  and tested.
- **Writing anything outside the vault.** The sweep only ever *removes* a
  source file as the second half of a move. It never creates, and never writes
  into a watched folder.
- **A separate enable toggle.** An empty list is the off state.

## Verification

**Unit — config:** a table for `validateWatchFolders` covering an accepted
absolute path; relative; containing `..`; inside the vault; each deny-listed
root; a symlink resolving to a deny-listed root; duplicates; and the empty
list (valid, the default).

**Unit — the sweep** (a `t.TempDir()` playing the watched folder, a second as
the vault): an ordinary file is moved and lands in `00-Inbox`; a **symlink is
not moved** (assert the vault has no such entry and the link still exists);
a **file with a fresh mtime is not moved**, and is moved on a later sweep once
it ages past the window; a directory and a dotfile are skipped; a name
collision produces `-2` rather than clobbering; the per-tick cap moves exactly
`watchMaxPerTick` and says so in the summary; a missing folder is skipped
without error; a per-file failure leaves the file in place and is reported.

**Unit — the change-gate:** the regression that protects the whole feature —
with `00-Inbox` unchanged and a new file in a watched folder, `DetectChange`
must report **changed**. This is the test that would have caught the failure
mode described above.

**Unit — end-to-end within the package:** a PDF-shaped file dropped in a
watched folder is swept, ingested through the fake pipeline, and archived,
with the watched folder left empty.

**Live smoke** in an isolated env (scratch `AXON_HOME`, dashboard port 7799 —
**never 7777**, the live daemon's port; build the smoke config by editing
`axon.config.example.yaml`, `sed` the port immediately, then
`grep -rn 7777 <scratch>` before any `axon start`; `mkdir -p` the data dir or
SQLite fails with `unable to open database file (14)`; `cd` back to the repo
root by absolute path after `cd web && npm run build`): a real text file
dropped into a watched folder outside the vault, `axon run capture`, the note
appearing in the knowledge base and the original in `04-Archive/Capture/`;
a symlink to a sensitive file placed in the same folder and **not** ingested;
a config naming `$HOME` refused at `axon config validate` with the deny-list
message; `axon doctor` showing the `watch-folders` check; and `self-check`
filing the warning when a watched folder is deleted.

## Docs to update on completion

`docs/03` (FR-208/FR-209 rows), `docs/02` (ADR-040 planned → built at the
cut), `docs/04` (the `capture.watch_folders` config reference),
`docs/05-component-knowledge-ingestion.md` and `docs/06-component-automation-engine.md`
(the capture section gains the sweep), `docs/AUTOMATIONS.md` (capture's row
and prose), `docs/GUIDE.md` (a short "drop files in from anywhere" section),
`axon.config.example.yaml` (a commented `watch_folders` example),
`docs/20` E1 (shipped), and `CHANGELOG.md` `[Unreleased]`.
