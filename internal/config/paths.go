package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultAxonHome is the conventional root for per-profile data dirs when
// AXON_HOME is unset.
const DefaultAxonHome = "~/.axon"

// AxonHome returns the resolved AXON home directory: AXON_HOME if set, else the
// built-in default, with a leading ~ expanded to the user's home directory.
func AxonHome() string {
	if v := os.Getenv("AXON_HOME"); v != "" {
		return ExpandPath(v)
	}
	return ExpandPath(DefaultAxonHome)
}

// ExpandPath expands a leading ~ (or ~/) to the user's home directory and
// returns a cleaned, absolute-where-possible path. A bare "~" maps to home; an
// unexpandable ~ is returned unchanged rather than erroring, since config paths
// are validated for presence, not reachability, at this stage.
func ExpandPath(p string) string {
	if p == "" {
		return p
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if p == "~" {
				return filepath.Clean(home)
			}
			return filepath.Clean(filepath.Join(home, p[2:]))
		}
	}
	return filepath.Clean(p)
}

// ResolvedPaths holds the concrete, ~-expanded paths a running profile needs.
type ResolvedPaths struct {
	VaultPath string
	DataDir   string
	ConfigDir string // Claude CLAUDE_CONFIG_DIR
	DBPath    string // <data_dir>/db.sqlite
	LogsDir   string // <data_dir>/logs
}

// Paths derives the expanded filesystem paths for a profile. It performs no IO
// (no directory creation) — that is axon init's job in Phase 1.
func (p Profile) Paths() ResolvedPaths {
	dataDir := ExpandPath(p.DataDir)
	return ResolvedPaths{
		VaultPath: ExpandPath(p.VaultPath),
		DataDir:   dataDir,
		ConfigDir: ExpandPath(p.Claude.ConfigDir),
		DBPath:    filepath.Join(dataDir, "db.sqlite"),
		LogsDir:   filepath.Join(dataDir, "logs"),
	}
}
