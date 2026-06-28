package embeddings

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaEmbed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/embed" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		var req ollamaEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		// Return a dim-3 vector per input.
		out := ollamaEmbedResponse{}
		for range req.Input {
			out.Embeddings = append(out.Embeddings, []float32{0.1, 0.2, 0.3})
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "nomic-embed-text", 3)
	vecs, err := o.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 {
		t.Fatalf("got %d vectors of dim %d", len(vecs), len(vecs[0]))
	}
	if err := o.Healthcheck(context.Background()); err != nil {
		t.Errorf("healthcheck: %v", err)
	}
}

func TestOllamaDimMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(ollamaEmbedResponse{Embeddings: [][]float32{{0.1, 0.2}}})
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "model", 768) // expect 768, server returns 2
	if _, err := o.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("expected a dimension-mismatch error")
	}
}

func TestOllamaServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()

	o := NewOllama(srv.URL, "missing", 3)
	if _, err := o.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("expected an error on non-200 response")
	}
}
