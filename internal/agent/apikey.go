package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// apiKeyMaxTokens caps a single direct-API response. AXON's calls are short
// (classification, summaries, distillation), so a modest ceiling is plenty.
const apiKeyMaxTokens = 4096

// APIKey is the direct Anthropic API adapter, used ONLY in auth_mode: api_key
// (ADR-008). It is the single sanctioned path that does not go through Claude
// Code, in exchange for exact token counting (CountTokens) and per-token cost.
// It still sits behind the token-manager chokepoint like every other call.
type APIKey struct {
	client    anthropic.Client
	maxTokens int64
}

// NewAPIKey builds the adapter from an Anthropic API key (from ANTHROPIC_API_KEY
// or a config secret ref). An empty key is allowed at construction; the first
// call then fails clearly rather than panicking.
func NewAPIKey(apiKey string) *APIKey {
	return &APIKey{
		client:    anthropic.NewClient(option.WithAPIKey(apiKey)),
		maxTokens: apiKeyMaxTokens,
	}
}

// AuthMode reports api_key so the token manager enables exact counting + cost.
func (a *APIKey) AuthMode() string { return "api_key" }

// Run executes a single Messages API turn and returns the text + exact usage.
func (a *APIKey) Run(ctx context.Context, req Request) (*Response, error) {
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: a.maxTokens,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(req.Prompt))},
	}
	if strings.TrimSpace(req.System) != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}
	msg, err := a.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("anthropic messages (%s): %w", req.Operation, err)
	}
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return &Response{
		Text:  sb.String(),
		Model: string(msg.Model),
		Usage: Usage{
			InputTokens:  int(msg.Usage.InputTokens),
			OutputTokens: int(msg.Usage.OutputTokens),
			CacheRead:    int(msg.Usage.CacheReadInputTokens),
			CacheWrite:   int(msg.Usage.CacheCreationInputTokens),
		},
	}, nil
}

// CountTokens returns the EXACT input-token count for a model+system+prompt via
// the API's count_tokens endpoint (FR-40, api_key mode only). The token manager
// uses it for an exact pre-flight estimate, falling back to the heuristic if the
// call fails.
func (a *APIKey) CountTokens(ctx context.Context, model, system, prompt string) (int, error) {
	params := anthropic.MessageCountTokensParams{
		Model:    anthropic.Model(model),
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(prompt))},
	}
	if strings.TrimSpace(system) != "" {
		params.System = anthropic.MessageCountTokensParamsSystemUnion{OfString: anthropic.String(system)}
	}
	res, err := a.client.Messages.CountTokens(ctx, params)
	if err != nil {
		return 0, err
	}
	return int(res.InputTokens), nil
}

// compile-time assertion that *APIKey satisfies Agent.
var _ Agent = (*APIKey)(nil)
