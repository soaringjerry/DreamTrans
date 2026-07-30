package handlers

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/gorilla/websocket"
)

type fakeWebSocketBilling struct {
	mu sync.Mutex

	recordCalls int
	settleCalls int
	refundCalls int

	recordErr error
	settleErr error
	refundErr error
	duplicate bool

	lastReservation billing.UsageRecord
	lastSettlement  billing.UsageRecord
	lastKey         string
}

func (f *fakeWebSocketBilling) CanUsePaidFeatures(context.Context, string) (bool, error) {
	return true, nil
}

func (f *fakeWebSocketBilling) RecordUsage(
	_ context.Context,
	record *billing.UsageRecord,
) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordCalls++
	f.lastReservation = *record
	f.lastKey = record.IdempotencyKey
	record.IdempotencyDuplicate = f.duplicate
	if f.recordErr != nil {
		return 0, f.recordErr
	}
	return 1.25, nil
}

func TestStableRealtimeReservationRefusesUnreplayableDuplicate(t *testing.T) {
	ledger := &fakeWebSocketBilling{duplicate: true}
	_, err := reserveRealtimeUsageWithID(
		t.Context(),
		ledger,
		"ws-translation:",
		"request-stable",
		&billing.UsageRecord{
			UserID: "user", TenantID: "tenant", Action: "translation",
			Model: "server-model", InputTokens: 100, OutputTokens: 1_000,
		},
	)
	if !errors.Is(err, errRealtimeUsageAlreadyRecorded) {
		t.Fatalf("duplicate stable reservation would re-run provider work: %v", err)
	}
}

func (f *fakeWebSocketBilling) SettleUsageReservation(
	_ context.Context,
	key string,
	record *billing.UsageRecord,
) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settleCalls++
	f.lastSettlement = *record
	f.lastKey = key
	if f.settleErr != nil {
		return 0, f.settleErr
	}
	return 0.25, nil
}

func (f *fakeWebSocketBilling) RefundUsage(
	_ context.Context,
	key string,
	_ string,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refundCalls++
	f.lastKey = key
	return f.refundErr
}

func (f *fakeWebSocketBilling) GetUserBalance(
	context.Context,
	string,
) (*billing.UserBalance, error) {
	return &billing.UserBalance{}, nil
}

func (f *fakeWebSocketBilling) callCounts() (record, settle, refund int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.recordCalls, f.settleCalls, f.refundCalls
}

