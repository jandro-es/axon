package ingestion

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

//go:embed ocr_apple_helper.swift
var ocrHelperSource []byte

// ocrCompile compiles src → dst; a var so tests can fake the toolchain.
var ocrCompile = swiftCompileOCR

// EnsureOCRHelper writes + compiles the embedded Swift OCR helper to helperPath,
// idempotently (SHA-256 marker beside the binary records the source it was built
// from). Returns changed=true when a (re)compile happened. Mirrors
// embeddings.EnsureAppleHelper.
func EnsureOCRHelper(ctx context.Context, helperPath string) (bool, error) {
	sum := sha256.Sum256(ocrHelperSource)
	want := hex.EncodeToString(sum[:])
	marker := helperPath + ".src.sha256"
	if have, err := os.ReadFile(marker); err == nil && string(have) == want {
		if st, err := os.Stat(helperPath); err == nil && st.Mode()&0o111 != 0 {
			return false, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(helperPath), 0o755); err != nil {
		return false, fmt.Errorf("ocr helper: create dir: %w", err)
	}
	srcPath := helperPath + ".swift"
	if err := os.WriteFile(srcPath, ocrHelperSource, 0o644); err != nil {
		return false, fmt.Errorf("ocr helper: write source: %w", err)
	}
	if err := ocrCompile(ctx, srcPath, helperPath); err != nil {
		return false, fmt.Errorf("ocr helper: compile: %w", err)
	}
	if err := os.WriteFile(marker, []byte(want), 0o644); err != nil {
		return false, fmt.Errorf("ocr helper: write marker: %w", err)
	}
	return true, nil
}

func swiftCompileOCR(ctx context.Context, src, dst string) error {
	cmd := exec.CommandContext(ctx, "swiftc", "-O", src, "-o", dst)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("swiftc: %w: %s", err, out)
	}
	return nil
}
