# Operations Overhaul Plan B — Releases, Self-Update, Setup, Uninstall, Vault Move

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One-command install (`install.sh` → `axon setup`), update (`axon update`), removal (`axon uninstall`), and vault migration (`axon vault move`), backed by GitHub Releases.

**Architecture:** A new `internal/selfupdate` package owns release checking/download/verification/atomic-swap against GitHub Releases (assets named `axon_<version>_<goos>_<goarch>` + `checksums.txt`, exactly what `make release` produces). New cobra commands (`setup`, `update`, `uninstall`, `vault move`) compose existing seams: pidfile helpers, `service` unit generation, `setConfigValue`, `core.Init`, `claudeassets`, and Plan A's `tui` surfaces. A starter-config generator in `internal/config` lets `setup` provision without a repo checkout.

**Tech Stack:** Go stdlib (net/http for the GitHub API — no new deps), GitHub Actions, existing Charm tui package.

**Spec:** `docs/superpowers/specs/2026-07-02-operations-overhaul-design.md` (Components 4–6)

## Global Constraints

- Same as Plan A (plain/JSON canonical, no TTY blocking headless, config edits via `setConfigValue`, gofmt/vet/lint clean).
- Self-update: HTTPS + SHA-256 verification against the release's `checksums.txt`; explicit user action only (no auto-update); keep the previous binary as `<path>.old` until the new one is verified in place.
- Uninstall NEVER touches the vault; `--purge` (data dir + AXON home) requires TypedConfirm interactively and `--yes-purge-all-data` headless.
- `vault move` refuses while the daemon runs unless it may stop it (interactive offer, or `--stop-daemon`).
- GitHub repo coordinates: `jandro-es/axon` (from the module path); override for testing via `AXON_UPDATE_BASE_URL`.

---

### Task B1: `internal/selfupdate` — check, download, verify, swap

**Files:** Create `internal/selfupdate/selfupdate.go`, `internal/selfupdate/selfupdate_test.go`

**Interfaces (produced):**

```go
package selfupdate

type Release struct {
	Version string // tag without leading v
	Assets  map[string]string // asset name → download URL
}

// CheckLatest queries the latest GitHub release. baseURL "" = api.github.com;
// tests point it at an httptest server (also honoured from AXON_UPDATE_BASE_URL).
func CheckLatest(ctx context.Context, baseURL, owner, repo string) (Release, error)

// IsNewer reports whether latest is strictly newer than current
// ("1.2.3" vs "1.2.2"; "dev"/"" current → always false, never nag dev builds).
func IsNewer(current, latest string) bool

// AssetName returns the release asset for this platform: axon_<v>_<goos>_<goarch>.
func AssetName(version, goos, goarch string) string

// DownloadVerified downloads asset `name` and the release's checksums.txt,
// verifies the SHA-256, and writes the binary to destDir; returns its path.
func DownloadVerified(ctx context.Context, rel Release, name, destDir string) (string, error)

// Swap atomically replaces target with newBin: target → target.old,
// newBin → target (0755). Caller removes .old after verifying.
func Swap(target, newBin string) error
```

Tests (httptest server serving a fake release JSON + assets + checksums): IsNewer table (incl. dev/empty/equal/prerelease-suffix), CheckLatest parses assets, DownloadVerified rejects a bad checksum, Swap leaves .old and the new binary executable.

Commit: `Add internal/selfupdate: release check, verified download, atomic swap`

---

### Task B2: release workflow + checksums

**Files:** Create `.github/workflows/release.yml`; Modify `Makefile` (release target appends `checksums.txt`).

- Makefile `release`: after the build loop add `(cd dist && shasum -a 256 axon_* > checksums.txt)`.
- Workflow: on `push: tags: ['v*']` → setup Go (from go.mod) + Node, `make release VERSION=${GITHUB_REF_NAME#v}`, `gh release create "$GITHUB_REF_NAME" dist/* --generate-notes` (permissions: contents: write).

Verify: `bash -n` not applicable; `actionlint` if installed, else YAML-parse check. `make release VERSION=0.0.0-test` locally → dist/ contains 4 binaries + checksums.txt (then `git clean -fd dist`).

Commit: `Publish tagged releases with checksums (GitHub Actions)`

