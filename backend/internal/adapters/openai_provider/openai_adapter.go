package openaiprovider

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "regexp"
    "strings"
    "time"
)

// Config holds OpenAI-style API configuration.
type Config struct {
    BaseURL     string
    APIKey      string
    Model       string
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
        // Default general chat/summarize model
        model = "gpt-5"
    }

    // Default to 0 to omit the field (OpenAI defaults to 1)
    temp := 0.0
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
        httpClient: &http.Client{Timeout: cfg.Timeout},
    }
}

// openAIChatRequest represents minimal Chat Completions payload.
type openAIChatRequest struct {
    Model       string              `json:"model"`
    Messages    []map[string]string `json:"messages"`
    Temperature float64             `json:"temperature,omitempty"`
    Stream      bool                `json:"stream,omitempty"`
}

type openAIChatResponse struct {
    Choices []struct {
        Message struct {
            Role    string `json:"role"`
            Content string `json:"content"`
        } `json:"message"`
    } `json:"choices"`
}

// nolint:gocyclo // fallback and retry logic is intentionally explicit for clarity
func (t *Translator) chatComplete(ctx context.Context, messages []map[string]string) (string, error) {
    // Helper to call API with a specific model + temp
    sendWith := func(model string, temp float64) (string, int, string, error) {
        reqBody := openAIChatRequest{
            Model:       model,
            Messages:    messages,
            Temperature: temp, // omitted when 0 due to omitempty
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
            return "", 0, "", err
        }
        req.Header.Set("Content-Type", "application/json")
        req.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)

        resp, err := t.httpClient.Do(req)
        if err != nil {
            return "", 0, "", err
        }
        defer resp.Body.Close()

        var raw bytes.Buffer
        _, _ = raw.ReadFrom(resp.Body)

        if resp.StatusCode < 200 || resp.StatusCode >= 300 {
            return "", resp.StatusCode, raw.String(), fmt.Errorf("openai api error: %d %s", resp.StatusCode, raw.String())
        }

        var out openAIChatResponse
        if err := json.Unmarshal(raw.Bytes(), &out); err != nil {
            return "", resp.StatusCode, raw.String(), err
        }
        if len(out.Choices) == 0 {
            return "", resp.StatusCode, raw.String(), fmt.Errorf("no choices returned")
        }
        return out.Choices[0].Message.Content, resp.StatusCode, raw.String(), nil
    }

    // Build model candidates: primary + env fallbacks (default only gpt-5 family; never fallback to gpt-4 series)
    modelPrimary := t.cfg.Model
    fallbacks := []string{}
    if v := os.Getenv("OPENAI_FALLBACK_MODELS"); v != "" {
        for _, p := range strings.Split(v, ",") {
            p = strings.TrimSpace(p)
            if p != "" && !strings.EqualFold(p, modelPrimary) {
                fallbacks = append(fallbacks, p)
            }
        }
    } else {
        for _, p := range []string{"gpt-5-mini", "gpt-5-nano"} {
            if !strings.EqualFold(p, modelPrimary) { fallbacks = append(fallbacks, p) }
        }
    }
    models := append([]string{modelPrimary}, fallbacks...)

    var lastErr error
    for _, model := range models {
        // First attempt with configured temperature
        content, code, body, err := sendWith(model, t.cfg.Temperature)
        if os.Getenv("OPENAI_DEBUG") == "1" {
            log.Printf("openai.chat model=%s code=%d err=%v", model, code, err)
        }
        if err == nil { return content, nil }
        lastErr = err
        bl := strings.ToLower(body)
        // If temp rejected, retry without temp
        if code == http.StatusBadRequest && (strings.Contains(bl, "temperature") || strings.Contains(bl, "unsupported_value")) {
            content2, code2, body2, err2 := sendWith(model, 0)
            if err2 == nil { return content2, nil }
            if os.Getenv("OPENAI_DEBUG") == "1" { log.Printf("openai.chat retry(no-temp) model=%s code=%d err=%v body=%s", model, code2, err2, trimBody(body2)) }
            lastErr = err2
        }
        // If model invalid, continue to next fallback
        if code == http.StatusBadRequest || code == http.StatusNotFound {
            if strings.Contains(bl, "model") || strings.Contains(bl, "unknown") || strings.Contains(bl, "not found") || strings.Contains(bl, "unsupported") {
                continue
            }
        }
        // For other errors, break and return
        break
    }
    return "", lastErr
}

