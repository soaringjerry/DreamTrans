package auth

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientIPTrustBoundary(t *testing.T) {
	for _, tc := range []struct{ name, trusted, peer, forwarded, want string }{
		{"disabled", "", "127.0.0.1:42", "192.0.2.1", "127.0.0.1"},
		{"untrusted peer", "127.0.0.1/32", "192.0.2.2:42", "192.0.2.1", "192.0.2.2"},
		{"trusted peer", "127.0.0.1/32", "127.0.0.1:42", "192.0.2.1", "192.0.2.1"},
		{"forged prefix", "127.0.0.1/32", "127.0.0.1:42", "203.0.113.1, 192.0.2.1", "192.0.2.1"},
		{"multiple proxies", "127.0.0.1/32,10.0.0.0/24", "127.0.0.1:42", "203.0.113.1, 192.0.2.1, 10.0.0.2", "192.0.2.1"},
		{"ipv6", "::1/128", "[::1]:42", "2001:db8::1", "2001:db8::1"},
		{"mapped peer", "127.0.0.1/32", "[::ffff:127.0.0.1]:42", "::ffff:192.0.2.1", "192.0.2.1"},
		{"malformed", "127.0.0.1/32", "127.0.0.1:42", "192.0.2.1, invalid", "127.0.0.1"},
		{"missing", "127.0.0.1/32", "127.0.0.1:42", "", "127.0.0.1"},
		{"bad config", "127.0.0.1/32,bad", "127.0.0.1:42", "192.0.2.1", "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &APIGuard{trustedProxies: parseTrustedProxies(tc.trusted)}
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			if got := g.clientIP(r); got != tc.want {
				t.Fatalf("clientIP=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestRateLimitSeparatesTrustedProxyClients(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.1/32")
	g := NewAPIGuard(nil)
	h := g.RateLimitWindow(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), 1, time.Hour)
	for index, client := range []int{1, 2, 3, 4, 5, 6, 1} {
		r := httptest.NewRequest("POST", "/api/auth/register", nil)
		r.RemoteAddr = "127.0.0.1:42"
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("192.0.2.%d", client))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := http.StatusNoContent
		if index == 6 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("client %d: status=%d, want %d", client, w.Code, want)
		}
	}
}
