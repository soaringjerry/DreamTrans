package rag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	openaiprovider "github.com/dreamtrans/backend/internal/adapters/openai_provider"
)

type meterTestCall struct {
	reserved ProviderUsage
	actual   ProviderUsage
	settled  bool
	refunded bool
}

type meterTestMeter struct {
	mu sync.Mutex

	calls      []*meterTestCall
	reserveErr error
	settleErr  error
	refundErr  error
}

type meterTestReservation struct {
	meter *meterTestMeter
	call  *meterTestCall
}

func (m *meterTestMeter) ReserveProviderUsage(
	_ context.Context,
	usage ProviderUsage,
) (ProviderUsageReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reserveErr != nil {
		return nil, m.reserveErr
	}
	call := &meterTestCall{reserved: usage}
	m.calls = append(m.calls, call)
	return &meterTestReservation{meter: m, call: call}, nil
}

func (r *meterTestReservation) Settle(_ context.Context, usage ProviderUsage) error {
	r.meter.mu.Lock()
	defer r.meter.mu.Unlock()
	if r.meter.settleErr != nil {
		return r.meter.settleErr
	}
	r.call.actual = usage
	r.call.settled = true
	return nil
}

func (r *meterTestReservation) Refund(_ string) error {
	r.meter.mu.Lock()
	defer r.meter.mu.Unlock()
	if r.meter.refundErr != nil {
		return r.meter.refundErr
	}
	r.call.refunded = true
	return nil
}

func (m *meterTestMeter) snapshot() []meterTestCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]meterTestCall, len(m.calls))
	for i, call := range m.calls {
		out[i] = *call
	}
	return out
}

type failingMeterTestEmbedder struct {
	err error
}

func (e failingMeterTestEmbedder) Embed(context.Context, string) ([]float32, error) {
	return nil, e.err
}

func TestIngestSettlementFailureLeavesNoPersistentOrLiveState(t *testing.T) {
	service, _ := newIngestTestService(t)
	service.SetSummaryOutputEnabled(true)
	meter := &meterTestMeter{settleErr: errors.New("ledger unavailable")}
	ctx := WithProviderUsageMeter(t.Context(), meter)

	const input = "This is a sufficiently long paragraph about atomic persistence."
	result, err := service.IngestParagraphWithResult(ctx, "session-1", "speaker", input, 1, 2)
	if err == nil {
		t.Fatal("expected settlement failure")
	}
	if result.Embedded {
		t.Fatalf("unexpected persisted result: %#v", result)
	}
	exists, err := service.store.HasDocument(
		"session-1",
		"this is a sufficiently long paragraph about atomic persistence",
		1,
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("document persisted before usage settlement")
	}
	summary, err := service.StoreSummary("session-1")
	if err != nil {
		t.Fatal(err)
	}
	if summary != "" {
		t.Fatalf("summary persisted before usage settlement: %q", summary)
	}
	if live := service.recentLiveDocuments("session-1", 10); len(live) != 0 {
		t.Fatalf("live state published before usage settlement: %#v", live)
	}
	calls := meter.snapshot()
	if len(calls) != 1 || calls[0].reserved.Action != "embedding" || calls[0].settled {
		t.Fatalf("unexpected metering lifecycle: %#v", calls)
	}
}

func TestIngestProviderFailureRefundsAndDoesNotPersist(t *testing.T) {
	service, _ := newIngestTestService(t)
	service.embedder = failingMeterTestEmbedder{err: errors.New("provider failed")}
	meter := &meterTestMeter{}
	ctx := WithProviderUsageMeter(t.Context(), meter)

	_, err := service.IngestParagraphWithResult(
		ctx,
		"session-1",
		"speaker",
		"This is a sufficiently long paragraph about provider failure.",
		1,
		2,
	)
	if err == nil {
		t.Fatal("expected provider failure")
	}
	calls := meter.snapshot()
	if len(calls) != 1 || !calls[0].refunded || calls[0].settled {
		t.Fatalf("unexpected metering lifecycle: %#v", calls)
	}
	docs, err := service.RecentDocuments("session-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("provider failure persisted documents: %#v", docs)
	}
}

