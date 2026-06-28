package claudeassets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/vault"
)

func testParams() Params {
	return Params{
		Profile: "personal", Binary: "/usr/local/bin/axon",
		ConfigPath: "/home/u/axon.config.yaml", ConfigDir: "/home/u/.axon/profiles/personal/claude",
		AxonHome: "/home/u/.axon",
	}
}

func TestGenerateWritesIntegration(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)

	res, err := Generate(v, testParams())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed() {
		t.Fatal("first Generate reported no changes")
	}
	for _, p := range []string{
		".claude/CLAUDE.md", ".claude/.mcp.json", ".claude/settings.json",
		".claude/agents/librarian.md", ".claude/skills/ingest-url/SKILL.md",
		".claude/plugins/axon/.claude-plugin/plugin.json",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err != nil {
			t.Errorf("expected %q: %v", p, err)
		}
	}

	// CLAUDE.md is profile-aware.
	cm, _ := os.ReadFile(filepath.Join(dir, ".claude", "CLAUDE.md"))
	if !strings.Contains(string(cm), "personal") || !strings.Contains(string(cm), "vault_move") {
		t.Error("CLAUDE.md missing profile name / wikilink-safety rule")
	}
}

func TestMcpJSONRegistersAxonServer(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)
	if _, err := Generate(v, testParams()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".claude", ".mcp.json"))
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf(".mcp.json invalid: %v", err)
	}
	axon, ok := doc.MCPServers["axon"]
	if !ok {
		t.Fatal("no axon server registered")
	}
	if axon.Command != "/usr/local/bin/axon" || len(axon.Args) == 0 || axon.Args[0] != "mcp" {
		t.Errorf("unexpected server spec: %+v", axon)
	}
	if axon.Env["CLAUDE_CONFIG_DIR"] == "" {
		t.Error("CLAUDE_CONFIG_DIR not set in env (profile isolation)")
	}
}

func TestSettingsJSONHasAllHooks(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)
	if _, err := Generate(v, testParams()); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	var doc struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("settings.json invalid: %v", err)
	}
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop"} {
		if _, ok := doc.Hooks[ev]; !ok {
			t.Errorf("missing hook %q", ev)
		}
	}
	if !strings.Contains(string(raw), "axon\\\" hook PreToolUse") && !strings.Contains(string(raw), "hook PreToolUse") {
		t.Error("PreToolUse hook command not wired")
	}
}

func TestGenerateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	v := vault.NewFS(dir)
	if _, err := Generate(v, testParams()); err != nil {
		t.Fatal(err)
	}
	res, err := Generate(v, testParams())
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() {
		t.Errorf("second Generate changed files: %v", res.Created)
	}
}

func TestGenerateNeverClobbersUserEdits(t *testing.T) {
	dir := t.TempDir()
	// Pre-existing user-customised CLAUDE.md.
	_ = os.MkdirAll(filepath.Join(dir, ".claude"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".claude", "CLAUDE.md"), []byte("MY CUSTOM RULES"), 0o644)

	v := vault.NewFS(dir)
	if _, err := Generate(v, testParams()); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dir, ".claude", "CLAUDE.md"))
	if string(got) != "MY CUSTOM RULES" {
		t.Errorf("user CLAUDE.md clobbered: %q", got)
	}
}
