//go:build event_worker

// Package main implements the DreamTrans PCAS transcription event worker.
package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

const (
	maxAudioBytes     int64 = 100 << 20 // 100 MiB
	maxErrorBodyBytes int64 = 4 << 10   // 4 KiB
	audioFetchTimeout       = 30 * time.Second
	maxRedirects            = 3
)

var blockedAddressPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT and cloud metadata
	netip.MustParsePrefix("::/96"),         // IPv4-compatible addresses
	netip.MustParsePrefix("64:ff9b::/96"),  // well-known NAT64
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001::/32"), // Teredo
	netip.MustParsePrefix("2002::/16"), // 6to4
}

func newRestrictedAudioHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           restrictedDialContext(dialer, net.DefaultResolver),
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          10,
	}

	return &http.Client{
		Transport: transport,
		Timeout:   audioFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return validateRemoteAudioURL(req.URL)
		},
	}
}

// restrictedDialContext resolves and pins the destination before dialing. This
// avoids a DNS-rebinding gap between a preflight lookup and the actual request.
func restrictedDialContext(dialer *net.Dialer, resolver *net.Resolver) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid remote address: %w", err)
		}

		addresses, err := resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("resolve remote host: %w", err)
		}
		if len(addresses) == 0 {
			return nil, fmt.Errorf("remote host has no addresses")
		}

		// Reject the whole hostname if any answer is non-public. Otherwise a
		// mixed answer set could be used to reach an internal service.
		for _, address := range addresses {
			if !isPublicIP(address.IP) {
				return nil, fmt.Errorf("audio_url resolves to a non-public address")
			}
		}

		var lastErr error
		for _, address := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("dial remote host: %w", lastErr)
	}
}

func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	for _, blockedPrefix := range blockedAddressPrefixes {
		if blockedPrefix.Contains(address) {
			return false
		}
	}
	return ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsLinkLocalMulticast()
}

func validateRemoteAudioURL(parsed *url.URL) error {
	if parsed == nil || parsed.Scheme != "https" {
		if parsed == nil || parsed.Scheme != "http" ||
			!strings.EqualFold(strings.TrimSpace(os.Getenv("AUDIO_URL_ALLOW_HTTP")), "true") {
			return fmt.Errorf("audio_url must use https")
		}
	}
	if parsed.Hostname() == "" {
		return fmt.Errorf("audio_url is missing a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("audio_url must not contain credentials")
	}
	return nil
}

func extractAudioAndLang(ctx context.Context, client *http.Client, data *anypb.Any) ([]byte, string, string, error) {
	language := "en"
	if data == nil {
		return nil, "", language, fmt.Errorf("missing data")
	}

	val := &structpb.Value{}
	if err := data.UnmarshalTo(val); err != nil {
		return nil, "", language, fmt.Errorf("invalid data: %w", err)
	}
	m, ok := val.AsInterface().(map[string]interface{})
	if !ok {
		return nil, "", language, fmt.Errorf("data not object")
	}
	if v, ok := m["language"].(string); ok && v != "" {
		language = strings.ToLower(strings.TrimSpace(v))
	}
	if language == "" || len(language) > 10 {
		return nil, "", "en", fmt.Errorf("invalid language")
	}
	for _, character := range language {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '-' || character == '_' {
			continue
		}
		return nil, "", "en", fmt.Errorf("invalid language")
	}

	if encoded, ok := m["audio_base64"].(string); ok && encoded != "" {
		if int64(base64.StdEncoding.DecodedLen(len(encoded))) > maxAudioBytes {
			return nil, "", language, fmt.Errorf("audio_base64 exceeds %d bytes", maxAudioBytes)
		}
		audio, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", language, fmt.Errorf("bad audio_base64: %w", err)
		}
		if len(audio) == 0 {
			return nil, "", language, fmt.Errorf("audio_base64 is empty")
		}
		return audio, "request.wav", language, nil
	}

	if rawURL, ok := m["audio_url"].(string); ok && rawURL != "" {
		if len(rawURL) > 2048 {
			return nil, "", language, fmt.Errorf("audio_url is too long")
		}
		return fetchAudioURL(ctx, client, rawURL, language)
	}
	return nil, "", language, fmt.Errorf("no audio_base64 or audio_url")
}

func fetchAudioURL(ctx context.Context, client *http.Client, rawURL, language string) ([]byte, string, string, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", language, fmt.Errorf("invalid audio_url: %w", err)
	}
	if err := validateRemoteAudioURL(parsed); err != nil {
		return nil, "", language, err
	}
	if client == nil {
		return nil, "", language, errors.New("audio URL fetching is unavailable")
	}

	// The URL is syntax-validated above, and the production client resolves,
	// rejects non-public addresses, and pins the accepted IP before dialing.
	//nolint:gosec // G107: fetches only through the restricted transport.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), http.NoBody)
	if err != nil {
		return nil, "", language, fmt.Errorf("create audio_url request: %w", err)
	}
	req.Header.Set("Accept", "audio/*, application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", language, fmt.Errorf("fetch audio_url failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		return nil, "", language, fmt.Errorf("fetch audio_url status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.ContentLength > maxAudioBytes {
		return nil, "", language, fmt.Errorf("audio_url response exceeds %d bytes", maxAudioBytes)
	}

	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxAudioBytes+1))
	if err != nil {
		return nil, "", language, fmt.Errorf("read audio_url failed: %w", err)
	}
	if int64(len(audio)) > maxAudioBytes {
		return nil, "", language, fmt.Errorf("audio_url response exceeds %d bytes", maxAudioBytes)
	}

	decodedPath, decodeErr := url.PathUnescape(parsed.EscapedPath())
	if decodeErr != nil {
		decodedPath = parsed.Path
	}
	name := path.Base(decodedPath)
	if name == "" || name == "." || name == "/" {
		name = "request.bin"
	}
	name = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f || character == '/' || character == '\\' {
			return -1
		}
		return character
	}, name)
	if name == "" {
		name = "request.bin"
	}
	if runes := []rune(name); len(runes) > 200 {
		name = string(runes[:200])
	}
	if len(audio) == 0 {
		return nil, "", language, fmt.Errorf("audio_url response is empty")
	}
	return audio, name, language, nil
}