func TestIngestSkipsMeterForFilteredAndDuplicateInput(t *testing.T) {
	service, _ := newIngestTestService(t)
	meter := &meterTestMeter{}
	ctx := WithProviderUsageMeter(t.Context(), meter)

	if _, err := service.IngestParagraphWithResult(ctx, "session-1", "", "uh.", 1, 2); err != nil {
		t.Fatal(err)
	}
	const input = "This is a sufficiently long paragraph about duplicate metering."
	if _, err := service.IngestParagraphWithResult(ctx, "session-1", "", input, 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := service.IngestParagraphWithResult(ctx, "session-1", "", input, 1, 2); err != nil {
		t.Fatal(err)
	}
	calls := meter.snapshot()
	if len(calls) != 1 || calls[0].reserved.Action != "embedding" {
		t.Fatalf("provider meter calls = %#v, want one embedding", calls)
	}
}

func TestBuildAnswerMetersEmbeddingAndChatWithExactSettlement(t *testing.T) {
	var maxOutputTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode provider request: %v", err)
		}
		maxOutputTokens = body.MaxCompletionTokens
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "answer-model",
			"choices": []map[string]any{{
				"message": map[string]string{"role": "assistant", "content": "answer"},
			}},
			"usage": map[string]int{
				"prompt_tokens":     17,
				"completion_tokens": 5,
				"total_tokens":      22,
			},
		})
	}))
	defer server.Close()

	service, _ := newIngestTestService(t)
	service.SetChatConfigProvider(func() (*openaiprovider.Config, error) {
		return &openaiprovider.Config{
			APIKey:  "test-key",
			BaseURL: server.URL,
			Model:   "answer-model",
		}, nil
	})
	meter := &meterTestMeter{}
	ctx := WithProviderUsageMeter(t.Context(), meter)

	out, usage, _, err := service.BuildAnswerWithHistoryWithConfigUsage(
		ctx,
		"session-1",
		"what happened?",
		5,
		nil,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if out != "answer" || usage == nil || usage.TotalTokens != 22 {
		t.Fatalf("unexpected answer/usage: %q %#v", out, usage)
	}
	if maxOutputTokens != ragAnswerMaxOutputTokens {
		t.Fatalf("max_completion_tokens = %d, want %d", maxOutputTokens, ragAnswerMaxOutputTokens)
	}
	calls := meter.snapshot()
	if len(calls) != 2 {
		t.Fatalf("meter calls = %#v, want embedding + chat", calls)
	}
	if calls[0].reserved.Action != "embedding" || !calls[0].settled ||
		calls[1].reserved.Action != "chat" || !calls[1].settled {
		t.Fatalf("unexpected metering lifecycle: %#v", calls)
	}
	if calls[1].reserved.OutputTokens != ragAnswerMaxOutputTokens ||
		calls[1].actual.InputTokens != 17 ||
		calls[1].actual.OutputTokens != 5 {
		t.Fatalf("unexpected chat reservation/settlement: %#v", calls[1])
	}
}

func TestIngestMetersOptionalSummaryAndEmbeddingSeparately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "summary-model",
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": "a concise paragraph summary",
				},
			}},
			"usage": map[string]int{
				"prompt_tokens":     29,
				"completion_tokens": 6,
				"total_tokens":      35,
			},
		})
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_BASE", server.URL)
	t.Setenv("OPENAI_SUMMARY_MODEL", "summary-model")
	t.Setenv("OPENAI_USE_RESPONSES", "")

	service, _ := newIngestTestService(t)
	service.SetIngestSummarizeEnabled(true)
	meter := &meterTestMeter{}
	ctx := WithProviderUsageMeter(t.Context(), meter)
	input := strings.Repeat(
		"This paragraph contains meaningful information for summary metering ",
		6,
	)

	result, err := service.IngestParagraphWithResult(
		ctx,
		"session-1",
		"speaker",
		input,
		1,
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Embedded || result.EmbeddedText != "a concise paragraph summary" {
		t.Fatalf("unexpected ingest result: %#v", result)
	}
	calls := meter.snapshot()
	if len(calls) != 2 {
		t.Fatalf("meter calls = %#v, want summarize + embedding", calls)
	}
	if calls[0].reserved.Action != "summarize" ||
		!calls[0].settled ||
		calls[0].actual.InputTokens != 29 ||
		calls[0].actual.OutputTokens != 6 {
		t.Fatalf("unexpected summary lifecycle: %#v", calls[0])
	}
	if calls[1].reserved.Action != "embedding" || !calls[1].settled {
		t.Fatalf("unexpected embedding lifecycle: %#v", calls[1])
	}
}
