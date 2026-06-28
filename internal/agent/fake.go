package agent

import (
	"context"
	"sync"
)

// Fake is an in-memory Agent for tests and the Phase 0 skeleton. It records
// every request and returns a canned response, standing in for `claude -p` so
// the rest of the system can be exercised without spending tokens.
type Fake struct {
	mu        sync.Mutex
	Mode      string                           // reported by AuthMode; defaults to "subscription"
	Calls     []Request                        // every request received, in order
	Reply     string                           // static reply text (used if RespondFn is nil)
	RespondFn func(Request) (*Response, error) // optional per-call behaviour
	Err       error                            // if set (and RespondFn nil), Run returns this
}

// NewFake returns a Fake that echoes a fixed reply and reports subscription mode.
func NewFake() *Fake {
	return &Fake{Mode: "subscription", Reply: "ok"}
}

// Run records the request and returns the configured response.
func (f *Fake) Run(ctx context.Context, req Request) (*Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	f.Calls = append(f.Calls, req)
	respond := f.RespondFn
	reply, cannedErr := f.Reply, f.Err
	f.mu.Unlock()

	if respond != nil {
		return respond(req)
	}
	if cannedErr != nil {
		return nil, cannedErr
	}
	return &Response{
		Text:  reply,
		Model: req.Model,
		Usage: Usage{InputTokens: len(req.Prompt), OutputTokens: len(reply)},
	}, nil
}

// AuthMode reports the configured mode (defaulting to "subscription").
func (f *Fake) AuthMode() string {
	if f.Mode == "" {
		return "subscription"
	}
	return f.Mode
}

// CallCount returns how many times Run was invoked.
func (f *Fake) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Calls)
}

// compile-time assertion that Fake satisfies Agent.
var _ Agent = (*Fake)(nil)
