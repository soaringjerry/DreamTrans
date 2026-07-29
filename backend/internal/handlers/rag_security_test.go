package handlers

import (
	"context"
	"testing"
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
