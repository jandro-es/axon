# Watch-folders Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let files dropped into config-listed folders outside the vault flow into the knowledge base on the existing capture tick.

**Architecture:** Three layers. `internal/config` gains a `watch_folders` list on the capture block plus a profile-level validator (it needs the vault path, which the capture-block-only validator cannot see). `internal/automations` gains a sweep that moves eligible files into `00-Inbox` before capture's existing listing runs, and extends capture's change-gate fingerprint to cover the watched folders. `internal/core` gains a doctor check. Nothing downstream changes — once a file is in `00-Inbox`, the shipped ingest-and-archive flow handles it.

**Tech Stack:** Go 1.26+, standard library only (`os`, `io`, `filepath`) — no new dependency.

**Spec:** `docs/superpowers/specs/2026-08-21-watch-folders-design.md`

## Global Constraints

- **FR IDs:** FR-208 (config + validation + doctor check), FR-209 (the sweep + the change-gate fingerprint). **ADR-040** covers the decision; do not write a second ADR.
- **No migration.** Schema stays `0007`.
- **No new automation.** `capture` grows a pre-step; the built-in count stays **26**. Do not touch `registry_test.go`, `seeds_test.go` or `internal/mcp/tools_more_test.go` — if a count assertion moves, something is wrong.
- **No new dependency.** Polling on the existing tick, not `fsnotify`.
- **Empty list is the off state.** Absent or empty `watch_folders` must produce byte-identical behaviour to today, on both profiles. No separate enable toggle.
- **Three refusals (copied verbatim from the spec):** symlinks are skipped; files modified within `watchSettleSeconds = 30` are skipped; moves are capped at `watchMaxPerTick = 20` per tick and the run summary says so when the cap bites.
- **Deny-list roots:** `$HOME` itself, `/`, `/etc`, `~/.ssh`, `~/.aws`, `~/.config`, `~/Library`. Compared against the cleaned absolute path always, and additionally the `filepath.EvalSymlinks`-resolved path **when the folder exists**.
- **Never write into a watched folder.** The sweep only ever removes a source file as the second half of a move. It never creates anything there.
- **Top-level only.** No recursion, matching `00-Inbox`.
- **Go hygiene:** `gofmt`/`goimports` clean, `go vet` and `golangci-lint` green, errors wrapped with `%w`, `context.Context` propagated. Run `gofmt -l .` before every commit.
- **Never bind port 7777** in any smoke config — that is the live daemon's port. Smoke work uses 7799.
- **Isolate the vault path in smoke work**, not just `AXON_HOME` — the example config points `vault_path` at `~/Notes/Personal`, and a scratch run that only overrides `AXON_HOME` will operate on the real vault.

---

### Task 1: `watch_folders` config + validation (FR-208)

**Files:**
- Modify: `internal/config/types.go` (`CaptureConfig`, ~line 123)
- Create: `internal/config/watch.go` (the validator and its deny-list)
- Modify: `internal/config/load.go:67` (add the call beside `validateRecipes`)
- Test: `internal/config/watch_test.go`

**Interfaces:**
- Consumes: `config.Profile`, `Profile.Paths().VaultPath`, `config.ExpandPath` (`internal/config/paths.go:41`).
- Produces: `CaptureConfig.WatchFolders []string` (`yaml:"watch_folders,omitempty"`) and `validateWatchFolders(p Profile) error`. Task 2 reads the field; Task 4 reads it for the doctor check.

- [ ] **Step 1: Write the failing test**

Create `internal/config/watch_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateWatchFolders(t *testing.T) {
	vault := t.TempDir()
	ok1 := t.TempDir()
	ok2 := t.TempDir()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}

	profile := func(folders ...string) Profile {
		return Profile{VaultPath: vault, Capture: CaptureConfig{WatchFolders: folders}}
	}

	// Accepted.
	for _, good := range [][]string{nil, {}, {ok1}, {ok1, ok2}} {
		if err := validateWatchFolders(profile(good...)); err != nil {
			t.Fatalf("valid folders %v rejected: %v", good, err)
		}
	}

	cases := []struct {
		name    string
		folders []string
		want    string
	}{
		{"relative", []string{"Downloads/axon"}, "absolute"},
		{"dot dot", []string{filepath.Join(ok1, "..", "x")}, "absolute"},
		{"inside the vault", []string{filepath.Join(vault, "00-Inbox")}, "inside the vault"},
		{"the vault itself", []string{vault}, "inside the vault"},
		{"home itself", []string{home}, "not be watched"},
		{"root", []string{"/"}, "not be watched"},
		{"etc", []string{"/etc"}, "not be watched"},
		{"ssh", []string{filepath.Join(home, ".ssh")}, "not be watched"},
		{"aws", []string{filepath.Join(home, ".aws")}, "not be watched"},
		{"config dir", []string{filepath.Join(home, ".config")}, "not be watched"},
		{"library", []string{filepath.Join(home, "Library")}, "not be watched"},
		{"duplicate", []string{ok1, ok1}, "duplicate"},
		{"empty string", []string{"   "}, "absolute"},
	}
	for _, c := range cases {
		err := validateWatchFolders(profile(c.folders...))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
		}
	}
}

// A symlink pointing at a deny-listed root must be refused too — the lexical
// check alone cannot see it.
func TestValidateWatchFoldersResolvesSymlinks(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir on this machine")
	}
	link := filepath.Join(t.TempDir(), "sneaky")
	if err := os.Symlink(filepath.Join(home, ".ssh"), link); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	p := Profile{VaultPath: t.TempDir(), Capture: CaptureConfig{WatchFolders: []string{link}}}
	if err := validateWatchFolders(p); err == nil || !strings.Contains(err.Error(), "not be watched") {
		t.Fatalf("a symlink to a deny-listed root must be refused, got %v", err)
	}
}

// A folder that does not exist is NOT a load-time error: an unmounted volume
// must not break `axon config validate`. The doctor check surfaces it instead.
func TestValidateWatchFoldersAllowsMissingFolder(t *testing.T) {
	p := Profile{
		VaultPath: t.TempDir(),
		Capture:   CaptureConfig{WatchFolders: []string{filepath.Join(t.TempDir(), "not-mounted-yet")}},
	}
	if err := validateWatchFolders(p); err != nil {
		t.Fatalf("a missing folder must validate (runtime + doctor handle it), got %v", err)
	}
}
```

