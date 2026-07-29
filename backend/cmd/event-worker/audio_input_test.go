//go:build event_worker

package main

import (
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func eventData(t *testing.T, fields map[string]interface{}) *anypb.Any {
	t.Helper()
	value, err := structpb.NewValue(fields)
	if err != nil {
		t.Fatal(err)
	}
	data, err := anypb.New(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExtractAudioAndLangBase64(t *testing.T) {
	input := []byte("test audio")
	data := eventData(t, map[string]interface{}{
		"audio_base64": base64.StdEncoding.EncodeToString(input),
		"language":     "EN-US",
	})

	audio, filename, language, err := extractAudioAndLang(context.Background(), nil, data)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != string(input) {
		t.Fatalf("audio = %q, want %q", audio, input)
	}
	if filename != "request.wav" {
		t.Fatalf("filename = %q", filename)
	}
	if language != "en-us" {
		t.Fatalf("language = %q", language)
	}
}

func TestFetchAudioURLRejectsUnsafeURLs(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe URL should not be fetched")
		return nil, nil
	})}

	for _, rawURL := range []string{
		"file:///etc/passwd",
		"https://user:password@example.com/audio.wav",
		"//example.com/audio.wav",
	} {
		t.Run(rawURL, func(t *testing.T) {
			if _, _, _, err := fetchAudioURL(context.Background(), client, rawURL, "en"); err == nil {
				t.Fatalf("fetchAudioURL(%q) succeeded", rawURL)
			}
		})
	}
}

func TestRestrictedClientRejectsLoopback(t *testing.T) {
	_, _, _, err := fetchAudioURL(
		context.Background(),
		newRestrictedAudioHTTPClient(),
		"https://127.0.0.1:8080/private.wav",
		"en",
	)
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("err = %v, want non-public address error", err)
	}
}

func TestFetchAudioURLRejectsPlainHTTPByDefault(t *testing.T) {
	t.Setenv("AUDIO_URL_ALLOW_HTTP", "")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("plain HTTP URL should not be fetched")
		return nil, nil
	})}
	if _, _, _, err := fetchAudioURL(context.Background(), client, "http://example.com/audio.wav", "en"); err == nil {
		t.Fatal("plain HTTP URL succeeded")
	}
}

func TestFetchAudioURLLimitsContentLength(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: maxAudioBytes + 1,
			Body:          io.NopCloser(strings.NewReader("")),
			Header:        make(http.Header),
		}, nil
	})}

	_, _, _, err := fetchAudioURL(context.Background(), client, "https://example.com/audio.wav", "en")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want size limit error", err)
	}
}

func TestFetchAudioURLReturnsBoundedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.Header.Get("Accept"); got == "" {
			t.Error("Accept header is empty")
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			ContentLength: 5,
			Body:          io.NopCloser(strings.NewReader("audio")),
			Header:        make(http.Header),
		}, nil
	})}

	audio, filename, language, err := fetchAudioURL(
		context.Background(),
		client,
		"https://example.com/files/sample.wav?signature=secret",
		"de",
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "audio" || filename != "sample.wav" || language != "de" {
		t.Fatalf("got audio=%q filename=%q language=%q", audio, filename, language)
	}
}

func TestIsPublicIP(t *testing.T) {
	tests := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"127.0.0.1", false},
		{"10.0.0.1", false},
		{"100.100.100.200", false},
		{"169.254.169.254", false},
		{"::1", false},
		{"fd00::1", false},
		{"64:ff9b::a00:1", false},
		{"2002:0a00:0001::1", false},
	}

	for _, tt := range tests {
		if got := isPublicIP(net.ParseIP(tt.ip)); got != tt.want {
			t.Errorf("isPublicIP(%q) = %v, want %v", tt.ip, got, tt.want)
		}
	}
}
