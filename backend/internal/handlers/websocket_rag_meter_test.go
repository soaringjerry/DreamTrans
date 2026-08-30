package handlers

import (
	"testing"

	"github.com/dreamtrans/backend/internal/rag"
)

func TestWebSocketRAGMeterSettlesTheOriginalReservation(t *testing.T) {
	ledger := &fakeWebSocketBilling{}
	sessionID := "session-1"
	meter := &websocketRAGUsageMeter{
		billing:   ledger,
		tenantID:  "tenant-1",
		userID:    "user-1",
		sessionID: &sessionID,
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), &rag.ProviderUsage{
		Action:      "embedding",
		Model:       "embedding-model",
		InputTokens: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.Settle(t.Context(), &rag.ProviderUsage{
		Action:      "embedding",
		Model:       "embedding-model",
		InputTokens: 12,
	}); err != nil {
		t.Fatal(err)
	}

	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if recordCalls != 1 || settleCalls != 1 || refundCalls != 0 {
		t.Fatalf("billing calls = %d/%d/%d", recordCalls, settleCalls, refundCalls)
	}
	if ledger.lastReservation.SessionID == nil ||
		*ledger.lastReservation.SessionID != "session-1" ||
		ledger.lastReservation.InputTokens != 4096 ||
		ledger.lastSettlement.InputTokens != 12 ||
		ledger.lastKey == "" {
		t.Fatalf(
			"unexpected reservation/settlement: %#v %#v key=%q",
			ledger.lastReservation,
			ledger.lastSettlement,
			ledger.lastKey,
		)
	}
}

func TestWebSocketRAGMeterRecordsCustomerFundedServiceFeeUsage(t *testing.T) {
	ledger := &fakeWebSocketBilling{}
	meter := &websocketRAGUsageMeter{
		billing:  ledger,
		tenantID: "tenant-1",
		userID:   "user-1",
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), &rag.ProviderUsage{
		Action:         "embedding",
		InputTokens:    100,
		CustomerFunded: true,
	})
	if err != nil || reservation == nil {
		t.Fatalf("reservation/error = %#v/%v", reservation, err)
	}
	if err := reservation.Settle(t.Context(), &rag.ProviderUsage{
		Action:         "embedding",
		InputTokens:    20,
		CustomerFunded: true,
	}); err != nil {
		t.Fatal(err)
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if recordCalls != 1 || settleCalls != 1 || refundCalls != 0 {
		t.Fatalf("billing calls = %d/%d/%d", recordCalls, settleCalls, refundCalls)
	}
	if !ledger.lastReservation.CustomerFunded || !ledger.lastSettlement.CustomerFunded {
		t.Fatalf(
			"customer-funded attribution was not preserved: %#v %#v",
			ledger.lastReservation,
			ledger.lastSettlement,
		)
	}
}