If `Profile`'s vault field is not `VaultPath`, check with `grep -n "VaultPath" internal/config/types.go` and adjust.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestValidateWatchFolders -v`
Expected: FAIL — `undefined: validateWatchFolders`, and `WatchFolders` unknown on `CaptureConfig` (compile errors).

- [ ] **Step 3: Add the config field**

In `internal/config/types.go`, extend `CaptureConfig`:

```go
	// WatchFolders are absolute paths outside the vault whose top-level files
	// are moved into 00-Inbox on each capture tick (FR-208, ADR-040). Absent
	// or empty means no watching at all — there is no separate toggle, and
	// that is the default on both profiles.
	WatchFolders []string `yaml:"watch_folders,omitempty"`
```

- [ ] **Step 4: Write the validator**

Create `internal/config/watch.go`:

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// deniedWatchRoots are directories that must never be bulk-ingested
// (ADR-040). Entries beginning with "~" resolve against the home directory.
// A watched folder equal to one of these — or resolving to one through a
// symlink — is refused at config load.
var deniedWatchRoots = []string{"/", "/etc", "~", "~/.ssh", "~/.aws", "~/.config", "~/Library"}

// validateWatchFolders enforces the ADR-040 rules that struct tags cannot
// express. It lives at profile level rather than in validateCapture because
// the vault-containment rule needs the profile's vault path.
//
// Deliberately NOT an error: a folder that does not exist. An unmounted
// volume must not break `axon config validate`; the runtime sweep skips it
// and the doctor `watch-folders` check surfaces it.
func validateWatchFolders(p Profile) error {
	folders := p.Capture.WatchFolders
	if len(folders) == 0 {
		return nil
	}
	vault := filepath.Clean(ExpandPath(p.VaultPath))
	denied := make(map[string]bool, len(deniedWatchRoots))
	for _, d := range deniedWatchRoots {
		denied[filepath.Clean(ExpandPath(d))] = true
	}
	seen := map[string]bool{}
	for _, raw := range folders {
		f := strings.TrimSpace(raw)
		if f == "" || !filepath.IsAbs(f) || strings.Contains(f, "..") {
			return fmt.Errorf("capture.watch_folders: %q must be an absolute path without '..'", raw)
		}
		clean := filepath.Clean(ExpandPath(f))
		if seen[clean] {
			return fmt.Errorf("capture.watch_folders: duplicate entry %q", raw)
		}
		seen[clean] = true
		if clean == vault || strings.HasPrefix(clean, vault+string(filepath.Separator)) {
			return fmt.Errorf("capture.watch_folders: %q is inside the vault — capture already sweeps 00-Inbox", raw)
		}
		// Lexical check always; resolved check only when the folder exists,
		// since EvalSymlinks cannot resolve a path that is not there.
		candidates := []string{clean}
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			candidates = append(candidates, filepath.Clean(resolved))
		}
		for _, c := range candidates {
			if denied[c] {
				return fmt.Errorf("capture.watch_folders: %q may not be watched — it is a system or home root, and AXON never bulk-ingests one", raw)
			}
		}
	}
	return nil
}

// exists reports whether a watched folder is readable right now. Used by the
// runtime sweep and the doctor check, never by validation.
func WatchFolderReadable(path string) error {
	st, err := os.Stat(ExpandPath(path))
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}
```

Then wire it into `internal/config/load.go`, immediately after the `validateRecipes` block:

```go
		if err := validateWatchFolders(p); err != nil {
			return fmt.Errorf("config validation failed: profile %q: %w", name, err)
		}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/config/ -v -run TestValidateWatchFolders && gofmt -l internal/config && go vet ./internal/config/`
Expected: PASS for all three tests, `gofmt -l` silent, vet clean.

- [ ] **Step 6: Commit**

```bash
git add internal/config/types.go internal/config/watch.go internal/config/watch_test.go internal/config/load.go
git commit -m "feat(config): capture.watch_folders with a deny-list validator (FR-208)"
```

---

### Task 2: The sweep (FR-209)

**Files:**
- Create: `internal/automations/watch.go`
- Test: `internal/automations/watch_test.go`

**Interfaces:**
- Consumes: `config.CaptureConfig.WatchFolders` (Task 1), `config.ExpandPath`, `RunCtx` (`.Config`, `.Vault`, `.now()`).
- Produces: `sweepWatchFolders(rc RunCtx) (moved []string, capped bool, problems []string)` — `moved` holds the `00-Inbox`-relative names of files moved in, `capped` reports whether `watchMaxPerTick` bit, `problems` holds per-file error strings. Task 3 calls it from `Capture.Run` and reuses `eligibleWatchFiles` for the fingerprint.

- [ ] **Step 1: Write the failing test**

Create `internal/automations/watch_test.go`:

```go
package automations

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

// agedFile writes a file and backdates its mtime past the settle window, so
// the sweep considers it stable.
func agedFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

func watchRC(t *testing.T, folders ...string) RunCtx {
	t.Helper()
	rc, _ := newRC(t, nil)
	rc.Config.Capture.WatchFolders = folders
	return rc
}

