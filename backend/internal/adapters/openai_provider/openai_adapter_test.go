package openaiprovider

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIsRetryableErrorSeparatesTransientAndPermanentFailures(t *testing.T) {
	for _, err := range []error{
		context.DeadlineExceeded,
		errors.New("openai api error: status 429"),
		errors.New("openai api error: status 503"),
		errors.New("connection reset by peer"),
		errors.New("unexpected EOF"),
	} {
		if !IsRetryableError(err) {
			t.Fatalf("transient error was terminal: %v", err)
		}
	}
	for _, err := range []error{
		context.Canceled,
		errors.New("openai api error: status 400"),
		errors.New("openai api error: status 401"),
		errors.New("no choices returned"),
	} {
		if IsRetryableError(err) {
			t.Fatalf("permanent error was retryable: %v", err)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestParseUsageCanonicalSnakeCase(t *testing.T) {
	usage := parseUsageCanonical(
		[]byte(`{"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`),
		"model-a",
	)
	if usage == nil {
		t.Fatal("canonical usage was not parsed")
	}
	if usage.PromptTokens != 11 || usage.CompletionTokens != 7 || usage.TotalTokens != 18 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestParseUsageAltSnakeCase(t *testing.T) {
	raw := []byte(`{"model":"model-b","usage":{"input_tokens":13,"output_tokens":5,"total_tokens":18}}`)
	if usage := parseUsageCanonical(raw, ""); usage != nil {
		t.Fatalf("alternative usage was misclassified as canonical: %+v", usage)
	}
	usage := parseUsageAlt(
		raw,
		"",
	)
	if usage == nil {
		t.Fatal("alternative usage was not parsed")
	}
	if usage.PromptTokens != 13 || usage.CompletionTokens != 5 ||
		usage.TotalTokens != 18 || usage.Model != "model-b" {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestParseUsageLooseRejectsUnsafeCounts(t *testing.T) {
	for _, raw := range []string{
		`{"usage":{"prompt_tokens":-1,"completion_tokens":2}}`,
		`{"usage":{"prompt_tokens":1.5,"completion_tokens":2}}`,
		`{"usage":{"prompt_tokens":"1000000001","completion_tokens":2}}`,
	} {
		if usage := parseUsageLoose([]byte(raw), "model"); usage != nil {
			t.Fatalf("unsafe usage %s parsed as %+v", raw, usage)
		}
	}
}

func TestNewTranslatorSnapshotsAndNormalizesConfig(t *testing.T) {
	cfg := &Config{
		BaseURL:     "https://provider.example/v1",
		APIKey:      "key-a",
		Model:       "model-a",
		Temperature: math.NaN(),
		Timeout:     0,
	}
	translator := NewTranslator(cfg)
	cfg.APIKey = "key-b"
	cfg.Model = "model-b"

	if translator.cfg.APIKey != "key-a" || translator.cfg.Model != "model-a" {
		t.Fatalf("translator retained mutable caller config: %+v", translator.cfg)
	}
	if translator.cfg.Temperature != 0 {
		t.Fatalf("temperature = %v, want 0", translator.cfg.Temperature)
	}
	if translator.httpClient.Timeout != defaultProviderTimeout {
		t.Fatalf("timeout = %v, want %v", translator.httpClient.Timeout, defaultProviderTimeout)
	}
}

func TestChatWithUsageParsesUsageAndDoesNotLeakErrorBody(t *testing.T) {
	translator := NewTranslator(&Config{
		BaseURL:         "https://provider.example/v1",
		APIKey:          "secret-key",
		Model:           "model-a",
		Timeout:         time.Second,
		MaxOutputTokens: 321,
	})
	responseBody := `{"model":"model-a","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`
	translator.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://provider.example/v1/chat/completions" {
			t.Fatalf("unexpected endpoint %q", request.URL)
		}
		if request.Header.Get("Authorization") != "Bearer secret-key" {
			t.Fatal("authorization header missing")
		}
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"max_completion_tokens":321`) {
			t.Fatalf("chat output bound missing from request: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(responseBody)),
		}, nil
	})}

	content, usage, err := translator.ChatWithUsage(
		context.Background(),
		[]map[string]string{{"role": "user", "content": "hello"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if content != "ok" || usage == nil || usage.TotalTokens != 5 {
		t.Fatalf("content=%q usage=%+v", content, usage)
	}

	translator.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`private transcript echoed by provider`)),
		}, nil
	})}
	_, _, err = translator.ChatWithUsage(
		context.Background(),
		[]map[string]string{{"role": "user", "content": "hello"}},
	)
	if err == nil {
		t.Fatal("provider error unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "private transcript") {
		t.Fatalf("provider response body leaked through error: %v", err)
	}
}

func TestResponsesCompleteUsesInputTextAndParsesUsage(t *testing.T) {
	translator := NewTranslator(&Config{
		BaseURL:         "https://provider.example/v1",
		APIKey:          "key",
		Model:           "model-a",
		Timeout:         time.Second,
		MaxOutputTokens: 123,
		UseResponsesAPI: true,
	})
	translator.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), `"type":"text"`) ||
			!strings.Contains(string(body), `"type":"input_text"`) {
			t.Fatalf("invalid Responses API content type: %s", body)
		}
		if !strings.Contains(string(body), `"max_output_tokens":123`) {
			t.Fatalf("Responses API output bound missing from request: %s", body)
		}
		if !strings.Contains(string(body), `"store":false`) {
			t.Fatalf("Responses API request must disable provider storage: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"model":"model-a","output":[{"content":[{"text":"ok"}]}],` +
					`"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`,
			)),
		}, nil
	})}

	content, usage, err := translator.responsesComplete(
		context.Background(), "system", "context", "user", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if content != "ok" || usage == nil || usage.PromptTokens != 4 ||
		usage.CompletionTokens != 3 || usage.TotalTokens != 7 {
		t.Fatalf("content=%q usage=%+v", content, usage)
	}
}

