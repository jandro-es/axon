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
	// The deny-list is compared on BOTH the lexical and the resolved form of
	// each root, because a root can itself be a symlink: on macOS /etc is a
	// link to /private/etc, so a literal-only list would let /private/etc (or
	// any link resolving there) through.
	denied := make(map[string]bool, len(deniedWatchRoots)*2)
	for _, d := range deniedWatchRoots {
		clean := filepath.Clean(ExpandPath(d))
		denied[clean] = true
		if resolved, err := filepath.EvalSymlinks(clean); err == nil {
			denied[filepath.Clean(resolved)] = true
		}
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

// WatchFolderReadable reports whether a watched folder is readable right now.
// Used by the runtime sweep and the doctor check, never by validation.
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
