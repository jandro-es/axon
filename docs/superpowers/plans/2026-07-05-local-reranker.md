# Optional Local Reranker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional local reranker that fetches top-k×3 hybrid candidates, scores each against the query with a local Ollama model, and reorders to top-k — off by default, best-effort (any failure falls back to the fused order).

**Architecture:** A new `internal/rerank` leaf package (interface + Ollama pointwise scorer + fake), called by `internal/search` as a **retrieval primitive outside the token-manager chokepoint** (ADR-027, like embeddings). `Search` overfetches and reorders when a reranker is configured; every retrieval caller inherits it via `Search`/`Retrieve`. Selected by `retrieval.rerank: off | ollama:<model>`, wired at the composition root, reported by `axon doctor`.

**Tech Stack:** Go 1.26, stdlib `net/http` to Ollama `/api/generate`. No new Go dependency.

## Global Constraints

- **Chokepoint (rule 1):** the reranker makes **no Claude call** and never produces vault-/Claude-bound content — it emits an ordering of already-retrieved candidates. Per ADR-027 it is a retrieval primitive (like embeddings), outside the token manager, budget-exempt by construction.
- **Local-first (NFR-01):** scoring runs on the local Ollama server; no bytes leave the machine.
- **Best-effort:** any Ollama failure or parse error falls back to the original fused top-k; reranking never breaks search.
- **Toggleable, off by default; no new Go dependency, no cloud.**
- **Requirements:** FR-126 (rerank retrieval primitive), FR-127 (Ollama pointwise reranker + doctor). **ADR-027.**
- **Test runs must strip the ambient colour env:** prefix every `go test` with `env -u FORCE_COLOR`.
- Reference spec: `docs/superpowers/specs/2026-07-05-local-reranker-design.md`.

---

## File Structure

- `internal/rerank/rerank.go` — **create**: `Reranker` interface, `Candidate`, `RerankerFor`.
- `internal/rerank/rerank_ollama.go` — **create**: `OllamaReranker` (pointwise scorer).
- `internal/rerank/rerank_fake.go` — **create**: `Fake` (non-test, usable by search tests).
- `internal/rerank/rerank_test.go` — **create**.
- `internal/config/types.go` — **modify**: `RetrievalConfig.Rerank`/`RerankOverfetch` + accessors.
- `internal/config/rerank_test.go` — **create**.
- `internal/search/search.go` — **modify**: `Searcher.Reranker`/`Overfetch`, `WithReranker`, overfetch+reorder in `Search`, `reorder`/`clampHits` helpers.
- `internal/search/rerank_test.go` — **create**.
- `cmd/axon/deps.go` — **modify**: build + inject the reranker.
- `internal/core/doctor.go` — **modify**: `rerankCheck` + registration + imports.
- `internal/core/rerank_doctor_test.go` — **create**.
- `axon.config.example.yaml`, `internal/config/starter.go` — **modify**: document `retrieval.rerank`.
- `docs/02-architecture.md`, `docs/03-requirements.md`, `docs/05-component-knowledge-ingestion.md`, `docs/14-roadmap-1.1.md` — **modify**: ADR-027, FRs, docs.

---

## Task 1: `internal/rerank` package

**Files:**
- Create: `internal/rerank/rerank.go`, `internal/rerank/rerank_ollama.go`, `internal/rerank/rerank_fake.go`
- Test: `internal/rerank/rerank_test.go`

**Interfaces:**
- Produces: `type Candidate struct{ Text string; Score float64 }`; `type Reranker interface { Rerank(ctx, query, []Candidate) ([]int, error); Name() string }`; `func RerankerFor(rerank, host string) (Reranker, error)`; `func NewOllamaReranker(host, model string) *OllamaReranker`; `type Fake struct{ Order []int; Err error }`; `const DefaultOllamaHost`.

- [ ] **Step 1: Write the failing test**

Create `internal/rerank/rerank_test.go`:

```go
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
		if contains(body, "relevant") {
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/rerank/ 2>&1 | head`
Expected: FAIL — package does not compile (`parseScore`/`NewOllamaReranker`/`RerankerFor`/`Fake` undefined).

