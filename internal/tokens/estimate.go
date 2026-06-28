// Package tokens is AXON's mandatory token & context chokepoint (Component 07,
// ADR-007): no code path reaches Claude except through Manager.Run, which
// pre-flights an estimate, checks budgets, executes via the agent adapter,
// records usage to the token_ledger, updates the day/week windows and emits a
// dashboard event. "Token-aware, not wasting tokens" is structural here, not a
// matter of good intentions.
package tokens

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// Estimator produces a pre-flight input-token estimate. On subscription/
// enterprise there is no count_tokens endpoint, so the default is a local
// heuristic; an api_key adapter can supply an exact counter behind this seam.
type Estimator interface {
	Estimate(text string) int
}

// HeuristicEstimator approximates ~4 characters per token. The estimate only
// needs to bound context and guard against rate-limit / credit burn, not be
// exact (docs/07 §2).
type HeuristicEstimator struct{}

// Estimate returns the approximate token count of text.
func (HeuristicEstimator) Estimate(text string) int {
	n := len([]rune(text))
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// cachingEstimator memoises estimates by content hash, so repeated pre-flights
// of the same prompt (e.g. retries) don't re-scan it.
type cachingEstimator struct {
	inner Estimator
	mu    sync.Mutex
	cache map[string]int
}

func newCachingEstimator(inner Estimator) *cachingEstimator {
	return &cachingEstimator{inner: inner, cache: make(map[string]int)}
}

func (c *cachingEstimator) Estimate(text string) int {
	key := hashText(text)
	c.mu.Lock()
	if v, ok := c.cache[key]; ok {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()

	v := c.inner.Estimate(text)

	c.mu.Lock()
	c.cache[key] = v
	c.mu.Unlock()
	return v
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
