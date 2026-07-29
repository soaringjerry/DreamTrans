package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLogoutAcceptsTrulyEmptyBody(t *testing.T) {
	handler := &AuthHandler{}
	request := httptest.NewRequest(http.MethodPost, "/api/auth/logout", http.NoBody)
	response := httptest.NewRecorder()

	handler.HandleLogout(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if strings.TrimSpace(response.Body.String()) != `{"success":true}` {
		t.Fatalf("unexpected response: %q", response.Body.String())
	}
}

func TestLogoutRejectsMalformedBody(t *testing.T) {
	handler := &AuthHandler{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/logout",
		strings.NewReader(`{"refresh_token":`),
	)
	response := httptest.NewRecorder()

	handler.HandleLogout(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
}
