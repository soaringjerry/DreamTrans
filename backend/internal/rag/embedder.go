package rag

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "strings"
    "time"
)

// EmbeddingProvider defines an interface to obtain vector embeddings for text.
type EmbeddingProvider interface {
    Embed(ctx context.Context, input string) ([]float32, error)
}

type openAIEmbeddingProvider struct {
    baseURL   string
    apiKey    string
    model     string
    timeout   time.Duration
    httpClient *http.Client
}

// NewOpenAIEmbeddingFromEnv creates an embedding provider compatible with OpenAI APIs.
func NewOpenAIEmbeddingFromEnv() (EmbeddingProvider, error) {
    base := os.Getenv("OPENAI_API_BASE")
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
        apiKey: key,
        model: model,
        timeout: 60 * time.Second,
        httpClient: &http.Client{Timeout: 60 * time.Second},
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
}

func (p *openAIEmbeddingProvider) Embed(ctx context.Context, input string) ([]float32, error) {
    payload := embeddingsRequest{
        Input: input,
        Model: p.model,
    }
    b, _ := json.Marshal(payload)
    url := strings.TrimRight(p.baseURL, "/") + "/embeddings"
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+p.apiKey)
    resp, err := p.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        return nil, fmt.Errorf("embedding api error: %s", resp.Status)
    }
    var out embeddingsResponse
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, err
    }
    if len(out.Data) == 0 {
        return nil, fmt.Errorf("no embedding returned")
    }
    return out.Data[0].Embedding, nil
}

