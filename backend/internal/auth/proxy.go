package auth

import (
	"log"
	"net/http"
	"net/netip"
	"strings"
)

// Trust is opt-in. An invalid configuration disables forwarding-header trust.
func parseTrustedProxies(raw string) []netip.Prefix {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var prefixes []netip.Prefix
	for _, item := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(item))
		if err != nil {
			log.Print("invalid TRUSTED_PROXY_CIDRS; forwarding headers will be ignored")
			return nil
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func (g *APIGuard) trustsProxy(address netip.Addr) bool {
	for _, prefix := range g.trustedProxies {
		if prefix.Contains(address.Unmap()) {
			return true
		}
	}
	return false
}

func (g *APIGuard) clientIP(r *http.Request) string {
	fallback := remoteIP(r)
	peer, err := netip.ParseAddr(fallback)
	if err != nil {
		return fallback
	}
	fallback = peer.Unmap().String()
	if !g.trustsProxy(peer) {
		return fallback
	}
	header := strings.Join(r.Header.Values("X-Forwarded-For"), ",")
	if len(header) > 4096 {
		return fallback
	}
	chain := strings.Split(header, ",")
	if len(chain) > 32 {
		return fallback
	}
	// Walk from the direct peer towards the client. Never trust addresses
	// supplied to the left of the first untrusted hop.
	for i := len(chain) - 1; i >= 0; i-- {
		address, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil || address.Zone() != "" {
			return fallback
		}
		peer = address.Unmap()
		if !g.trustsProxy(peer) {
			return peer.String()
		}
	}
	return peer.String()
}