func TestResponsesCompleteSkipsReasoningAndCollectsMessageOutput(t *testing.T) {
	translator := NewTranslator(&Config{
		BaseURL:         "https://api.openai.com/v1",
		APIKey:          "key",
		Model:           "gpt-5.6-sol",
		Timeout:         time.Second,
		MaxOutputTokens: 123,
		UseResponsesAPI: true,
	})
	translator.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), `"store":false`) {
			t.Fatalf("Responses API request must disable provider storage: %s", body)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"model":"gpt-5.6-sol","output":[` +
					`{"type":"reasoning","summary":[]},` +
					`{"type":"message","content":[` +
					`{"type":"output_text","text":"first"},` +
					`{"type":"output_text","text":"second"}]}],` +
					`"usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}`,
			)),
		}, nil
	})}

	content, _, err := translator.responsesComplete(
		context.Background(), "system", "context", "user", false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if content != "first\nsecond" {
		t.Fatalf("content = %q, want collected message output", content)
	}
}

func TestParseUsageCanonicalIncludesPromptCacheDetails(t *testing.T) {
	usage := parseUsageCanonical(
		[]byte(`{"usage":{"prompt_tokens":100,"completion_tokens":7,"total_tokens":107,`+
			`"prompt_tokens_details":{"cached_tokens":60,"cache_write_tokens":20}}}`),
		"model-cache",
	)
	if usage == nil || usage.CachedTokens != 60 || usage.CacheWriteTokens != 20 {
		t.Fatalf("unexpected cache usage: %+v", usage)
	}
}

func TestResponsesCompleteUsesOfficialExplicitCacheFields(t *testing.T) {
	translator := NewTranslator(&Config{
		BaseURL:           "https://api.openai.com/v1",
		APIKey:            "key",
		Model:             "gpt-5.6-sol",
		Timeout:           time.Second,
		MaxOutputTokens:   123,
		UseResponsesAPI:   true,
		EnablePromptCache: true,
	})
	translator.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		payload := string(body)
		for _, expected := range []string{
			`"prompt_cache_key":"session-1"`,
			`"prompt_cache_options":{"mode":"explicit","ttl":"30m"}`,
			`"type":"prompt_cache_breakpoint"`,
		} {
			if !strings.Contains(payload, expected) {
				t.Fatalf("missing %s in %s", expected, payload)
			}
		}
		if strings.Contains(payload, `"cache_control"`) {
			t.Fatalf("legacy cache_control leaked into Responses request: %s", payload)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"model":"gpt-5.6-sol","output":[{"content":[{"text":"ok"}]}],` +
					`"usage":{"input_tokens":100,"output_tokens":3,"total_tokens":103,` +
					`"input_tokens_details":{"cached_tokens":60,"cache_write_tokens":20}}}`,
			)),
		}, nil
	})}
	_, usage, err := translator.responsesComplete(
		context.Background(), "system", "stable context", "question", true, "session-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.CachedTokens != 60 || usage.CacheWriteTokens != 20 {
		t.Fatalf("cache usage = %#v", usage)
	}
}

func TestRespondDoesNotFallbackOnResponsesServerError(t *testing.T) {
	translator := NewTranslator(&Config{
		BaseURL: "https://api.openai.com/v1", APIKey: "key", Model: "gpt-5.6-sol",
		Timeout: time.Second, UseResponsesAPI: true,
	})
	calls := 0
	translator.httpClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":"temporary"}`)),
		}, nil
	})}
	_, _, err := translator.RespondWithUsage(
		context.Background(), "system", "context", "", "question", "session-1",
	)
	if err == nil || calls != 1 {
		t.Fatalf("err/calls = %v/%d, want Responses error without fallback", err, calls)
	}
}

func TestRespondFallsBackWhenResponsesRouteIsUnsupported(t *testing.T) {
	translator := NewTranslator(&Config{
		BaseURL: "https://provider.example/v1", APIKey: "key", Model: "model-a",
		Timeout: time.Second, UseResponsesAPI: true,
	})
	var paths []string
	translator.httpClient = &http.Client{Transport: roundTripFunc(func(
		request *http.Request,
	) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if request.URL.Path == "/v1/responses" {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"error":"unsupported"}`)),
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"model":"model-a","choices":[{"message":{"content":"fallback ok"}}],` +
					`"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`,
			)),
		}, nil
	})}
	content, usage, err := translator.RespondWithUsage(
		context.Background(), "system", "context", "", "question", "session-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if content != "fallback ok" || usage == nil || usage.TotalTokens != 6 {
		t.Fatalf("content=%q usage=%#v", content, usage)
	}
	if strings.Join(paths, ",") != "/v1/responses,/v1/chat/completions" {
		t.Fatalf("provider call paths = %#v", paths)
	}
}

func TestProviderEndpointRejectsCredentialBearingURL(t *testing.T) {
	if _, err := providerEndpoint("https://user:password@example.com/v1", "/responses"); err == nil {
		t.Fatal("credential-bearing provider URL was accepted")
	}
}