- [ ] **Step 3: Write the implementations**

Create `internal/rerank/rerank.go`:

```go
// Package rerank reorders retrieval candidates by relevance to a query using a
// local model. It is a retrieval primitive (ADR-027): local, non-Claude, and
// OUTSIDE the token-manager chokepoint — like embeddings, it produces an
// ordering, never vault- or Claude-bound content. Reranking is best-effort;
// callers fall back to the original order on error.
package rerank

import (
	"context"
	"fmt"
	"strings"
)

// Candidate is one passage to score, with its original fused score for
// tie-breaking.
type Candidate struct {
	Text  string
	Score float64
}

// Reranker reorders retrieval candidates by relevance to a query.
type Reranker interface {
	// Rerank returns candidate indices best-first. On failure it returns an
	// error and the caller keeps the original order.
	Rerank(ctx context.Context, query string, cands []Candidate) ([]int, error)
	// Name identifies the reranker for diagnostics.
	Name() string
}

// RerankerFor builds the configured reranker, or nil when off. host is the
// Ollama server (embeddings.host or the default). A malformed value errors so
// wiring can leave reranking off and doctor can surface it.
func RerankerFor(rerank, host string) (Reranker, error) {
	switch {
	case rerank == "" || rerank == "off":
		return nil, nil
	case strings.HasPrefix(rerank, "ollama:"):
		model := strings.TrimSpace(strings.TrimPrefix(rerank, "ollama:"))
		if model == "" {
			return nil, fmt.Errorf("retrieval.rerank: ollama: needs a model name")
		}
		return NewOllamaReranker(host, model), nil
	default:
		return nil, fmt.Errorf("retrieval.rerank: unknown provider %q (use off or ollama:<model>)", rerank)
	}
}
```

Create `internal/rerank/rerank_ollama.go`:

```go
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
```

Create `internal/rerank/rerank_fake.go`:

```go
package rerank

import "context"

// Fake is a deterministic reranker for tests (in the non-test build so other
// packages' tests can use it, mirroring embeddings.NewFake).
type Fake struct {
	Order []int // returned as-is when non-nil; otherwise the input is reversed
	Err   error // returned to exercise the caller's fallback path
}

func (f *Fake) Name() string { return "fake" }

func (f *Fake) Rerank(ctx context.Context, query string, cands []Candidate) ([]int, error) {
	if f.Err != nil {
		return nil, f.Err
	}
	if f.Order != nil {
		return f.Order, nil
	}
	n := len(cands)
	order := make([]int, n)
	for i := range order {
		order[i] = n - 1 - i
	}
	return order, nil
}

var _ Reranker = (*Fake)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/rerank/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/rerank/
git commit -m "feat(rerank): local reranker package — Ollama pointwise scorer + RerankerFor (FR-127, ADR-027)"
```

---

## Task 2: Config — `retrieval.rerank` field + accessors

**Files:**
- Modify: `internal/config/types.go`
- Test: `internal/config/rerank_test.go`

**Interfaces:**
- Produces: `RetrievalConfig.Rerank string`, `RetrievalConfig.RerankOverfetch int`, `func (RetrievalConfig) RerankMode() string`, `func (RetrievalConfig) RerankOverfetchOr() int`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/rerank_test.go`:

```go
package config

import "testing"

func TestRerankModeDefaultsOff(t *testing.T) {
	if got := (RetrievalConfig{}).RerankMode(); got != "off" {
		t.Errorf("empty RerankMode = %q, want off", got)
	}
	if got := (RetrievalConfig{Rerank: "ollama:qwen2.5"}).RerankMode(); got != "ollama:qwen2.5" {
		t.Errorf("RerankMode = %q", got)
	}
}

