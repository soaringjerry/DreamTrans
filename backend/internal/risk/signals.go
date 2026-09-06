// Package risk assesses self-registration and gates free signup rewards.
package risk

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const CookieName = "dt_signup_device"
const deviceLifetime = 30 * 24 * time.Hour

// Detector uses a server-signed random first-party cookie, not fingerprinting.
type Detector struct {
	key      []byte
	clientIP func(*http.Request) string
}

func NewDetector(secret string, clientIP func(*http.Request) string) (*Detector, error) {
	if len(secret) < 32 {
		return nil, errors.New("signup risk secret must contain at least 32 bytes")
	}
	return &Detector{key: []byte(secret), clientIP: clientIP}, nil
}
func (d *Detector) hash(kind, value string) string {
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte(kind + ":" + value))
	return hex.EncodeToString(mac.Sum(nil))
}
func (d *Detector) validDevice(value string) string {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return ""
	}
	id, err := hex.DecodeString(parts[0])
	if err != nil || len(id) != 24 {
		return ""
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || expiry <= time.Now().Unix() || expiry > time.Now().Add(deviceLifetime+time.Minute).Unix() {
		return ""
	}
	expected := d.hash("cookie", parts[0]+"."+parts[1])
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return ""
	}
	return parts[0]
}

// Prepare issues a cookie on a same-origin registration context request. Invalid
// signatures never count as proof of an existing device.
func (d *Detector) Prepare(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(CookieName); err == nil && d.validDevice(c.Value) != "" {
		return nil
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	value := hex.EncodeToString(raw) + "." + strconv.FormatInt(time.Now().Add(deviceLifetime).Unix(), 10)
	value += "." + d.hash("cookie", value)
	// APP_BASE_URL-derived secure behavior is provided by the caller's request
	// URL or TLS; callers may force Secure when deployed behind TLS termination.
	// #nosec G124 -- Secure is enforced for TLS/configured HTTPS; local HTTP development must also receive the cookie.
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: value, Path: "/api/auth", MaxAge: int(deviceLifetime.Seconds()), HttpOnly: true, Secure: r.TLS != nil || r.URL.Scheme == "https", SameSite: http.SameSiteLaxMode})
	return nil
}

type Signals struct {
	EmailHash, DeviceHash, NetworkHash, PrefixHash, FingerprintHash string
	Browser, Platform                                               string
	BrowserReasons                                                  []string
	MissingDevice                                                   bool
}

func (d *Detector) Signals(r *http.Request, canonicalEmail string) *Signals {
	s := &Signals{EmailHash: d.hash("email", canonicalEmail), MissingDevice: true}
	if c, err := r.Cookie(CookieName); err == nil {
		if id := d.validDevice(c.Value); id != "" {
			s.DeviceHash = d.hash("device", id)
			s.MissingDevice = false
		}
	}
	if addr, err := netip.ParseAddr(d.clientIP(r)); err == nil {
		addr = addr.Unmap()
		network := addr.String()
		if addr.Is6() {
			network = netip.PrefixFrom(addr, 64).Masked().String()
		}
		s.NetworkHash = d.hash("network", network)
		bits := 24
		if addr.Is6() {
			bits = 48
		}
		s.PrefixHash = d.hash("prefix", netip.PrefixFrom(addr, bits).Masked().String())
	}
	return s
}
