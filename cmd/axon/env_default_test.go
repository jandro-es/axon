package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvDefaultsToAxonHome: with no --env flag, secrets must load from
// <AXON_HOME>/.env — not from ./.env of whatever cwd the command runs in
// (the daemon and most shells are never in ~/.axon).
func TestEnvDefaultsToAxonHome(t *testing.T) {
	const varName = "AXON_TEST_TOKEN_ENVDEFAULT"
	os.Unsetenv(varName)
	t.Cleanup(func() { os.Unsetenv(varName) })

	home := t.TempDir()
	t.Setenv("AXON_HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".env"), []byte(varName+"=sk-ant-oat01-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	cfg := `version: 1
project_name: axon
active_profile: work
profiles:
  work:
    vault_path: "` + filepath.ToSlash(filepath.Join(dir, "vault")) + `"
    data_dir: "` + filepath.ToSlash(filepath.Join(dir, "data")) + `"
    claude: { auth_mode: enterprise, config_dir: "` + filepath.ToSlash(filepath.Join(dir, "data", "claude")) + `", oauth_token: "env:` + varName + `" }
    dashboard: { host: "127.0.0.1", port: 7797 }
    embeddings: { provider: ollama, host: "http://127.0.0.1:1", model: m, dim: 8, batch_size: 4 }
    models: { classify: h, routine: s, synthesis: o }
    limits: { daily_tokens: 1_000, weekly_tokens: 5_000, guard_pause_at_pct: 80 }
    retrieval: { top_k: 4, max_context_tokens: 1000 }
    policy: { data_residency: local-only }
`
	cfgPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	// No --env: the default must find <AXON_HOME>/.env from this foreign cwd.
	out, err := run(t, "doctor", "--config", cfgPath)
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	if strings.Contains(out, "no OAuth token resolvable") || !strings.Contains(out, "OAuth token resolvable") {
		t.Errorf("token in AXON_HOME/.env not resolved without --env:\n%s", out)
	}
}
