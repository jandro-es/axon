package agent

import (
	"context"
	"errors"
	"testing"
)

func TestFakeRecordsCallsAndReplies(t *testing.T) {
	f := NewFake()
	f.Reply = "hello"

	resp, err := f.Run(context.Background(), Request{Operation: "test", Model: "m", Prompt: "hi"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.Text != "hello" {
		t.Errorf("Text = %q, want hello", resp.Text)
	}
	if resp.Model != "m" {
		t.Errorf("Model = %q, want m", resp.Model)
	}
	if f.CallCount() != 1 {
		t.Errorf("CallCount = %d, want 1", f.CallCount())
	}
	if f.Calls[0].Operation != "test" {
		t.Errorf("recorded operation = %q, want test", f.Calls[0].Operation)
	}
}

func TestFakeRespondFnOverrides(t *testing.T) {
	f := NewFake()
	f.RespondFn = func(r Request) (*Response, error) {
		return &Response{Text: "custom:" + r.Prompt, Usage: Usage{OutputTokens: 7}}, nil
	}
	resp, err := f.Run(context.Background(), Request{Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "custom:x" || resp.Usage.OutputTokens != 7 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestFakeReturnsErr(t *testing.T) {
	f := NewFake()
	f.Err = errors.New("boom")
	if _, err := f.Run(context.Background(), Request{}); err == nil {
		t.Error("expected error from fake")
	}
}

func TestFakeRespectsContext(t *testing.T) {
	f := NewFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Run(ctx, Request{}); err == nil {
		t.Error("expected context cancellation error")
	}
}

func TestFakeAuthModeDefault(t *testing.T) {
	if got := (&Fake{}).AuthMode(); got != "subscription" {
		t.Errorf("default AuthMode = %q, want subscription", got)
	}
}
