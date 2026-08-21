package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeCollidingRecipeConfig writes a config whose recipe shadows a built-in
// automation name, so `axon start` refuses at its earliest guard.
func writeCollidingRecipeConfig(t *testing.T, dir string) string {
	t.Helper()
	for _, sub := range []string{"vault", "data"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := `version: 1
project_name: axon
active_profile: personal
profiles:
  personal:
    vault_path: "` + filepath.ToSlash(filepath.Join(dir, "vault")) + `"
    data_dir: "` + filepath.ToSlash(filepath.Join(dir, "data")) + `"
    claude: { auth_mode: subscription, config_dir: "` + filepath.ToSlash(filepath.Join(dir, "data", "claude")) + `" }
    dashboard: { host: "127.0.0.1", port: 7799 }
    embeddings: { provider: ollama, host: "http://127.0.0.1:1", model: nomic-embed-text, dim: 768, batch_size: 32 }
    models: { classify: h, routine: s, synthesis: o }
    limits: { daily_tokens: 1_000_000, weekly_tokens: 5_000_000, guard_pause_at_pct: 80 }
    retrieval: { top_k: 8, max_context_tokens: 12_000 }
    policy: { data_residency: local-only, egress_allowlist: ["*"], ingest_domains_allow: ["*"], ingest_domains_deny: [], redaction_rules: [], allowed_automations: ["*"] }
    automations: {}
    recipes:
      - name: heartbeat
        purpose: "Shadows a built-in; start must refuse."
        inputs:
          - name: n
            note: { path: "03-Resources/List.md" }
        render: "{{n}}"
        output:
          block: { note: "03-Resources/Out.md", block: "shadow" }
`
	path := filepath.Join(dir, "axon.config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A guard that refuses `axon start` must not leave the profile's resources
// open. start registers deps.close() inside its shutdown closure — late, so
// every refusal returning before that point has to be covered by its own
// cleanup, or an in-process caller leaks a DB handle per refused start.
func TestStartRefusalClosesDeps(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeCollidingRecipeConfig(t, dir)

	var captured *profileDeps
	orig := loadProfileDeps
	loadProfileDeps = func(gf *globalFlags, openDB bool) (*profileDeps, error) {
		d, err := orig(gf, openDB)
		captured = d
		return d, err
	}
	t.Cleanup(func() { loadProfileDeps = orig })

	out, err := run(t, "start", "--config", cfgPath)
	if err == nil {
		t.Fatalf("colliding recipe should refuse start:\n%s", out)
	}
	if !strings.Contains(err.Error(), "collides with a built-in") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
	if captured == nil || captured.db == nil {
		t.Fatal("no deps captured — the test cannot observe cleanup")
	}
	if perr := captured.db.Ping(); perr == nil {
		t.Fatal("start refused but left the DB handle open (deps.close never ran)")
	}
}