---

### Task B3: `axon update` + `version --check` + cached availability in doctor/health

**Files:** Create `cmd/axon/update_cmd.go`; Modify `cmd/axon/version.go` (`--check`), `cmd/axon/root.go` (register), `internal/core/doctor.go` (cached-availability check), `cmd/axon/start_cmd.go` (daily background check writing the cache + health field). Tests: `cmd/axon/update_cmd_test.go` against an httptest release server.

- Cache file: `<AXON_HOME>/update-check.json` `{ "latest": "1.2.3", "checked_at": RFC3339 }`; helpers `readUpdateCache`/`writeUpdateCache` in update_cmd.go.
- `axon update`: resolve current version (buildVersion), CheckLatest (honouring `AXON_UPDATE_BASE_URL`), `--check-only` prints and exits 0; otherwise DownloadVerified → Swap(os.Executable()) → restart service when a unit exists (`launchctl`/`systemctl` best-effort via the service package label) → run `init` convergence advice (print `axon init` next step rather than re-exec). `--json` for scripts. All under tui.Spin steps.
- `axon version --check`: appends `latest: vX (update available)` or `up to date`.
- doctor: `update-available` check reads ONLY the cache (StatusOK "up to date …" / StatusWarn "vX available — run axon update" / OK "never checked").
- start_cmd: on daemon start + every 24h, goroutine CheckLatest → writeUpdateCache (errors ignored); Health payload gains `"update_available": <bool>, "latest_version": <string>` from the cache.

Tests: `--check-only` against httptest (newer → announces; same → "up to date"); a full update run in a temp dir swapping a fake target binary; doctor cache reading (write cache file, assert check text).

Commit: `Add axon update: verified self-update from GitHub Releases, surfaced in doctor/health`

---

### Task B4: starter config + `axon setup`

**Files:** Create `internal/config/starter.go` (+ test), `cmd/axon/setup_cmd.go` (+ test); Modify `cmd/axon/root.go`.

- `config.Starter(profile, vaultPath, provider, model string, dim int) []byte` — a commented single-profile starter config rendered from a text/template (fields: version, project_name axon, active_profile, vault_path, data_dir `~/.axon/profiles/<p>`, claude subscription + config_dir, dashboard 7777, embeddings from args, models trio (current defaults), limits defaults, retrieval defaults, permissive personal policy, standard automations block, memory defaults). Test: `config.Parse(config.Starter(...))` validates; provider/vault values land.
- `axon setup`: the in-binary provisioning flow.
  - TTY: huh form — vault path (Input), profile name (Input, default personal), embeddings (Select apple/ollama, apple only offered on darwin), service-at-login (Confirm). Then: write config if absent (Starter; never overwrite an existing config — converge instead), write `.env` template if absent (0600, token placeholder + comment pointing at `claude setup-token`), run `core.Init` (live steps), optionally `service install` + load, print next steps (claude login / setup-token, dashboard URL).
  - Non-TTY: requires flags `--vault <path>` [`--profile`, `--embeddings`, `--service`]; same flow without prompts; errors with clear usage if `--vault` missing and no config exists.
  - Idempotent: existing config/env are kept (reported "already"); init is already idempotent.

Tests (non-TTY): fresh AXON_HOME + `setup --vault <tmp>/vault` creates config (+validates), .env (0600), runs init (vault scaffolded); second run reports keeps and changes nothing (config mtime unchanged); `--embeddings apple` lands in the config.

Commit: `Add axon setup: in-binary provisioning (config, secrets, init, service)`

---

### Task B5: `axon uninstall`

**Files:** Create `cmd/axon/uninstall_cmd.go` (+ test); Modify `cmd/axon/root.go`.

Flow (each step reports done/already/warn): stop daemon if running (pidfile + signalStop); `service uninstall` equivalent (remove unit file + launchctl unload / systemctl disable best-effort); binary removal — `os.Remove(os.Executable())` attempted, on permission error print the exact `sudo rm <path>` line; `--purge`: TypedConfirm("purge", …) interactively or `--yes-purge-all-data` headless → `os.RemoveAll(AxonHome())`. NEVER touches vault_path. `--json` summary.

