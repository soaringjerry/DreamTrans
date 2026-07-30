package handlers

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// maxClientSegmentIDLength bounds client-chosen idempotency keys so they stay
// safely inside the transcripts.client_segment_id VARCHAR(128) column.
const maxClientSegmentIDLength = 128

func billingSessionReference(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if _, err := uuid.Parse(value); err != nil {
		return nil
	}
	return &value
}

// normalizeClientSegmentID validates a client-provided transcript idempotency
// key. Legacy clients send UUIDs (canonicalized here); newer clients send
// bounded opaque keys such as seg_<start>_<end>_<hash>. Server-generated IDs
// (empty input) remain UUIDs, so billing reservation identifiers keep their
// strict UUID shape.
func normalizeClientSegmentID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return uuid.NewString(), nil
	}
	if parsed, err := uuid.Parse(value); err == nil {
		return parsed.String(), nil
	}
	if len(value) > maxClientSegmentIDLength {
		return "", fmt.Errorf("client_segment_id must be at most %d characters", maxClientSegmentIDLength)
	}
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
		case char >= 'A' && char <= 'Z':
		case char >= '0' && char <= '9':
		case char == '_' || char == '-' || char == '.' || char == ':':
		default:
			return "", fmt.Errorf("client_segment_id contains unsupported character %q", char)
		}
	}
	return value, nil
}