func TestRerankOverfetchOr(t *testing.T) {
	if got := (RetrievalConfig{}).RerankOverfetchOr(); got != 3 {
		t.Errorf("default overfetch = %d, want 3", got)
	}
	if got := (RetrievalConfig{RerankOverfetch: 5}).RerankOverfetchOr(); got != 5 {
		t.Errorf("overfetch = %d, want 5", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run 'TestRerank' -v`
Expected: FAIL — `RerankMode`/`RerankOverfetchOr` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/config/types.go`, add the fields to `RetrievalConfig` (after `ANN`):

```go
	// Rerank selects an optional local reranker applied to a wider candidate
	// pool: "" or "off" (default), or "ollama:<model>" (ADR-027). Best-effort —
	// any failure falls back to the fused order.
	Rerank string `yaml:"rerank,omitempty"`
	// RerankOverfetch is the candidate multiple fetched before reranking
	// (default 3; ignored when rerank is off).
	RerankOverfetch int `yaml:"rerank_overfetch,omitempty"`
```

and the accessors (near `IndexMode`):

```go
// RerankMode returns the configured reranker, defaulting to "off" when unset.
func (r RetrievalConfig) RerankMode() string {
	if r.Rerank == "" {
		return "off"
	}
	return r.Rerank
}

// RerankOverfetchOr returns the candidate multiple for reranking, default 3.
func (r RetrievalConfig) RerankOverfetchOr() int {
	if r.RerankOverfetch <= 0 {
		return 3
	}
	return r.RerankOverfetch
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/config/ -run 'TestRerank' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/types.go internal/config/rerank_test.go
git commit -m "feat(config): retrieval.rerank + rerank_overfetch fields + accessors (FR-126)"
```

---

## Task 3: Search integration — overfetch + reorder in `Search`

**Files:**
- Modify: `internal/search/search.go`
- Test: `internal/search/rerank_test.go`

**Interfaces:**
- Consumes: `rerank.Reranker`, `rerank.Candidate` (Task 1).
- Produces: `Searcher.Reranker rerank.Reranker`, `Searcher.Overfetch int`, `func (*Searcher) WithReranker(rerank.Reranker, int) *Searcher`, `reorder`, `clampHits`.

- [ ] **Step 1: Write the failing test**

Create `internal/search/rerank_test.go`:

```go
package search

import (
	"context"
	"errors"
	"testing"

	"github.com/jandro-es/axon/internal/db"
	"github.com/jandro-es/axon/internal/embeddings"
	"github.com/jandro-es/axon/internal/rerank"
)

func seedThree(t *testing.T) (*Searcher, context.Context) {
	t.Helper()
	ctx := context.Background()
	d, err := db.Open(db.MemoryDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := db.Migrate(d); err != nil {
		t.Fatal(err)
	}
	for i, txt := range []string{"alpha graph one", "beta graph two", "gamma graph three"} {
		nid, _ := db.UpsertNote(ctx, d, db.NoteRow{Path: string(rune('a'+i)) + ".md", Title: "N"})
		cid, _ := db.InsertChunk(ctx, d, db.ChunkRow{NoteID: &nid, Text: txt, ContentHash: "h" + txt})
		_ = db.InsertChunkFTS(ctx, d, cid, txt)
		_ = db.UpsertChunkVector(ctx, d, cid, "fake", []float32{1, 0, 0, 0})
	}
	return New(d, embeddings.NewFake()), ctx
}

func TestSearchReordersWithReranker(t *testing.T) {
	s, ctx := seedThree(t)
	baseline, err := s.Search(ctx, "graph", 3)
	if err != nil || len(baseline) != 3 {
		t.Fatalf("baseline hits=%d err=%v", len(baseline), err)
	}
	// A reranker that reverses order must flip the top result.
	s.WithReranker(&rerank.Fake{}, 3)
	reranked, err := s.Search(ctx, "graph", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(reranked) != 3 {
		t.Fatalf("reranked hits=%d", len(reranked))
	}
	if reranked[0].ChunkID != baseline[len(baseline)-1].ChunkID {
		t.Fatalf("reranker did not reorder: top=%d baseline-last=%d", reranked[0].ChunkID, baseline[len(baseline)-1].ChunkID)
	}
}

func TestSearchFallsBackWhenRerankerErrors(t *testing.T) {
	s, ctx := seedThree(t)
	baseline, _ := s.Search(ctx, "graph", 3)
	s.WithReranker(&rerank.Fake{Err: errors.New("ollama down")}, 3)
	got, err := s.Search(ctx, "graph", 3)
	if err != nil {
		t.Fatalf("rerank error must not fail search: %v", err)
	}
	if len(got) != len(baseline) || got[0].ChunkID != baseline[0].ChunkID {
		t.Fatalf("expected fallback to fused order; got top=%d want=%d", got[0].ChunkID, baseline[0].ChunkID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/search/ -run 'TestSearchReorders|TestSearchFallsBack' -v`
Expected: FAIL — `WithReranker` undefined.

- [ ] **Step 3: Write minimal implementation**

In `internal/search/search.go`, add the import `"github.com/jandro-es/axon/internal/rerank"`, add fields to `Searcher`:

```go
	// Reranker, when non-nil, reorders a wider candidate pool (retrieval
	// primitive, ADR-027). Overfetch is the candidate multiple (≤0 ⇒ 3).
	Reranker  rerank.Reranker
	Overfetch int
```

Add the setter (after `Configure`):

```go
// WithReranker attaches an optional local reranker and returns the receiver.
// overfetch is the candidate multiple fetched before reranking (≤0 ⇒ 3).
func (s *Searcher) WithReranker(r rerank.Reranker, overfetch int) *Searcher {
	s.Reranker = r
	s.Overfetch = overfetch
	return s
}
```

Replace `Search` with the overfetch+rerank version:

```go
// Search returns the top-k hybrid results for a free-text query. When a
// reranker is configured it overfetches top-k×overfetch candidates, reorders
// them locally, and returns the top-k; any reranker failure falls back to the
// fused order (best-effort, never breaks search).
func (s *Searcher) Search(ctx context.Context, query string, topK int) ([]db.ChunkHit, error) {
	var qv []float32
	if s.Embedder != nil && strings.TrimSpace(query) != "" {
		if vecs, err := s.Embedder.Embed(ctx, []string{query}); err == nil && len(vecs) == 1 {
			qv = vecs[0]
		}
	}
	fetch := topK
	if s.Reranker != nil {
		of := s.Overfetch
		if of <= 0 {
			of = 3
		}
		fetch = topK * of
	}
	hits, err := db.HybridSearch(ctx, s.DB, db.SearchOpts{Query: query, QueryVector: qv, TopK: fetch, Index: s.vindex()})
	if err != nil {
		return nil, err
	}
	if s.Reranker == nil || len(hits) <= 1 {
		return clampHits(hits, topK), nil
	}
	cands := make([]rerank.Candidate, len(hits))
	for i, h := range hits {
		cands[i] = rerank.Candidate{Text: h.Snippet, Score: h.Score}
	}
	order, rerr := s.Reranker.Rerank(ctx, query, cands)
	if rerr != nil {
		return clampHits(hits, topK), nil // best-effort fallback to fused order
	}
	return clampHits(reorder(hits, order), topK), nil
}

// reorder applies an index permutation defensively: valid unseen indices first,
// then any leftover hits in original order (robust to partial/garbage input).
func reorder(hits []db.ChunkHit, order []int) []db.ChunkHit {
	out := make([]db.ChunkHit, 0, len(hits))
	seen := make([]bool, len(hits))
	for _, idx := range order {
		if idx < 0 || idx >= len(hits) || seen[idx] {
			continue
		}
		seen[idx] = true
		out = append(out, hits[idx])
	}
	for i, h := range hits {
		if !seen[i] {
			out = append(out, h)
		}
	}
	return out
}

// clampHits caps the slice to topK (topK ≤ 0 ⇒ unchanged).
func clampHits(hits []db.ChunkHit, topK int) []db.ChunkHit {
	if topK > 0 && len(hits) > topK {
		return hits[:topK]
	}
	return hits
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `env -u FORCE_COLOR go test ./internal/search/ -v`
Expected: PASS (new rerank tests + the existing `TestSearcherSearch`).

- [ ] **Step 5: Commit**

```bash
git add internal/search/search.go internal/search/rerank_test.go
git commit -m "feat(search): overfetch + rerank + fused-order fallback in Search (FR-126)"
```

---

## Task 4: Wiring + doctor

**Files:**
- Modify: `cmd/axon/deps.go`, `internal/core/doctor.go`
- Test: `internal/core/rerank_doctor_test.go`

**Interfaces:**
- Consumes: `rerank.RerankerFor` (Task 1), `RetrievalConfig.RerankMode`/`RerankOverfetchOr` (Task 2), `search.WithReranker` (Task 3), in-package `ollamaReachable`/`ollamaModelPresent` (init.go).
- Produces: `func rerankCheck(p config.Profile) Check`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/rerank_doctor_test.go`:

```go
package core

import (
	"strings"
	"testing"

	"github.com/jandro-es/axon/internal/config"
)

func TestRerankCheckMalformed(t *testing.T) {
	p := config.Profile{Retrieval: config.RetrievalConfig{Rerank: "cohere:x"}}
	c := rerankCheck(p)
	if c.Status != StatusWarn || !strings.Contains(strings.ToLower(c.Detail), "off or ollama") {
		t.Fatalf("malformed rerank check = %+v", c)
	}
}

func TestRerankCheckUnreachableWarns(t *testing.T) {
	// Point at a host that is not listening → warn (never fails doctor).
	p := config.Profile{Retrieval: config.RetrievalConfig{Rerank: "ollama:qwen2.5"},
		Embeddings: config.EmbeddingsConfig{Host: "http://127.0.0.1:1"}}
	if c := rerankCheck(p); c.Status != StatusWarn {
		t.Fatalf("unreachable reranker should warn, got %v", c.Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestRerankCheck -v`
Expected: FAIL — `rerankCheck` undefined.

- [ ] **Step 3: Write the implementations**

In `internal/core/doctor.go`, add imports `"time"` and `"github.com/jandro-es/axon/internal/embeddings"` to the import block. Register the check next to `ocrCheck` (guarded):

```go
			if p.Retrieval.RerankMode() != "off" {
				checks = append(checks, rerankCheck(p))
			}
```

```go
// rerankCheck verifies the configured local reranker's prerequisite: a
// reachable Ollama server with the model pulled. Read-only and tolerant — a
// missing prerequisite or malformed value warns (rerank silently falls back to
// the fused order), never fails doctor.
func rerankCheck(p config.Profile) Check {
	const name = "rerank"
	mode := p.Retrieval.RerankMode()
	if !strings.HasPrefix(mode, "ollama:") {
		return Check{name, StatusWarn, fmt.Sprintf("retrieval.rerank %q not recognised — use off or ollama:<model>", mode)}
	}
	model := strings.TrimPrefix(mode, "ollama:")
	host := p.Embeddings.Host
	if host == "" {
		host = embeddings.DefaultOllamaHost
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if !ollamaReachable(ctx, host) {
		return Check{name, StatusWarn, fmt.Sprintf("reranker Ollama not reachable at %s — start `ollama serve` (rerank falls back to fused order)", host)}
	}
	if !ollamaModelPresent(ctx, host, model) {
		return Check{name, StatusWarn, fmt.Sprintf("reranker model %q not pulled — run `ollama pull %s`", model, model)}
	}
	return Check{name, StatusOK, "reranker ready: " + mode}
}
```

In `cmd/axon/deps.go` `buildServices`, build + inject the reranker (add import `"github.com/jandro-es/axon/internal/rerank"`):

```go
	rerankHost := d.profile.Embeddings.Host
	if rerankHost == "" {
		rerankHost = embeddings.DefaultOllamaHost
	}
	reranker, _ := rerank.RerankerFor(d.profile.Retrieval.Rerank, rerankHost) // off/misconfig → nil; doctor surfaces it
	searcher := search.New(d.db, d.embedder).Configure(d.profile.Retrieval).WithReranker(reranker, d.profile.Retrieval.RerankOverfetchOr())
```

(Replace the existing `searcher := search.New(...).Configure(...)` line at `cmd/axon/deps.go:159`.) `embeddings` is already imported in deps.go.

- [ ] **Step 4: Run test + build**

Run: `env -u FORCE_COLOR go test ./internal/core/ -run TestRerankCheck -v && env -u FORCE_COLOR go build ./...`
Expected: PASS + clean build.

- [ ] **Step 5: Commit**

```bash
git add cmd/axon/deps.go internal/core/doctor.go internal/core/rerank_doctor_test.go
git commit -m "feat(core): wire reranker into Searcher + axon doctor rerankCheck (FR-126/127)"
```

---

## Task 5: ADR-027, requirements, docs, config example

**Files:**
- Modify: `docs/02-architecture.md`, `docs/03-requirements.md`, `docs/05-component-knowledge-ingestion.md`, `docs/14-roadmap-1.1.md`, `axon.config.example.yaml`, `internal/config/starter.go`

**Interfaces:** none (documentation + config comments).

- [ ] **Step 1: Add ADR-027**

In `docs/02-architecture.md`, add above the ADR-026 heading (newest-first — search `ADR-026`):

```markdown
### ADR-027 — Local reranking as a retrieval primitive (outside the chokepoint) *(accepted — built)*

**Status:** Accepted (2026-07-05, roadmap 1.1 B2).

**Context:** Hybrid search's fused order is coarse. A reranker re-scores a wider
candidate pool to lift the genuinely relevant passages into the top-k. Ollama
has no rerank endpoint, so a local reranker is an LLM-as-scorer call — raising
the question of whether it must route through the token-manager chokepoint.

**Decision:** Reranking is a **retrieval primitive**, like embeddings: a local,
non-Claude scoring op over already-retrieved candidates that produces an
*ordering*, never vault- or Claude-bound content. It therefore lives in a leaf
package (`internal/rerank`) called by `search`, calls Ollama directly, sits
**outside** the token manager, and is budget-exempt by construction. This is a
narrow amendment to cardinal-rule-1/ADR-015: "no *generative content* call
bypasses the chokepoint" — a scoring call that only reorders retrieval results
is not such a call, exactly as embeddings are not. Selected by
`retrieval.rerank: off | ollama:<model>` (default off); pointwise 0–10 scoring
with bounded concurrency; **best-effort** — any failure falls back to the fused
order, so reranking never breaks search.

**Consequences:** Better top-k on opt-in, with no Claude spend and no new Go
dependency. `search` stays a leaf (no `search → tokens` coupling); ~top-k×3 local
calls per query are not ledgered (they are retrieval, not model traffic). A
future reranker provider (e.g. a compiled cross-encoder helper) can join behind
the same `Reranker` interface. (Spec: `docs/superpowers/specs/2026-07-05-local-reranker-design.md`; FR-126/127.)

```

- [ ] **Step 2: Add the FRs**

In `docs/03-requirements.md`, after FR-125 (search `FR-125`), add:

```markdown
| FR-126 | S | **Optional local reranker (roadmap 1.1 B2).** When `retrieval.rerank` is enabled, `search.Search` fetches `top_k × rerank_overfetch` (default 3) hybrid candidates, scores them against the query with a local reranker, reorders, and returns the top-k; any reranker failure falls back to the original fused top-k (best-effort, never breaks search). Reranking is a retrieval primitive outside the token-manager chokepoint (ADR-027), local-only, budget-exempt, default off; every retrieval caller inherits it via `Search`/`Retrieve`. Spec: `docs/superpowers/specs/2026-07-05-local-reranker-design.md`. |
| FR-127 | S | **Ollama pointwise reranker.** `retrieval.rerank: ollama:<model>` scores each candidate pointwise via Ollama `/api/generate` (bounded concurrency, per-call timeout), parsing a 0–10 relevance number; unreachable/garbage output degrades to the fused order. `axon doctor` reports Ollama/model availability and warns on a malformed `retrieval.rerank` value. All processing is local (ADR-027). |
```

- [ ] **Step 3: Update docs/05 and the roadmap**

In `docs/05-component-knowledge-ingestion.md`, in the `## 3. Retrieval` section (after the "Hybrid search" bullet, line ~41), add a bullet:

```markdown
- **Optional reranking** (ADR-027, FR-126/127): when `retrieval.rerank: ollama:<model>` is set, hybrid search overfetches `top_k × rerank_overfetch` candidates and a local Ollama model re-scores them pointwise (0–10) to reorder the top-k. It is a retrieval primitive outside the chokepoint (like embeddings), budget-exempt, off by default, and best-effort — any failure falls back to the fused order.
```

In `docs/14-roadmap-1.1.md`, mark **B2** built (search `### B2`): `### B2 — Optional local reranker (S) · FR-126/127, ADR-027 *(built)*`.

- [ ] **Step 4: Document the config key**

In `axon.config.example.yaml`, under each profile's `retrieval:` block, add:

```yaml
      rerank: off                             # off | ollama:<model> — optional local reranker (overfetch top_k×3, pointwise rescoring; best-effort, budget-exempt)
      # rerank_overfetch: 3                    # candidate multiple fetched before reranking
```

In `internal/config/starter.go` `starterTemplate`, add `rerank: off` under the `retrieval:` block (keep the generated config valid — it round-trips through `Parse`).

- [ ] **Step 5: Verify + commit**

Run: `grep -oE 'FR-12[67]' docs/03-requirements.md | sort | uniq -c` (each once); `grep -c 'ADR-027' docs/02-architecture.md` (≥1); `env -u FORCE_COLOR go test ./internal/config/` (starter validates).

```bash
git add docs/02-architecture.md docs/03-requirements.md docs/05-component-knowledge-ingestion.md docs/14-roadmap-1.1.md axon.config.example.yaml internal/config/starter.go
git commit -m "docs: ADR-027 + FR-126/127 reranker; document retrieval.rerank; mark roadmap B2 built"
```

---

## Final verification (after all tasks)

- [ ] **Full build + vet + tests**

Run: `env -u FORCE_COLOR go build ./... && env -u FORCE_COLOR go vet ./... && env -u FORCE_COLOR go test ./internal/rerank/ ./internal/config/ ./internal/search/ ./internal/core/ ./cmd/...`
Expected: build clean, vet clean, all PASS.

- [ ] **Lint**

Run: `golangci-lint run ./internal/rerank/... ./internal/search/... ./internal/config/... ./internal/core/... 2>&1 | tail`
Expected: `0 issues`. Fix any `gofmt` drift with `gofmt -w` and amend the relevant commit.

- [ ] **Live smoke (real Ollama)** — scratch `AXON_HOME`: pull a small chat model (`ollama pull qwen2.5:0.5b`), set `retrieval.rerank: ollama:qwen2.5:0.5b`, `axon doctor` shows the reranker OK; ingest a few notes; run `axon search <query>` with rerank off vs on and confirm the order changes (and that with Ollama stopped, search still returns results — fallback).

---

## Self-Review

**Spec coverage:**
- FR-126 (overfetch + rerank + fallback + config + inherited by all callers) → Task 2 (config) + Task 3 (`Search`) + Task 4 (wiring).
- FR-127 (Ollama pointwise scorer + doctor) → Task 1 (`OllamaReranker`) + Task 4 (`rerankCheck`).
- ADR-027 → Task 5.
- Chokepoint boundary → Task 1 package is a leaf (imports only stdlib + nothing from tokens); `search → rerank` only. Budget-exempt/off-by-default → Task 2 defaults off; Task 3 nil-reranker path unchanged. Best-effort → Task 1 all-errored→error + Task 3 fallback-on-error.

**Placeholder scan:** none — every code step is complete; Task 5 names exact search anchors (`ADR-026`, `FR-125`, `### B2`, docs/05 `## 3. Retrieval`).

**Type consistency:** `Reranker.Rerank(ctx, string, []Candidate) ([]int, error)` defined in Task 1, implemented by `OllamaReranker`/`Fake` (Task 1), consumed by `Searcher.Search` (Task 3) and `WithReranker` (Task 3). `RerankerFor(rerank, host) (Reranker, error)` (Task 1) called in Task 4 wiring. `RerankMode()`/`RerankOverfetchOr()` (Task 2) used in Task 3 default and Task 4 wiring/doctor. `rerank.Candidate{Text, Score}` fields match between Task 1 and Task 3.
