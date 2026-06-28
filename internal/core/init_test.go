package core

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

// initProfile builds a minimal valid config + profile rooted at temp dirs.
func initProfile(t *testing.T) (*config.Config, config.Profile) {
	t.Helper()
	root := t.TempDir()
	profile := config.Profile{
		VaultPath: filepath.Join(root, "vault"),
		DataDir:   filepath.Join(root, "data"),
		Claude: config.ClaudeConfig{
			AuthMode:  "subscription",
			ConfigDir: filepath.Join(root, "data", "claude"),
		},
		Dashboard:  config.DashboardConfig{Host: "127.0.0.1", Port: 7777},
		Embeddings: config.EmbeddingsConfig{Provider: "ollama", Model: "nomic-embed-text", Dim: 768, BatchSize: 32},
		Models:     config.ModelsConfig{Classify: "h", Routine: "s", Synthesis: "o"},
		Limits:     config.LimitsConfig{DailyTokens: 1000, WeeklyTokens: 5000, GuardPauseAtPct: 80},
		Retrieval:  config.RetrievalConfig{TopK: 8, MaxContextTokens: 12000},
		Policy:     config.PolicyConfig{DataResidency: "local-only"},
	}
	cfg := &config.Config{
		Version:       1,
		ProjectName:   "axon",
		ActiveProfile: "personal",
		Profiles:      map[string]config.Profile{"personal": profile},
	}
	return cfg, profile
}

// stubEmbedCheck avoids any network call during init tests.
func stubEmbedCheck(ctx context.Context, e config.EmbeddingsConfig) StepResult {
	return StepResult{"embeddings", StepWarn, "stubbed (no network in test)"}
}

func runInit(t *testing.T, cfg *config.Config, p config.Profile) InitReport {
	t.Helper()
	rep, err := Init(context.Background(), InitOptions{
		Config:              cfg,
		ProfileName:         "personal",
		Profile:             p,
		Out:                 io.Discard,
		CheckEmbeddingModel: stubEmbedCheck,
	})
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return rep
}

func TestInitConvergesThenIdempotent(t *testing.T) {
	cfg, p := initProfile(t)

	first := runInit(t, cfg, p)
	if !first.OK {
		t.Fatal("first init not OK")
	}
	if !first.Changed {
		t.Error("first init should report changes")
	}
	if first.Reindex.Notes == 0 {
		t.Error("first init indexed zero notes (scaffold READMEs/templates expected)")
	}

	// Data dir, DB and scaffold must now exist.
	paths := p.Paths()
	for _, must := range []string{paths.DataDir, paths.DBPath, filepath.Join(paths.VaultPath, "00-Inbox", "README.md")} {
		if _, err := os.Stat(must); err != nil {
			t.Errorf("expected %q after init: %v", must, err)
		}
	}

	second := runInit(t, cfg, p)
	if !second.OK {
		t.Fatal("second init not OK")
	}
	if second.Changed {
		t.Errorf("second init reported changes; want idempotent no-op. steps=%+v", second.Steps)
	}
}

func TestInitDoesNotClobberExistingVault(t *testing.T) {
	cfg, p := initProfile(t)

	// Pre-seed the vault with a user note where the scaffold would also write.
	inbox := filepath.Join(p.VaultPath, "00-Inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(inbox, "README.md")
	if err := os.WriteFile(readme, []byte("USER CONTENT"), 0o644); err != nil {
		t.Fatal(err)
	}
	userNote := filepath.Join(inbox, "idea.md")
	if err := os.WriteFile(userNote, []byte("---\ntitle: Idea\n---\nmine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := runInit(t, cfg, p)
	if !rep.OK {
		t.Fatal("init not OK over existing vault")
	}

	got, _ := os.ReadFile(readme)
	if string(got) != "USER CONTENT" {
		t.Errorf("existing README clobbered: %q", got)
	}
	got, _ = os.ReadFile(userNote)
	if string(got) != "---\ntitle: Idea\n---\nmine\n" {
		t.Errorf("user note clobbered: %q", got)
	}
}
