package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newTestAPIKey points the adapter at a mock Anthropic endpoint so the exact
// request/response mapping (the api_key-mode budgeting path) is exercised
// without network or spend.
func newTestAPIKey(srvURL string) *APIKey {
	return &APIKey{
		client:    anthropic.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(srvURL)),
		maxTokens: apiKeyMaxTokens,
	}
}

func TestAPIKeyRunMapsUsageAndText(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1", "type": "message", "role": "assistant",
			"model": "claude-test-1",
			"content": [{"type": "text", "text": "hello "}, {"type": "text", "text": "world"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 42, "output_tokens": 7,
			          "cache_read_input_tokens": 3, "cache_creation_input_tokens": 2}
		}`))
	}))
	defer srv.Close()

	a := newTestAPIKey(srv.URL)
	res, err := a.Run(context.Background(), Request{
		Operation: "test", Model: "claude-test-1", System: "sys", Prompt: "hi",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/messages") {
		t.Errorf("path = %q, want .../messages", gotPath)
	}
	if gotBody["system"] == nil {
		t.Error("system prompt not sent")
	}
	if res.Text != "hello world" {
		t.Errorf("text = %q (must concatenate text blocks)", res.Text)
	}
	if res.Model != "claude-test-1" {
		t.Errorf("model = %q", res.Model)
	}
	u := res.Usage
	if u.InputTokens != 42 || u.OutputTokens != 7 || u.CacheRead != 3 || u.CacheWrite != 2 {
		t.Errorf("usage mapped wrong: %+v", u)
	}
}

func TestAPIKeyRunSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()

	a := newTestAPIKey(srv.URL)
	_, err := a.Run(context.Background(), Request{Operation: "test", Model: "m", Prompt: "p"})
	if err == nil {
		t.Fatal("expected an authentication error")
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("error should name the operation: %v", err)
	}
}

func TestAPIKeyCountTokensExact(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/messages/count_tokens") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 1234}`))
	}))
	defer srv.Close()

	a := newTestAPIKey(srv.URL)
	n, err := a.CountTokens(context.Background(), "claude-test-1", "sys", "prompt")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1234 {
		t.Errorf("count = %d, want 1234", n)
	}
}

func TestAPIKeyAuthMode(t *testing.T) {
	if got := NewAPIKey("k").AuthMode(); got != "api_key" {
		t.Errorf("AuthMode = %q, want api_key", got)
	}
}
