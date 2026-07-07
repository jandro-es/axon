package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/eval"
	"github.com/jandro-es/axon/internal/tokens"
)

func TestPersistEvalRunsWritesRows(t *testing.T) {
	ctx := context.Background()
	sqlDB, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if _, err := db.Migrate(sqlDB); err != nil {
		t.Fatal(err)
	}
	rep := eval.Report{Families: []eval.FamilyReport{
		{Family: eval.FamilyClassify, Model: "ollama:qwen", Total: 4, Passed: 3},
	}}
	if err := persistEvalRuns(ctx, sqlDB, rep, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.LatestEvalRun(ctx, sqlDB, "classify", "ollama:qwen")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.PassPct != 75 {
		t.Fatalf("pass_pct = %d, want 75", got.PassPct)
	}
}

// stubCP is a Chokepoint that answers target calls with a fixed classify answer
// and judge calls with pass:true — enough to exercise scorecard/JSON plumbing.
type stubCP struct{ text, model string }

func (s stubCP) Run(_ context.Context, call tokens.AgentCall) (tokens.AgentResult, error) {
	if call.Operation == "eval.judge" {
		return tokens.AgentResult{Text: `{"pass":true,"reason":"ok"}`, Model: "claude"}, nil
	}
	return tokens.AgentResult{Text: s.text, Model: s.model}, nil
}

func TestRunEvalScorecardAndJSON(t *testing.T) {
	cases, err := eval.LoadCases("classify")
	if err != nil {
		t.Fatal(err)
	}
	cp := stubCP{text: `{"kind":"article"}`, model: "qwen"}
	rep, err := eval.Run(context.Background(), cp, cases, eval.Options{
		Model: "ollama:qwen", ExpectModel: func(string) string { return "qwen" },
	})
	if err != nil {
		t.Fatal(err)
	}

	var text bytes.Buffer
	writeScorecard(&text, rep)
	if !strings.Contains(text.String(), "classify") {
		t.Fatalf("scorecard missing family header:\n%s", text.String())
	}

	var jsonOut bytes.Buffer
	if err := writeJSON(&jsonOut, rep); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"Family"`) {
		t.Fatalf("json missing Family field:\n%s", jsonOut.String())
	}
}
