package risk

import (
	"encoding/json"
	"net/http"
	"strings"
)

// BrowserSignals are optional, untrusted client observations. No client-supplied
// score or identifier is accepted; the server normalizes and keys correlations.
type BrowserSignals struct {
	Version      int    `json:"version"`
	Platform     string `json:"platform"`
	Language     string `json:"language"`
	Timezone     string `json:"timezone"`
	ScreenWidth  int    `json:"screen_width"`
	ScreenHeight int    `json:"screen_height"`
	Cores        int    `json:"cores"`
	TouchPoints  int    `json:"touch_points"`
	Webdriver    bool   `json:"webdriver"`
}

func family(ua string) string {
	for _, v := range []struct{ token, name string }{{"edg/", "Edge"}, {"firefox/", "Firefox"}, {"fxios/", "Firefox"}, {"chrome/", "Chrome"}, {"crios/", "Chrome"}, {"safari/", "Safari"}} {
		if strings.Contains(ua, v.token) {
			return v.name
		}
	}
	return "Other"
}
func platform(value string) string {
	value = strings.ToLower(value)
	for _, v := range []struct{ token, name string }{{"android", "Android"}, {"iphone", "iOS"}, {"ipad", "iOS"}, {"ios", "iOS"}, {"win", "Windows"}, {"mac", "macOS"}, {"linux", "Linux"}} {
		if strings.Contains(value, v.token) {
			return v.name
		}
	}
	return "Other"
}
func bounded(value string, limit int) string {
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
func bucket(value, step, limit int) int {
	if value < 0 || value > limit {
		return 0
	}
	return value / step * step
}
func (d *Detector) AddBrowserSignals(s *Signals, r *http.Request, b *BrowserSignals) {
	ua := strings.ToLower(bounded(r.UserAgent(), 1024))
	s.Browser, s.Platform = family(ua), platform(ua)
	if ua == "" {
		s.BrowserReasons = append(s.BrowserReasons, "ua_missing")
	}
	automatic := false
	for _, token := range []string{"headlesschrome", "phantomjs", "python-requests", "python-httpx", "curl/", "wget/", "go-http-client", "selenium", "playwright", "puppeteer"} {
		if strings.Contains(ua, token) {
			automatic = true
			break
		}
	}
	if b != nil && b.Webdriver {
		automatic = true
	}
	if automatic {
		s.BrowserReasons = append(s.BrowserReasons, "automation")
	}
	if b == nil || b.Version != 1 {
		s.BrowserReasons = append(s.BrowserReasons, "browser_missing")
		return
	}
	clientPlatform := platform(b.Platform)
	// Android may report Linux; desktop-mode iPads may report macOS.
	compatible := clientPlatform == s.Platform || clientPlatform == "Other" || s.Platform == "Other" || (s.Platform == "Android" && clientPlatform == "Linux") || (s.Platform == "iOS" && clientPlatform == "macOS")
	headerLang := strings.Split(strings.ToLower(r.Header.Get("Accept-Language")), ",")[0]
	headerLang = strings.Split(strings.Split(headerLang, ";")[0], "-")[0]
	clientLang := strings.Split(strings.ToLower(bounded(b.Language, 32)), "-")[0]
	hintPlatform := platform(bounded(r.Header.Get("Sec-CH-UA-Platform"), 64))
	if !compatible || (headerLang != "" && clientLang != "" && headerLang != clientLang) || (hintPlatform != "Other" && s.Platform != "Other" && hintPlatform != s.Platform) {
		s.BrowserReasons = append(s.BrowserReasons, "browser_inconsistent")
	}
	width, height := bucket(b.ScreenWidth, 200, 20000), bucket(b.ScreenHeight, 200, 20000)
	if width > height {
		width, height = height, width
	}
	// No raw UA, timezone, screen dimensions or fingerprint is persisted.
	value, _ := json.Marshal([]any{1, s.Browser, s.Platform, clientLang, bounded(b.Timezone, 64), width, height, bucket(b.Cores, 2, 256), bucket(b.TouchPoints, 1, 20)})
	s.FingerprintHash = d.hash("browser-cohort", string(value))
}
