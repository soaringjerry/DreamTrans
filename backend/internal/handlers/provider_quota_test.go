package handlers

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/dreamtrans/backend/internal/store"
)

type providerQuotaStub struct {
	calls    int
	tenantID string
	userID   string
	err      error
}

func (s *providerQuotaStub) ConsumeAPIRequest(
	_ context.Context,
	tenantID, userID string,
) (store.APIQuotaStatus, error) {
	s.calls++
	s.tenantID = tenantID
	s.userID = userID
	return store.APIQuotaStatus{}, s.err
}

func TestConsumeProviderAPIRequest(t *testing.T) {
	stub := &providerQuotaStub{}
	if err := consumeProviderAPIRequest(
		context.Background(),
		stub,
		"tenant-1",
		"user-1",
	); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 1 || stub.tenantID != "tenant-1" || stub.userID != "user-1" {
		t.Fatalf("unexpected quota call: %#v", stub)
	}
}

func TestConsumeProviderAPIRequestPreservesQuotaError(t *testing.T) {
	stub := &providerQuotaStub{err: store.ErrAPIQuota}
	err := consumeProviderAPIRequest(
		context.Background(),
		stub,
		"tenant-1",
		"user-1",
	)
	if !errors.Is(err, store.ErrAPIQuota) ||
		!strings.Contains(err.Error(), "monthly API quota exceeded") {
		t.Fatalf("unexpected quota error: %v", err)
	}
}

func TestConsumeProviderAPIRequestSkipsUnattributedCalls(t *testing.T) {
	stub := &providerQuotaStub{}
	if err := consumeProviderAPIRequest(context.Background(), stub, "", ""); err != nil {
		t.Fatal(err)
	}
	if stub.calls != 0 {
		t.Fatalf("unattributed request consumed %d quota entries", stub.calls)
	}
}
