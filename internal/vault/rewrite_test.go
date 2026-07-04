package vault

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewriteSystemFile(t *testing.T) {
	v := NewFS(t.TempDir())
	if err := v.Append(".axon/review-queue.md", "- [ ] item one\n"); err != nil {
		t.Fatal(err)
	}
	if err := v.RewriteSystemFile(".axon/review-queue.md", "- [x] item one ✓\n"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(v.Root(), ".axon", "review-queue.md"))
	if string(data) != "- [x] item one ✓\n" {
		t.Fatalf("content = %q", data)
	}

	// The guard is code, not convention.
	for _, rel := range []string{"01-Projects/x.md", "notes.md", "../escape.md", ".axonish/x.md"} {
		if err := v.RewriteSystemFile(rel, "x"); err == nil || !strings.Contains(err.Error(), ".axon") {
			t.Fatalf("rel %q: err = %v, want .axon-only refusal", rel, err)
		}
	}
}
