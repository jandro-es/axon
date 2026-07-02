package embeddings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureAppleHelperCompilesOnceThenSkips(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "bin", "axon-apple-embed")
	compiles := 0
	orig := appleCompile
	appleCompile = func(ctx context.Context, src, dst string) error {
		compiles++
		return os.WriteFile(dst, []byte("fake-binary"), 0o755)
	}
	defer func() { appleCompile = orig }()

	changed, err := EnsureAppleHelper(context.Background(), helper)
	if err != nil || !changed || compiles != 1 {
		t.Fatalf("first run: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
	changed, err = EnsureAppleHelper(context.Background(), helper)
	if err != nil || changed || compiles != 1 {
		t.Fatalf("second run should skip: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
	// A corrupted marker forces recompilation.
	if err := os.WriteFile(helper+".src.sha256", []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err = EnsureAppleHelper(context.Background(), helper)
	if err != nil || !changed || compiles != 2 {
		t.Fatalf("stale marker: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
}

func TestEnsureAppleHelperCompileFailure(t *testing.T) {
	dir := t.TempDir()
	orig := appleCompile
	appleCompile = func(ctx context.Context, src, dst string) error {
		return os.ErrPermission
	}
	defer func() { appleCompile = orig }()
	if _, err := EnsureAppleHelper(context.Background(), filepath.Join(dir, "h")); err == nil {
		t.Error("expected compile error to propagate")
	}
}
