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

	"github.com/dreamtrans/backend/internal/billing"
	"github.com/gorilla/websocket"
)

type speechmaticsBillingStub struct {
	mu              sync.Mutex
	recordErr       error
	recorded        []billing.UsageRecord
	settled         []billing.UsageRecord
	settlementKeys  []string
	settlementCtxOK bool
}

func (s *speechmaticsBillingStub) CanUsePaidFeatures(context.Context, string) (bool, error) {
	return true, nil
}

func (s *speechmaticsBillingStub) RecordUsage(
	_ context.Context,
	record *billing.UsageRecord,
) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record != nil {
		s.recorded = append(s.recorded, *record)
	}
	if s.recordErr != nil {
		return 0, s.recordErr
	}
	return record.Quantity, nil
}

func (s *speechmaticsBillingStub) SettleUsageReservation(
	ctx context.Context,
	key string,
	actual *billing.UsageRecord,
) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settlementCtxOK = ctx.Err() == nil
	s.settlementKeys = append(s.settlementKeys, key)
	if actual != nil {
		s.settled = append(s.settled, *actual)
		return actual.Quantity, nil
	}
	return 0, nil
}

func (s *speechmaticsBillingStub) GetUserBalance(
	context.Context,
	string,
) (*billing.UserBalance, error) {
	return nil, nil
}

func (s *speechmaticsBillingStub) snapshot() (
	[]billing.UsageRecord,
	[]billing.UsageRecord,
	[]string,
	bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]billing.UsageRecord(nil), s.recorded...),
		append([]billing.UsageRecord(nil), s.settled...),
		append([]string(nil), s.settlementKeys...),
		s.settlementCtxOK
}

func configureTestAudioMeter(t *testing.T, meter *audioUsageMeter) {
	t.Helper()
	isStart, err := meter.ConfigureStartRecognition([]byte(`{
		"message":"StartRecognition",
		"audio_format":{"type":"raw","encoding":"pcm_s16le","sample_rate":16000}
	}`))
	if err != nil || !isStart {
		t.Fatalf("configure test audio meter: isStart=%v err=%v", isStart, err)
	}
}

func TestAudioUsageReservationRequiresPrepaidCoverageAndSettlesExactTail(t *testing.T) {
	meter := &audioUsageMeter{}
	configureTestAudioMeter(t, meter)

	const oneSecondBytes = 16000 * 2
	if err := meter.AddReservedForwardedBytes(oneSecondBytes); err == nil {
		t.Fatal("audio without an up-front reservation was accepted")
	}

	reservation, err := meter.ReserveNextBytes(oneSecondBytes, "connection-1")
	if err != nil {
		t.Fatalf("reserve first audio frame: %v", err)
	}
	if reservation == nil {
		t.Fatal("first audio frame did not create a reservation")
	}
	wantReservedMinutes := speechmaticsReservationPeriod.Minutes()
	if math.Abs(reservation.minutes-wantReservedMinutes) > 1e-12 {
		t.Fatalf("reserved %v minutes, want %v", reservation.minutes, wantReservedMinutes)
	}
	meter.ConfirmReservation(reservation.key)
	if err := meter.AddReservedForwardedBytes(oneSecondBytes); err != nil {
		t.Fatalf("commit covered audio: %v", err)
	}
	if another, reserveErr := meter.ReserveNextBytes(oneSecondBytes, "connection-1"); reserveErr != nil {
		t.Fatalf("reuse existing reservation: %v", reserveErr)
	} else if another != nil {
		t.Fatal("audio inside the prepaid window created a duplicate reservation")
	}

	settlements := meter.PendingSettlements()
	if len(settlements) != 1 {
		t.Fatalf("got %d settlement tails, want 1", len(settlements))
	}
	wantActualMinutes := time.Second.Minutes()
	if settlements[0].key != reservation.key ||
		math.Abs(settlements[0].minutes-wantActualMinutes) > 1e-12 {
		t.Fatalf("unexpected exact settlement: %#v", settlements[0])
	}
}

