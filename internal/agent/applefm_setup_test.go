package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAppleLMHelperIdempotent(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "axon-apple-lm")

	compiles := 0
	orig := appleLMCompile
	appleLMCompile = func(ctx context.Context, src, dst string) error {
		compiles++
		return os.WriteFile(dst, []byte("#!/bin/sh\n"), 0o755)
	}
	defer func() { appleLMCompile = orig }()

	changed, err := EnsureAppleLMHelper(context.Background(), helper)
	if err != nil || !changed {
		t.Fatalf("first ensure: changed=%v err=%v, want true/nil", changed, err)
	}
	changed, err = EnsureAppleLMHelper(context.Background(), helper)
	if err != nil || changed {
		t.Fatalf("second ensure: changed=%v err=%v, want false/nil (marker skip)", changed, err)
	}
	if compiles != 1 {
		t.Fatalf("compiled %d times, want 1", compiles)
	}
	if _, err := os.Stat(helper + ".src.sha256"); err != nil {
		t.Fatalf("marker missing: %v", err)
	}
}