func TestRealtimeUsageReservationSettlesExactlyOnceConcurrently(t *testing.T) {
	ledger := &fakeWebSocketBilling{}
	reservation, err := reserveRealtimeUsage(
		t.Context(),
		ledger,
		"ws-test:",
		&billing.UsageRecord{
			UserID: "user", TenantID: "tenant", Action: "translation",
			Model: "server-model", InputTokens: 100, OutputTokens: 1_000,
		},
	)
	if err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	if !strings.HasPrefix(ledger.lastKey, "ws-test:") || ledger.lastReservation.IdempotencyKey == "" {
		t.Fatalf("reservation did not receive a scoped idempotency key: %#v", ledger.lastReservation)
	}

	const callers = 24
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, settleErr := reservation.settle(&billing.UsageRecord{
				UserID: "user", TenantID: "tenant", Action: "translation",
				Model: "server-model", InputTokens: 10, OutputTokens: 20,
			})
			errs <- settleErr
		}()
	}
	wg.Wait()
	close(errs)
	for settleErr := range errs {
		if settleErr != nil {
			t.Fatalf("concurrent settlement failed: %v", settleErr)
		}
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if recordCalls != 1 || settleCalls != 1 || refundCalls != 0 {
		t.Fatalf(
			"reservation lifecycle calls = record:%d settle:%d refund:%d",
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}

func TestRealtimeUsageReservationRefundsExactlyOnceConcurrently(t *testing.T) {
	ledger := &fakeWebSocketBilling{}
	reservation, err := reserveRealtimeUsage(
		t.Context(),
		ledger,
		"ws-test:",
		&billing.UsageRecord{
			UserID: "user", TenantID: "tenant", Action: "embedding", InputTokens: 100,
		},
	)
	if err != nil {
		t.Fatalf("reserve usage: %v", err)
	}

	const callers = 24
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- reservation.refund("provider failed")
		}()
	}
	wg.Wait()
	close(errs)
	for refundErr := range errs {
		if refundErr != nil {
			t.Fatalf("concurrent refund failed: %v", refundErr)
		}
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if recordCalls != 1 || settleCalls != 0 || refundCalls != 1 {
		t.Fatalf(
			"reservation lifecycle calls = record:%d settle:%d refund:%d",
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}

func TestRealtimeUsageFailedSettlementRemainsCharged(t *testing.T) {
	ledger := &fakeWebSocketBilling{settleErr: errors.New("insufficient balance")}
	reservation, err := reserveRealtimeUsage(
		t.Context(),
		ledger,
		"ws-test:",
		&billing.UsageRecord{
			UserID: "user", TenantID: "tenant", Action: "summarize",
			InputTokens: 100, OutputTokens: 1_000,
		},
	)
	if err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	if _, err := reservation.settle(&billing.UsageRecord{
		UserID: "user", TenantID: "tenant", Action: "summarize",
		InputTokens: 100, OutputTokens: 2_000,
	}); err == nil {
		t.Fatal("failed settlement was accepted")
	}
	if err := reservation.refund("must not refund consumed upstream work"); err == nil {
		t.Fatal("failed settlement reservation was refunded")
	}
	_, settleCalls, refundCalls := ledger.callCounts()
	if settleCalls != 1 || refundCalls != 0 {
		t.Fatalf("settle/refund calls = %d/%d", settleCalls, refundCalls)
	}
}

func TestMeteredWebSocketSanitizesClientModelOverrides(t *testing.T) {
	classicConfig := &clientConfig{
		Model:                "gpt-5-mini",
		TranslateModel:       "gpt-5-mini",
		SummaryModel:         "gpt-5",
		SessionID:            "classic-session",
		MinChunkChars:        32,
		DisableSummarization: true,
	}
	if err := validateClientConfig(classicConfig); err != nil {
		t.Fatalf("legacy validation should preserve model compatibility: %v", err)
	}

	sanitized, adjusted := sanitizeMeteredClientConfig(classicConfig)
	if !adjusted {
		t.Fatal("Classic-shaped model overrides were not marked as adjusted")
	}
	if sanitized == classicConfig {
		t.Fatal("sanitization mutated the caller-owned config instead of copying it")
	}
	if sanitized.Model != "" || sanitized.TranslateModel != "" || sanitized.SummaryModel != "" {
		t.Fatalf("model overrides survived sanitization: %#v", sanitized)
	}
	if sanitized.SessionID != classicConfig.SessionID ||
		sanitized.MinChunkChars != classicConfig.MinChunkChars ||
		sanitized.DisableSummarization != classicConfig.DisableSummarization {
		t.Fatalf("non-model Classic config was not preserved: %#v", sanitized)
	}
	if classicConfig.Model == "" ||
		classicConfig.TranslateModel == "" ||
		classicConfig.SummaryModel == "" {
		t.Fatalf("caller-owned config was modified: %#v", classicConfig)
	}

	state := defaultConnState()
	serverTranslateModel := state.selectedModelTranslate
	serverSummaryModel := state.selectedModelSummary
	state.applyConfig(sanitized)
	if state.selectedModelTranslate != serverTranslateModel ||
		state.selectedModelSummary != serverSummaryModel {
		t.Fatalf(
			"sanitized config changed server models: translate=%q summary=%q",
			state.selectedModelTranslate,
			state.selectedModelSummary,
		)
	}

	modelFree := &clientConfig{MinChunkChars: 64}
	unchanged, adjusted := sanitizeMeteredClientConfig(modelFree)
	if adjusted || unchanged != modelFree {
		t.Fatal("model-free config should pass through without an allocation")
	}
	if sanitized, adjusted := sanitizeMeteredClientConfig(nil); adjusted || sanitized != nil {
		t.Fatal("nil config should pass through unchanged")
	}
}

func TestMeteredWebSocketClassicInitProcessesTranscript(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	handler := &WebSocketHandler{
		billing:     &fakeWebSocketBilling{},
		connections: newWebSocketConnectionLimiter(4, 4),
	}
	serverDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := &auth.UserClaims{
			UserID:   "classic-user",
			TenantID: "classic-tenant",
			Email:    "classic@example.test",
			Role:     "user",
		}
		ctx := context.WithValue(r.Context(), auth.UserClaimsKey, claims)
		handler.Handle(w, r.WithContext(ctx))
		close(serverDone)
	}))
	defer server.Close()

	client, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"),
		nil,
	)
	if err != nil {
		t.Fatalf("dial metered translation WebSocket: %v", err)
	}
	if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set test WebSocket deadline: %v", err)
	}

	if err := client.WriteJSON(map[string]interface{}{
		"type": "init",
		"mode": "ai_rolling",
		"config": map[string]interface{}{
			"rolling_window_chars":  1000,
			"model":                 "gpt-5-mini",
			"translate_model":       "gpt-5-mini",
			"summary_model":         "gpt-5",
			"session_id":            "classic-session",
			"disable_summarization": true,
			"embeddings_enabled":    true,
		},
	}); err != nil {
		t.Fatalf("send Classic init: %v", err)
	}

	type serverMessage struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Reason  string `json:"reason"`
		Workers int    `json:"workers"`
	}
	seenAdjusted := false
	seenInitialized := false
	negotiatedWorkers := 0
	for range 2 {
		var message serverMessage
		if err := client.ReadJSON(&message); err != nil {
			t.Fatalf("read Classic init response: %v", err)
		}
		if message.Message == "Error" {
			t.Fatalf("Classic init was rejected: %#v", message)
		}
		if message.Message == "Info" && message.Type == "config_adjusted" {
			seenAdjusted = true
		}
		if message.Message == "Info" && message.Reason == "translator initialized" {
			seenInitialized = true
			negotiatedWorkers = message.Workers
		}
	}
	if !seenAdjusted || !seenInitialized || negotiatedWorkers < 1 || negotiatedWorkers > 8 {
		t.Fatalf(
			"Classic init responses invalid: adjusted=%v initialized=%v workers=%d",
			seenAdjusted,
			seenInitialized,
			negotiatedWorkers,
		)
	}

	if err := client.WriteJSON(map[string]interface{}{
		"type": "transcript",
		"payload": map[string]interface{}{
			"speaker":    "S1",
			"transcript": "this accepted transcript must reach the translation worker",
			"start_time": 0.0,
			"end_time":   1.0,
		},
	}); err != nil {
		t.Fatalf("send transcript after adjusted init: %v", err)
	}
	if err := client.WriteJSON(map[string]string{"type": "flush"}); err != nil {
		t.Fatalf("flush transcript after adjusted init: %v", err)
	}

	seenTranscriptWork := false
	seenFlush := false
	for range 2 {
		var message serverMessage
		if err := client.ReadJSON(&message); err != nil {
			t.Fatalf("read transcript processing response: %v", err)
		}
		if message.Message == "Error" && strings.Contains(message.Reason, "OPENAI_API_KEY not set") {
			// The deliberately missing provider key fails inside the translation
			// worker. Reaching this point proves the transcript was accepted
			// after the adjusted init instead of being dropped as uninitialized.
			seenTranscriptWork = true
		}
		if message.Message == "Info" && message.Reason == "pending buffers flushed" {
			seenFlush = true
		}
	}
	if !seenTranscriptWork || !seenFlush {
		t.Fatalf(
			"transcript path did not complete: worker=%v flush=%v",
			seenTranscriptWork,
			seenFlush,
		)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close test WebSocket: %v", err)
	}
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("metered translation WebSocket did not shut down")
	}
}

