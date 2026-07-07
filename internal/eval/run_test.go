package eval

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/tokens"
)

// fakeCP is a scripted Chokepoint. It answers target calls (Operation
// "eval.target") from targetFn and judge calls ("eval.judge") from judgeFn.
type fakeCP struct {
	targetModel string
	targetFn    func(prompt string) (string, error)
	judgeFn     func() (string, error)
	calls       int
}

func (f *fakeCP) Run(_ context.Context, call tokens.AgentCall) (tokens.AgentResult, error) {
	f.calls++
	if call.Operation == "eval.judge" {
		txt, err := f.judgeFn()
		if err != nil {
			return tokens.AgentResult{}, err
		}
		return tokens.AgentResult{Text: txt, Model: "claude-judge"}, nil
	}
	prompt := ""
	if len(call.Messages) > 0 {
		prompt = call.Messages[len(call.Messages)-1].Content
	}
	txt, err := f.targetFn(prompt)
	if err != nil {
		return tokens.AgentResult{}, err
	}
	return tokens.AgentResult{Text: txt, Model: f.targetModel}, nil
}

func expectQwen(string) string { return "qwen" }

func TestRunClassifyPassAndFail(t *testing.T) {
	cases := []Case{
		{Name: "ok", Family: FamilyClassify, Grade: Grade{ExpectJSON: `{"kind":"article"}`}, Prompt: "p"},
		{Name: "bad", Family: FamilyClassify, Grade: Grade{ExpectText: "high"}, Prompt: "p"},
	}
	cp := &fakeCP{targetModel: "qwen", targetFn: func(string) (string, error) { return `{"kind":"article"}`, nil }}
	rep, err := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	if err != nil {
		t.Fatal(err)
	}
	fr := rep.Families[0]
	if fr.Total != 2 || fr.Passed != 1 || fr.Failed != 1 {
		t.Fatalf("totals: %+v", fr)
	}
}

func TestRunRoutineHybridJudge(t *testing.T) {
	cases := []Case{{
		Name: "sum", Family: FamilyRoutine, Prompt: "p",
		Grade: Grade{MustInclude: []string{"Alice"}, Rubric: "must mention Alice"},
	}}
	cp := &fakeCP{
		targetModel: "qwen",
		targetFn:    func(string) (string, error) { return "Alice shipped it", nil },
		judgeFn:     func() (string, error) { return `{"pass":true,"reason":"ok"}`, nil },
	}
	rep, err := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Families[0].Passed != 1 {
		t.Fatalf("hybrid pass expected: %+v", rep.Families[0])
	}
	if cp.calls != 2 { // one target + one judge
		t.Fatalf("want 2 chokepoint calls, got %d", cp.calls)
	}
}

func TestRunRoutineMustIncludeFailsBeforeJudge(t *testing.T) {
	cases := []Case{{
		Name: "sum", Family: FamilyRoutine, Prompt: "p",
		Grade: Grade{MustInclude: []string{"Bob"}, Rubric: "must mention Bob"},
	}}
	judged := false
	cp := &fakeCP{
		targetModel: "qwen",
		targetFn:    func(string) (string, error) { return "Alice only", nil },
		judgeFn:     func() (string, error) { judged = true; return `{"pass":true}`, nil },
	}
	rep, _ := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	if rep.Families[0].Failed != 1 {
		t.Fatalf("missing anchor must fail: %+v", rep.Families[0])
	}
	if judged {
		t.Fatal("judge must not run when must_include gate fails")
	}
}

func TestRunEscalationVisible(t *testing.T) {
	cases := []Case{{Name: "ok", Family: FamilyClassify, Prompt: "p",
		Grade: Grade{ExpectJSON: `{"kind":"article"}`}}}
	// Target returns the RIGHT answer but the WRONG model (fell forward to Claude).
	cp := &fakeCP{targetModel: "claude-opus", targetFn: func(string) (string, error) { return `{"kind":"article"}`, nil }}
	rep, _ := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	fr := rep.Families[0]
	if fr.Escalated != 1 || fr.Passed != 0 {
		t.Fatalf("escalated answer must not count as pass: %+v", fr)
	}
}

func TestRunTransportErrorFails(t *testing.T) {
	cases := []Case{{Name: "ok", Family: FamilyClassify, Prompt: "p",
		Grade: Grade{ExpectText: "high"}}}
	cp := &fakeCP{targetModel: "qwen", targetFn: func(string) (string, error) { return "", errors.New("connection refused") }}
	rep, _ := Run(context.Background(), cp, cases, Options{Model: "ollama:qwen", ExpectModel: expectQwen})
	fr := rep.Families[0]
	if fr.Failed != 1 || !strings.Contains(fr.Cases[0].Verdict.Reason, "connection refused") {
		t.Fatalf("transport error must be a failed case with the error reason: %+v", fr)
	}
}

func TestMinPass(t *testing.T) {
	rep := Report{Families: []FamilyReport{{Total: 4, Passed: 3}}} // 75%
	if !rep.MinPass(75) || rep.MinPass(76) {
		t.Fatal("MinPass boundary wrong")
	}
}
