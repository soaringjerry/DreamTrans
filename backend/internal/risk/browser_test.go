package risk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBrowserSignalsAreKeyedCoarseAndUntrusted(t *testing.T) {
	d, _ := NewDetector(strings.Repeat("x", 32), func(_ *http.Request) string { return "198.51.100.1" })
	r := httptest.NewRequest("POST", "https://app.test/api/auth/register", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) Chrome/140.0.0.0 Safari/537.36")
	r.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	b := &BrowserSignals{Version: 1, Platform: "Linux x86_64", Language: "zh-CN", Timezone: "Asia/Shanghai", ScreenWidth: 1920, ScreenHeight: 1080, Cores: 8}
	s := d.Signals(r, "a@test.example")
	d.AddBrowserSignals(s, r, b)
	if s.FingerprintHash == "" || len(s.BrowserReasons) != 0 || s.Browser != "Chrome" {
		t.Fatalf("normal browser: %+v", s)
	}
	b.ScreenWidth = 1900
	other := d.Signals(r, "b@test.example")
	d.AddBrowserSignals(other, r, b)
	if s.FingerprintHash != other.FingerprintHash {
		t.Fatal("coarse screen changed cohort")
	}
	b.Webdriver = true
	b.Platform = "Win32"
	automated := d.Signals(r, "c@test.example")
	d.AddBrowserSignals(automated, r, b)
	reasons := strings.Join(automated.BrowserReasons, ",")
	if !strings.Contains(reasons, "automation") || !strings.Contains(reasons, "browser_inconsistent") {
		t.Fatalf("automation missing: %s", reasons)
	}
	missing := d.Signals(r, "d@test.example")
	d.AddBrowserSignals(missing, r, nil)
	if missing.FingerprintHash != "" {
		t.Fatal("missing data created shared fingerprint")
	}
	r.Header.Set("User-Agent", "curl/8.0")
	tool := d.Signals(r, "e@test.example")
	d.AddBrowserSignals(tool, r, nil)
	if !strings.Contains(strings.Join(tool.BrowserReasons, ","), "automation") {
		t.Fatal("tool UA missed")
	}
}
