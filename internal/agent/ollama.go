package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultOllamaHost mirrors the embeddings default; models.ollama_host
// overrides it independently of embeddings.host.
const DefaultOllamaHost = "http://localhost:11434"

// Ollama is the local chat adapter (ADR-015): a models tier written as
// "ollama:<model>" is served by a local Ollama server's /api/chat. It is
// dispatched only by the token manager's router — never called directly —
// so every local call is ledgered (cardinal rule 1, generalized).
type Ollama struct {
	host       string
	httpClient *http.Client
}

// NewOllama constructs the adapter. A blank host falls back to the default.
func NewOllama(host string) *Ollama {
	if host == "" {
		host = DefaultOllamaHost
	}
	return &Ollama{
		host:       strings.TrimRight(host, "/"),
		httpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

// AuthMode reports "local": no subscription, no API key, no cost.
func (o *Ollama) AuthMode() string { return "local" }

type ollamaChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string              `json:"model"`
	Messages []ollamaChatMessage `json:"messages"`
	Stream   bool                `json:"stream"`
	Format   string              `json:"format,omitempty"`
	Options  map[string]any      `json:"options,omitempty"`
}

type ollamaChatResponse struct {
	Model           string            `json:"model"`
	Message         ollamaChatMessage `json:"message"`
	PromptEvalCount int               `json:"prompt_eval_count"`
	EvalCount       int               `json:"eval_count"`
	Error           string            `json:"error"`
}

// Run executes one chat turn against the local Ollama server.
func (o *Ollama) Run(ctx context.Context, req Request) (*Response, error) {
	msgs := make([]ollamaChatMessage, 0, 2)
	if req.System != "" {
		msgs = append(msgs, ollamaChatMessage{Role: "system", Content: req.System})
	}
	msgs = append(msgs, ollamaChatMessage{Role: "user", Content: req.Prompt})

	body := ollamaChatRequest{Model: req.Model, Messages: msgs, Stream: false}
	if req.JSONOutput {
		body.Format = "json"
	}
	raw, err := o.post(ctx, body)
	if err != nil {
		return nil, err
	}
	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama chat: decode: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama chat: %s", out.Error)
	}
	return &Response{
		Text:  out.Message.Content,
		Model: out.Model,
		Usage: Usage{InputTokens: out.PromptEvalCount, OutputTokens: out.EvalCount},
	}, nil
}

// Healthcheck verifies the server is reachable and the model is loadable with
// a single-token chat round trip (used by configure convergence and doctor).
func (o *Ollama) Healthcheck(ctx context.Context, model string) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := o.post(ctx, ollamaChatRequest{
		Model:    model,
		Messages: []ollamaChatMessage{{Role: "user", Content: "ok"}},
		Stream:   false,
		Options:  map[string]any{"num_predict": 1},
	})
	if err != nil {
		return err
	}
	var out ollamaChatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("ollama healthcheck: decode: %w", err)
	}
	if out.Error != "" {
		return fmt.Errorf("ollama healthcheck: %s", out.Error)
	}
	return nil
}

func (o *Ollama) post(ctx context.Context, body ollamaChatRequest) ([]byte, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.host+"/api/chat", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama chat request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama chat: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return raw, nil
}

// compile-time assertion that *Ollama satisfies Agent.
var _ Agent = (*Ollama)(nil)
