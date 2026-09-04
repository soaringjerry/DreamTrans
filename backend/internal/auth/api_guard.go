// Package auth provides JWT, API-key, role, and quota middleware.
package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type rateWindow struct {
	start time.Time
	count int
	span  time.Duration
}

const maxRateLimitWindows = 4096
const webSocketJWTProtocolPrefix = "dreamtrans.jwt."

// APIGuard protects provider-backed endpoints for both SaaS and explicitly
// configured single-user deployments.
type APIGuard struct {
	jwt            *JWTManager
	apiKey         string
	adminAPIKey    string
	allowAnonymous bool
	allowQueryJWT  bool
	perMinute      int
	validator      func(context.Context, *UserClaims) error

	mu      sync.Mutex
	windows map[string]rateWindow
	lastGC  time.Time
}

func (g *APIGuard) SetClaimsValidator(validator func(context.Context, *UserClaims) error) {
	g.validator = validator
}

func NewAPIGuard(jwtManager *JWTManager) *APIGuard {
	limit := 120
	if value, err := strconv.Atoi(os.Getenv("API_RATE_LIMIT_PER_MINUTE")); err == nil && value > 0 {
		limit = value
	}
	return &APIGuard{
		jwt:            jwtManager,
		apiKey:         strings.TrimSpace(os.Getenv("DREAMTRANS_API_KEY")),
		adminAPIKey:    strings.TrimSpace(os.Getenv("DREAMTRANS_ADMIN_API_KEY")),
		allowAnonymous: envBool("ALLOW_ANONYMOUS_API"),
		allowQueryJWT:  envBool("ALLOW_WEBSOCKET_QUERY_TOKEN"),
		perMinute:      limit,
		windows:        make(map[string]rateWindow),
	}
}

// Protect allows a valid user JWT, a configured service API key, or the
// deliberately enabled anonymous compatibility mode.
func (g *APIGuard) Protect(next http.Handler) http.Handler {
	return g.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, credentialPresent, err := g.withJWTClaims(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "invalid or expired access token")
			return
		}
		if GetUserClaims(request.Context()) != nil || g.matchesAPIKey(request, g.apiKey) {
			next.ServeHTTP(w, request)
			return
		}
		if credentialPresent || hasAPIKey(request) {
			writeAuthError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		if g.allowAnonymous {
			next.ServeHTTP(w, request)
			return
		}
		writeAuthError(w, http.StatusUnauthorized, "authentication required; configure DREAMTRANS_API_KEY for single-user mode")
	}))
}

// RequireAdmin permits admin/super-admin JWTs or a dedicated admin service key.
// Anonymous compatibility mode never grants administrative access.
func (g *APIGuard) RequireAdmin(next http.Handler) http.Handler {
	return g.limit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, credentialPresent, err := g.withJWTClaims(r)
		if err != nil {
			writeAuthError(w, http.StatusUnauthorized, "invalid or expired access token")
			return
		}
		if claims := GetUserClaims(request.Context()); claims != nil {
			if claims.Role != "admin" && claims.Role != "super_admin" {
				writeAuthError(w, http.StatusForbidden, "admin access required")
				return
			}
			next.ServeHTTP(w, request)
			return
		}
		if g.matchesAPIKey(request, g.adminAPIKey) {
			next.ServeHTTP(w, request)
			return
		}
		if credentialPresent || hasAPIKey(request) {
			writeAuthError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		writeAuthError(w, http.StatusUnauthorized, "admin authentication required")
	}))
}

