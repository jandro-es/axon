package tokens

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/agent"
)

// verifyConfig routes routine locally with verification on (gate off so the
// local tier serves without an eval row).
func verifyConfig() Config {
	c := localTestConfig()
	c.Models.Routine = "ollama:qwen"
	c.Models.Verify = "ollama:judge"
	return c
}

// localAnswerThenScore scripts the ollama fake: the ":verify" call returns
// score, every other call returns answer.
func localAnswerThenScore(answer, score string) *agent.Fake {
	f := agent.NewFake()
	f.RespondFn = func(r agent.Request) (*agent.Response, error) {
		if strings.HasSuffix(r.Operation, ":verify") {
			return &agent.Response{Text: score, Model: r.Model}, nil
		}
		return &agent.Response{Text: answer, Model: r.Model}, nil
	}
	return f
}

func hasLedgerOp(rows [][2]string, op string) bool {
	for _, r := range rows {
		if r[0] == op {
			return true
		}
	}
	return false
}

func TestVerifyKeepsLocalWhenJudgePasses(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := localAnswerThenScore("local-answer", "9")
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "local-answer" {
		t.Fatalf("passing judge should keep local, got %q", res.Text)
	}
	if claude.CallCount() != 0 {
		t.Fatalf("claude must not be called on a pass, got %d", claude.CallCount())
	}
	rows := ledgerRows(t, m.db)
	if !hasLedgerOp(rows, "op") || !hasLedgerOp(rows, "op:verify") {
		t.Fatalf("ledger missing op/op:verify rows: %v", rows)
	}
}

func TestVerifyEscalatesWhenJudgeFails(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := localAnswerThenScore("local-answer", "2")
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "from-claude" {
		t.Fatalf("failing judge should escalate to Claude, got %q", res.Text)
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude should be called once, got %d", claude.CallCount())
	}
	rows := ledgerRows(t, m.db)
	if !hasLedgerOp(rows, "op:verify") {
		t.Fatalf("ledger missing op:verify row: %v", rows)
	}
}

func TestVerifyInconclusiveKeepsLocal(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Reply = "from-claude"
	local := agent.NewFake()
	local.RespondFn = func(r agent.Request) (*agent.Response, error) {
		if strings.HasSuffix(r.Operation, ":verify") {
			return nil, errors.New("judge down")
		}
		return &agent.Response{Text: "local-answer", Model: r.Model}, nil
	}
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, _ := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if res.Text != "local-answer" {
		t.Fatalf("inconclusive judge should keep local, got %q", res.Text)
	}
	if claude.CallCount() != 0 {
		t.Fatalf("a broken judge must not spend Claude, got %d", claude.CallCount())
	}
}

func TestVerifyEscalationDegradesWhenClaudeErrors(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	claude.Err = errors.New("boom")
	local := localAnswerThenScore("local-answer", "1")
	m := testManagerRouter(t, verifyConfig(), agent.Router{Claude: claude, Ollama: local})

	res, err := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if err != nil {
		t.Fatalf("degrade should not error, got %v", err)
	}
	if res.Text != "local-answer" {
		t.Fatalf("failed escalation should degrade to local, got %q", res.Text)
	}
	if claude.CallCount() != 1 {
		t.Fatalf("claude should have been attempted once, got %d", claude.CallCount())
	}
}

func TestVerifyOffTakesNoJudgeCall(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	local := agent.NewFake()
	local.Reply = "local-answer"
	c := localTestConfig()
	c.Models.Routine = "ollama:qwen" // verify unset → off
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})

	res, _ := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "routine",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if res.Text != "local-answer" {
		t.Fatalf("verify off should return local, got %q", res.Text)
	}
	if local.CallCount() != 1 {
		t.Fatalf("verify off should make exactly one local call, got %d", local.CallCount())
	}
}

func TestVerifyDoesNotTouchClassify(t *testing.T) {
	ctx := context.Background()
	claude := agent.NewFake()
	local := agent.NewFake()
	local.Reply = "local-answer"
	c := verifyConfig() // classify defaults to ollama:qwen3:8b, verify on
	m := testManagerRouter(t, c, agent.Router{Claude: claude, Ollama: local})

	res, _ := m.Run(ctx, AgentCall{Operation: "op", ModelKey: "classify",
		Messages: []Message{{Role: "user", Content: "q"}}})
	if res.Text != "local-answer" {
		t.Fatalf("classify should return local, got %q", res.Text)
	}
	if local.CallCount() != 1 {
		t.Fatalf("classify must not be verified (scope), got %d local calls", local.CallCount())
	}
}
