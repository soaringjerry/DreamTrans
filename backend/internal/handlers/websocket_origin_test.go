package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/gorilla/websocket"
)

func TestWebSocketOriginPolicy(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.example")

	sameOrigin := httptest.NewRequest("GET", "http://service.example/ws", nil)
	sameOrigin.Host = "service.example"
	sameOrigin.Header.Set("Origin", "http://service.example")
	if !websocketOriginAllowed(sameOrigin) {
		t.Fatal("same-origin websocket was rejected")
	}

	allowed := httptest.NewRequest("GET", "http://service.example/ws", nil)
	allowed.Host = "service.example"
	allowed.Header.Set("Origin", "https://app.example")
	if !websocketOriginAllowed(allowed) {
		t.Fatal("configured origin was rejected")
	}

	rejected := httptest.NewRequest("GET", "http://service.example/ws", nil)
	rejected.Host = "service.example"
	rejected.Header.Set("Origin", "https://evil.example")
	if websocketOriginAllowed(rejected) {
		t.Fatal("unconfigured cross-origin websocket was allowed")
	}

	authenticated := httptest.NewRequest("GET", "http://internal:8080/ws", nil)
	authenticated.Host = "internal:8080"
	authenticated.Header.Set("Origin", "https://public.example")
	authenticated = authenticated.WithContext(context.WithValue(
		authenticated.Context(),
		auth.UserClaimsKey,
		&auth.UserClaims{UserID: "user-1", TenantID: "tenant-1"},
	))
	if !websocketOriginAllowed(authenticated) {
		t.Fatal("authenticated websocket was rejected after reverse proxy Host rewrite")
	}

	malformedAuthenticated := authenticated.Clone(authenticated.Context())
	malformedAuthenticated.Header = authenticated.Header.Clone()
	malformedAuthenticated.Header.Set("Origin", "null")
	if websocketOriginAllowed(malformedAuthenticated) {
		t.Fatal("malformed authenticated websocket origin was allowed")
	}
}

func TestWebSocketJWTProtocolCompletesUpgrade(t *testing.T) {
	manager, err := auth.NewJWTManagerWithSecrets(
		"websocket-access-secret-longer-than-thirty-two-bytes",
		"websocket-refresh-secret-different-and-long-enough",
	)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateAccessToken(
		"user-1",
		"tenant-1",
		"user@example.com",
		"user",
	)
	if err != nil {
		t.Fatal(err)
	}

	receivedClaims := make(chan *auth.UserClaims, 1)
	guard := auth.NewAPIGuard(manager)
	server := httptest.NewServer(guard.Protect(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			conn, upgradeErr := upgrader.Upgrade(w, r, nil)
			if upgradeErr != nil {
				return
			}
			defer conn.Close()
			receivedClaims <- auth.GetUserClaims(r.Context())
		},
	)))
	defer server.Close()

	dialer := websocket.Dialer{
		HandshakeTimeout: 5 * time.Second,
		Subprotocols: []string{
			webSocketApplicationProtocol,
			"dreamtrans.jwt." + token,
		},
	}
	conn, response, err := dialer.Dial(
		"ws"+strings.TrimPrefix(server.URL, "http"),
		http.Header{"Origin": []string{"https://public.example"}},
	)
	if err != nil {
		if response != nil {
			t.Fatalf("WebSocket dial failed with HTTP %d: %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	_ = conn.Close()
	if conn.Subprotocol() != webSocketApplicationProtocol {
		t.Fatalf(
			"unexpected negotiated subprotocol: got %q, want %q",
			conn.Subprotocol(),
			webSocketApplicationProtocol,
		)
	}
	if negotiated := response.Header.Get("Sec-WebSocket-Protocol"); negotiated != webSocketApplicationProtocol {
		t.Fatalf("unexpected response subprotocol header: %q", negotiated)
	}
	if strings.Contains(response.Header.Get("Sec-WebSocket-Protocol"), token) {
		t.Fatal("JWT was reflected in the WebSocket response protocol")
	}

	select {
	case claims := <-receivedClaims:
		if claims == nil || claims.UserID != "user-1" {
			t.Fatalf("unexpected claims after upgrade: %#v", claims)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for authenticated WebSocket upgrade")
	}
}
