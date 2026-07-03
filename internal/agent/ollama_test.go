package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOllamaRun(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s, want /api/chat", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":             "qwen3:8b",
			"message":           map[string]string{"role": "assistant", "content": `{"label":"02-Areas"}`},
			"done":              true,
			"prompt_eval_count": 42,
			"eval_count":        7,
		})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL)
	resp, err := o.Run(context.Background(), Request{
		Operation: "test", Model: "qwen3:8b",
		System: "You classify.", Prompt: "classify this",
		JSONOutput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != `{"label":"02-Areas"}` {
		t.Errorf("Text = %q", resp.Text)
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 7 {
		t.Errorf("Usage = %+v, want 42/7", resp.Usage)
	}
	if gotBody["format"] != "json" {
		t.Errorf("format = %v, want json (JSONOutput hint)", gotBody["format"])
	}
	if gotBody["stream"] != false {
		t.Errorf("stream = %v, want false", gotBody["stream"])
	}
	msgs := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (system + user)", len(msgs))
	}
	if o.AuthMode() != "local" {
		t.Errorf("AuthMode = %q, want local", o.AuthMode())
	}
}

func TestOllamaRunServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model not found"}`))
	}))
	defer srv.Close()

	_, err := NewOllama(srv.URL).Run(context.Background(), Request{Model: "nope", Prompt: "x"})
	if err == nil || !strings.Contains(err.Error(), "model not found") {
		t.Fatalf("err = %v, want model-not-found", err)
	}
}

func TestOllamaHealthcheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if opts, ok := body["options"].(map[string]any); !ok || opts["num_predict"] != float64(1) {
			t.Errorf("options = %v, want num_predict 1", body["options"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "qwen3:8b",
			"message": map[string]string{"role": "assistant", "content": "ok"},
			"done":    true,
		})
	}))
	defer srv.Close()

	if err := NewOllama(srv.URL).Healthcheck(context.Background(), "qwen3:8b"); err != nil {
		t.Fatal(err)
	}
}