func TestSweepMovesStableFiles(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "report.pdf", "%PDF-1.4 fake")
	rc := watchRC(t, watched)

	moved, capped, problems := sweepWatchFolders(rc)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	if capped {
		t.Fatal("one file must not trip the cap")
	}
	if len(moved) != 1 || moved[0] != "report.pdf" {
		t.Fatalf("want [report.pdf], got %v", moved)
	}
	if !rc.Vault.Exists("00-Inbox/report.pdf") {
		t.Fatal("file did not land in 00-Inbox")
	}
	if _, err := os.Stat(filepath.Join(watched, "report.pdf")); !os.IsNotExist(err) {
		t.Fatal("the source file must be gone after a move")
	}
}

// The security-relevant refusal: a symlink must never be moved in, because
// Ingest would follow it and read its target into the vault and the model.
func TestSweepSkipsSymlinks(t *testing.T) {
	watched := t.TempDir()
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(watched, "innocent.txt")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("cannot create symlink here: %v", err)
	}
	rc := watchRC(t, watched)

	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 0 {
		t.Fatalf("a symlink must never be moved, got %v", moved)
	}
	if rc.Vault.Exists("00-Inbox/innocent.txt") {
		t.Fatal("a symlink reached the vault — Ingest would follow it to its target")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("the symlink must be left in place, not removed")
	}
}

func TestSweepSkipsUnsettledFiles(t *testing.T) {
	watched := t.TempDir()
	// Written now: still inside the settle window.
	if err := os.WriteFile(filepath.Join(watched, "downloading.pdf"), []byte("half"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := watchRC(t, watched)

	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 0 {
		t.Fatalf("a file written just now must wait for the settle window, got %v", moved)
	}

	// Age it past the window; now it moves.
	old := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(filepath.Join(watched, "downloading.pdf"), old, old); err != nil {
		t.Fatal(err)
	}
	moved, _, _ = sweepWatchFolders(rc)
	if len(moved) != 1 {
		t.Fatalf("a settled file must move, got %v", moved)
	}
}

func TestSweepSkipsDirsAndDotfiles(t *testing.T) {
	watched := t.TempDir()
	if err := os.Mkdir(filepath.Join(watched, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	agedFile(t, watched, ".hidden", "x")
	agedFile(t, watched, "real.txt", "y")
	rc := watchRC(t, watched)

	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 1 || moved[0] != "real.txt" {
		t.Fatalf("want just real.txt, got %v", moved)
	}
}

func TestSweepSuffixesNameCollisions(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "notes.txt", "first")
	rc := watchRC(t, watched)
	if _, _, p := sweepWatchFolders(rc); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
	// A second file with the same name arrives later.
	agedFile(t, watched, "notes.txt", "second")
	moved, _, _ := sweepWatchFolders(rc)
	if len(moved) != 1 || moved[0] != "notes-2.txt" {
		t.Fatalf("want [notes-2.txt], got %v", moved)
	}
	if !rc.Vault.Exists("00-Inbox/notes.txt") || !rc.Vault.Exists("00-Inbox/notes-2.txt") {
		t.Fatal("both files must exist; the first must not be clobbered")
	}
}

func TestSweepCapsPerTick(t *testing.T) {
	watched := t.TempDir()
	for i := 0; i < watchMaxPerTick+5; i++ {
		agedFile(t, watched, "f"+string(rune('a'+i))+".txt", "x")
	}
	rc := watchRC(t, watched)

	moved, capped, _ := sweepWatchFolders(rc)
	if len(moved) != watchMaxPerTick {
		t.Fatalf("want %d moves, got %d", watchMaxPerTick, len(moved))
	}
	if !capped {
		t.Fatal("the cap must be reported so a truncated sweep never reads as complete")
	}
}

func TestSweepSkipsMissingFolderWithoutError(t *testing.T) {
	rc := watchRC(t, filepath.Join(t.TempDir(), "not-mounted"))
	moved, _, problems := sweepWatchFolders(rc)
	if len(moved) != 0 {
		t.Fatalf("nothing to move, got %v", moved)
	}
	if len(problems) != 0 {
		t.Fatalf("a missing folder is a doctor concern, not a per-run problem: %v", problems)
	}
}

func TestSweepIsNoOpWithoutFolders(t *testing.T) {
	rc, _ := newRC(t, nil) // no watch folders configured
	moved, capped, problems := sweepWatchFolders(rc)
	if len(moved) != 0 || capped || len(problems) != 0 {
		t.Fatalf("an empty list must be a complete no-op, got %v %v %v", moved, capped, problems)
	}
}

func TestSweepNeverWritesIntoTheWatchedFolder(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "a.txt", "x")
	rc := watchRC(t, watched)
	if _, _, p := sweepWatchFolders(rc); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
	entries, err := os.ReadDir(watched)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("the sweep must only remove, never create: %v", names)
	}
}

func TestEligibleWatchFilesIsContentFree(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "a.txt", "x")
	rc := watchRC(t, watched)
	files := eligibleWatchFiles(rc)
	if len(files) != 1 {
		t.Fatalf("want 1 eligible file, got %v", files)
	}
	if !strings.HasSuffix(files[0].Name, "a.txt") {
		t.Fatalf("unexpected entry: %+v", files[0])
	}
	if files[0].Size == 0 {
		t.Fatal("size is part of the fingerprint and must be populated")
	}
}

var _ = config.CaptureConfig{}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/automations/ -run 'TestSweep|TestEligible' -v`
Expected: FAIL — `undefined: sweepWatchFolders` (compile error).

- [ ] **Step 3: Write the implementation**

Create `internal/automations/watch.go`:

```go
package automations

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

// Watch-folder caps are code, not config (ADR-040).
const (
	watchSettleSeconds = 30 // a file must be this old before it is moved
	watchMaxPerTick    = 20 // moves per capture tick
)

// watchFile is one eligible top-level file in a watched folder. Name/Size/
// ModNano are exactly what the capture change-gate hashes — no content.
type watchFile struct {
	Dir     string
	Name    string
	Size    int64
	ModNano int64
}

