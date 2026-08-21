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
		// A literal ".." in the configured string. filepath.Join would clean it
		// away, so it has to be built by concatenation to reach the validator.
		{"dot dot", []string{ok1 + "/../x"}, "absolute"},
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
//
// The target is /etc rather than ~/.ssh deliberately: EvalSymlinks resolves
// nothing when the target does not exist, and a CI runner has no ~/.ssh — so
// that version of this test passed locally and failed on Linux. /etc exists
// on every platform this builds for. (A symlink to a MISSING deny-listed path
// is not caught, by design: it is unreadable at runtime too, so the
// watch-folders doctor check surfaces it.)
func TestValidateWatchFoldersResolvesSymlinks(t *testing.T) {
	if _, err := os.Stat("/etc"); err != nil {
		t.Skip("/etc not present on this platform")
	}
	link := filepath.Join(t.TempDir(), "sneaky")
	if err := os.Symlink("/etc", link); err != nil {
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

// A deny-listed root can itself be a symlink: on macOS /etc links to
// /private/etc. Configuring the resolved form must be refused too, or the
// deny-list is trivially sidestepped on that platform.
func TestValidateWatchFoldersDeniesResolvedFormOfRoots(t *testing.T) {
	resolved, err := filepath.EvalSymlinks("/etc")
	if err != nil {
		t.Skip("/etc not resolvable on this platform")
	}
	if resolved == "/etc" {
		t.Skip("/etc is not a symlink here — nothing to sidestep")
	}
	p := Profile{VaultPath: t.TempDir(), Capture: CaptureConfig{WatchFolders: []string{resolved}}}
	if err := validateWatchFolders(p); err == nil || !strings.Contains(err.Error(), "not be watched") {
		t.Fatalf("the resolved form of a deny-listed root must be refused, got %v", err)
	}
}
