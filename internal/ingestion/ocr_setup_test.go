package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureOCRHelperCompilesOnceThenSkips(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "bin", "axon-apple-ocr")
	compiles := 0
	orig := ocrCompile
	ocrCompile = func(ctx context.Context, src, dst string) error {
		compiles++
		return os.WriteFile(dst, []byte("fake-binary"), 0o755)
	}
	defer func() { ocrCompile = orig }()

	changed, err := EnsureOCRHelper(context.Background(), helper)
	if err != nil || !changed || compiles != 1 {
		t.Fatalf("first run: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
	changed, err = EnsureOCRHelper(context.Background(), helper)
	if err != nil || changed || compiles != 1 {
		t.Fatalf("second run should skip: changed=%v err=%v compiles=%d", changed, err, compiles)
	}
}
