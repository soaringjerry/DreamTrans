package risk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSignedSignupDeviceAndNetworkNormalization(t *testing.T) {
	detector, err := NewDetector(strings.Repeat("s", 32), func(r *http.Request) string { return r.Header.Get("Test-IP") })
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://app.test/api/auth/signup-context", nil)
	w := httptest.NewRecorder()
	if err := detector.Prepare(w, r); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	c := cookies[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 2592000 {
		t.Fatalf("cookie attributes: %+v", c)
	}
	r.AddCookie(c)
	r.Header.Set("Test-IP", "2001:db8:1234:5678::1")
	first := detector.Signals(r, "a@example.test")
	r.Header.Set("Test-IP", "2001:db8:1234:5678::9999")
	second := detector.Signals(r, "b@example.test")
	if first.MissingDevice || first.DeviceHash == "" || first.DeviceHash != second.DeviceHash || first.NetworkHash != second.NetworkHash || first.EmailHash == second.EmailHash {
		t.Fatalf("signals: %+v %+v", first, second)
	}
	tampered := httptest.NewRequest(http.MethodPost, "https://app.test/api/auth/register", nil)
	tampered.AddCookie(&http.Cookie{Name: CookieName, Value: c.Value + "x"})
	if !detector.Signals(tampered, "a@example.test").MissingDevice {
		t.Fatal("forged cookie accepted")
	}
	missing := detector.Signals(httptest.NewRequest(http.MethodPost, "https://app.test/", nil), "a@example.test")
	if !missing.MissingDevice || missing.NetworkHash != "" {
		t.Fatalf("missing signals: %+v", missing)
	}
	other, _ := NewDetector(strings.Repeat("x", 32), func(*http.Request) string { return "127.0.0.1" })
	if !other.Signals(r, "a@example.test").MissingDevice {
		t.Fatal("cookie valid under wrong key")
	}
}