func TestOrderedTranslationResultsAdvancesPastFailure(t *testing.T) {
	queue := newOrderedTranslationResults()

	if ready := queue.Add(&translateResult{seq: 2, content: "second"}); len(ready) != 0 {
		t.Fatalf("out-of-order result was delivered early: %#v", ready)
	}

	failure := errors.New("first request failed")
	ready := queue.Add(&translateResult{seq: 1, err: failure})
	if len(ready) != 2 {
		t.Fatalf("expected failure and following success, got %d results", len(ready))
	}
	if !errors.Is(ready[0].err, failure) {
		t.Fatalf("first result should be the sequence failure: %#v", ready[0])
	}
	if ready[1].seq != 2 || ready[1].content != "second" {
		t.Fatalf("later sequence did not advance after failure: %#v", ready[1])
	}
}

func TestProviderTranslationFailureRetriesOnlyAfterSafeTransientRefund(t *testing.T) {
	transient := errors.New("openai api error: status 503")
	retryable := classifyProviderTranslationFailure(
		translateResult{requestID: "transient", err: transient},
		transient,
		nil,
	)
	if !retryable.retryable || retryable.errorType != "translation_processing" ||
		retryable.retryAfterMs <= 0 {
		t.Fatalf("refunded transient failure was not retryable: %#v", retryable)
	}

	permanent := errors.New("openai api error: status 400")
	terminal := classifyProviderTranslationFailure(
		translateResult{requestID: "permanent", err: permanent},
		permanent,
		nil,
	)
	if terminal.retryable || terminal.errorType != "" {
		t.Fatalf("permanent provider failure became retryable: %#v", terminal)
	}

	refundFailure := classifyProviderTranslationFailure(
		translateResult{requestID: "ambiguous-refund", err: transient},
		transient,
		errors.New("refund commit is ambiguous"),
	)
	if refundFailure.retryable || refundFailure.errorType != "" {
		t.Fatalf("ambiguous refund became retryable: %#v", refundFailure)
	}

	overload := markTranslationProcessing(translateResult{
		requestID: "overload",
		err:       errors.New("queue full"),
	})
	if !overload.retryable || overload.errorType != "translation_processing" {
		t.Fatalf("queue overload was terminal: %#v", overload)
	}
}

