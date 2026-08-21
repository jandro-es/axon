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
	// Wall clock, deliberately — NOT rc.now(). File mtimes come from the OS,
	// so comparing them against an injectable logical clock would make every
	// file look unsettled whenever the two disagree (as they do under test).
	cutoff := time.Now().Add(-watchSettleSeconds * time.Second)
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
		if _, statErr := os.Lstat(dest); os.IsNotExist(statErr) {
			return dest, chosen, nil
		}
	}
	return "", "", fmt.Errorf("no free inbox name for %q", name)
}

// moveFile renames, falling back to copy-then-remove across filesystems
// (a watched folder on an external volume or network mount). The source is
// removed only after the destination is durably written.
func moveFile(src, dest string) error {
	err := os.Rename(src, dest)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
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
