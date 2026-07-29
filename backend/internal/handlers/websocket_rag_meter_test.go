package handlers

import (
	"errors"
	"testing"

	"github.com/dreamtrans/backend/internal/rag"
	"github.com/dreamtrans/backend/internal/store"
)

func TestWebSocketRAGMeterConsumesAPIQuotaBeforeBillingReservation(t *testing.T) {
	quota := &providerQuotaStub{err: store.ErrAPIQuota}
	ledger := &fakeWebSocketBilling{}
	sessionID := "session-1"
	meter := &websocketRAGUsageMeter{
		apiQuota:  quota,
		billing:   ledger,
		tenantID:  "tenant-1",
		userID:    "user-1",
		sessionID: &sessionID,
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), rag.ProviderUsage{
		Action:      "embedding",
		Model:       "embedding-model",
		InputTokens: 4096,
	})
	if reservation != nil || !errors.Is(err, store.ErrAPIQuota) {
		t.Fatalf("reservation/error = %#v/%v, want quota rejection", reservation, err)
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if quota.calls != 1 || recordCalls != 0 || settleCalls != 0 || refundCalls != 0 {
		t.Fatalf(
			"quota/billing calls = %d/%d/%d/%d",
			quota.calls,
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}

func TestWebSocketRAGMeterSettlesTheOriginalReservation(t *testing.T) {
	quota := &providerQuotaStub{}
	ledger := &fakeWebSocketBilling{}
	sessionID := "session-1"
	meter := &websocketRAGUsageMeter{
		apiQuota:  quota,
		billing:   ledger,
		tenantID:  "tenant-1",
		userID:    "user-1",
		sessionID: &sessionID,
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), rag.ProviderUsage{
		Action:      "embedding",
		Model:       "embedding-model",
		InputTokens: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.Settle(t.Context(), rag.ProviderUsage{
		Action:      "embedding",
		Model:       "embedding-model",
		InputTokens: 12,
	}); err != nil {
		t.Fatal(err)
	}

	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if quota.calls != 1 || recordCalls != 1 || settleCalls != 1 || refundCalls != 0 {
		t.Fatalf(
			"quota/billing calls = %d/%d/%d/%d",
			quota.calls,
			recordCalls,
			settleCalls,
			refundCalls,
		)
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

func TestWebSocketRAGMeterSupportsQuotaWithoutBilling(t *testing.T) {
	quota := &providerQuotaStub{}
	meter := &websocketRAGUsageMeter{
		apiQuota: quota,
		tenantID: "tenant-1",
		userID:   "user-1",
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), rag.ProviderUsage{
		Action:      "embedding",
		InputTokens: 10,
	})
	if err != nil || reservation == nil {
		t.Fatalf("reservation/error = %#v/%v", reservation, err)
	}
	if err := reservation.Settle(t.Context(), rag.ProviderUsage{
		Action:      "embedding",
		InputTokens: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if quota.calls != 1 {
		t.Fatalf("quota calls = %d, want 1", quota.calls)
	}
}

func TestWebSocketRAGMeterDoesNotBillCustomerFundedProviderUsage(t *testing.T) {
	quota := &providerQuotaStub{}
	ledger := &fakeWebSocketBilling{}
	meter := &websocketRAGUsageMeter{
		apiQuota: quota,
		billing:  ledger,
		tenantID: "tenant-1",
		userID:   "user-1",
	}

	reservation, err := meter.ReserveProviderUsage(t.Context(), rag.ProviderUsage{
		Action:         "embedding",
		InputTokens:    100,
		CustomerFunded: true,
	})
	if err != nil || reservation == nil {
		t.Fatalf("reservation/error = %#v/%v", reservation, err)
	}
	if err := reservation.Settle(t.Context(), rag.ProviderUsage{
		Action:         "embedding",
		InputTokens:    20,
		CustomerFunded: true,
	}); err != nil {
		t.Fatal(err)
	}
	recordCalls, settleCalls, refundCalls := ledger.callCounts()
	if quota.calls != 1 || recordCalls != 0 || settleCalls != 0 || refundCalls != 0 {
		t.Fatalf(
			"quota/billing calls = %d/%d/%d/%d",
			quota.calls,
			recordCalls,
			settleCalls,
			refundCalls,
		)
	}
}
