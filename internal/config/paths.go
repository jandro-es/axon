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

// DefaultConfigPath is the absolute path the CLI reads when no --config flag is
// given: <AXON_HOME>/config.yaml (so it follows AXON_HOME and the standard
// per-user layout instead of depending on the working directory).
func DefaultConfigPath() string {
	return filepath.Join(AxonHome(), DefaultConfigFile)
}

// DefaultEnvPath is the absolute path the CLI loads secrets from when no --env
// flag is given: <AXON_HOME>/.env — anchored like DefaultConfigPath so secrets
// are found regardless of the working directory (a daemon's cwd, or any shell
// location, is almost never ~/.axon).
func DefaultEnvPath() string {
	return filepath.Join(AxonHome(), ".env")
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

// AppleEmbeddingModel and AppleEmbeddingDim are the defaults written to config
// when the apple embeddings provider is selected. The dim is asserted live by
// the init probe; NLContextualEmbedding v1 reports 512.
const (
	AppleEmbeddingModel = "apple-nlcontextual-v1"
	AppleEmbeddingDim   = 512
)

// DefaultAppleHelperPath is where `axon init` compiles the Apple embeddings
// helper: a machine-level tool (like the ollama binary), not per-profile.
func DefaultAppleHelperPath() string {
	return filepath.Join(AxonHome(), "bin", "axon-apple-embed")
}

// AppleFoundationModel identifies the on-device Foundation Models system
// model in ledger rows and ModelRefs (ADR-015). Versioned like the
// embeddings identifier so a future model change is visible in the ledger.
const AppleFoundationModel = "apple-foundation-v1"

// DefaultAppleLMHelperPath is where `axon init` compiles the Foundation
// Models helper: machine-level (outside profile isolation), like the
// embeddings helper.
func DefaultAppleLMHelperPath() string {
	return filepath.Join(AxonHome(), "bin", "axon-apple-lm")
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
