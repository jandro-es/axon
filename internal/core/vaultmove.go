package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jandro-es/axon/internal/claudeassets"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/vault"
)

// VaultMoveOptions parameterises MoveVault. SetConfig persists a config key
// (the CLI passes a closure over its comment-preserving setter) so this
// package stays free of YAML-editing concerns.
type VaultMoveOptions struct {
	ProfileName string
	Profile     config.Profile
	Dest        string // new vault path (may contain ~)
	ConfigPath  string // absolute; baked into regenerated .claude wiring
	BinaryPath  string // absolute; baked into regenerated .claude wiring
	SetConfig   func(key, value string) error
}

// VaultMoveReport lists what happened, step by step (same vocabulary as init).
type VaultMoveReport struct {
	Steps []StepResult `json:"steps"`
	From  string       `json:"from"`
	To    string       `json:"to"`
}

// MoveVault relocates the vault directory and updates every reference AXON
// owns: vault_path in config and the .claude/ wiring inside the vault (its
// absolute config/binary paths travel with regeneration). SQLite needs
// nothing — it stores vault-relative paths (ADR-006: the vault is the source
// of truth; the index is derived). What it can NOT update is Obsidian's own
// vault bookmark; the caller tells the user.
func MoveVault(ctx context.Context, opts VaultMoveOptions) (VaultMoveReport, error) {
	src := config.ExpandPath(opts.Profile.VaultPath)
	dst := config.ExpandPath(opts.Dest)
	rep := VaultMoveReport{From: src, To: dst}
	add := func(s StepResult) { rep.Steps = append(rep.Steps, s) }

	// 1. Preflight.
	st, err := os.Stat(src)
	if err != nil || !st.IsDir() {
		return rep, fmt.Errorf("vault %q is not a directory: %w", src, err)
	}
	if src == dst {
		return rep, fmt.Errorf("destination equals the current vault path")
	}
	if strings.HasPrefix(dst+string(filepath.Separator), src+string(filepath.Separator)) {
		return rep, fmt.Errorf("destination %q is inside the vault itself", dst)
	}
	if dstInfo, err := os.Stat(dst); err == nil {
		if !dstInfo.IsDir() {
			return rep, fmt.Errorf("destination %q exists and is not a directory", dst)
		}
		entries, rerr := os.ReadDir(dst)
		if rerr != nil {
			return rep, rerr
		}
		if len(entries) > 0 {
			return rep, fmt.Errorf("destination %q exists and is not empty — refusing to merge", dst)
		}
		// An empty directory is fine to move INTO: drop it so Rename works.
		if err := os.Remove(dst); err != nil {
			return rep, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return rep, err
	}
	add(StepResult{"preflight", StepDone, fmt.Sprintf("%s → %s", src, dst)})

	// 2. Move: rename fast-path, copy+verify+delete across filesystems.
	if err := os.Rename(src, dst); err != nil {
		var linkErr *os.LinkError
		if !errors.As(err, &linkErr) {
			return rep, fmt.Errorf("move vault: %w", err)
		}
		n, cerr := copyTree(src, dst)
		if cerr != nil {
			return rep, fmt.Errorf("cross-device copy: %w (source left untouched)", cerr)
		}
		if err := os.RemoveAll(src); err != nil {
			return rep, fmt.Errorf("copied %d files but could not remove the old vault at %s: %w", n, src, err)
		}
		add(StepResult{"move", StepDone, fmt.Sprintf("copied %d files across filesystems, removed the old tree", n)})
	} else {
		add(StepResult{"move", StepDone, "renamed in place (same filesystem)"})
	}

	// 3. Point the config at the new location.
	if err := opts.SetConfig("vault_path", opts.Dest); err != nil {
		return rep, fmt.Errorf("vault moved to %s but config update failed: %w — set vault_path manually", dst, err)
	}
	add(StepResult{"config", StepDone, "vault_path = " + opts.Dest})

	// 4. Regenerate the .claude wiring at the new location (re-bakes absolute
	// config/binary paths in .mcp.json + settings.json hooks).
	res, err := claudeassets.Generate(vault.NewFS(dst), claudeassets.Params{
		Profile:    opts.ProfileName,
		Binary:     opts.BinaryPath,
		ConfigPath: opts.ConfigPath,
		ConfigDir:  opts.Profile.Paths().ConfigDir,
		AxonHome:   config.AxonHome(),
	})
	if err != nil {
		add(StepResult{"claude-wiring", StepWarn, fmt.Sprintf("regeneration failed: %v — run `axon init`", err)})
	} else {
		add(StepResult{"claude-wiring", StepDone, fmt.Sprintf("regenerated (%d file(s) rewritten)", len(res.Created))})
	}

	// 5. The index needs nothing: paths in SQLite are vault-relative (ADR-006).
	add(StepResult{"index", StepAlready, "SQLite stores vault-relative paths — no reindex required"})
	return rep, nil
}

// copyTree copies a directory recursively preserving modes; returns the file
// count. Used only for cross-filesystem moves.
func copyTree(src, dst string) (int, error) {
	count := 0
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		in, oerr := os.Open(path)
		if oerr != nil {
			return oerr
		}
		defer in.Close()
		out, cerr := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if cerr != nil {
			return cerr
		}
		written, werr := io.Copy(out, in)
		if cerr := out.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		if written != info.Size() {
			return fmt.Errorf("short copy of %s: %d of %d bytes", rel, written, info.Size())
		}
		count++
		return nil
	})
	return count, err
}
