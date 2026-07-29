package speechmatics

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestNormalizeStreamingConfig(t *testing.T) {
	config, err := normalizeStreamingConfig(StreamingConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if config.Language != "en" {
		t.Fatalf("default language = %q, want en", config.Language)
	}

	for _, config := range []StreamingConfig{
		{Language: "en\r\nInjected"},
		{Language: strings.Repeat("a", 11)},
		{Language: "en", MaxDelay: math.NaN()},
		{Language: "en", MaxDelay: math.Inf(1)},
		{Language: "en", MaxDelay: 31},
	} {
		if _, err := normalizeStreamingConfig(config); err == nil {
			t.Fatalf("invalid config accepted: %+v", config)
		}
	}
}

func TestStartStreamingRejectsNilChannelsBeforeNetwork(t *testing.T) {
	client := &Client{}
	if err := client.StartStreamingTranscription(
		context.Background(),
		StreamingConfig{},
		nil,
		make(chan string),
	); err == nil {
		t.Fatal("nil audio channel was accepted")
	}
	if err := client.StartStreamingTranscription(
		context.Background(),
		StreamingConfig{},
		make(chan []byte),
		nil,
	); err == nil {
		t.Fatal("nil text channel was accepted")
	}
}

func TestSendAudioRejectsOversizedChunkBeforeWrite(t *testing.T) {
	audioInput := make(chan []byte, 1)
	audioInput <- make([]byte, maxRealtimeAudioChunk+1)
	close(audioInput)
	errChan := make(chan error, 1)

	(&Client{}).sendAudio(context.Background(), nil, audioInput, errChan)

	err := <-errChan
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSpeechmaticsMessageDetailIsBoundedAndSingleLine(t *testing.T) {
	detail := speechmaticsMessageDetail(map[string]interface{}{
		"reason": strings.Repeat("private\r\n", 100),
	})
	if strings.ContainsAny(detail, "\r\n") {
		t.Fatalf("message detail contains control characters: %q", detail)
	}
	if len([]rune(detail)) > 259 {
		t.Fatalf("message detail is not bounded: %d runes", len([]rune(detail)))
	}
}