func TestSpeechmaticsDisconnectSettlesReservationWithDetachedContext(t *testing.T) {
	stub := &speechmaticsBillingStub{}
	handler := &SpeechmaticsProxyHandler{billing: stub}
	meter := &audioUsageMeter{}
	configureTestAudioMeter(t, meter)

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	if err := handler.reserveSpeechmaticsAudio(
		requestCtx,
		nil,
		meter,
		"disconnect-1",
		"user-1",
		"tenant-1",
		16000*2,
	); err != nil {
		t.Fatalf("reserve usage: %v", err)
	}
	if err := meter.AddReservedForwardedBytes(16000 * 2); err != nil {
		t.Fatalf("commit one second: %v", err)
	}
	cancelRequest()

	handler.settleSpeechmaticsReservations(nil, meter, "user-1", "tenant-1")

	recorded, settled, keys, settlementCtxOK := stub.snapshot()
	if len(recorded) != 1 || len(settled) != 1 || len(keys) != 1 {
		t.Fatalf(
			"unexpected billing calls: reservations=%d settlements=%d keys=%d",
			len(recorded),
			len(settled),
			len(keys),
		)
	}
	if !settlementCtxOK {
		t.Fatal("client cancellation propagated into usage settlement")
	}
	if math.Abs(recorded[0].Quantity-speechmaticsReservationPeriod.Minutes()) > 1e-12 {
		t.Fatalf("unexpected prepaid quantity: %v", recorded[0].Quantity)
	}
	if math.Abs(settled[0].Quantity-time.Second.Minutes()) > 1e-12 {
		t.Fatalf("short session was not settled exactly: %v", settled[0].Quantity)
	}
	if keys[0] != recorded[0].IdempotencyKey {
		t.Fatalf("settled key %q, want %q", keys[0], recorded[0].IdempotencyKey)
	}
}

func TestSpeechmaticsChargeFailureStopsRecognitionBeforeUpstreamWrite(t *testing.T) {
	upstreamMessages := make(chan int, 4)
	upstreamDone := make(chan struct{})
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			close(upstreamDone)
			return
		}
		defer conn.Close()
		defer close(upstreamDone)
		for {
			messageType, _, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			upstreamMessages <- messageType
		}
	}))
	defer upstreamServer.Close()

	upstreamURL := "ws" + strings.TrimPrefix(upstreamServer.URL, "http")
	upstreamConn, _, err := websocket.DefaultDialer.Dial(upstreamURL, nil)
	if err != nil {
		t.Fatalf("dial fake Speechmatics: %v", err)
	}
	safeUpstreamConn := newSafeWebSocketConn(upstreamConn)
	defer safeUpstreamConn.Close()

	stub := &speechmaticsBillingStub{recordErr: errors.New("insufficient balance")}
	handler := &SpeechmaticsProxyHandler{billing: stub}
	recognitionQuotaCalls := 0
	proxyDone := make(chan error, 1)
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientConn, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr != nil {
			proxyDone <- upgradeErr
			return
		}
		defer clientConn.Close()

		meter := &audioUsageMeter{}
		errChan := make(chan error, 1)
		handler.proxyClientToSpeechmatics(
			r.Context(),
			clientConn,
			safeUpstreamConn,
			errChan,
			meter,
			true,
			func(ctx context.Context, count int) error {
				return handler.reserveSpeechmaticsAudio(
					ctx,
					nil,
					meter,
					"tiny-balance-1",
					"user-1",
					"tenant-1",
					count,
				)
			},
			func(context.Context) error {
				recognitionQuotaCalls++
				return nil
			},
		)
		proxyDone <- <-errChan
	}))
	defer proxyServer.Close()

	proxyURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http")
	clientConn, _, err := websocket.DefaultDialer.Dial(proxyURL, nil)
	if err != nil {
		t.Fatalf("dial proxy test endpoint: %v", err)
	}
	defer clientConn.Close()

	start := []byte(`{
		"message":"StartRecognition",
		"audio_format":{"type":"raw","encoding":"pcm_s16le","sample_rate":16000}
	}`)
	if err := clientConn.WriteMessage(websocket.TextMessage, start); err != nil {
		t.Fatalf("write StartRecognition: %v", err)
	}
	select {
	case proxyErr := <-proxyDone:
		if proxyErr == nil || !strings.Contains(proxyErr.Error(), "usage charge failed") {
			t.Fatalf("unexpected proxy result: %v", proxyErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy did not stop after the failed initial usage reservation")
	}
	if err := safeUpstreamConn.Close(); err != nil {
		t.Fatalf("close fake Speechmatics connection: %v", err)
	}
	select {
	case <-upstreamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fake Speechmatics reader did not stop")
	}

	select {
	case messageType := <-upstreamMessages:
		t.Fatalf("unpaid StartRecognition message type %d reached Speechmatics", messageType)
	default:
	}
	recorded, _, _, _ := stub.snapshot()
	if len(recorded) != 1 {
		t.Fatalf("got %d reservation attempts, want 1", len(recorded))
	}
	if recognitionQuotaCalls != 1 {
		t.Fatalf("got %d recognition API quota calls, want 1", recognitionQuotaCalls)
	}
}
