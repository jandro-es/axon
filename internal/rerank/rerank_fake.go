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
