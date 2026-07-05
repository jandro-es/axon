package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func TestApplyWritesResearchQuestions(t *testing.T) {
	dir := t.TempDir()
	if _, err := Apply(vault.NewFS(dir)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "03-Resources", "Research Questions.md"))
	if err != nil {
		t.Fatalf("template not scaffolded: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "<!-- axon:answers:start -->") || !strings.Contains(s, "<!-- axon:answers:end -->") {
		t.Fatalf("answers block missing:\n%s", s)
	}
	// Examples must be fenced so they are NOT parsed as live questions.
	if !strings.Contains(s, "```") {
		t.Fatalf("examples not fenced:\n%s", s)
	}
}
