package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/dreamtrans/backend/internal/store"
)

type ragHTTPBillingStub struct {
	mu sync.Mutex

	records     []billing.UsageRecord
	settlements []billing.UsageRecord
	settleKeys  []string
	refundKeys  []string

	recordErr error
	settleErr error
	refundErr error
}

func (s *ragHTTPBillingStub) RecordUsage(
	_ context.Context,
	record *billing.UsageRecord,
) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, *record)
	if s.recordErr != nil {
		return 0, s.recordErr
	}
	return 1, nil
}

func (s *ragHTTPBillingStub) SettleUsageReservation(
	_ context.Context,
	key string,
	record *billing.UsageRecord,
) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settleKeys = append(s.settleKeys, key)
	s.settlements = append(s.settlements, *record)
	if s.settleErr != nil {
		return 0, s.settleErr
	}
	return 0.25, nil
}

func (s *ragHTTPBillingStub) RefundUsage(
	_ context.Context,
	key string,
	_ string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refundKeys = append(s.refundKeys, key)
	return s.refundErr
}

func (s *ragHTTPBillingStub) GetSystemSetting(context.Context, string) (string, error) {
	return "", errors.New("setting not configured")
}

func (s *ragHTTPBillingStub) snapshot() (
	records []billing.UsageRecord,
	settlements []billing.UsageRecord,
	refunds []string,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]billing.UsageRecord(nil), s.records...),
		append([]billing.UsageRecord(nil), s.settlements...),
		append([]string(nil), s.refundKeys...)
}

func authenticatedRAGRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	return request.WithContext(context.WithValue(
		request.Context(),
		auth.UserClaimsKey,
		&auth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
	))
}

func TestRAGDatabaseModeRejectsRequestsWithoutUserPrincipal(t *testing.T) {
	handler := &RAGHandler{billing: &ragHTTPBillingStub{}}
	tests := []struct {
		name   string
		method string
		target string
		call   func(http.ResponseWriter, *http.Request)
	}{
		{name: "ask", method: http.MethodPost, target: "/api/rag/ask", call: handler.HandleAsk},
		{name: "query", method: http.MethodPost, target: "/api/rag/query", call: handler.HandleQuery},
		{name: "ingest", method: http.MethodPost, target: "/api/rag/ingest", call: handler.HandleIngest},
		{name: "summary", method: http.MethodGet, target: "/api/rag/summary", call: handler.HandleSummary},
		{name: "title", method: http.MethodGet, target: "/api/rag/title", call: handler.HandleTitle},
		{name: "stats", method: http.MethodGet, target: "/api/rag/stats", call: handler.HandleStats},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.call(response, httptest.NewRequest(test.method, test.target, nil))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestRAGProviderQuotaIsConsumedBeforeBilling(t *testing.T) {
	quota := &providerQuotaStub{err: store.ErrAPIQuota}
	ledger := &ragHTTPBillingStub{}
	meter := &ragHTTPUsageMeter{
		apiQuota: quota,
		billing:  ledger,
		userID:   "user-1",
		tenantID: "tenant-1",
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), rag.ProviderUsage{
		Action:      "embedding",
		Model:       "embedding-model",
		InputTokens: 4096,
	})
	if reservation != nil || !errors.Is(err, store.ErrAPIQuota) {
		t.Fatalf("reservation/error = %#v/%v, want quota rejection", reservation, err)
	}
	records, settlements, refunds := ledger.snapshot()
	if quota.calls != 1 || len(records) != 0 || len(settlements) != 0 || len(refunds) != 0 {
		t.Fatalf(
			"quota/ledger calls = %d/%d/%d/%d",
			quota.calls,
			len(records),
			len(settlements),
			len(refunds),
		)
	}
}

func TestRAGCustomerFundedProviderUsageConsumesQuotaWithoutDreamPoints(t *testing.T) {
	quota := &providerQuotaStub{}
	ledger := &ragHTTPBillingStub{}
	meter := &ragHTTPUsageMeter{
		apiQuota: quota,
		billing:  ledger,
		userID:   "user-1",
		tenantID: "tenant-1",
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), rag.ProviderUsage{
		Action:         "chat",
		Model:          "customer-model",
		InputTokens:    4096,
		OutputTokens:   2048,
		CustomerFunded: true,
	})
	if err != nil || reservation == nil {
		t.Fatalf("reservation/error = %#v/%v", reservation, err)
	}
	if err := reservation.Settle(t.Context(), rag.ProviderUsage{
		Action:         "chat",
		Model:          "customer-model",
		InputTokens:    11,
		OutputTokens:   7,
		CustomerFunded: true,
	}); err != nil {
		t.Fatal(err)
	}
	records, settlements, refunds := ledger.snapshot()
	if quota.calls != 1 || len(records) != 0 || len(settlements) != 0 || len(refunds) != 0 {
		t.Fatalf(
			"quota/ledger calls = %d/%d/%d/%d",
			quota.calls,
			len(records),
			len(settlements),
			len(refunds),
		)
	}
}

