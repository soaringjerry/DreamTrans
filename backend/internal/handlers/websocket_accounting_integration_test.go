package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/gorilla/websocket"
)

func TestTranslationSettlementFailureDeliversBeforeCloseAndReplaysLocally(
	t *testing.T,
) {
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			MaxCompletionTokens int `json:"max_completion_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode provider request: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if request.MaxCompletionTokens != realtimeProviderMaxOutputTokens {
			t.Errorf(
				"provider max output tokens = %d, want %d",
				request.MaxCompletionTokens,
				realtimeProviderMaxOutputTokens,
			)
		}
		providerCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-provider-model",
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": "translated exactly once",
				},
			}},
			"usage": map[string]int{
				"prompt_tokens":     20,
				"completion_tokens": 4,
				"total_tokens":      24,
			},
		})
	}))
	defer provider.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_API_BASE", provider.URL)
	t.Setenv("OPENAI_MODEL", "test-provider-model")
	t.Setenv("OPENAI_USE_RESPONSES", "false")
	t.Setenv("RAG_DB_PATH", filepath.Join(t.TempDir(), "rag.db"))

	ledger := &fakeWebSocketBilling{
		settleErr: errors.New("settlement commit outcome is unknown"),
	}
	handler := &WebSocketHandler{
		billing:     ledger,
		connections: newWebSocketConnectionLimiter(8, 8),
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		claims := &auth.UserClaims{
			UserID:   "settlement-user",
			TenantID: "settlement-tenant",
			Role:     "user",
		}
		ctx := context.WithValue(r.Context(), auth.UserClaimsKey, claims)
		handler.Handle(w, r.WithContext(ctx))
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial first translation socket: %v", err)
	}
	if err := first.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	initializeAccountingTestSocket(t, first)
	sendAccountingTestTranscript(t, first)

	var (
		firstMessages   []map[string]any
		translationAt   = -1
		accountingAt    = -1
		firstCloseError *websocket.CloseError
	)
	for {
		var message map[string]any
		readErr := first.ReadJSON(&message)
		if readErr != nil {
			if !errors.As(readErr, &firstCloseError) {
				t.Fatalf(
					"read first translation socket: %v; messages=%#v",
					readErr,
					firstMessages,
				)
			}
			break
		}
		firstMessages = append(firstMessages, message)
		index := len(firstMessages) - 1
		if message["message"] == "AddTranslation" {
			translationAt = index
		}
		if message["message"] == "Error" &&
			message["type"] == "accounting_uncertain" {
			accountingAt = index
		}
	}
	if translationAt < 0 || accountingAt <= translationAt {
		t.Fatalf(
			"successful translation was not delivered before accounting close: %#v",
			firstMessages,
		)
	}
	if firstCloseError == nil || firstCloseError.Code != websocket.CloseTryAgainLater {
		t.Fatalf("close error = %#v, want code %d", firstCloseError, websocket.CloseTryAgainLater)
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("provider calls after first socket = %d, want 1", providerCalls.Load())
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if recordCalls != 1 || settleCalls != 1 || refundCalls != 0 {
		t.Fatalf(
			"billing calls after first socket = record:%d settle:%d refund:%d",
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}

	second, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial replay translation socket: %v", err)
	}
	defer second.Close()
	if err := second.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	initializeAccountingTestSocket(t, second)
	sendAccountingTestTranscript(t, second)
	for {
		var message map[string]any
		if readErr := second.ReadJSON(&message); readErr != nil {
			t.Fatalf("read replay translation: %v", readErr)
		}
		if message["message"] != "AddTranslation" {
			continue
		}
		results, ok := message["results"].([]any)
		if !ok || len(results) != 1 {
			t.Fatalf("replay results = %#v", message["results"])
		}
		result, ok := results[0].(map[string]any)
		if !ok ||
			result["request_id"] != "accounting-request-1" ||
			result["content"] != "translated exactly once" {
			t.Fatalf("unexpected replay result: %#v", results[0])
		}
		break
	}
	if providerCalls.Load() != 1 {
		t.Fatalf("replay called provider again: %d", providerCalls.Load())
	}
	recordCalls, settleCalls, refundCalls = ledger.callCounts()
	if recordCalls != 1 || settleCalls != 1 || refundCalls != 0 {
		t.Fatalf(
			"replay changed billing calls = record:%d settle:%d refund:%d",
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}

func TestTranslationRefundFailureIsTerminalWithoutDurableReplay(t *testing.T) {
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		providerCalls.Add(1)
		http.Error(w, "provider rejected request", http.StatusBadRequest)
	}))
	defer provider.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_API_BASE", provider.URL)
	t.Setenv("OPENAI_MODEL", "test-provider-model")
	t.Setenv("OPENAI_USE_RESPONSES", "false")
	t.Setenv("RAG_DB_PATH", filepath.Join(t.TempDir(), "rag.db"))

	ledger := &fakeWebSocketBilling{
		refundErr: errors.New("refund commit outcome is unknown"),
	}
	handler := &WebSocketHandler{
		billing:     ledger,
		connections: newWebSocketConnectionLimiter(8, 8),
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		claims := &auth.UserClaims{
			UserID:   "refund-user",
			TenantID: "refund-tenant",
			Role:     "user",
		}
		ctx := context.WithValue(r.Context(), auth.UserClaimsKey, claims)
		handler.Handle(w, r.WithContext(ctx))
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial refund-failure socket: %v", err)
	}
	if err := first.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	initializeAccountingTestSocket(t, first)
	sendAccountingTestTranscript(t, first)
	var (
		requestFailure map[string]any
		closeError     *websocket.CloseError
	)
	for {
		var message map[string]any
		readErr := first.ReadJSON(&message)
		if readErr != nil {
			if !errors.As(readErr, &closeError) {
				t.Fatalf("read refund-failure socket: %v", readErr)
			}
			break
		}
		if message["message"] == "Error" &&
			message["request_id"] == "accounting-request-1" {
			requestFailure = message
		}
	}
	assertTerminalAccountingFailure(t, requestFailure)
	if closeError == nil || closeError.Code != websocket.CloseTryAgainLater {
		t.Fatalf("close error = %#v, want code %d", closeError, websocket.CloseTryAgainLater)
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if providerCalls.Load() != 1 ||
		recordCalls != 1 ||
		settleCalls != 0 ||
		refundCalls != 1 {
		t.Fatalf(
			"first refund-failure lifecycle = provider:%d record:%d settle:%d refund:%d",
			providerCalls.Load(),
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}

	second, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial refund replay socket: %v", err)
	}
	defer second.Close()
	if err := second.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	initializeAccountingTestSocket(t, second)
	sendAccountingTestTranscript(t, second)
	for {
		var message map[string]any
		if readErr := second.ReadJSON(&message); readErr != nil {
			t.Fatalf("read cached refund failure: %v", readErr)
		}
		if message["message"] == "Error" &&
			message["request_id"] == "accounting-request-1" {
			assertTerminalAccountingFailure(t, message)
			break
		}
	}
	recordCalls, settleCalls, refundCalls = ledger.callCounts()
	if providerCalls.Load() != 1 ||
		recordCalls != 1 ||
		settleCalls != 0 ||
		refundCalls != 1 {
		t.Fatalf(
			"cached refund failure repeated work = provider:%d record:%d settle:%d refund:%d",
			providerCalls.Load(),
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}

func TestSettlementFailureDoesNotCancelOtherReservedProviderWork(t *testing.T) {
	var providerCalls atomic.Int32
	secondStarted := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		call := providerCalls.Add(1)
		switch call {
		case 1:
			select {
			case <-secondStarted:
			case <-time.After(5 * time.Second):
				t.Error("second concurrent provider request did not start")
			}
		case 2:
			close(secondStarted)
			time.Sleep(100 * time.Millisecond)
		default:
			t.Errorf("unexpected provider call %d", call)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-provider-model",
			"choices": []map[string]any{{
				"message": map[string]string{
					"role":    "assistant",
					"content": fmt.Sprintf("translated provider call %d", call),
				},
			}},
			"usage": map[string]int{
				"prompt_tokens":     20,
				"completion_tokens": 4,
				"total_tokens":      24,
			},
		})
	}))
	defer provider.Close()

	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_API_BASE", provider.URL)
	t.Setenv("OPENAI_MODEL", "test-provider-model")
	t.Setenv("OPENAI_USE_RESPONSES", "false")
	t.Setenv("RAG_DB_PATH", filepath.Join(t.TempDir(), "rag.db"))

	ledger := &fakeWebSocketBilling{
		settleErr: errors.New("settlement commit outcome is unknown"),
	}
	handler := &WebSocketHandler{
		billing:     ledger,
		connections: newWebSocketConnectionLimiter(8, 8),
	}
	server := httptest.NewServer(http.HandlerFunc(func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		claims := &auth.UserClaims{
			UserID:   "concurrent-user",
			TenantID: "concurrent-tenant",
			Role:     "user",
		}
		ctx := context.WithValue(r.Context(), auth.UserClaimsKey, claims)
		handler.Handle(w, r.WithContext(ctx))
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial concurrent translation socket: %v", err)
	}
	if err := first.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	initializeAccountingTestSocketWithWorkers(t, first, 2)
	sendAccountingTestTranscriptWith(
		t,
		first,
		"concurrent-request-1",
		"translate the first concurrent request",
		0,
	)
	sendAccountingTestTranscriptWith(
		t,
		first,
		"concurrent-request-2",
		"translate the second concurrent request",
		1,
	)
	var firstCloseError *websocket.CloseError
	for {
		var message map[string]any
		readErr := first.ReadJSON(&message)
		if readErr == nil {
			continue
		}
		if !errors.As(readErr, &firstCloseError) {
			t.Fatalf("read concurrent translation socket: %v", readErr)
		}
		break
	}
	if firstCloseError == nil || firstCloseError.Code != websocket.CloseTryAgainLater {
		t.Fatalf("close error = %#v, want code %d", firstCloseError, websocket.CloseTryAgainLater)
	}

	second, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial concurrent replay socket: %v", err)
	}
	defer second.Close()
	if err := second.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	initializeAccountingTestSocketWithWorkers(t, second, 2)
	sendAccountingTestTranscriptWith(
		t,
		second,
		"concurrent-request-2",
		"translate the second concurrent request",
		1,
	)
	for {
		var message map[string]any
		if readErr := second.ReadJSON(&message); readErr != nil {
			t.Fatalf("read concurrent replay: %v", readErr)
		}
		if message["message"] != "AddTranslation" {
			continue
		}
		results, ok := message["results"].([]any)
		if !ok || len(results) != 1 {
			t.Fatalf("concurrent replay results = %#v", message["results"])
		}
		result, ok := results[0].(map[string]any)
		if !ok || result["request_id"] != "concurrent-request-2" {
			t.Fatalf("unexpected concurrent replay: %#v", results[0])
		}
		break
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if providerCalls.Load() != 2 ||
		recordCalls != 2 ||
		settleCalls != 2 ||
		refundCalls != 0 {
		t.Fatalf(
			"concurrent replay repeated or canceled work = provider:%d record:%d settle:%d refund:%d",
			providerCalls.Load(),
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}

func assertTerminalAccountingFailure(t *testing.T, message map[string]any) {
	t.Helper()
	if message == nil ||
		message["type"] != "accounting_uncertain" ||
		message["retryable"] != false ||
		message["connection_terminal"] != true {
		t.Fatalf("terminal accounting response = %#v", message)
	}
}

func initializeAccountingTestSocket(t *testing.T, conn *websocket.Conn) {
	initializeAccountingTestSocketWithWorkers(t, conn, 1)
}

func initializeAccountingTestSocketWithWorkers(
	t *testing.T,
	conn *websocket.Conn,
	workers int,
) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"type": "init",
		"mode": "ai_rolling",
		"config": map[string]any{
			"session_id":            "11111111-1111-4111-8111-111111111111",
			"min_chunk_chars":       1,
			"translate_workers":     workers,
			"disable_summarization": true,
			"disable_embeddings":    true,
		},
	}); err != nil {
		t.Fatalf("initialize translation socket: %v", err)
	}
	for {
		var message map[string]any
		if err := conn.ReadJSON(&message); err != nil {
			t.Fatalf("read translation initialization: %v", err)
		}
		if message["message"] == "Info" &&
			message["reason"] == "translator initialized" {
			return
		}
	}
}

func sendAccountingTestTranscript(t *testing.T, conn *websocket.Conn) {
	sendAccountingTestTranscriptWith(
		t,
		conn,
		"accounting-request-1",
		"translate this request exactly once",
		0,
	)
}

func sendAccountingTestTranscriptWith(
	t *testing.T,
	conn *websocket.Conn,
	requestID string,
	transcript string,
	startTime float64,
) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"type": "transcript",
		"payload": map[string]any{
			"request_id": requestID,
			"speaker":    "S1",
			"transcript": transcript,
			"start_time": startTime,
			"end_time":   startTime + 1,
		},
	}); err != nil {
		t.Fatalf("send translation transcript: %v", err)
	}
	if err := conn.WriteJSON(map[string]string{"type": "flush"}); err != nil {
		t.Fatalf("flush translation transcript: %v", err)
	}
}