// eligibleWatchFiles lists the files the sweep would move, applying every
// skip rule (directories, dotfiles, symlinks, and anything inside the settle
// window). Shared with the change-gate so the gate and the sweep can never
// disagree about what counts.
func eligibleWatchFiles(rc RunCtx) []watchFile {
	var out []watchFile
	cutoff := rc.now().Add(-watchSettleSeconds * time.Second)
	for _, folder := range rc.Config.Capture.WatchFolders {
		dir := config.ExpandPath(strings.TrimSpace(folder))
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // absent or unreadable: a doctor concern, not a run failure
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || strings.HasPrefix(name, ".") {
				continue
			}
			// Type() reports the directory entry's own type, so a symlink is
			// visible here without following it. Skipping symlinks is the
			// point: Ingest would otherwise follow one to its target and read
			// e.g. ~/.ssh/id_rsa into the vault and the model (ADR-040).
			if e.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if info.ModTime().After(cutoff) {
				continue // still settling — a download may be mid-write
			}
			out = append(out, watchFile{Dir: dir, Name: name, Size: info.Size(), ModNano: info.ModTime().UnixNano()})
		}
	}
	return out
}

// sweepWatchFolders moves eligible watched-folder files into 00-Inbox, after
// which capture's shipped flow ingests and archives them unchanged (FR-209).
// Returns the inbox-relative names moved, whether the per-tick cap bit, and
// any per-file problems (non-fatal — the file is left where it is).
func sweepWatchFolders(rc RunCtx) (moved []string, capped bool, problems []string) {
	files := eligibleWatchFiles(rc)
	if len(files) == 0 {
		return nil, false, nil
	}
	if len(files) > watchMaxPerTick {
		files, capped = files[:watchMaxPerTick], true
	}
	inbox := filepath.Join(rc.Vault.Root(), inboxDir)
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return nil, capped, []string{fmt.Sprintf("cannot prepare %s: %v", inboxDir, err)}
	}
	for _, f := range files {
		dest, name, err := freeInboxName(inbox, f.Name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f.Name, err))
			continue
		}
		if err := moveFile(filepath.Join(f.Dir, f.Name), dest); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", f.Name, err))
			continue
		}
		moved = append(moved, name)
	}
	return moved, capped, problems
}

// freeInboxName picks a non-colliding destination in 00-Inbox, suffixing -2,
// -3 … exactly as archiveInboxFile does for the archive.
func freeInboxName(inbox, name string) (dest, chosen string, err error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; i <= 100; i++ {
		candidate := stem
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", stem, i)
		}
		chosen = candidate + ext
		dest = filepath.Join(inbox, chosen)
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			return dest, chosen, nil
		}
	}
	return "", "", fmt.Errorf("no free inbox name for %q", name)
}

// moveFile renames, falling back to copy-then-remove across filesystems
// (a watched folder on an external volume or network mount). The source is
// removed only after the destination is durably written.
func moveFile(src, dest string) error {
	if err := os.Rename(src, dest); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return fmt.Errorf("move %q: %w", filepath.Base(src), err)
	}
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", filepath.Base(src), err)
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create %q: %w", filepath.Base(dest), err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dest)
		return fmt.Errorf("copy %q: %w", filepath.Base(src), err)
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(dest)
		return fmt.Errorf("sync %q: %w", filepath.Base(dest), err)
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return fmt.Errorf("close %q: %w", filepath.Base(dest), err)
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove source %q after copy: %w", filepath.Base(src), err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/automations/ -run 'TestSweep|TestEligible' -v && gofmt -l internal/automations && go vet ./internal/automations/`
Expected: PASS for all ten tests, `gofmt -l` silent, vet clean.

If `TestSweepSkipsSymlinks` fails because `e.Type()` does not report the link, check whether the platform's `os.ReadDir` follows links here and switch to `os.Lstat(filepath.Join(dir, name))` with `info.Mode()&os.ModeSymlink != 0`. Do not weaken the assertion.

- [ ] **Step 5: Commit**

```bash
git add internal/automations/watch.go internal/automations/watch_test.go
git commit -m "feat(automations): watched-folder sweep, skipping symlinks and unsettled files (FR-209)"
```

---

### Task 3: Wire the sweep into capture, and fix the change-gate (FR-209)

**Files:**
- Modify: `internal/automations/capture.go` — `inboxFingerprint` (~line 67), `Capture.DetectChange` (~line 93), `Capture.Run` (~line 106)
- Test: `internal/automations/watch_test.go` (append)

**Interfaces:**
- Consumes: `sweepWatchFolders`, `eligibleWatchFiles`, `watchFile` (Task 2).
- Produces: no new exported symbols. This is the last behaviour task.

- [ ] **Step 1: Write the failing test — the one that protects the whole feature**

Append to `internal/automations/watch_test.go`:

