package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIGuardRejectsAnonymousByDefault(t *testing.T) {
	t.Setenv("ALLOW_ANONYMOUS_API", "")
	t.Setenv("DREAMTRANS_API_KEY", "service-key")
	guard := NewAPIGuard(nil)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAPIGuardBoundsRateLimitCardinality(t *testing.T) {
	guard := NewAPIGuard(nil)
	handler := guard.limitAt(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}), 10_000)

	for index := 0; index < maxRateLimitWindows+1000; index++ {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = fmt.Sprintf("client-%d:1234", index)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	if len(guard.windows) > maxRateLimitWindows {
		t.Fatalf("rate window map grew to %d entries", len(guard.windows))
	}
}

func TestAPIGuardAcceptsConfiguredServiceKey(t *testing.T) {
	t.Setenv("ALLOW_ANONYMOUS_API", "")
	t.Setenv("DREAMTRANS_API_KEY", "service-key")
	guard := NewAPIGuard(nil)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-DreamTrans-API-Key", "service-key")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAPIGuardDoesNotTreatWrongKeyAsAnonymous(t *testing.T) {
	t.Setenv("ALLOW_ANONYMOUS_API", "true")
	t.Setenv("DREAMTRANS_API_KEY", "service-key")
	guard := NewAPIGuard(nil)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-DreamTrans-API-Key", "wrong")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAPIGuardRejectsQueryCredentialsOnHTTP(t *testing.T) {
	t.Setenv("ALLOW_ANONYMOUS_API", "")
	t.Setenv("DREAMTRANS_API_KEY", "service-key")
	guard := NewAPIGuard(nil)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/api/rag/stats?api_key=service-key", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAPIGuardAcceptsQueryCredentialsForWebSocketUpgrade(t *testing.T) {
	t.Setenv("ALLOW_ANONYMOUS_API", "")
	t.Setenv("DREAMTRANS_API_KEY", "service-key")
	guard := NewAPIGuard(nil)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/ws/translate?api_key=service-key", nil)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAPIGuardAcceptsJWTWebSocketProtocol(t *testing.T) {
	manager, err := NewJWTManagerWithSecrets(testAccessSecret, testRefreshSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateAccessToken("user-1", "tenant-1", "user@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	guard := NewAPIGuard(manager)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserClaims(r.Context())
		if claims == nil || claims.UserID != "user-1" {
			t.Error("WebSocket protocol JWT did not populate user claims")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/ws/translate", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Protocol", webSocketJWTProtocolPrefix+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAPIGuardRejectsJWTWebSocketQueryTokenByDefault(t *testing.T) {
	t.Setenv("ALLOW_WEBSOCKET_QUERY_TOKEN", "")
	manager, err := NewJWTManagerWithSecrets(testAccessSecret, testRefreshSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateAccessToken("user-1", "tenant-1", "user@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	guard := NewAPIGuard(manager)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/ws/translate?token="+token, nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestAPIGuardAllowsJWTWebSocketQueryTokenOnlyWhenExplicitlyEnabled(t *testing.T) {
	t.Setenv("ALLOW_WEBSOCKET_QUERY_TOKEN", "true")
	manager, err := NewJWTManagerWithSecrets(testAccessSecret, testRefreshSecret)
	if err != nil {
		t.Fatal(err)
	}
	token, err := manager.GenerateAccessToken("user-1", "tenant-1", "user@example.com", "user")
	if err != nil {
		t.Fatal(err)
	}
	guard := NewAPIGuard(manager)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserClaims(r.Context())
		if claims == nil || claims.UserID != "user-1" {
			t.Error("query-token compatibility mode did not populate user claims")
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/ws/translate?token="+token, nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusNoContent)
	}
}

func TestAPIGuardRejectsAmbiguousJWTWebSocketProtocols(t *testing.T) {
	manager, err := NewJWTManagerWithSecrets(testAccessSecret, testRefreshSecret)
	if err != nil {
		t.Fatal(err)
	}
	guard := NewAPIGuard(manager)
	handler := guard.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	request := httptest.NewRequest(http.MethodGet, "/ws/translate", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set(
		"Sec-WebSocket-Protocol",
		webSocketJWTProtocolPrefix+"first, "+webSocketJWTProtocolPrefix+"second",
	)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", response.Code, http.StatusUnauthorized)
	}
}
