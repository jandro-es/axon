package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAppleFMRun(t *testing.T) {
	a := NewAppleFM("/fake/axon-apple-lm")
	a.goos = "darwin"
	var gotStdin []byte
	a.run = func(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
		gotStdin = stdin
		return []byte(`{"text":"{\"title\":\"T\"}"}`), nil, nil
	}

	resp, err := a.Run(context.Background(), Request{
		Model: "apple-foundation-v1", System: "sys", Prompt: "classify",
		OutputSchema: json.RawMessage(`{"properties":{"title":{"type":"string"}}}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"title":"T"}` {
		t.Errorf("Text = %q", resp.Text)
	}
	var req map[string]any
	_ = json.Unmarshal(gotStdin, &req)
	if req["prompt"] != "classify" || req["system"] != "sys" {
		t.Errorf("helper request = %v", req)
	}
	if req["schema"] == nil {
		t.Error("schema not forwarded to helper")
	}
	if a.AuthMode() != "local" {
		t.Errorf("AuthMode = %q", a.AuthMode())
	}
}

func TestAppleFMRunNonDarwin(t *testing.T) {
	a := NewAppleFM("/fake/helper")
	a.goos = "linux"
	if _, err := a.Run(context.Background(), Request{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "macOS") {
		t.Fatalf("err = %v, want macOS-only error", err)
	}
}

func TestAppleFMRunHelperFailure(t *testing.T) {
	a := NewAppleFM("/fake/helper")
	a.goos = "darwin"
	a.run = func(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
		return nil, []byte("input exceeds the on-device context window"), errors.New("exit status 4")
	}
	if _, err := a.Run(context.Background(), Request{Prompt: "x"}); err == nil ||
		!strings.Contains(err.Error(), "context window") {
		t.Fatalf("err = %v, want stderr surfaced", err)
	}
}

func TestAppleFMCheckAvailability(t *testing.T) {
	a := NewAppleFM("/fake/helper")
	a.goos = "darwin"
	var gotArgs []string
	a.run = func(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, []byte, error) {
		gotArgs = args
		return []byte("available\n"), nil, nil
	}
	if err := a.CheckAvailability(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(gotArgs) != 1 || gotArgs[0] != "--check-availability" {
		t.Fatalf("args = %v, want [--check-availability]", gotArgs)
	}
}