func TestTranslationRequestRegistryDeduplicatesAndReplaysByID(t *testing.T) {
	var registry translationRequestRegistry
	now := time.Now()
	entry, disposition := registry.Begin("user\x00session\x00request-1", "fingerprint", now)
	if disposition != translationRequestOwner || entry == nil {
		t.Fatalf("first request should own provider work: %v %#v", disposition, entry)
	}
	duplicate, disposition := registry.Begin(
		"user\x00session\x00request-1",
		"fingerprint",
		now.Add(time.Millisecond),
	)
	if disposition != translationRequestDuplicate || duplicate != entry {
		t.Fatalf("concurrent retry did not join original work: %v %#v", disposition, duplicate)
	}

	expected := translateResult{
		requestID: "request-1",
		content:   "cached translation",
		model:     "model",
	}
	registry.Complete(
		"user\x00session\x00request-1",
		entry,
		expected,
		now.Add(10*time.Millisecond),
	)
	replayed, ok := registry.Wait(t.Context(), duplicate)
	if !ok || replayed.content != expected.content || replayed.requestID != expected.requestID {
		t.Fatalf("completed retry did not replay the cached result: %#v %v", replayed, ok)
	}

	cached, disposition := registry.Begin(
		"user\x00session\x00request-1",
		"fingerprint",
		now.Add(time.Second),
	)
	if disposition != translationRequestDuplicate || cached != entry {
		t.Fatalf("completed result was not retained for reconnect: %v %#v", disposition, cached)
	}
	if _, disposition := registry.Begin(
		"user\x00session\x00request-1",
		"different-content",
		now.Add(time.Second),
	); disposition != translationRequestConflict {
		t.Fatalf("reused request ID with different content was accepted: %v", disposition)
	}
}