```go
// THE regression for this slice. DetectChange runs BEFORE Run, so if the
// fingerprint covers only 00-Inbox, a new file in a watched folder leaves the
// inbox unchanged, capture reports "no change", Run never executes, and the
// sweep never happens — a feature that looks correct and does nothing.
func TestCaptureDetectsChangesInWatchedFolders(t *testing.T) {
	watched := t.TempDir()
	rc := watchRC(t, watched)
	ctx := context.Background()

	// Baseline with an empty inbox and an empty watched folder.
	first, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	rc.LastCursor = first.Cursor
	if again, err := (Capture{}).DetectChange(ctx, rc); err != nil || again.Changed {
		t.Fatalf("nothing moved; want unchanged, got %+v err=%v", again, err)
	}

	// A file appears in the WATCHED folder. The inbox is untouched.
	agedFile(t, watched, "new.txt", "hello")
	after, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Changed {
		t.Fatal("a new file in a watched folder must re-arm the capture gate")
	}
}

// A file in a watched folder is swept into the inbox by Run and reported.
func TestCaptureRunSweepsWatchedFolders(t *testing.T) {
	watched := t.TempDir()
	agedFile(t, watched, "dropped.txt", "some captured text")
	rc := watchRC(t, watched)

	res, err := (Capture{}).Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(watched, "dropped.txt")); !os.IsNotExist(err) {
		t.Fatal("the watched folder should be empty after the sweep")
	}
	if !strings.Contains(res.Summary, "watch") && len(res.Changes) == 0 {
		t.Fatalf("the run should report what it swept: %+v", res)
	}
}

// With no watch folders configured, capture's fingerprint and behaviour must
// be exactly what they were before this slice.
func TestCaptureUnchangedWithoutWatchFolders(t *testing.T) {
	rc, _ := newRC(t, nil)
	ctx := context.Background()
	ch, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if ch.Cursor == "" {
		t.Fatal("fingerprint must still be produced")
	}
	rc.LastCursor = ch.Cursor
	again, err := (Capture{}).DetectChange(ctx, rc)
	if err != nil {
		t.Fatal(err)
	}
	if again.Changed {
		t.Fatal("an unchanged empty vault with no watch folders must skip")
	}
}
```

Add `"context"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/automations/ -run 'TestCaptureDetects|TestCaptureRunSweeps' -v`
Expected: FAIL — `TestCaptureDetectsChangesInWatchedFolders` reports that the gate did not re-arm, because the fingerprint ignores watched folders.

- [ ] **Step 3: Extend the fingerprint**

In `internal/automations/capture.go`, change `inboxFingerprint` to take the run context so it can see the configured folders. Replace the function and its call sites:

```go
// inboxFingerprint hashes the inbox listing (name + size + mtime) plus every
// eligible watched-folder file — the capture change gate. Deliberately does
// not read content: a tick over an unchanged inbox must be near-free.
//
// The watched-folder half is load-bearing (FR-209): DetectChange runs before
// Run, so a fingerprint covering only 00-Inbox would skip the tick whenever a
// file appeared outside the vault, and the sweep would never execute.
func inboxFingerprint(rc RunCtx) (string, error) {
	root := rc.Vault.Root()
	entries, err := listInboxDir(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, e := range entries {
		st, err := os.Stat(filepath.Join(root, inboxDir, e.Name))
		if err != nil {
			continue // raced away between ReadDir and Stat; next tick catches it
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\n", e.Name, st.Size(), st.ModTime().UnixNano())
	}
	for _, f := range eligibleWatchFiles(rc) {
		fmt.Fprintf(h, "watch\x00%s\x00%s\x00%d\x00%d\n", f.Dir, f.Name, f.Size, f.ModNano)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
```

In `Capture.DetectChange`, change the call and the reason text:

```go
	fp, err := inboxFingerprint(rc)
	if err != nil {
		return Change{}, err
	}
	if fp == rc.LastCursor {
		return Change{Changed: false, Reason: "inbox and watched folders unchanged since last capture"}, nil
	}
```

Check for other callers before changing the signature:

```bash
grep -rn "inboxFingerprint" internal/ | grep -v "func inboxFingerprint"
```

Update each one found (including tests).

- [ ] **Step 4: Call the sweep from Run**

At the very top of `Capture.Run`, before `listInboxDir`:

```go
func (Capture) Run(ctx context.Context, rc RunCtx) (RunResult, error) {
	root := rc.Vault.Root()

	// Watched folders first (FR-209): anything moved in is picked up by the
	// inbox listing below, so the shipped ingest-and-archive flow handles it
	// with no special casing.
	var watchNotes []string
	if !rc.DryRun {
		swept, capped, problems := sweepWatchFolders(rc)
		if len(swept) > 0 {
			watchNotes = append(watchNotes, fmt.Sprintf("swept %d file(s) from watched folders", len(swept)))
		}
		if capped {
			watchNotes = append(watchNotes, fmt.Sprintf("watch-folder cap reached (%d/tick) — more will arrive next tick", watchMaxPerTick))
		}
		watchNotes = append(watchNotes, problems...)
	} else if files := eligibleWatchFiles(rc); len(files) > 0 {
		watchNotes = append(watchNotes, fmt.Sprintf("would sweep %d file(s) from watched folders", len(files)))
	}

	entries, err := listInboxDir(root)
	...
```

Then fold `watchNotes` into the result. Find where `Run` builds its `RunResult` and prepend the notes to the summary — for example, if it returns `RunResult{Summary: summary, Changes: changes}`, change it to:

```go
	if len(watchNotes) > 0 {
		summary = strings.Join(watchNotes, "; ") + "; " + summary
	}
```

Read the end of `Run` first to match the actual variable names:

```bash
sed -n '/^func (Capture) Run/,/^}/p' internal/automations/capture.go | tail -25
```

- [ ] **Step 5: Run the whole package**

Run: `go test ./internal/automations/ -v 2>&1 | tail -20 && gofmt -l internal/automations && go vet ./internal/automations/`
Expected: PASS for the whole package. The pre-existing capture tests must still pass — with no watch folders configured, the fingerprint has nothing extra to hash and behaviour is identical.

- [ ] **Step 6: Full gate, then commit**

```bash
go build ./... && go test ./... && golangci-lint run
git add internal/automations/capture.go internal/automations/watch_test.go
git commit -m "feat(automations): capture sweeps watched folders and gates on them (FR-209)"
```

Expected: whole suite green, 0 lint issues. **The built-in automation count must not move** — if `registry_test`, `seeds_test` or `internal/mcp/tools_more_test.go` fails, a new automation was added by mistake.

---

### Task 4: The doctor check (FR-208)

**Files:**
- Modify: `internal/core/doctor.go` (add the check and append it in `Doctor`)
- Test: `internal/core/doctor_test.go` (append)

