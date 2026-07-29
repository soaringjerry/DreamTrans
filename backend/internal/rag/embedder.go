// Package rag provides tenant-scoped retrieval, summaries, and embeddings.
package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// EmbeddingProvider defines an interface to obtain vector embeddings for text.
type EmbeddingProvider interface {
	Embed(ctx context.Context, input string) ([]float32, error)
}

// embeddingUsageProvider is implemented by providers that expose authoritative
// token usage. Third-party embedders can continue implementing EmbeddingProvider;
// metered requests keep their conservative reservation when exact usage is absent.
type embeddingUsageProvider interface {
	EmbedWithUsage(ctx context.Context, input string) ([]float32, int, error)
}

type openAIEmbeddingProvider struct {
	baseURL    string
	apiKey     string
	model      string
	timeout    time.Duration
	httpClient *http.Client
}

// NewOpenAIEmbeddingFromEnv creates an embedding provider compatible with OpenAI APIs.
func NewOpenAIEmbeddingFromEnv() (EmbeddingProvider, error) {
	base := os.Getenv("OPENAI_API_BASE")
	if base == "" {
		base = os.Getenv("OPENAI_BASE")
	}
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}
	model := os.Getenv("OPENAI_EMBEDDING_MODEL")
	if model == "" {
		model = "text-embedding-3-small"
	}
	return &openAIEmbeddingProvider{
		baseURL: base,
		apiKey:  key,
		model:   model,
		timeout: 60 * time.Second,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type embeddingsRequest struct {
	Input any    `json:"input"`
	Model string `json:"model"`
}

type embeddingsResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

func (p *openAIEmbeddingProvider) Embed(ctx context.Context, input string) ([]float32, error) {
	embedding, _, err := p.EmbedWithUsage(ctx, input)
	return embedding, err
}

func (p *openAIEmbeddingProvider) EmbedWithUsage(
	ctx context.Context,
	input string,
) ([]float32, int, error) {
	payload := embeddingsRequest{
		Input: input,
		Model: p.model,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("encode embedding request: %w", err)
	}
	url := strings.TrimRight(p.baseURL, "/") + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, 0, fmt.Errorf("embedding api error: %s", resp.Status)
	}
	const maxEmbeddingResponse = 16 << 20
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxEmbeddingResponse+1))
	if err != nil {
		return nil, 0, err
	}
	if len(raw) > maxEmbeddingResponse {
		return nil, 0, fmt.Errorf("embedding response is too large")
	}
	var out embeddingsResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, 0, err
	}
	if len(out.Data) == 0 {
		return nil, 0, fmt.Errorf("no embedding returned")
	}
	embedding := out.Data[0].Embedding
	if len(embedding) == 0 || len(embedding) > 65_536 {
		return nil, 0, fmt.Errorf("invalid embedding dimension: %d", len(embedding))
	}
	inputTokens := out.Usage.PromptTokens
	if inputTokens <= 0 {
		inputTokens = out.Usage.TotalTokens
	}
	return embedding, inputTokens, nil
}