func TestTranslationRequestRegistryNeverStealsValidExecutingOwner(t *testing.T) {
	if translationRequestInFlightTTL <=
		translationEndToEndBudget+10*time.Second {
		t.Fatalf(
			"in-flight TTL %s must exceed execution plus settlement grace %s",
			translationRequestInFlightTTL,
			translationEndToEndBudget+10*time.Second,
		)
	}
	if translationBarrierTimeout <
		translationEndToEndBudget+10*time.Second {
		t.Fatalf(
			"barrier %s cannot cover execution plus settlement grace %s",
			translationBarrierTimeout,
			translationEndToEndBudget+10*time.Second,
		)
	}
	if translationDurableStaleAfter <=
		translationEndToEndBudget+10*time.Second {
		t.Fatalf(
			"durable stale threshold %s can steal a settling owner with budget %s",
			translationDurableStaleAfter,
			translationEndToEndBudget+10*time.Second,
		)
	}

	var registry translationRequestRegistry
	startedAt := time.Now()
	entry, disposition := registry.Begin("scoped-request", "fingerprint", startedAt)
	if disposition != translationRequestOwner {
		t.Fatalf("first claim disposition = %v, want owner", disposition)
	}
	duplicate, disposition := registry.Begin(
		"scoped-request",
		"fingerprint",
		startedAt.Add(translationEndToEndBudget+10*time.Second),
	)
	if disposition != translationRequestDuplicate || duplicate != entry {
		t.Fatalf("valid executing owner was replaced: %v %#v", disposition, duplicate)
	}

	registry.Complete(
		"scoped-request",
		entry,
		translateResult{
			err:       errors.New("durable owner is still processing"),
			retryable: true,
		},
		startedAt.Add(time.Second),
	)
	retry, disposition := registry.Begin(
		"scoped-request",
		"fingerprint",
		startedAt.Add(2*time.Second),
	)
	if disposition != translationRequestOwner || retry == entry {
		t.Fatalf("retryable completion did not release local ownership: %v %#v", disposition, retry)
	}
}

func TestTranslationReservationIDIsStableAndScoped(t *testing.T) {
	first := translationReservationID("tenant\x00user\x00session\x00request")
	if first == "" || first != translationReservationID("tenant\x00user\x00session\x00request") {
		t.Fatalf("reservation ID is not deterministic: %q", first)
	}
	if first == translationReservationID("tenant\x00other-user\x00session\x00request") {
		t.Fatal("reservation ID is not scoped to the authenticated owner")
	}
	if len("ws-translation:"+first) > 255 {
		t.Fatalf("reservation ID exceeds billing storage: %d", len(first))
	}
}

func TestFlushPendingAfterSilence(t *testing.T) {
	state := defaultConnState()
	state.flushGapSeconds = 0.5
	state.paragraphWindowSeconds = 10
	state.ragFlushGapSeconds = 0.5

	if flushed, _, _, _ := state.handleAggregation("S1", "short tail", 1, 2); flushed {
		t.Fatal("incomplete segment should wait for the silence timer")
	}
	if flushed, _, _, _ := state.handleRAGAggregation("S1", "useful short tail", 1, 2); flushed {
		t.Fatal("small RAG segment should wait for the silence timer")
	}

	state.mu.Lock()
	updatedAt := state.speakers["S1"].updatedAt
	state.mu.Unlock()
	paragraphs, ragParagraphs := state.flushPending(updatedAt.Add(600*time.Millisecond), false)

	if len(paragraphs) != 1 || paragraphs[0].text != "short tail" {
		t.Fatalf("short translation tail was not flushed: %#v", paragraphs)
	}
	if len(ragParagraphs) != 1 || ragParagraphs[0].text != "useful short tail" {
		t.Fatalf("short RAG tail was not flushed: %#v", ragParagraphs)
	}

	paragraphs, ragParagraphs = state.flushPending(updatedAt.Add(time.Second), false)
	if len(paragraphs) != 0 || len(ragParagraphs) != 0 {
		t.Fatalf("already drained buffers were emitted twice: %#v %#v", paragraphs, ragParagraphs)
	}
}