// RequireSuperAdmin permits current super-admin JWTs or the dedicated
// operational admin key.
func (g *APIGuard) RequireSuperAdmin(next http.Handler) http.Handler {
	return g.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if claims := GetUserClaims(r.Context()); claims != nil && claims.Role != "super_admin" {
			writeAuthError(w, http.StatusForbidden, "super admin access required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// RateLimit applies a separate fixed-window limit, useful for login/register.
func (g *APIGuard) RateLimit(next http.Handler, perMinute int) http.Handler {
	if perMinute <= 0 {
		perMinute = 20
	}
	return g.limitWindow(next, perMinute, time.Minute)
}

// RateLimitWindow applies a fixed-window limit over an arbitrary span, for
// example five registrations per hour per address. Windows for different
// spans never share a bucket.
func (g *APIGuard) RateLimitWindow(next http.Handler, limit int, span time.Duration) http.Handler {
	if limit <= 0 {
		limit = 1
	}
	if span <= 0 {
		span = time.Minute
	}
	return g.limitWindow(next, limit, span)
}

func (g *APIGuard) withJWTClaims(r *http.Request) (*http.Request, bool, error) {
	token := ""
	credentialPresent := false
	if header := strings.TrimSpace(r.Header.Get("Authorization")); header != "" {
		credentialPresent = true
		parts := strings.Fields(header)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return r, true, ErrInvalidToken
		}
		token = parts[1]
	} else if isWebSocketUpgrade(r) {
		protocolToken, protocolPresent, err := webSocketJWTFromProtocols(r)
		if err != nil {
			return r, true, ErrInvalidToken
		}
		if protocolPresent {
			credentialPresent = true
			token = protocolToken
		} else {
			queryToken := strings.TrimSpace(r.URL.Query().Get("token"))
			if queryToken == "" {
				return r, credentialPresent, nil
			}
			credentialPresent = true
			// URLs routinely land in reverse-proxy access logs, browser
			// history, and observability systems. The legacy query-token
			// transport is therefore opt-in only; modern browser clients use
			// the Sec-WebSocket-Protocol bearer token above.
			if !g.allowQueryJWT {
				return r, true, ErrInvalidToken
			}
			token = queryToken
		}
	}
	if token == "" {
		return r, credentialPresent, nil
	}
	if g.jwt == nil {
		return r, true, ErrInvalidToken
	}
	claims, err := g.jwt.ValidateAccessToken(token)
	if err != nil {
		return r, true, err
	}
	if g.validator != nil {
		if err := g.validator(r.Context(), claims); err != nil {
			return r, true, ErrInvalidToken
		}
	}
	ctx := context.WithValue(r.Context(), UserClaimsKey, claims)
	return r.WithContext(ctx), true, nil
}

func webSocketJWTFromProtocols(r *http.Request) (string, bool, error) {
	var token string
	present := false
	for _, protocol := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		protocol = strings.TrimSpace(protocol)
		if !strings.HasPrefix(protocol, webSocketJWTProtocolPrefix) {
			continue
		}
		present = true
		candidate := strings.TrimSpace(strings.TrimPrefix(protocol, webSocketJWTProtocolPrefix))
		if candidate == "" || token != "" {
			return "", true, ErrInvalidToken
		}
		token = candidate
	}
	return token, present, nil
}

func (g *APIGuard) matchesAPIKey(r *http.Request, expected string) bool {
	if expected == "" {
		return false
	}
	provided := strings.TrimSpace(r.Header.Get("X-DreamTrans-API-Key"))
	if provided == "" && isWebSocketUpgrade(r) {
		provided = strings.TrimSpace(r.URL.Query().Get("api_key"))
	}
	if provided == "" || len(provided) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func hasAPIKey(r *http.Request) bool {
	return strings.TrimSpace(r.Header.Get("X-DreamTrans-API-Key")) != "" ||
		(isWebSocketUpgrade(r) && strings.TrimSpace(r.URL.Query().Get("api_key")) != "")
}

func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}
	return false
}

func (g *APIGuard) limit(next http.Handler) http.Handler {
	return g.limitWindow(next, g.perMinute, time.Minute)
}

func (g *APIGuard) limitWindow(next http.Handler, limit int, span time.Duration) http.Handler {
	keyPrefix := strconv.Itoa(limit) + "/" + span.String() + "|"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		key := keyPrefix + remoteIP(r)
		g.mu.Lock()
		if g.lastGC.IsZero() || now.Sub(g.lastGC) >= time.Minute {
			for address, value := range g.windows {
				if now.Sub(value.start) >= 2*value.spanOrMinute() {
					delete(g.windows, address)
				}
			}
			g.lastGC = now
		}
		window, exists := g.windows[key]
		if !exists && len(g.windows) >= maxRateLimitWindows {
			// Stop high-cardinality source addresses from growing memory
			// without bound. New addresses share a deliberately restrictive
			// overflow bucket until older windows expire.
			key = keyPrefix + "overflow"
			window, exists = g.windows[key]
			if !exists {
				for oldKey := range g.windows {
					delete(g.windows, oldKey)
					break
				}
			}
		}
		if window.start.IsZero() || now.Sub(window.start) >= span {
			window = rateWindow{start: now, span: span}
		}
		window.count++
		g.windows[key] = window
		allowed := window.count <= limit
		retryAfter := span - now.Sub(window.start)
		g.mu.Unlock()
		if !allowed {
			seconds := int(retryAfter/time.Second) + 1
			if seconds < 1 {
				seconds = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			writeAuthError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (w rateWindow) spanOrMinute() time.Duration {
	if w.span <= 0 {
		return time.Minute
	}
	return w.span
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func envBool(key string) bool {
	value := strings.TrimSpace(os.Getenv(key))
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