Tests (non-TTY, temp AXON_HOME): purge refused without `--yes-purge-all-data`; with it, the home dir is removed while a sentinel vault dir outside it survives; no daemon/no service → "already" statuses, exit 0.

Commit: `Add axon uninstall: daemon, service, binary, optional data purge (vault never touched)`

---

### Task B6: `axon vault move <new-path>`

**Files:** Create `cmd/axon/vault_cmd.go` (+ test), `internal/core/vaultmove.go` (+ test); Modify `cmd/axon/root.go`.

**Interfaces:** `core.MoveVault(ctx, opts core.VaultMoveOptions) (core.VaultMoveReport, error)` with `Opts{Config *config.Config, ProfileName string, Profile config.Profile, ConfigPath, ProfileFlag, Dest string, SetConfig func(key, value string) error, BinaryPath string}`; report lists steps (moved, config-updated, wiring-regenerated, verify).

Core sequence:
1. Preflight: source exists & is dir; dest doesn't exist OR is an empty dir (then use it); dest not inside source; expand `~`.
2. Move: `os.Rename`; on `LinkError` (cross-device) → `copyTree` (walk, copy files+modes, byte-count verify) → `os.RemoveAll(source)` only after full success.
3. `SetConfig("vault_path", dest)` (the cmd passes a closure over `setConfigValue`).
4. Regenerate `.claude/` wiring at the new location via `claudeassets.Generate` with the same Params init uses (re-bakes absolute config/binary paths).
5. Return report; the CLI prints steps + the Obsidian note ("open the vault at its new location once — AXON cannot update Obsidian's bookmark").

Command wrapper: refuse if daemon running (pidfile) — interactive Confirm offers stop→move→"restart with `axon start`/service" note; headless requires `--stop-daemon`. `--json` report.

Tests: core happy-path move in tmp (file content survives, old dir gone, wiring regenerated at dest, config updated via recorded SetConfig calls); dest-exists-nonempty → error; command-level: daemon "running" simulated by a live pidfile → refusal without `--stop-daemon`; with a fake stopped process → proceeds. Cross-device path exercised via the copyTree fallback directly (unit test on copyTree).

Commit: `Add axon vault move: one-command vault migration with reference updates`

---

### Task B7: `install.sh` bootstrap + script slimming + Makefile

**Files:** Create `install.sh` (repo root); Modify `scripts/install-macos.sh`, `scripts/install-linux.sh` (banner pointing at `axon setup` as the primary path; keep source-build flow working), `Makefile` (mention `axon update` in the update target help), `scripts/update-*.sh` (note: release users run `axon update`).

`install.sh`: `set -euo pipefail`; detect OS (darwin/linux) + arch (arm64/amd64); latest tag via GitHub API; download `axon_<v>_<os>_<arch>` + checksums.txt to a temp dir; `shasum -a 256 -c` the single line; install to `/usr/local/bin` (sudo if needed) or `~/.local/bin` with `--user`; then `exec axon setup` (interactive) unless `--no-setup`. `bash -n` + shellcheck clean.

Commit: `Add one-liner install.sh bootstrap (release binary + axon setup)`

---

### Task B8: docs sweep + gates

- GUIDE.md: new top "Install" section (one-liner + `axon setup`), "Updating" (`axon update`), "Uninstall" (`axon uninstall`), "Moving your vault" (`axon vault move`); prune stale three-step instructions.
- docs/10 (installer/bootstrap component): `setup`/`update`/`uninstall`/`install.sh` are the product; scripts are the source-build path.
- docs/09: health payload documents `update_available`/`latest_version`.
- Full gates: gofmt/vet/test/lint; live temp-profile pass: setup (headless flags) → doctor → vault move → uninstall --purge --yes-purge-all-data.

Commit: `Plan B docs: install/update/uninstall/vault-move are the product surface`

## Self-Review (plan time)

Spec coverage: releases+checksums (B2), self-update+surfacing (B1/B3), install.sh+setup (B4/B7), uninstall (B5), vault move incl. reference updates + Obsidian caveat (B6), docs (B8). Type consistency: selfupdate API used only in B3/B7; core.MoveVault only in B6. No placeholders: each task carries its full contract; code-level details follow existing seams named per task.