func TestForceFlushCombinesSentenceAndTail(t *testing.T) {
	state := defaultConnState()
	state.maxSentences = 10
	state.paragraphWindowSeconds = 30

	flushed, text, start, end := state.handleAggregation("S1", "First.", 0, 1)
	if !flushed {
		t.Fatal("punctuated sentence should leave aggregation immediately")
	}
	if emitted, _, _, _ := state.enqueueSentence("S1", text, start, end); emitted {
		t.Fatal("single sentence should still be in the paragraph batch")
	}
	if flushed, _, _, _ := state.handleAggregation("S1", "final words", 1, 2); flushed {
		t.Fatal("incomplete final words should remain buffered")
	}

	paragraphs, _ := state.flushPending(time.Now(), true)
	if len(paragraphs) != 1 {
		t.Fatalf("expected one forced paragraph, got %#v", paragraphs)
	}
	if paragraphs[0].text != "First. final words" {
		t.Fatalf("forced flush lost or split the tail: %q", paragraphs[0].text)
	}
}

func TestWorkerCountIsBounded(t *testing.T) {
	state := defaultConnState()
	state.applyConfig(&clientConfig{TranslateWorkers: 1000})
	if got := state.workerCount(); got != 8 {
		t.Fatalf("worker count should be capped at 8, got %d", got)
	}
}

func TestValidateClientMessageBounds(t *testing.T) {
	mode := modeAIRolling
	valid := &clientMessage{
		Type: "init",
		Mode: &mode,
		Config: &clientConfig{
			RollingWindowChars: 1000,
			TranslateWorkers:   3,
			FlushGapSeconds:    0.5,
			SessionID:          "session-1",
		},
	}
	if err := validateClientMessage(valid); err != nil {
		t.Fatalf("valid init rejected: %v", err)
	}

	tooManyWorkers := *valid
	configCopy := *valid.Config
	configCopy.TranslateWorkers = 9
	tooManyWorkers.Config = &configCopy
	if err := validateClientMessage(&tooManyWorkers); err == nil {
		t.Fatal("unbounded worker count was accepted")
	}

	tooLongTranscript := &clientMessage{
		Type: "transcript",
		Payload: &clientPayload{
			Speaker: "S1", Transcript: strings.Repeat("x", maxTranscriptRunes+1),
			StartTime: 1, EndTime: 2,
		},
	}
	if err := validateClientMessage(tooLongTranscript); err == nil {
		t.Fatal("oversized transcript was accepted")
	}

	badTimestamp := &clientMessage{
		Type: "transcript",
		Payload: &clientPayload{
			Speaker: "S1", Transcript: "hello", StartTime: math.NaN(), EndTime: 2,
		},
	}
	if err := validateClientMessage(badTimestamp); err == nil {
		t.Fatal("non-finite timestamp was accepted")
	}

	invalidRequestID := &clientMessage{
		Type: "transcript",
		Payload: &clientPayload{
			RequestID: "contains a space",
			Speaker:   "S1", Transcript: "hello", StartTime: 1, EndTime: 2,
		},
	}
	if err := validateClientMessage(invalidRequestID); err == nil {
		t.Fatal("unsafe translation request ID was accepted")
	}
}

func TestResetSessionContextClearsConversationState(t *testing.T) {
	state := defaultConnState()
	state.sessionID = "old"
	state.recentBuffer = "old transcript"
	state.recentSegments = []string{"old transcript"}
	state.summary = "old summary"
	state.summaryBacklog.WriteString("old summary backlog")
	state.recentTranslated = []string{"旧翻译"}
	state.lastSummaryAt = time.Now()
	state.speakers["S1"] = &aggState{buffer: "tail"}
	state.paragraphs["S1"] = &paraState{list: []sentence{{text: "sentence"}}}
	state.ragBuffers["S1"] = &ragState{buffer: "rag"}

	state.resetSessionContext(" new ")

	sessionID, _ := state.sessionSnapshot()
	if sessionID != "new" {
		t.Fatalf("session id was not normalized: %q", sessionID)
	}
	if state.recentBuffer != "" || len(state.recentSegments) != 0 ||
		state.summary != "" || state.summaryBacklog.Len() != 0 || len(state.recentTranslated) != 0 ||
		!state.lastSummaryAt.IsZero() || len(state.speakers) != 0 ||
		len(state.paragraphs) != 0 || len(state.ragBuffers) != 0 {
		t.Fatal("old session context survived reset")
	}
}

