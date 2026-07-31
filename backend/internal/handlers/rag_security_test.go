package handlers

import (
	"context"
	"net/http"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
)

func TestAllowedUserAPIBaseUsesExplicitHTTPSAllowlist(t *testing.T) {
	t.Setenv("OPENAI_API_BASE", "https://api.openai.com/v1")
	t.Setenv("USER_API_BASE_ALLOWLIST", "https://llm.example/v1")

	for _, value := range []string{
		"https://api.openai.com/v1",
		"https://llm.example/custom/path",
	} {
		if !allowedUserAPIBase(value) {
			t.Fatalf("expected %q to be allowed", value)
		}
	}
	for _, value := range []string{
		"http://llm.example/v1",
		"https://127.0.0.1/v1",
		"https://llm.example.evil.invalid/v1",
		"https://user:password@llm.example/v1",
	} {
		if allowedUserAPIBase(value) {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}

func TestRAGModelOverrideRequiresUserAPIKey(t *testing.T) {
	handler := &RAGHandler{}
	if err := handler.validateOverrides(context.Background(), &askConfig{
		Model: "unpriced-expensive-model",
	}); err == nil {
		t.Fatal("server-funded model override was accepted")
	}
	if err := handler.validateOverrides(context.Background(), &askConfig{
		Prompt: "custom prompt",
	}); err != nil {
		t.Fatalf("prompt-only override was rejected: %v", err)
	}

	t.Setenv("ALLOW_USER_API_KEY", "true")
	if err := handler.validateOverrides(context.Background(), &askConfig{
		APIKey: "request-scoped-key",
		Model:  "user-funded-model",
	}); err != nil {
		t.Fatalf("user-funded model override was rejected: %v", err)
	}
}

func TestValidateContextSessionAccess(t *testing.T) {
	claims := &auth.UserClaims{UserID: "user-1", TenantID: "tenant-1"}
	if status, err := validateContextSessionAccess(nil, claims); status !=
		http.StatusNotFound || err == nil {
		t.Fatalf("missing session = (%d, %v), want 404", status, err)
	}
	session := &models.Session{UserID: "user-1", TenantID: "tenant-1"}
	if status, err := validateContextSessionAccess(
		session,
		claims,
	); status != http.StatusOK || err != nil {
		t.Fatalf("owned session = (%d, %v), want 200", status, err)
	}
	session.UserID = "another-user"
	if status, err := validateContextSessionAccess(
		session,
		claims,
	); status != http.StatusForbidden || err == nil {
		t.Fatalf("foreign session = (%d, %v), want 403", status, err)
	}
}
