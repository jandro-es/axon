package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exampleConfigPath locates the repo's example config relative to this package.
func exampleConfigPath(t *testing.T) string {
	t.Helper()
	// internal/config -> repo root is two levels up.
	p, err := filepath.Abs(filepath.Join("..", "..", "axon.config.example.yaml"))
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return p
}

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load(exampleConfigPath(t))
	if err != nil {
		t.Fatalf("Load example config: %v", err)
	}
	if cfg.Version != 1 {
		t.Errorf("version = %d, want 1", cfg.Version)
	}
	if cfg.ActiveProfile != "personal" {
		t.Errorf("active_profile = %q, want personal", cfg.ActiveProfile)
	}
	if _, ok := cfg.Profiles["personal"]; !ok {
		t.Error("missing personal profile")
	}
	if _, ok := cfg.Profiles["work"]; !ok {
		t.Error("missing work profile")
	}

	// FlexInt underscore parsing must have produced the real magnitude.
	if got := cfg.Profiles["personal"].Limits.DailyTokens.Int(); got != 1_500_000 {
		t.Errorf("personal daily_tokens = %d, want 1500000", got)
	}
	if got := cfg.Profiles["work"].Limits.WeeklyTokens.Int(); got != 3_000_000 {
		t.Errorf("work weekly_tokens = %d, want 3000000", got)
	}
	// Auth modes.
	if got := cfg.Profiles["personal"].Claude.AuthMode; got != "subscription" {
		t.Errorf("personal auth_mode = %q, want subscription", got)
	}
	if got := cfg.Profiles["work"].Claude.AuthMode; got != "enterprise" {
		t.Errorf("work auth_mode = %q, want enterprise", got)
	}
}

func TestValidate(t *testing.T) {
	base := func() *Config {
		return &Config{
			Version:       1,
			ProjectName:   "axon",
			ActiveProfile: "personal",
			Profiles: map[string]Profile{
				"personal": validProfile(),
			},
		}
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{"valid", func(*Config) {}, false},
		{"bad version", func(c *Config) { c.Version = 2 }, true},
		{"missing project_name", func(c *Config) { c.ProjectName = "" }, true},
		{"no profiles", func(c *Config) { c.Profiles = nil }, true},
		{"active profile missing", func(c *Config) { c.ActiveProfile = "ghost" }, true},
		{"bad auth_mode", func(c *Config) {
			p := c.Profiles["personal"]
			p.Claude.AuthMode = "carrier-pigeon"
			c.Profiles["personal"] = p
		}, true},
		{"port out of range", func(c *Config) {
			p := c.Profiles["personal"]
			p.Dashboard.Port = 70000
			c.Profiles["personal"] = p
		}, true},
		{"missing vault_path", func(c *Config) {
			p := c.Profiles["personal"]
			p.VaultPath = ""
			c.Profiles["personal"] = p
		}, true},
		{"apple embeddings provider valid", func(c *Config) {
			p := c.Profiles["personal"]
			p.Embeddings.Provider = "apple"
			c.Profiles["personal"] = p
		}, false},
		{"unknown embeddings provider", func(c *Config) {
			p := c.Profiles["personal"]
			p.Embeddings.Provider = "openai"
			c.Profiles["personal"] = p
		}, true},
		{"missing embeddings provider", func(c *Config) {
			p := c.Profiles["personal"]
			p.Embeddings.Provider = ""
			c.Profiles["personal"] = p
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base()
			tt.mutate(c)
			err := c.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func validProfile() Profile {
	return Profile{
		VaultPath:  "~/Notes/Personal",
		DataDir:    "~/.axon/profiles/personal",
		Claude:     ClaudeConfig{AuthMode: "subscription"},
		Dashboard:  DashboardConfig{Host: "127.0.0.1", Port: 7777},
		Embeddings: EmbeddingsConfig{Provider: "ollama", Model: "nomic-embed-text", Dim: 768, BatchSize: 32},
		Models:     ModelsConfig{Classify: "claude-haiku-4-5", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8"},
		Limits:     LimitsConfig{DailyTokens: 1_500_000, WeeklyTokens: 8_000_000, GuardPauseAtPct: 80},
		Retrieval:  RetrievalConfig{TopK: 8, MaxContextTokens: 12_000},
		Policy:     PolicyConfig{DataResidency: "local-only"},
	}
}

func TestEmbeddingsHelperField(t *testing.T) {
	raw := []byte(`
version: 1
project_name: axon
active_profile: p
profiles:
  p:
    vault_path: "/tmp/v"
    data_dir: "/tmp/d"
    claude: {auth_mode: subscription}
    dashboard: {host: "127.0.0.1", port: 7777}
    embeddings: {provider: apple, model: apple-nlcontextual-v1, dim: 512, batch_size: 16, helper: "/opt/helper"}
    models: {classify: c, routine: r, synthesis: s}
    limits: {daily_tokens: 1, weekly_tokens: 1}
    retrieval: {top_k: 4, max_context_tokens: 1000}
    policy: {data_residency: local-only}
`)
	cfg, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := cfg.ResolveProfile("")
	if err != nil {
		t.Fatal(err)
	}
	if p.Embeddings.Helper != "/opt/helper" {
		t.Errorf("helper = %q, want /opt/helper", p.Embeddings.Helper)
	}
}

func TestDefaultAppleHelperPath(t *testing.T) {
	got := DefaultAppleHelperPath()
	if !strings.HasSuffix(got, filepath.Join("bin", "axon-apple-embed")) {
		t.Errorf("unexpected helper path %q", got)
	}
}

func TestFlexIntUnmarshal(t *testing.T) {
	tests := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"1_500_000", 1_500_000, false},
		{"50000", 50_000, false},
		{"0", 0, false},
		{`"80_000"`, 80_000, false},
		{"~", 0, false},
		{"null", 0, false},
		{"not-a-number", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var f FlexInt
			err := f.UnmarshalYAML([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("UnmarshalYAML(%q) err = %v, wantErr = %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && f.Int() != tt.want {
				t.Errorf("UnmarshalYAML(%q) = %d, want %d", tt.in, f.Int(), tt.want)
			}
		})
	}
}

func TestResolveProfile(t *testing.T) {
	cfg := &Config{
		ActiveProfile: "personal",
		Profiles: map[string]Profile{
			"personal": validProfile(),
			"work":     validProfile(),
		},
	}

	t.Run("flag wins over env and config", func(t *testing.T) {
		t.Setenv("AXON_PROFILE", "work")
		name, _, err := cfg.ResolveProfile("personal")
		if err != nil || name != "personal" {
			t.Fatalf("got (%q, %v), want personal", name, err)
		}
	})

	t.Run("env wins over config", func(t *testing.T) {
		t.Setenv("AXON_PROFILE", "work")
		name := cfg.ResolveProfileName("")
		if name != "work" {
			t.Errorf("got %q, want work", name)
		}
	})

	t.Run("config default", func(t *testing.T) {
		os.Unsetenv("AXON_PROFILE")
		name := cfg.ResolveProfileName("")
		if name != "personal" {
			t.Errorf("got %q, want personal", name)
		}
	})

	t.Run("unknown profile errors", func(t *testing.T) {
		_, _, err := cfg.ResolveProfile("ghost")
		if err == nil {
			t.Error("expected error for unknown profile")
		}
	})
}