**Interfaces:**
- Consumes: `config.Profile.Capture.WatchFolders` (Task 1), `config.WatchFolderReadable` (Task 1), `core.Check`.
- Produces: a `watch-folders` check in the standard report. Nothing later depends on it.

- [ ] **Step 1: Write the failing test**

Append to `internal/core/doctor_test.go`:

```go
func TestWatchFoldersCheck(t *testing.T) {
	// Off: no folders configured.
	off := watchFoldersCheck(config.Profile{})
	if off.Status != StatusOK || !strings.Contains(off.Detail, "no watched folders") {
		t.Fatalf("empty list should read as off: %+v", off)
	}
	if off.Fix != "" {
		t.Fatalf("an off check has nothing to fix: %+v", off)
	}

	// Healthy: a real directory.
	good := t.TempDir()
	okCheck := watchFoldersCheck(config.Profile{Capture: config.CaptureConfig{WatchFolders: []string{good}}})
	if okCheck.Status != StatusOK || !strings.Contains(okCheck.Detail, "1") {
		t.Fatalf("a readable folder should pass: %+v", okCheck)
	}

	// Warn: a missing directory, and it must carry a Fix so self-check
	// (FR-207) can file it.
	missing := filepath.Join(t.TempDir(), "not-mounted")
	warn := watchFoldersCheck(config.Profile{Capture: config.CaptureConfig{WatchFolders: []string{good, missing}}})
	if warn.Status != StatusWarn {
		t.Fatalf("a missing folder should warn: %+v", warn)
	}
	if !strings.Contains(warn.Detail, missing) {
		t.Fatalf("the warning should name the folder: %+v", warn)
	}
	if warn.Fix == "" {
		t.Fatal("the warning must carry a Fix — self-check only proposes checks that have one")
	}
}
```

Ensure the test file imports `path/filepath`, `strings`, and `github.com/jandro-es/axon/internal/config`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestWatchFoldersCheck -v`
Expected: FAIL — `undefined: watchFoldersCheck` (compile error).

- [ ] **Step 3: Write the check**

Add to `internal/core/doctor.go`:

```go
// watchFoldersCheck reports on capture.watch_folders (FR-208). A folder that
// is absent or unreadable is a warning rather than a config-load error, so an
// unmounted volume cannot break `axon config validate` — this is where the
// owner finds out. The Fix matters: self-check (FR-207) only proposes checks
// that carry one.
func watchFoldersCheck(p config.Profile) Check {
	const name = "watch-folders"
	folders := p.Capture.WatchFolders
	if len(folders) == 0 {
		return Check{Name: name, Status: StatusOK,
			Detail: "no watched folders (drop-in capture from outside the vault is off; add capture.watch_folders to enable)"}
	}
	var bad []string
	for _, f := range folders {
		if err := config.WatchFolderReadable(f); err != nil {
			bad = append(bad, fmt.Sprintf("%s (%v)", f, err))
		}
	}
	if len(bad) > 0 {
		return Check{Name: name, Status: StatusWarn,
			Detail: fmt.Sprintf("%d of %d watched folder(s) unreadable: %s", len(bad), len(folders), strings.Join(bad, "; ")),
			Fix:    "create the folder, or remove it from capture.watch_folders"}
	}
	return Check{Name: name, Status: StatusOK,
		Detail: fmt.Sprintf("%d watched folder(s) readable — files dropped there are captured on the capture tick", len(folders))}
}
```

Then append it inside `Doctor`, beside the other profile-scoped checks. Find where the profile is resolved:

```bash
grep -n "cfg.Profiles\[activeProfile\]" internal/core/doctor.go | head -3
```

Add `checks = append(checks, watchFoldersCheck(p))` in that block, guarded by the same `cfg != nil` / profile-found condition the neighbours use.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ ./cmd/axon/ && gofmt -l internal/core && go vet ./internal/core/`
Expected: PASS both. The `cmd/axon` doctor tests must still pass; if a test asserts an exact check count, update it and note that in the commit.

- [ ] **Step 5: Commit**

```bash
git add internal/core/doctor.go internal/core/doctor_test.go
git commit -m "feat(core): watch-folders doctor check, with a Fix so self-check files it (FR-208)"
```

---

### Task 5: Documentation, config example, and the roadmap

**Files:**
- Modify: `docs/03-requirements.md`, `docs/02-architecture.md` (ADR-040 planned → built), `docs/04-data-model-and-config.md`, `docs/05-component-knowledge-ingestion.md`, `docs/06-component-automation-engine.md`, `docs/AUTOMATIONS.md`, `docs/GUIDE.md`, `axon.config.example.yaml`, `docs/20-roadmap-ai-os.md`, `CHANGELOG.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: the final shapes from Tasks 1–4. No code.
- Produces: nothing consumed by a later task.

- [ ] **Step 1: Add the FR rows to `docs/03-requirements.md`**

After the FR-207 row, add a section and two rows:

```markdown
### Watch-folders (docs/20 E1) *(built 2026-08-21)*

FR-208…FR-209 trace to **ADR-040** and graduate `docs/20` E1; spec in
`docs/superpowers/specs/2026-08-21-watch-folders-design.md`. No migration, and
no new automation — `capture` grows a pre-step.

