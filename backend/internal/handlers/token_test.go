package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
)

func TestClassicSpeechmaticsTokenIsRejectedInBillingModeByDefault(t *testing.T) {
	t.Setenv("ALLOW_UNMETERED_CLASSIC_TOKEN_WITH_BILLING", "")
	handler := &TokenHandler{billing: &billing.Service{}}
	request := httptest.NewRequest(http.MethodPost, "/api/token/rt", nil)
	request = request.WithContext(context.WithValue(
		request.Context(),
		auth.UserClaimsKey,
		&auth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
	))
	response := httptest.NewRecorder()

	handler.HandleTokenRequest(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
	if !strings.Contains(response.Body.String(), "/ws/speechmatics") {
		t.Fatalf("response does not direct clients to the metered proxy: %q", response.Body.String())
	}
}