func TestSequenceProgressWaitsForContiguousCompletion(t *testing.T) {
	progress := newSequenceProgress()
	progress.Mark(2)
	if got := progress.Current(); got != 0 {
		t.Fatalf("out-of-order completion advanced barrier to %d", got)
	}
	progress.Mark(1)
	if got := progress.Current(); got != 2 {
		t.Fatalf("contiguous completion should advance to 2, got %d", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if progress.Wait(ctx, 3) {
		t.Fatal("barrier unexpectedly completed missing sequence 3")
	}
}

func TestSummaryBacklogTruncationPreservesUTF8(t *testing.T) {
	state := defaultConnState()
	state.summaryMaxBacklogChars = 3
	state.restoreSummaryBacklog("你好世界")

	got := state.summaryBacklog.String()
	if !utf8.ValidString(got) {
		t.Fatalf("summary backlog is invalid UTF-8: %q", got)
	}
	if got != "好世界" {
		t.Fatalf("expected last three runes, got %q", got)
	}
}

func TestConnectionContextBuffersAreBounded(t *testing.T) {
	state := defaultConnState()
	state.rollingWindowChars = 3
	for i := 0; i < maxRecentContextSegments+10; i++ {
		state.addSegmentEN("你好")
	}
	if len(state.recentSegments) != maxRecentContextSegments {
		t.Fatalf("recent segment history grew to %d", len(state.recentSegments))
	}
	if !utf8.ValidString(state.recentBuffer) ||
		utf8.RuneCountInString(state.recentBuffer) > state.rollingWindowChars {
		t.Fatalf("rolling context is not valid/bounded UTF-8: %q", state.recentBuffer)
	}

	for i := 0; i < maxSpeakersPerConnection; i++ {
		if !state.acceptSpeaker(string(rune('A' + i))) {
			t.Fatalf("speaker %d was rejected before the limit", i)
		}
	}
	if state.acceptSpeaker("one-too-many") {
		t.Fatal("speaker cardinality limit was not enforced")
	}
}

func TestAggregationBuffersAreBoundedUnderContinuousInput(t *testing.T) {
	state := defaultConnState()
	segment := strings.Repeat("界", 4096)
	translationFlushes := 0
	ragFlushes := 0
	for range 40 {
		if flushed, _, _, _ := state.handleAggregation("S1", segment, 1, 1); flushed {
			translationFlushes++
		}
		if flushed, _, _, _ := state.handleRAGAggregation("S1", segment, 1, 1); flushed {
			ragFlushes++
		}
	}
	state.mu.Lock()
	translationBuffer := state.speakers["S1"].buffer
	ragBuffer := state.ragBuffers["S1"].buffer
	state.mu.Unlock()

	if translationFlushes == 0 || ragFlushes == 0 {
		t.Fatalf(
			"hard caps did not force flushes: translation=%d RAG=%d",
			translationFlushes,
			ragFlushes,
		)
	}
	for name, buffer := range map[string]string{
		"translation": translationBuffer,
		"RAG":         ragBuffer,
	} {
		if !utf8.ValidString(buffer) ||
			utf8.RuneCountInString(buffer) > maxAggregationBufferRunes {
			t.Fatalf("%s buffer escaped hard cap: runes=%d", name, utf8.RuneCountInString(buffer))
		}
	}
}

func TestAudioUsageMeterUsesForwardedRawBytes(t *testing.T) {
	meter := &audioUsageMeter{}
	start := []byte(`{
		"message":"StartRecognition",
		"audio_format":{"type":"raw","encoding":"pcm_f32le","sample_rate":48000}
	}`)
	isStart, err := meter.ConfigureStartRecognition(start)
	if err != nil || !isStart {
		t.Fatalf("configure raw audio meter: isStart=%v err=%v", isStart, err)
	}
	if err := meter.AddForwardedBytes(48000 * 4 * 60); err != nil {
		t.Fatalf("add one minute of audio: %v", err)
	}
	snapshot, ok := meter.Pending()
	if !ok || math.Abs(snapshot.minutes-1) > 1e-9 {
		t.Fatalf("expected exactly one audio minute, got %#v ok=%v", snapshot, ok)
	}
	meter.Commit(snapshot)
	if _, ok := meter.Pending(); ok {
		t.Fatal("committed audio was billed twice")
	}
	if err := meter.AddForwardedBytes(48000 * 4 * 30); err != nil {
		t.Fatalf("add another half minute: %v", err)
	}
	snapshot, ok = meter.Pending()
	if !ok || math.Abs(snapshot.minutes-0.5) > 1e-9 {
		t.Fatalf("expected half an audio minute, got %#v ok=%v", snapshot, ok)
	}
}

func TestAudioUsageMeterRejectsUnmeteredFormats(t *testing.T) {
	meter := &audioUsageMeter{}
	if err := meter.AddForwardedBytes(1024); err == nil {
		t.Fatal("audio before StartRecognition was accepted")
	}
	isStart, err := meter.ConfigureStartRecognition([]byte(`{
		"message":"StartRecognition",
		"audio_format":{"type":"file","encoding":"pcm_f32le","sample_rate":48000}
	}`))
	if !isStart || err == nil {
		t.Fatalf("non-raw audio should be rejected: isStart=%v err=%v", isStart, err)
	}
}

func TestNamespacedRAGSessionID(t *testing.T) {
	first := namespacedRAGSessionID("tenant-a", "user-a", "same")
	second := namespacedRAGSessionID("tenant-a", "user-b", "same")
	if first == second {
		t.Fatal("different users received the same RAG namespace")
	}
	if got := namespacedRAGSessionID("", "", "same"); got != "anonymous/session/same" {
		t.Fatalf("unexpected anonymous namespace: %q", got)
	}
}

func TestSafeWebSocketConnSerializesConcurrentWrites(t *testing.T) {
	const (
		writers   = 12
		perWriter = 20
	)

	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		conn := newSafeWebSocketConn(raw)
		defer conn.Close()

		var wg sync.WaitGroup
		writeErrors := make(chan error, writers)
		for writer := 0; writer < writers; writer++ {
			writer := writer
			wg.Add(1)
			go func() {
				defer wg.Done()
				for message := 0; message < perWriter; message++ {
					if writeErr := conn.WriteJSON(map[string]int{
						"writer": writer,
						"index":  message,
					}); writeErr != nil {
						select {
						case writeErrors <- writeErr:
						default:
						}
						return
					}
				}
			}()
		}
		wg.Wait()
		select {
		case writeErr := <-writeErrors:
			serverDone <- writeErr
		default:
			serverDone <- nil
		}
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial test WebSocket: %v", err)
	}
	defer client.Close()

	for index := 0; index < writers*perWriter; index++ {
		var message map[string]int
		if readErr := client.ReadJSON(&message); readErr != nil {
			t.Fatalf("read message %d: %v", index, readErr)
		}
		if _, ok := message["writer"]; !ok {
			t.Fatalf("malformed message %d: %v", index, message)
		}
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("concurrent writer failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for concurrent writers")
	}
}

func TestTranslationWebSocketReadDeadlineClosesSilentPeer(t *testing.T) {
	serverDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		if err := configureTranslationReadLiveness(conn, 75*time.Millisecond); err != nil {
			serverDone <- err
			return
		}
		_, _, readErr := conn.ReadMessage()
		if readErr == nil {
			serverDone <- errors.New("silent peer read unexpectedly succeeded")
			return
		}
		serverDone <- nil
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial test WebSocket: %v", err)
	}
	defer client.Close()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("silent peer was not released by the read deadline")
	}
}
