// Package agent is the seam to Claude. The production adapter shells out to the
// Claude Code CLI (`claude -p`) on the user's subscription/enterprise login; an
// optional direct-API adapter exists only for auth_mode: api_key. Phase 0
// defines the contract and a fake; the real adapters arrive in Phase 4.
//
// Cardinal rule: no code reaches Claude except through this package, and every
// call is mediated by the token manager (Component 07). The interface is kept
// deliberately small so a local-model adapter can satisfy it later.
package agent

import (
	"context"
	"encoding/json"
)

// Usage is the post-hoc accounting reported for a single Claude call. On
// subscription/enterprise these are token counts (cost is left to api_key
// mode). It feeds the token_ledger.
type Usage struct {
	InputTokens  int
	OutputTokens int
	CacheRead    int
	CacheWrite   int
}

// Request is one unit of work sent to a model. Operation labels the call site
// (e.g. "ingest.enrich", "automation.daily-log") for ledgering. Model is the
// resolved model string (passed to `claude -p --model`, or the Ollama model
// tag, or the Apple model identifier).
type Request struct {
	Operation string
	Model     string
	System    string
	Prompt    string
	// JSONOutput hints JSON mode to providers that support it (Ollama
	// format:"json"). Claude adapters ignore it.
	JSONOutput bool
	// OutputSchema optionally constrains output via guided generation
	// (Apple Foundation Models). nil = plain text. Raw JSON Schema.
	OutputSchema json.RawMessage
}

// Response is the result of a Claude call plus the usage to be ledgered.
type Response struct {
	Text  string
	Model string
	Usage Usage
}

// Agent runs a single Claude turn. Implementations must be safe for concurrent
// use; callers always pass a context for cancellation and timeouts.
type Agent interface {
	// Run executes the request and returns the response or an error.
	Run(ctx context.Context, req Request) (*Response, error)
	// AuthMode reports the adapter's auth mode (subscription|enterprise|api_key)
	// so the token manager knows whether exact counting/cost is available.
	AuthMode() string
}
