package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type pingerFunc func(context.Context) error

func (fn pingerFunc) PingContext(ctx context.Context) error {
	return fn(ctx)
}

func TestProbeHandler(t *testing.T) {
	t.Run("liveness", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()
		probeHandler(nil).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.String() != "ok\n" {
			t.Fatalf("unexpected liveness response: status=%d body=%q", rec.Code, rec.Body.String())
		}
	})

	t.Run("readiness failure", func(t *testing.T) {
		expected := errors.New("database unavailable")
		pinger := pingerFunc(func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("database probe has no deadline")
			}
			return expected
		})
		req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
		rec := httptest.NewRecorder()
		probeHandler(pinger).ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("unexpected readiness status: got %d, want %d", rec.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("head and method guard", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodHead, "/healthz", nil)
		rec := httptest.NewRecorder()
		probeHandler(nil).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
			t.Fatalf("unexpected HEAD response: status=%d body=%q", rec.Code, rec.Body.String())
		}

		req = httptest.NewRequest(http.MethodPost, "/healthz", nil)
		rec = httptest.NewRecorder()
		probeHandler(nil).ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Fatalf("unexpected method response: status=%d allow=%q", rec.Code, rec.Header().Get("Allow"))
		}
	})
}

func TestSafePublicPathConfinesRequests(t *testing.T) {
	publicDir := filepath.Join(t.TempDir(), "public")
	for _, requestPath := range []string{
		"/assets/app.js",
		"/../../etc/passwd",
		`/..\..\windows\system.ini`,
		"//absolute-looking",
	} {
		candidate, ok := safePublicPath(publicDir, requestPath)
		if !ok {
			t.Fatalf("safePublicPath rejected sanitizable path %q", requestPath)
		}
		relative, err := filepath.Rel(publicDir, candidate)
		if err != nil {
			t.Fatalf("relative path for %q: %v", requestPath, err)
		}
		if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			t.Fatalf("request %q escaped public root as %q", requestPath, candidate)
		}
	}
}

func TestNewBootstrapAdministratorReceivesInitialCredit(t *testing.T) {
	user := newBootstrapAdministrator(
		"tenant-1",
		"admin@example.test",
		"password-hash",
		100,
	)
	if user.TenantID != "tenant-1" ||
		user.Email != "admin@example.test" ||
		user.Role != "super_admin" ||
		!user.IsActive ||
		!user.EmailVerified ||
		user.Dreampoints != 100 {
		t.Fatalf("unexpected bootstrap administrator: %#v", user)
	}
}
