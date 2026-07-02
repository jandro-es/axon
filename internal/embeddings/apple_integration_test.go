package embeddings

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestAppleHelperEndToEnd compiles the real Swift helper and embeds through it.
// Skipped unless on macOS with swiftc available AND the NLContextualEmbedding
// model assets already present — CI macOS runners have swiftc but cannot fetch
// the assets, so an assets probe (not just tool presence) gates the real run.
func TestAppleHelperEndToEnd(t *testing.T) {
	if runtime.GOOS != "darwin" || !SwiftAvailable() {
		t.Skip("requires macOS + swiftc")
	}
	if testing.Short() {
		t.Skip("skipping real swiftc compile in -short mode")
	}
	helper := filepath.Join(t.TempDir(), "axon-apple-embed")
	if _, err := EnsureAppleHelper(context.Background(), helper); err != nil {
		t.Fatalf("compile helper: %v", err)
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(probeCtx, helper, "--check-assets").CombinedOutput(); err != nil {
		t.Skipf("NLContextualEmbedding assets not available on this machine: %s", strings.TrimSpace(string(out)))
	}
	a := NewApple(helper, "apple-nlcontextual-v1", 512)
	vecs, err := a.Embed(context.Background(), []string{"hello world", "zettelkasten"})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 512 {
		t.Fatalf("got %d vectors, dim %d", len(vecs), len(vecs[0]))
	}
	if err := a.Healthcheck(context.Background()); err != nil {
		t.Errorf("healthcheck: %v", err)
	}
}
