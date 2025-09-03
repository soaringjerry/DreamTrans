package openai_provider

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "os"
    "time"
)

// Config holds OpenAI-style API configuration.
type Config struct {
    BaseURL string
    APIKey  string
    Model   string
    // Tuning
    Temperature float64
    Timeout     time.Duration
}

// NewConfigFromEnv builds Config from environment variables with sensible defaults.
// OPENAI_API_BASE, OPENAI_API_KEY, OPENAI_MODEL, OPENAI_TEMPERATURE
func NewConfigFromEnv() (*Config, error) {
    base := os.Getenv("OPENAI_API_BASE")
    if base == "" {
        base = "https://api.openai.com/v1"
    }

    key := os.Getenv("OPENAI_API_KEY")
    if key == "" {
        return nil, fmt.Errorf("OPENAI_API_KEY not set")
    }

    model := os.Getenv("OPENAI_MODEL")
    if model == "" {
        // The user mentioned gpt4omini; map to official identifier
        model = "gpt-4o-mini"
    }

    temp := 0.2
    if v := os.Getenv("OPENAI_TEMPERATURE"); v != "" {
        var f float64
        if _, err := fmt.Sscanf(v, "%f", &f); err == nil {
            temp = f
        }
    }

    return &Config{
        BaseURL:     base,
        APIKey:      key,
        Model:       model,
        Temperature: temp,
        Timeout:     60 * time.Second,
    }, nil
}

// Translator provides translation and summarization using an OpenAI-compatible Chat Completions API.
type Translator struct {
    cfg        *Config
    httpClient *http.Client
}

func NewTranslator(cfg *Config) *Translator {
    return &Translator{
        cfg: cfg,
        httpClient: &http.Client{
            Timeout: cfg.Timeout,
        },
    }
}

// translatePrompt builds the prompt for translation given context and the current segment.
func translatePrompt(contextText, segment string) []map[string]string {
    system := "You are a professional EN->ZH translator. Preserve meaning, tone, and technical terminology. Output ONLY the Chinese translation without any extra commentary."
    user := "Context (may be truncated):\n" + contextText + "\n---\nText to translate:\n" + segment
    return []map[string]string{
        {"role": "system", "content": system},
        {"role": "user", "content": user},
    }
}

// summarizePrompt asks the model to compress prior context while preserving important details.
func summarizePrompt(previousSummary, backlog string) []map[string]string {
    system := "You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise but information-dense. Output in English."
    user := "Previous summary (may be empty):\n" + previousSummary + "\n---\nNew backlog to compress:\n" + backlog + "\n---\nUpdate the summary."
    return []map[string]string{
        {"role": "system", "content": system},
        {"role": "user", "content": user},
    }
}

// openAIChatRequest represents minimal Chat Completions payload.
type openAIChatRequest struct {
    Model       string                   `json:"model"`
    Messages    []map[string]string      `json:"messages"`
    Temperature float64                  `json:"temperature,omitempty"`
    Stream      bool                     `json:"stream,omitempty"`
}

type openAIChatResponse struct {
    Choices []struct {
        Message struct {
            Role    string `json:"role"`
            Content string `json:"content"`
        } `json:"message"`
    } `json:"choices"`
}

func (t *Translator) chatComplete(ctx context.Context, messages []map[string]string) (string, error) {
    reqBody := openAIChatRequest{
        Model:       t.cfg.Model,
        Messages:    messages,
        Temperature: t.cfg.Temperature,
        Stream:      false,
    }
    b, _ := json.Marshal(reqBody)

    url := t.cfg.BaseURL
    if url == "" {
        url = "https://api.openai.com/v1"
    }
    if url[len(url)-1] == '/' {
        url = url[:len(url)-1]
    }
    endpoint := url + "/chat/completions"

    req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(b))
    if err != nil {
        return "", err
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)

    resp, err := t.httpClient.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        var raw bytes.Buffer
        _, _ = raw.ReadFrom(resp.Body)
        return "", fmt.Errorf("openai api error: %d %s", resp.StatusCode, raw.String())
    }

    var out openAIChatResponse
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return "", err
    }
    if len(out.Choices) == 0 {
        return "", fmt.Errorf("no choices returned")
    }
    return out.Choices[0].Message.Content, nil
}

// Translate produces a Chinese translation for the given segment using optional rolling or summarized context.
func (t *Translator) Translate(ctx context.Context, contextText, segment string) (string, error) {
    msgs := translatePrompt(contextText, segment)
    return t.chatComplete(ctx, msgs)
}

// Summarize compresses backlog into an updated summary.
func (t *Translator) Summarize(ctx context.Context, previousSummary, backlog string) (string, error) {
    msgs := summarizePrompt(previousSummary, backlog)
    return t.chatComplete(ctx, msgs)
}