func TestRAGHTTPEndpointsMeterOnlyActualProviderOperations(t *testing.T) {
	var (
		providerMu          sync.Mutex
		embeddingCalls      int
		chatMaxOutputTokens []int
	)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/embeddings":
			providerMu.Lock()
			embeddingCalls++
			providerMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data":  []map[string]any{{"embedding": []float32{1, 0}, "index": 0}},
				"usage": map[string]int{"prompt_tokens": 7, "total_tokens": 7},
			})
		case "/chat/completions":
			var request struct {
				MaxCompletionTokens int `json:"max_completion_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode chat request: %v", err)
			}
			providerMu.Lock()
			chatMaxOutputTokens = append(chatMaxOutputTokens, request.MaxCompletionTokens)
			providerMu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"model": "test-chat-model",
				"choices": []map[string]any{{
					"message": map[string]string{
						"role":    "assistant",
						"content": "测试标题",
					},
				}},
				"usage": map[string]int{
					"prompt_tokens":     13,
					"completion_tokens": 3,
					"total_tokens":      16,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_API_BASE", provider.URL)
	t.Setenv("OPENAI_MODEL", "test-chat-model")
	t.Setenv("OPENAI_SUMMARY_MODEL", "test-chat-model")
	t.Setenv("OPENAI_USE_RESPONSES", "")
	t.Setenv("ALLOW_USER_API_KEY", "true")
	t.Setenv("RAG_DB_PATH", filepath.Join(t.TempDir(), "rag.db"))
	service, err := rag.NewServiceFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()
	service.SetSummaryOutputEnabled(true)

	quota := &providerQuotaStub{}
	ledger := &ragHTTPBillingStub{}
	handler := &RAGHandler{svc: service, billing: ledger, apiQuota: quota}
	const sessionID = "11111111-1111-4111-8111-111111111111"

	cases := []struct {
		name         string
		call         func(http.ResponseWriter, *http.Request)
		method       string
		target       string
		body         string
		wantQuota    int
		wantRecords  int
		wantSettles  int
		wantProvider int
	}{
		{
			name:   "ingest",
			call:   handler.HandleIngest,
			method: http.MethodPost,
			target: "/api/rag/ingest",
			body: `{"session_id":"` + sessionID + `","speaker":"A",` +
				`"text":"This is a sufficiently long paragraph for endpoint metering.","start_time":1,"end_time":2}`,
			wantQuota:    1,
			wantRecords:  1,
			wantSettles:  1,
			wantProvider: 1,
		},
		{
			name:         "query",
			call:         handler.HandleQuery,
			method:       http.MethodPost,
			target:       "/api/rag/query",
			body:         `{"session_id":"` + sessionID + `","query":"what happened?","top_k":5}`,
			wantQuota:    2,
			wantRecords:  3,
			wantSettles:  2,
			wantProvider: 2,
		},
		{
			name:         "ask",
			call:         handler.HandleAsk,
			method:       http.MethodPost,
			target:       "/api/rag/ask",
			body:         `{"session_id":"` + sessionID + `","query":"summarize it","top_k":5}`,
			wantQuota:    4,
			wantRecords:  6,
			wantSettles:  4,
			wantProvider: 3,
		},
		{
			name:         "title",
			call:         handler.HandleTitle,
			method:       http.MethodGet,
			target:       "/api/rag/title?session_id=" + sessionID,
			wantQuota:    5,
			wantRecords:  7,
			wantSettles:  5,
			wantProvider: 3,
		},
		{
			name:         "cached title",
			call:         handler.HandleTitle,
			method:       http.MethodGet,
			target:       "/api/rag/title?session_id=" + sessionID,
			wantQuota:    5,
			wantRecords:  7,
			wantSettles:  5,
			wantProvider: 3,
		},
		{
			name:   "customer funded ask",
			call:   handler.HandleAsk,
			method: http.MethodPost,
			target: "/api/rag/ask",
			body: `{"session_id":"` + sessionID + `","query":"use my provider key","top_k":5,` +
				`"config":{"api_key":"customer-key","model":"test-chat-model"}}`,
			wantQuota:    7,
			wantRecords:  9,
			wantSettles:  6,
			wantProvider: 4,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := authenticatedRAGRequest(test.method, test.target, test.body)
			test.call(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
			}
			records, settlements, refunds := ledger.snapshot()
			providerMu.Lock()
			gotEmbeddingCalls := embeddingCalls
			gotChatCalls := len(chatMaxOutputTokens)
			providerMu.Unlock()
			if quota.calls != test.wantQuota ||
				len(records) != test.wantRecords ||
				len(settlements) != test.wantSettles ||
				gotEmbeddingCalls != test.wantProvider {
				t.Fatalf(
					"quota/records/settles/embeddings/chats/refunds = %d/%d/%d/%d/%d/%d",
					quota.calls,
					len(records),
					len(settlements),
					gotEmbeddingCalls,
					gotChatCalls,
					len(refunds),
				)
			}
			if len(refunds) != 0 {
				t.Fatalf("unexpected refunds: %#v", refunds)
			}
		})
	}

	providerMu.Lock()
	defer providerMu.Unlock()
	if len(chatMaxOutputTokens) != 3 ||
		chatMaxOutputTokens[0] != 2048 ||
		chatMaxOutputTokens[1] != 128 ||
		chatMaxOutputTokens[2] != 2048 {
		t.Fatalf("chat output bounds = %#v, want [2048 128 2048]", chatMaxOutputTokens)
	}
	records, settlements, _ := ledger.snapshot()
	providerActions := make([]string, 0, len(settlements))
	for _, settlement := range settlements {
		providerActions = append(providerActions, settlement.Action)
		if settlement.Action == "embedding" && settlement.InputTokens != 7 {
			t.Fatalf("embedding settled tokens = %d, want provider usage 7", settlement.InputTokens)
		}
	}
	if strings.Join(providerActions, ",") != "embedding,embedding,embedding,chat,chat,embedding" {
		t.Fatalf("settled provider actions = %#v", providerActions)
	}
	ragQueries := 0
	for _, record := range records {
		if record.Action == "rag_query" {
			ragQueries++
		}
	}
	if ragQueries != 3 {
		t.Fatalf("RAG query ledger count = %d, want 3", ragQueries)
	}
}
