package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeadersModes(t *testing.T) {
	for _, mode := range []string{"", "report-only", "invalid", "enforce"} {
		t.Setenv("CSP_MODE", mode)
		w := httptest.NewRecorder()
		securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })).ServeHTTP(w, httptest.NewRequest("GET", "/pro", nil))
		active, inactive := "Content-Security-Policy-Report-Only", "Content-Security-Policy"
		if mode == "enforce" {
			active, inactive = inactive, active
		}
		if w.Header().Get(active) != contentSecurityPolicy || w.Header().Get(inactive) != "" {
			t.Fatalf("mode %q: %v", mode, w.Header())
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Code != 204 {
			t.Fatalf("response=%v", w)
		}
	}
}

func TestCSPReportBounds(t *testing.T) {
	for _, tc := range []struct {
		method, body string
		status       int
	}{
		{"GET", "", 405}, {"POST", "invalid", 400},
		{"POST", `{"csp-report":{"effective-directive":"script-src","document-uri":"https://app.test/?verify=secret"}}`, 204},
		{"POST", `{"csp-report":{"effective-directive":"` + strings.Repeat("x", 17000) + `"}}`, 400},
	} {
		w := httptest.NewRecorder()
		handleCSPReport(w, httptest.NewRequest(tc.method, "/api/security/csp-report", strings.NewReader(tc.body)))
		if w.Code != tc.status {
			t.Fatalf("status=%d, want %d", w.Code, tc.status)
		}
	}
}