func trimBody(s string) string {
    if len(s) > 300 { return s[:300] + "..." }
    return s
}

// Chat exposes a generic chat completion using the configured model.
func (t *Translator) Chat(ctx context.Context, messages []map[string]string) (string, error) {
    return t.chatComplete(ctx, messages)
}

// polishedTranslatePrompt keeps strict separation of context and text and asks for fluency polishing.
func polishedTranslatePrompt(contextText, segment string) []map[string]string {
    system := strings.Join([]string{
        "You are a professional EN->ZH translator and copy editor.",
        "Use the <context> only to understand semantics and terms.",
        "Translate ONLY the text inside <text>...</text> into Simplified Chinese.",
        "Then polish the Chinese so it is fluent, natural, and easy to read while preserving meaning and tone.",
        "Prefer concise, idiomatic phrasing; merge fragments as needed; fix awkward word order; remove filler.",
        "Keep technical terminology accurate; keep numbers/units; standardize punctuation to Chinese style when appropriate.",
        "Do NOT include any content from <context> in the output.",
        "Do NOT add explanations, quotes, speaker labels, timestamps, or language tags.",
        "Return only the final polished Chinese sentence(s), nothing else.",
    }, " ")

    user := "<context>\n" + contextText + "\n</context>\n<text>\n" + segment + "\n</text>"
    return []map[string]string{
        {"role": "system", "content": system},
        {"role": "user", "content": user},
    }
}

// summarizePrompt asks the model to compress prior context while preserving important details.
func summarizePrompt(previousSummary, backlog string) []map[string]string {
    system := "You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English."
    user := "Previous summary (may be empty):\n" + previousSummary + "\n---\nNew backlog to compress:\n" + backlog + "\n---\nUpdate the summary."
    return []map[string]string{
        {"role": "system", "content": system},
        {"role": "user", "content": user},
    }
}

// Translate produces a Chinese translation for the given segment using optional rolling or summarized context.
func (t *Translator) Translate(ctx context.Context, contextText, segment string) (string, error) {
    msgs := polishedTranslatePrompt(contextText, segment)
    out, err := t.chatComplete(ctx, msgs)
    if err != nil {
        return "", err
    }
    return sanitizeTranslationOutput(contextText, segment, out), nil
}

// Summarize compresses backlog into an updated summary used by compressed-context mode.
func (t *Translator) Summarize(ctx context.Context, previousSummary, backlog string) (string, error) {
    msgs := summarizePrompt(previousSummary, backlog)
    return t.chatComplete(ctx, msgs)
}


// sanitizeTranslationOutput removes any leaked context/source and common prefixes the model might add.
func sanitizeTranslationOutput(contextText, segment, out string) string {
    s := strings.TrimSpace(out)
    // Remove common prefixes (ASCII-only to avoid encoding issues)
    for _, p := range []string{"Translation:", "Translated:", "Result:", "Output:"} {
        if strings.HasPrefix(strings.ToLower(s), strings.ToLower(p)) {
            s = strings.TrimSpace(s[len(p):])
            break
        }
    }
    // Strip code fences or quotes
    s = strings.Trim(s, "`\"")
    s = strings.TrimSpace(s)

    // If the model echoed the source or context, remove those substrings
    if segment != "" {
        s = strings.ReplaceAll(s, segment, "")
    }
    if contextText != "" {
        s = strings.ReplaceAll(s, contextText, "")
    }
    // Remove any obvious context headers if model echoed labels
    lines := strings.Split(s, "\n")
    filtered := make([]string, 0, len(lines))
    ctxLabelRe := regexp.MustCompile(`(?i)^(context)\s*:`)
    for _, line := range lines {
        L := strings.TrimSpace(line)
        if L == "" {
            continue
        }
        if ctxLabelRe.MatchString(L) {
            continue
        }
        filtered = append(filtered, L)
    }
    s = strings.Join(filtered, "\n")

    // Final trim; collapse excessive whitespace
    s = strings.TrimSpace(s)
    s = regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
    return s
}
