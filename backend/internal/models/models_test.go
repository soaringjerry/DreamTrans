package models

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSessionWithTranscriptsAlwaysEncodesArray(t *testing.T) {
	payload, err := json.Marshal(SessionWithTranscripts{})
	if err != nil {
		t.Fatalf("marshal empty session: %v", err)
	}
	if !strings.Contains(string(payload), `"transcripts":null`) {
		t.Fatalf("nil transcript slice must remain an explicit field, got %s", payload)
	}

	payload, err = json.Marshal(SessionWithTranscripts{
		Transcripts: make([]Transcript, 0),
	})
	if err != nil {
		t.Fatalf("marshal initialized session: %v", err)
	}
	if !strings.Contains(string(payload), `"transcripts":[]`) {
		t.Fatalf("initialized transcript slice must encode as an array, got %s", payload)
	}
}
