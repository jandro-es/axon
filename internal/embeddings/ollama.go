package embeddings

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

// DefaultOllamaHost is used when the config leaves embeddings.host blank.
const DefaultOllamaHost = "http://localhost:11434"

// Ollama is the default embedding Provider, backed by a local Ollama server's
// /api/embed endpoint. It is safe for concurrent use (http.Client is).
type Ollama struct {
	host       string
	model      string
	dim        int
	httpClient *http.Client
}

// NewOllama constructs an Ollama provider. A blank host falls back to the local
// default. dim is the configured/expected vector dimension (asserted lazily by
// Healthcheck and by the ingestion pipeline against live output).
func NewOllama(host, model string, dim int) *Ollama {
	if host == "" {
		host = DefaultOllamaHost
	}
	return &Ollama{
		host:       strings.TrimRight(host, "/"),
		model:      model,
		dim:        dim,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// Model reports the configured embedding model.
func (o *Ollama) Model() string { return o.model }

// Dim reports the configured/expected vector dimension.
func (o *Ollama) Dim() int { return o.dim }

type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

// Embed returns one vector per input text via Ollama's batch /api/embed.
func (o *Ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, err := json.Marshal(ollamaEmbedRequest{Model: o.model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.host+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed: status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out ollamaEmbedResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("ollama embed: decode: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("ollama embed: %s", out.Error)
	}
	if len(out.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embed: got %d vectors for %d inputs", len(out.Embeddings), len(texts))
	}
	// Validate dimension against config so a model mismatch is caught early.
	for i, v := range out.Embeddings {
		if o.dim > 0 && len(v) != o.dim {
			return nil, fmt.Errorf("ollama embed: vector %d has dim %d, config expects %d (model %q changed? run `axon reindex --embeddings`)",
				i, len(v), o.dim, o.model)
		}
	}
	return out.Embeddings, nil
}

// Healthcheck embeds a tiny probe string, verifying both reachability and that
// the live model's output dimension matches the configured dim.
func (o *Ollama) Healthcheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	vecs, err := o.Embed(ctx, []string{"ok"})
	if err != nil {
		return err
	}
	if len(vecs) != 1 {
		return fmt.Errorf("ollama healthcheck: unexpected response")
	}
	if o.dim > 0 && len(vecs[0]) != o.dim {
		return fmt.Errorf("ollama healthcheck: model %q dim %d != configured %d", o.model, len(vecs[0]), o.dim)
	}
	return nil
}

// compile-time assertion that *Ollama satisfies Provider.
var _ Provider = (*Ollama)(nil)