| ID | Pri | Requirement |
|----|-----|-------------|
| FR-208 | S | **`capture.watch_folders` + validation + doctor (ADR-040).** A list of absolute paths outside the vault; absent or empty means no watching, on both profiles, so the feature ships disabled with no separate toggle. `validateWatchFolders(p Profile)` runs in the `Config.Validate` per-profile loop (profile-level because the vault-containment rule needs the vault path) and refuses: relative paths or `..`; any path inside the vault (`capture` already owns `00-Inbox` — a loop); duplicates; and the deny-listed roots `$HOME`, `/`, `/etc`, `~/.ssh`, `~/.aws`, `~/.config`, `~/Library`, compared lexically always and against the `EvalSymlinks`-resolved path when the folder exists. A folder that is merely **absent is not a load error** — an unmounted volume must not break `axon config validate`; the runtime sweep skips it and a `watch-folders` doctor check warns, carrying a `Fix` so `self-check` (FR-207) files it. |
| FR-209 | S | **The sweep and the change-gate (ADR-040).** On each `capture` tick, before the inbox listing, top-level files in each watched folder are moved into `00-Inbox` — after which the shipped ingest-and-archive flow runs unchanged, with no new archive logic and no seen-ledger (a moved file cannot be reprocessed). Three refusals the inbox never needed: **symlinks are skipped** (`Ingest` would follow one to its target and read e.g. `~/.ssh/id_rsa` into the vault and the model), files modified within `watchSettleSeconds` (30) are skipped so a download in progress is not ingested truncated, and moves are capped at `watchMaxPerTick` (20) with the cap reported in the run summary. `os.Rename` falls back to copy-then-remove on `EXDEV`; name collisions suffix `-2`, `-3` as the archiver does; per-file errors leave the file in place and surface like any capture failure; nothing is ever written into a watched folder. **`inboxFingerprint` covers the watched folders as well as the inbox** — `DetectChange` runs before `Run`, so a fingerprint of `00-Inbox` alone would skip the tick and the sweep would never execute. It stays content-free. |
```

- [ ] **Step 2: Flip ADR-040 to built**

In `docs/02-architecture.md`, change `### ADR-040 — Watch-folders: polled, allow-listed, move-in ingress *(accepted — planned)*` to `*(accepted — built)*`.

- [ ] **Step 3: Config reference and example**

In `docs/04-data-model-and-config.md`, document `capture.watch_folders` under the capture block: what it accepts, that empty is the default and the off state, the deny-list, and that a missing folder warns rather than failing validation.

In `axon.config.example.yaml`, inside the `capture:` block, add:

```yaml
      # watch_folders:                        # drop-in capture from outside the vault (ADR-040)
      #   - "/Users/me/Downloads/axon"        # absolute paths only; top-level files only
      #   - "/Users/me/Pictures/Screenshots"  # files are MOVED into 00-Inbox, then ingested + archived
      # Empty/absent = off (the default, both profiles). Refused at load:
      # relative paths, paths inside the vault, and system/home roots
      # ($HOME, /, /etc, ~/.ssh, ~/.aws, ~/.config, ~/Library). Symlinks are
      # never moved, and a file is left alone until it stops changing.
```

Verify it still loads: `go run ./cmd/axon config validate --config axon.config.example.yaml`.

- [ ] **Step 4: Component docs, AUTOMATIONS, GUIDE**

- `docs/05-component-knowledge-ingestion.md`: note that local-file ingestion now has a third entry point (watched folders), all funnelling through `00-Inbox`.
- `docs/06-component-automation-engine.md`: extend the `capture` section with the sweep, the three refusals and the fingerprint change.
- `docs/AUTOMATIONS.md`: extend capture's prose — it now sweeps watched folders too; the table row is unchanged (same schedule, still zero-model).
- `docs/GUIDE.md`: a short "Drop files in from anywhere" subsection near the capture material — create a folder, list it, drop a PDF in, it appears in the knowledge base within five minutes and the original lands in `04-Archive/Capture/`.

- [ ] **Step 5: Roadmap, CHANGELOG, CLAUDE.md**

`docs/20-roadmap-ai-os.md` E1 → shipped:

```markdown
### E1 — Watch-folders (S) · **SHIPPED 2026-08-21 — FR-208/FR-209, ADR-040** (spec: `docs/superpowers/specs/2026-08-21-watch-folders-design.md`)
```

Resolve its two open decisions in place: **move, not copy** (drop-box semantics, self-dedups, no seen-ledger, and nothing is destroyed — the file lands in the vault archive); **no per-folder kind hints** (extension classification is shipped and tested; a second source of truth could disagree with the first). Also record the two refusals the entry did not anticipate — symlinks and the settle window — and update the sequencing sketch, which currently names E1 as a remaining theme-opener.

`CHANGELOG.md` under `[Unreleased]` → `### Added`:

```markdown
- **Drop a file in a folder outside your vault and it flows in.** (FR-208,
  FR-209, **ADR-040**; no schema change.) List absolute paths under
  `capture.watch_folders` and their top-level files are moved into `00-Inbox`
  on the existing capture tick, then ingested and archived exactly as inbox
  drops are. Off by default — an empty list is the off state. Symlinks are
  never followed, files still being written are left until they settle, and
  system and home roots are refused at config load.
```

`CLAUDE.md`: FR range → `FR-01…FR-209`, ADR range → `ADR-001…040`, and a line recording the slice beside the others.

- [ ] **Step 6: Final gate and commit**

```bash
gofmt -l . && go build ./... && go test ./... && golangci-lint run
git add -A
git commit -m "docs: FR-208/FR-209, ADR-040 built, watch-folders in the capture docs"
```

---

### Task 6: Live smoke in an isolated environment

**Files:**
- Create: `<scratchpad>/watch-smoke/` (throwaway — never committed)

**Interfaces:**
- Consumes: the built binary and every change from Tasks 1–5.
- Produces: a verification record for the completion report.

- [ ] **Step 1: Build, and isolate BOTH the home and the vault**

```bash
cd /Users/jandro/Projects/axon/web && npm run build
cd /Users/jandro/Projects/axon
S=/private/tmp/claude-501/-Users-jandro-Projects-axon/2535b695-9eab-42af-be3a-0a30892551fc/scratchpad/watch-smoke
mkdir -p "$S/home/profiles/personal" "$S/vault/00-Inbox" "$S/drop"
go build -o "$S/axon" ./cmd/axon
cp axon.config.example.yaml "$S/home/config.yaml"
sed -i '' 's/port: 7777/port: 7799/g' "$S/home/config.yaml"
grep -rn 7777 "$S" && echo "STOP: 7777 present" || echo "port clean"
```

