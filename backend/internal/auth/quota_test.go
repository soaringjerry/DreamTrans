package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

type quotaStoreStub struct {
	tenant          *models.Tenant
	summary         *models.UsageSummary
	tenantLookupID  string
	summaryTenantID string
	summaryMonthKey string
}

func (s *quotaStoreStub) ConsumeAPIRequest(
	context.Context,
	string,
	string,
) (store.APIQuotaStatus, error) {
	return store.APIQuotaStatus{}, nil
}

func (s *quotaStoreStub) GetTenantByID(
	_ context.Context,
	tenantID string,
) (*models.Tenant, error) {
	s.tenantLookupID = tenantID
	return s.tenant, nil
}

func (s *quotaStoreStub) GetUsageSummary(
	_ context.Context,
	tenantID, monthKey string,
) (*models.UsageSummary, error) {
	s.summaryTenantID = tenantID
	s.summaryMonthKey = monthKey
	return s.summary, nil
}

func (s *quotaStoreStub) CountActiveSessionsByUser(context.Context, string) (int, error) {
	return 0, nil
}

func TestCheckTranscriptionRejectsExhaustedTenantBeforeWebSocketHandler(t *testing.T) {
	quotaStore := &quotaStoreStub{
		tenant: &models.Tenant{
			ID:   "tenant-1",
			Plan: "free",
		},
		summary: &models.UsageSummary{
			TenantID:             "tenant-1",
			TranscriptionMinutes: 60.25,
		},
	}
	downstreamCalled := false
	handler := newQuotaMiddleware(quotaStore).CheckTranscription(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			downstreamCalled = true
			w.WriteHeader(http.StatusSwitchingProtocols)
		},
	))

	request := httptest.NewRequest(http.MethodGet, "/ws/speechmatics", nil)
	claims := &UserClaims{UserID: "user-1", TenantID: "tenant-1"}
	request = request.WithContext(context.WithValue(request.Context(), UserClaimsKey, claims))
	response := httptest.NewRecorder()

	monthBeforeRequest := time.Now().UTC().Format("2006-01")
	handler.ServeHTTP(response, request)
	monthAfterRequest := time.Now().UTC().Format("2006-01")

	if downstreamCalled {
		t.Fatal("quota-exhausted request reached the WebSocket handler")
	}
	if response.Code != http.StatusPaymentRequired {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusPaymentRequired)
	}
	if !strings.Contains(response.Body.String(), `"error":"transcription quota exceeded"`) {
		t.Fatalf("unexpected quota response: %q", response.Body.String())
	}
	if quotaStore.tenantLookupID != claims.TenantID ||
		quotaStore.summaryTenantID != claims.TenantID {
		t.Fatalf(
			"quota lookup escaped claims tenant: tenant=%q summary=%q",
			quotaStore.tenantLookupID,
			quotaStore.summaryTenantID,
		)
	}
	if quotaStore.summaryMonthKey != monthBeforeRequest &&
		quotaStore.summaryMonthKey != monthAfterRequest {
		t.Fatalf("quota used non-current UTC month %q", quotaStore.summaryMonthKey)
	}
}
