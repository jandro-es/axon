package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultOllamaHost is used when the reranker host is blank.
const DefaultOllamaHost = "http://localhost:11434"

// OllamaReranker scores each candidate pointwise via Ollama /api/generate. It is
// safe for concurrent use; each Rerank runs a bounded worker pool.
type OllamaReranker struct {
	host        string
	model       string
	timeout     time.Duration
	concurrency int
	post        func(ctx context.Context, url string, body []byte) (status int, resp []byte, err error)
}

// NewOllamaReranker constructs the reranker for a host + model.
func NewOllamaReranker(host, model string) *OllamaReranker {
	if host == "" {
		host = DefaultOllamaHost
	}
	r := &OllamaReranker{
		host:        strings.TrimRight(host, "/"),
		model:       model,
		timeout:     30 * time.Second,
		concurrency: 4,
	}
	r.post = r.httpPost
	return r
}

func (r *OllamaReranker) Name() string { return "ollama:" + r.model }

type ollamaGenerateRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
	Error    string `json:"error"`
}

// Rerank scores every candidate and returns indices best-first. If EVERY call
// errors (Ollama unreachable) it returns an error so the caller falls back to
// the original order; per-candidate errors/garbage score 0 and are tie-broken
// by the original fused score (so an all-zero round preserves fused order).
func (r *OllamaReranker) Rerank(ctx context.Context, query string, cands []Candidate) ([]int, error) {
	n := len(cands)
	if n == 0 {
		return nil, nil
	}
	scores := make([]float64, n)
	errs := make([]error, n)
	conc := r.concurrency
	if conc < 1 {
		conc = 1
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	for i := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			scores[i], errs[i] = r.score(ctx, query, cands[i].Text)
		}(i)
	}
	wg.Wait()

	allErr := true
	var firstErr error
	for _, e := range errs {
		if e == nil {
			allErr = false
		} else if firstErr == nil {
			firstErr = e
		}
	}
	if allErr {
		return nil, fmt.Errorf("ollama rerank: all %d candidate calls failed: %w", n, firstErr)
	}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if scores[ia] != scores[ib] {
			return scores[ia] > scores[ib]
		}
		return cands[ia].Score > cands[ib].Score
	})
	return order, nil
}

func (r *OllamaReranker) score(ctx context.Context, query, passage string) (float64, error) {
	cctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	prompt := "Query: " + query + "\nPassage: " + passage +
		"\nOn a scale of 0-10, how relevant is the passage to the query? Answer with only a number.\n"
	body, err := json.Marshal(ollamaGenerateRequest{Model: r.model, Prompt: prompt, Stream: false})
	if err != nil {
		return 0, err
	}
	status, raw, err := r.post(cctx, r.host+"/api/generate", body)
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("ollama generate: status %d: %s", status, strings.TrimSpace(string(raw)))
	}
	var out ollamaGenerateResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, fmt.Errorf("ollama generate: decode: %w", err)
	}
	if out.Error != "" {
		return 0, fmt.Errorf("ollama generate: %s", out.Error)
	}
	return parseScore(out.Response), nil
}

var scoreRe = regexp.MustCompile(`-?\d+(\.\d+)?`)

// parseScore extracts the first number from a model reply and clamps to 0..10.
// Unparseable output scores 0.
func parseScore(s string) float64 {
	m := scoreRe.FindString(s)
	if m == "" {
		return 0
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil {
		return 0
	}
	if v < 0 {
		v = 0
	}
	if v > 10 {
		v = 10
	}
	return v
}

func (r *OllamaReranker) httpPost(ctx context.Context, url string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, raw, nil
}

var _ Reranker = (*OllamaReranker)(nil)
