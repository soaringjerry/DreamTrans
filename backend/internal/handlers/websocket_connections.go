package handlers

import (
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	internalAuth "github.com/dreamtrans/backend/internal/auth"
)

const (
	defaultWebSocketMaxConnections             = 256
	defaultWebSocketMaxConnectionsPerPrincipal = 4
	maxConfigurableWebSocketConnections        = 100_000
)

type webSocketConnectionLimiter struct {
	mu              sync.Mutex
	maxTotal        int
	maxPerPrincipal int
	total           int
	byPrincipal     map[string]int
}

var (
	sharedWebSocketLimiterOnce sync.Once
	sharedWebSocketLimiter     *webSocketConnectionLimiter
)

func getSharedWebSocketConnectionLimiter() *webSocketConnectionLimiter {
	sharedWebSocketLimiterOnce.Do(func() {
		maxTotal := positiveWebSocketLimitFromEnv(
			"WEBSOCKET_MAX_CONNECTIONS",
			defaultWebSocketMaxConnections,
		)
		maxPerPrincipal := positiveWebSocketLimitFromEnv(
			"WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL",
			defaultWebSocketMaxConnectionsPerPrincipal,
		)
		if maxPerPrincipal > maxTotal {
			maxPerPrincipal = maxTotal
		}
		sharedWebSocketLimiter = newWebSocketConnectionLimiter(maxTotal, maxPerPrincipal)
	})
	return sharedWebSocketLimiter
}

func positiveWebSocketLimitFromEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 || value > maxConfigurableWebSocketConnections {
		log.Printf(
			"invalid %s; using %d (valid range: 1-%d)",
			name,
			fallback,
			maxConfigurableWebSocketConnections,
		)
		return fallback
	}
	return value
}

func newWebSocketConnectionLimiter(maxTotal, maxPerPrincipal int) *webSocketConnectionLimiter {
	if maxTotal <= 0 {
		maxTotal = defaultWebSocketMaxConnections
	}
	if maxPerPrincipal <= 0 {
		maxPerPrincipal = defaultWebSocketMaxConnectionsPerPrincipal
	}
	if maxPerPrincipal > maxTotal {
		maxPerPrincipal = maxTotal
	}
	return &webSocketConnectionLimiter{
		maxTotal:        maxTotal,
		maxPerPrincipal: maxPerPrincipal,
		byPrincipal:     make(map[string]int),
	}
}

// acquire reserves one slot and returns an idempotent release callback.
func (l *webSocketConnectionLimiter) acquire(principal string) (func(), bool) {
	if l == nil {
		return func() {}, true
	}
	principal = strings.TrimSpace(principal)
	if principal == "" {
		principal = "unknown"
	}

	l.mu.Lock()
	if l.total >= l.maxTotal || l.byPrincipal[principal] >= l.maxPerPrincipal {
		l.mu.Unlock()
		return nil, false
	}
	l.total++
	l.byPrincipal[principal]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.total > 0 {
				l.total--
			}
			if count := l.byPrincipal[principal]; count <= 1 {
				delete(l.byPrincipal, principal)
			} else {
				l.byPrincipal[principal] = count - 1
			}
		})
	}, true
}

func webSocketPrincipal(r *http.Request, claims *internalAuth.UserClaims) string {
	if claims != nil && strings.TrimSpace(claims.UserID) != "" {
		return "user:" + strings.TrimSpace(claims.TenantID) + ":" + strings.TrimSpace(claims.UserID)
	}
	if r != nil {
		if strings.TrimSpace(r.Header.Get("X-DreamTrans-API-Key")) != "" ||
			strings.TrimSpace(r.URL.Query().Get("api_key")) != "" {
			// The API guard already validated the configured service key before
			// a protected handler is reached. Never retain the secret itself.
			return "service-api-key"
		}
		host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
		if err == nil && host != "" {
			return "ip:" + host
		}
		if address := strings.TrimSpace(r.RemoteAddr); address != "" {
			return "ip:" + address
		}
	}
	return "anonymous"
}

func acquireWebSocketConnection(
	w http.ResponseWriter,
	r *http.Request,
	claims *internalAuth.UserClaims,
	limiter *webSocketConnectionLimiter,
) (func(), bool) {
	release, ok := limiter.acquire(webSocketPrincipal(r, claims))
	if ok {
		return release, true
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "10")
	http.Error(
		w,
		`{"error":"too many active WebSocket connections"}`,
		http.StatusTooManyRequests,
	)
	return nil, false
}
