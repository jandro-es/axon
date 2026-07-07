package automations

import (
	"context"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
	"github.com/jandro-es/axon/internal/config"
	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/tokens"
)

func driftProfile() config.Profile {
	return config.Profile{Models: config.ModelsConfig{
		Classify: "ollama:qwen", Routine: "claude-sonnet-4-6", Synthesis: "claude-opus-4-8",
		EvalMinPass: 80,
	}}
}

func TestEvalDriftRegistered(t *testing.T) {
	if _, ok := Registry(config.Profile{})["eval-drift"]; !ok {
		t.Fatal("eval-drift must be registered")
	}
}

func TestEvalDriftDetectChange(t *testing.T) {
	ctx := context.Background()
	a := EvalDrift{digestFn: func(context.Context, string, string) (string, bool) { return "d1", true }}

	// gate disabled → never changed
	off := driftProfile()
	off.Models.EvalMinPass = 0
	if c, _ := a.DetectChange(ctx, RunCtx{Config: off}); c.Changed {
		t.Fatal("ungated must not report change")
	}
	// same digest as last cursor → no change
	if c, _ := a.DetectChange(ctx, RunCtx{Config: driftProfile(), LastCursor: "classify=d1;"}); c.Changed {
		t.Fatalf("unchanged digest must not report change (cursor %q)", c.Cursor)
	}
	// new digest → change
	c, _ := a.DetectChange(ctx, RunCtx{Config: driftProfile(), LastCursor: ""})
	if !c.Changed || c.Cursor != "classify=d1;" {
		t.Fatalf("digest change expected, got %+v", c)
	}
}

func TestEvalDriftRunRecordsRowOnDrift(t *testing.T) {
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	prof := driftProfile()
	ollama := agent.NewFake()
	ollama.Reply = "irrelevant"
	mgr := tokens.NewWithRouter(d, agent.Router{Claude: agent.NewFake(), Ollama: ollama}, nil, nil,
		tokens.Config{Profile: "test", Models: prof.Models})

	a := EvalDrift{digestFn: func(context.Context, string, string) (string, bool) { return "newdig", true }}
	if _, err := a.Run(ctx, RunCtx{Config: prof, DB: d, Manager: mgr}); err != nil {
		t.Fatal(err)
	}
	row, ok, err := db.LatestEvalRun(ctx, d, "classify", "ollama:qwen")
	if err != nil || !ok {
		t.Fatalf("expected a classify row: ok=%v err=%v", ok, err)
	}
	if row.Digest != "newdig" {
		t.Fatalf("row digest = %q, want newdig", row.Digest)
	}
}

func TestEvalDriftRunNoWorkWhenUngated(t *testing.T) {
	ctx := context.Background()
	d, _ := db.Open(db.MemoryDSN)
	defer d.Close()
	_, _ = db.Migrate(d)
	prof := driftProfile()
	prof.Models.EvalMinPass = 0
	a := EvalDrift{digestFn: func(context.Context, string, string) (string, bool) { return "newdig", true }}
	if _, err := a.Run(ctx, RunCtx{Config: prof, DB: d, Manager: nil}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.LatestEvalRun(ctx, d, "classify", "ollama:qwen"); ok {
		t.Fatal("ungated Run must record nothing")
	}
}
