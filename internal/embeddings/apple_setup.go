package embeddings

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// appleHelperSource is the Swift helper program, embedded so `axon init` can
// (re)build the helper from an installed binary with no repo checkout.
//
//go:embed apple_helper.swift
var appleHelperSource []byte

// appleCompile compiles src → dst; a var so tests can fake the toolchain.
var appleCompile = swiftCompile

// SwiftAvailable reports whether the Swift compiler is on PATH (Xcode CLT).
func SwiftAvailable() bool {
	_, err := exec.LookPath("swiftc")
	return err == nil
}

// EnsureAppleHelper writes + compiles the embedded Swift helper to helperPath,
// idempotently: a SHA-256 marker beside the binary records the source it was
// built from, so re-runs skip compilation unless the embedded source changed.
// Returns changed=true when a (re)compile happened.
func EnsureAppleHelper(ctx context.Context, helperPath string) (bool, error) {
	sum := sha256.Sum256(appleHelperSource)
	want := hex.EncodeToString(sum[:])
	marker := helperPath + ".src.sha256"

	if have, err := os.ReadFile(marker); err == nil && string(have) == want {
		if st, err := os.Stat(helperPath); err == nil && st.Mode()&0o111 != 0 {
			return false, nil // up to date
		}
	}

	if err := os.MkdirAll(filepath.Dir(helperPath), 0o755); err != nil {
		return false, fmt.Errorf("apple helper: create dir: %w", err)
	}
	srcPath := helperPath + ".swift"
	if err := os.WriteFile(srcPath, appleHelperSource, 0o644); err != nil {
		return false, fmt.Errorf("apple helper: write source: %w", err)
	}
	if err := appleCompile(ctx, srcPath, helperPath); err != nil {
		return false, fmt.Errorf("apple helper: compile: %w", err)
	}
	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("apple helper: write marker: %w", err)
	}
	return true, nil
}

// swiftCompile is the real toolchain invocation (requires Xcode CLT).
func swiftCompile(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "swiftc", "-O", src, "-o", dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("swiftc: %w: %s", err, out)
	}
	return nil
}
