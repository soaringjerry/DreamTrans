package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAuthChecksCurrentAccount(t *testing.T) {
	manager, err := NewJWTManagerWithSecrets(
		"access-secret-that-is-at-least-thirty-two-bytes",
		"refresh-secret-that-is-distinct-and-thirty-two-bytes",
	)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateAccessToken("user-1", "tenant-1", "user@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}

	middleware := NewAuthMiddleware(manager)
	validatorCalled := false
	middleware.SetClaimsValidator(func(_ context.Context, claims *UserClaims) error {
		validatorCalled = true
		if claims == nil || claims.UserID != "user-1" {
			t.Fatalf("unexpected claims: %#v", claims)
		}
		return errors.New("account disabled")
	})

	nextCalled := false
	handler := middleware.RequireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		nextCalled = true
	}))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if !validatorCalled {
		t.Fatal("current-account validator was not called")
	}
	if nextCalled {
		t.Fatal("request reached protected handler for disabled account")
	}
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestRequireAuthAddsValidatedClaims(t *testing.T) {
	manager, err := NewJWTManagerWithSecrets(
		"access-secret-that-is-at-least-thirty-two-bytes",
		"refresh-secret-that-is-distinct-and-thirty-two-bytes",
	)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateAccessToken("user-1", "tenant-1", "user@example.com", "admin")
	if err != nil {
		t.Fatal(err)
	}

	middleware := NewAuthMiddleware(manager)
	middleware.SetClaimsValidator(func(context.Context, *UserClaims) error { return nil })
	handler := middleware.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserClaims(r.Context())
		if claims == nil || claims.UserID != "user-1" || claims.Role != "admin" {
			t.Fatalf("unexpected context claims: %#v", claims)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}