`cd` back to the repo root **by absolute path** before `go build` — the `cd web` persists within a compound command. Then edit `$S/home/config.yaml` to point the personal profile's `vault_path` at `$S/vault` and `data_dir` at `$S/home/profiles/personal`, and add `watch_folders: ["<$S>/drop"]` to its capture block. **Isolating `AXON_HOME` alone is not enough** — the example config's `vault_path` points at `~/Notes/Personal`, and a run that leaves it would operate on the real vault.

- [ ] **Step 2: Confirm the deny-list refuses a dangerous root**

```bash
export AXON_HOME="$S/home"
cp "$S/home/config.yaml" "$S/home/bad.yaml"
python3 - "$S" <<'PY'
import sys; S=sys.argv[1]
p=S+'/home/bad.yaml'; s=open(p).read()
s=s.replace(f'watch_folders: ["{S}/drop"]', 'watch_folders: ["$HOME"]'.replace("$HOME", __import__("os").path.expanduser("~")))
open(p,'w').write(s)
PY
"$S/axon" config validate --config "$S/home/bad.yaml"
```

Expected: a non-zero exit naming the folder and saying it may not be watched.

- [ ] **Step 3: Sweep a real file**

```bash
printf 'A dropped note about quantum entanglement and measurement.\n' > "$S/drop/dropped.txt"
# Back-date it past the settle window so the first tick takes it.
touch -A -001000 "$S/drop/dropped.txt" 2>/dev/null || touch -d '10 minutes ago' "$S/drop/dropped.txt"
"$S/axon" run capture --config "$S/home/config.yaml"
ls "$S/drop"                       # expect: empty
ls "$S/vault/04-Archive/Capture/"* # expect: the archived original
"$S/axon" search "quantum entanglement" --config "$S/home/config.yaml" | head -5
```

Verify: the watched folder is empty, the original is archived under `04-Archive/Capture/`, and the content is searchable.

- [ ] **Step 4: Confirm the symlink refusal, live**

```bash
printf 'PRIVATE KEY MATERIAL\n' > "$S/secret.txt"
ln -s "$S/secret.txt" "$S/drop/innocent.txt"
"$S/axon" run capture --config "$S/home/config.yaml"
ls -la "$S/drop"                          # the symlink must still be there
ls "$S/vault/00-Inbox"                    # must NOT contain innocent.txt
"$S/axon" search "PRIVATE KEY MATERIAL" --config "$S/home/config.yaml" | head -3
```

Expected: the symlink is untouched, nothing entered the vault, and the search finds nothing. **This is the slice's security property — check it explicitly, not by inference.**

- [ ] **Step 5: Settle window, doctor, and the daemon**

```bash
printf 'still being written\n' > "$S/drop/fresh.txt"    # mtime = now
"$S/axon" run capture --config "$S/home/config.yaml"
ls "$S/drop"                                            # fresh.txt must still be here
"$S/axon" doctor --config "$S/home/config.yaml" | grep -i watch
rm -rf "$S/drop"
"$S/axon" doctor --config "$S/home/config.yaml" | grep -i watch   # expect a warning with a Fix
"$S/axon" run self-check --config "$S/home/config.yaml"           # expect it to file that warning
```

Then start the daemon, confirm the log says **`daemon running`** (not merely `scheduled …` — a bind failure prints the banner and schedules everything before dying), confirm the live daemon on 7777 is untouched (`lsof -ti :7777`), SIGTERM, and confirm a clean exit with the pidfile removed.

- [ ] **Step 6: Record and clean up**

Delete `$S`. Note the verified properties in the completion report; nothing from the smoke directory is committed.

---

## Self-Review

**Spec coverage:** config field, the four load-time refusals, the exists-vs-lexical symlink rule, and the "missing is not an error" decision → Task 1; the sweep with all three refusals, collision suffixing, `EXDEV` fallback, the cap and its reporting, never-write-into-the-folder → Task 2; wiring into `Run` and the fingerprint fix → Task 3; the doctor check and its `Fix` (so FR-207 files it) → Task 4; FR rows, ADR-040 planned→built, config reference, component docs, GUIDE, roadmap, CHANGELOG → Task 5; live smoke with the deny-list, symlink and settle checks → Task 6. Out-of-scope items (`fsnotify`, recursion, kind hints, writing outside the vault, a separate toggle) appear in no task, correctly.

**Type consistency:** `CaptureConfig.WatchFolders []string` defined in Task 1 and read in Tasks 2 and 4. `validateWatchFolders(p Profile) error` defined in Task 1, called from `load.go` in the same task. `config.WatchFolderReadable(path string) error` defined in Task 1, called only by Task 4's doctor check. `sweepWatchFolders(rc RunCtx) ([]string, bool, []string)` and `eligibleWatchFiles(rc RunCtx) []watchFile` defined in Task 2, both called in Task 3. `watchFile{Dir, Name, Size, ModNano}` defined in Task 2 and consumed by Task 3's fingerprint with those exact fields. `watchSettleSeconds` / `watchMaxPerTick` declared once in Task 2 and referenced by those names in Tasks 2, 3 and 5.

**Two fragile points, flagged rather than hidden.** Task 3 changes `inboxFingerprint`'s signature, and there may be existing callers (including tests) beyond the one shown — the step includes a `grep` and an instruction to update each. And Task 2's symlink test depends on `os.ReadDir` reporting the entry type without following the link; the step names the `os.Lstat` fallback if a platform differs, with an explicit instruction not to weaken the assertion.
