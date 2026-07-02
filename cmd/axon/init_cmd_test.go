package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

// writeTempConfig writes a minimal valid config rooted at dir and returns its
// path. Vault and data dirs live under dir so the test is hermetic.
func writeTempConfig(t *testing.T, dir string) string {
	t.Helper()
	cfg := `version: 1
project_name: axon
active_profile: personal
profiles:
  personal:
    vault_path: "` + filepath.ToSlash(filepath.Join(dir, "vault")) + `"
    data_dir: "` + filepath.ToSlash(filepath.Join(dir, "data")) + `"
    claude: { auth_mode: subscription, config_dir: "` + filepath.ToSlash(filepath.Join(dir, "data", "claude")) + `" }
    dashboard: { host: "127.0.0.1", port: 7777 }
    embeddings: { provider: ollama, host: "http://127.0.0.1:1", model: nomic-embed-text, dim: 768, batch_size: 32 }
    models: { classify: h, routine: s, synthesis: o }
    limits: { daily_tokens: 1_000_000, weekly_tokens: 5_000_000, guard_pause_at_pct: 80 }
    retrieval: { top_k: 8, max_context_tokens: 12_000 }
    policy: { data_residency: local-only, egress_allowlist: ["*"], ingest_domains_allow: ["*"], ingest_domains_deny: [], redaction_rules: [], allowed_automations: ["*"] }
    automations: {}
`
	path := filepath.Join(dir, "axon.config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInitCommandConvergesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	out, err := run(t, "init", "--config", cfgPath, "--env", filepath.Join(dir, "nonexistent.env"))
	if err != nil {
		t.Fatalf("init failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "environment converged") {
		t.Errorf("first init missing convergence summary:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "vault", "Templates", "Daily Note.md")); err != nil {
		t.Errorf("scaffold not created: %v", err)
	}
	// Step 7: Claude Code wiring is generated.
	for _, p := range []string{".claude/.mcp.json", ".claude/settings.json", ".claude/CLAUDE.md", ".claude/agents/librarian.md"} {
		if _, err := os.Stat(filepath.Join(dir, "vault", filepath.FromSlash(p))); err != nil {
			t.Errorf("init did not generate %q: %v", p, err)
		}
	}
	// Step 8: in-vault Dataview dashboards.
	if _, err := os.Stat(filepath.Join(dir, "vault", ".axon", "dashboards", "Active Projects.md")); err != nil {
		t.Errorf("init did not generate in-vault dashboards: %v", err)
	}

	out, err = run(t, "init", "--config", cfgPath, "--env", filepath.Join(dir, "nonexistent.env"))
	if err != nil {
		t.Fatalf("second init failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no changes") {
		t.Errorf("second init not idempotent:\n%s", out)
	}
}

func TestInitCommandJSON(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	out, err := run(t, "init", "--json", "--config", cfgPath)
	if err != nil {
		t.Fatalf("init --json failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, `"profile": "personal"`) || !strings.Contains(out, `"steps"`) {
		t.Errorf("unexpected JSON output:\n%s", out)
	}
}

func TestInitEmbeddingsFlagPersistsProvider(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	// init itself may warn (no swiftc/helper in a hermetic env); we assert only
	// the persisted config.
	_, _ = run(t, "init", "--embeddings", "apple", "--config", cfgPath, "--env", filepath.Join(dir, "nonexistent.env"))

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Embeddings.Provider != "apple" {
		t.Errorf("provider = %q, want apple", p.Embeddings.Provider)
	}
	if p.Embeddings.Model != config.AppleEmbeddingModel || p.Embeddings.Dim != config.AppleEmbeddingDim {
		t.Errorf("model/dim = %q/%d, want apple defaults", p.Embeddings.Model, p.Embeddings.Dim)
	}

	// Invalid value is refused before any init work.
	if _, err := run(t, "init", "--embeddings", "banana", "--config", cfgPath); err == nil {
		t.Error("expected error for invalid --embeddings value")
	}
}

func TestReindexCommandRebuilds(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeTempConfig(t, dir)

	if _, err := run(t, "init", "--config", cfgPath); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Delete the DB and confirm reindex rebuilds it from the vault (S9).
	dbPath := filepath.Join(dir, "data", "db.sqlite")
	if err := os.Remove(dbPath); err != nil {
		t.Fatalf("remove db: %v", err)
	}
	out, err := run(t, "reindex", "--config", cfgPath)
	if err != nil {
		t.Fatalf("reindex: %v\n%s", err, out)
	}
	if !strings.Contains(out, "notes,") {
		t.Errorf("reindex output missing counts:\n%s", out)
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("reindex did not recreate the database: %v", err)
	}
}
