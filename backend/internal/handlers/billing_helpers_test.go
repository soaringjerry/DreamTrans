package handlers

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeClientSegmentIDGeneratesUUIDWhenEmpty(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "   ", "\t"} {
		normalized, err := normalizeClientSegmentID(value)
		if err != nil {
			t.Fatalf("normalizeClientSegmentID(%q) returned error: %v", value, err)
		}
		if _, parseErr := uuid.Parse(normalized); parseErr != nil {
			t.Fatalf("expected generated UUID for %q, got %q", value, normalized)
		}
	}
}

func TestNormalizeClientSegmentIDCanonicalizesUUID(t *testing.T) {
	t.Parallel()

	normalized, err := normalizeClientSegmentID(" 11111111-1111-4111-8111-AAAAAAAAAAAA ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if normalized != "11111111-1111-4111-8111-aaaaaaaaaaaa" {
		t.Fatalf("expected canonical UUID, got %q", normalized)
	}
}

func TestNormalizeClientSegmentIDAcceptsBoundedOpaqueKeys(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"seg_1f2f_2s3t_a1b2c3d4",
		"seg_0_kx.9:z-A_B",
		strings.Repeat("a", maxClientSegmentIDLength),
	} {
		normalized, err := normalizeClientSegmentID(value)
		if err != nil {
			t.Fatalf("normalizeClientSegmentID(%q) returned error: %v", value, err)
		}
		if normalized != value {
			t.Fatalf("expected opaque key to be preserved, got %q for %q", normalized, value)
		}
	}
}

func TestNormalizeClientSegmentIDRejectsUnsafeKeys(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		strings.Repeat("a", maxClientSegmentIDLength+1),
		"seg with spaces",
		"seg/../../etc",
		"seg\x00null",
		"片段-一",
	} {
		if _, err := normalizeClientSegmentID(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
	}
}
