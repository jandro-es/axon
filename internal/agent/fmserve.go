package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jandro-es/axon/internal/config"
)

// FMServe is the macOS 27 Apple Foundation Models adapter (ADR-038): tiers
// written as "apple:system" / "apple:pcc" are served by a daemon-supervised
// `fm serve --socket` child over its OpenAI-shaped Chat Completions endpoint.
// Dispatched only by the token manager's router — never called directly — so
// every call is ledgered (cardinal rule 1). Unlike the ADR-013 Swift helper,
// fm serve reports real token usage, which lands in the ledger unaltered.
type FMServe struct {
	sup        *FMSupervisor
	httpClient *http.Client
}

// NewFMServe constructs the adapter around a supervisor. The HTTP client
// dials the supervisor's unix socket regardless of the request URL host.
func NewFMServe(sup *FMSupervisor) *FMServe {
	return &FMServe{
		sup: sup,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", sup.Socket())
				},
			},
		},
	}
}

// AuthMode reports "local": no subscription, no API key, no cost.
func (f *FMServe) AuthMode() string { return "local" }

type fmChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type fmChatRequest struct {
	Model    string          `json:"model"`
	Messages []fmChatMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type fmChatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
			Refusal string `json:"refusal"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Run executes one chat turn against the supervised fm serve child. req.Model
// is the AXON ref ("apple:system"/"apple:pcc"); the endpoint receives the
// bare variant and the ledger keeps the AXON ref.
func (f *FMServe) Run(ctx context.Context, req Request) (*Response, error) {
	if err := f.sup.Ensure(ctx); err != nil {
		return nil, fmt.Errorf("fm serve: %w", err)
	}
	variant := config.ParseModelRef(req.Model).Model
	if variant == "" {
		variant = req.Model
	}
	msgs := make([]fmChatMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, fmChatMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, fmChatMessage{Role: "user", Content: req.Prompt})

	buf, err := json.Marshal(fmChatRequest{Model: variant, Messages: msgs, Stream: false})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://fm/v1/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := f.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fm serve request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fm serve: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out fmChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("fm serve: decode: %w", err)
	}
	if out.Error.Message != "" {
		return nil, fmt.Errorf("fm serve: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("fm serve: response carried no choices")
	}
	if r := out.Choices[0].Message.Refusal; r != "" {
		return nil, fmt.Errorf("fm serve: model refused: %s", r)
	}
	return &Response{
		Text: out.Choices[0].Message.Content,
		// Ledger rows carry the AXON ref (apple:<variant>), whether the
		// request arrived with the full ref or the bare variant.
		Model: config.ProviderApple + ":" + variant,
		Usage: Usage{
			InputTokens:  out.Usage.PromptTokens,
			OutputTokens: out.Usage.CompletionTokens,
			CacheRead:    out.Usage.PromptTokensDetails.CachedTokens,
		},
	}, nil
}

// compile-time assertion that *FMServe satisfies Agent.
var _ Agent = (*FMServe)(nil)
