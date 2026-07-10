package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/actions"
)

func TestCompleteActionFlipsLine(t *testing.T) {
	ctx := context.Background()
	body := "## Todo\n- [ ] call bob 📅 2026-07-15\n- [ ] other task\n"
	note := "---\ntitle: T\n---\n" + body
	v := newTempVault(t, map[string]string{"01-Projects/p.md": note})

	// Hash the target line the T1 way (Extract stamps SourcePath/LineNo).
	var target string
	for _, a := range actions.Extract("01-Projects/p.md", body, false) {
		if strings.Contains(a.Text, "call bob") {
			target = a.Hash()
		}
	}
	if target == "" {
		t.Fatal("could not hash target line")
	}

	if err := v.CompleteAction(ctx, "01-Projects/p.md", target, "2026-07-10"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "01-Projects", "p.md"))
	got := string(raw)
	if !strings.Contains(got, "- [x] call bob 📅 2026-07-15 ✅ 2026-07-10") {
		t.Errorf("target line not completed:\n%s", got)
	}
	if !strings.Contains(got, "- [ ] other task") {
		t.Error("other task must be untouched")
	}
	if !strings.HasPrefix(got, "---\ntitle: T\n---\n") {
		t.Error("frontmatter must be byte-preserved")
	}
}

func TestCompleteActionStaleHash(t *testing.T) {
	ctx := context.Background()
	v := newTempVault(t, map[string]string{"p.md": "- [ ] x\n"})
	err := v.CompleteAction(ctx, "p.md", "deadbeef-not-a-real-hash", "2026-07-10")
	if !errors.Is(err, ErrActionNotFound) {
		t.Fatalf("want ErrActionNotFound, got %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(v.Root(), "p.md"))
	if string(raw) != "- [ ] x\n" {
		t.Errorf("file must be unchanged on stale hash: %q", raw)
	}
}
