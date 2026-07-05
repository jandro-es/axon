package rerank

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

func TestParseScore(t *testing.T) {
	cases := map[string]float64{"7": 7, "7/10": 7, "score: 8": 8, "": 0, "off the charts": 0, "12": 10, "-3": 0, "4.5": 4.5}
	for in, want := range cases {
		if got := parseScore(in); got != want {
			t.Errorf("parseScore(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestOllamaRerankOrdersByScore(t *testing.T) {
	// Candidate 1 ("relevant") scores 9, candidate 0 scores 2 → order [1,0].
	r := NewOllamaReranker("http://x", "m")
	r.post = func(ctx context.Context, url string, body []byte) (int, []byte, error) {
		// NB: the scoring prompt template itself contains the word "relevant",
		// so key off the candidate's unique phrase, not a template word.
		if contains(body, "relevant passage") {
			return http.StatusOK, []byte(`{"response":"9"}`), nil
		}
		return http.StatusOK, []byte(`{"response":"2"}`), nil
	}
	order, err := r.Rerank(context.Background(), "q", []Candidate{{Text: "noise", Score: 0.5}, {Text: "relevant passage", Score: 0.1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("order = %v, want [1 0]", order)
	}
}

func TestOllamaRerankAllErrorsFallsBack(t *testing.T) {
	r := NewOllamaReranker("http://x", "m")
	r.post = func(ctx context.Context, url string, body []byte) (int, []byte, error) {
		return 0, nil, errors.New("connection refused")
	}
	if _, err := r.Rerank(context.Background(), "q", []Candidate{{Text: "a"}, {Text: "b"}}); err == nil {
		t.Fatal("all-errored rerank should return an error so the caller falls back")
	}
}

func TestRerankerFor(t *testing.T) {
	if r, err := RerankerFor("off", "h"); r != nil || err != nil {
		t.Errorf("off → nil,nil; got %v,%v", r, err)
	}
	if r, err := RerankerFor("", "h"); r != nil || err != nil {
		t.Errorf("empty → nil,nil; got %v,%v", r, err)
	}
	r, err := RerankerFor("ollama:qwen2.5", "h")
	if err != nil || r == nil || r.Name() != "ollama:qwen2.5" {
		t.Errorf("ollama → reranker; got %v,%v", r, err)
	}
	if _, err := RerankerFor("cohere:rerank-3", "h"); err == nil {
		t.Error("unknown provider should error")
	}
}

func TestFakeReranker(t *testing.T) {
	f := &Fake{Order: []int{2, 0, 1}}
	got, _ := f.Rerank(context.Background(), "q", []Candidate{{}, {}, {}})
	if len(got) != 3 || got[0] != 2 {
		t.Fatalf("fake order = %v", got)
	}
}

func contains(b []byte, sub string) bool {
	return len(sub) == 0 || (len(b) >= len(sub) && indexOf(string(b), sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
