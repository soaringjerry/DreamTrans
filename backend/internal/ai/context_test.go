package ai

import (
	"errors"
	"testing"
)

func TestEstimateTokensCountsCJK(t *testing.T) {
	if got := EstimateTokens("这是中文内容"); got == 0 {
		t.Fatal("CJK text must contribute to the context budget")
	}
}

func TestResolveTranscriptSmartKeepsNewestWholeSegments(t *testing.T) {
	t.Setenv("AI_MAX_CONTEXT_TOKENS", "256000")
	segments := []TranscriptSegment{
		{ID: "one", Speaker: "A", Text: "first sentence with enough words"},
		{ID: "two", Speaker: "B", Text: "second sentence with enough words"},
		{ID: "three", Speaker: "A", Text: "newest sentence with enough words"},
	}
	result, err := ResolveTranscript(segments, ContextPolicy{Mode: "smart", MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.EffectiveMode != "smart" {
		t.Fatalf("expected truncated smart context, got %#v", result)
	}
	if result.Text == "" || result.Sources[0].Label != "1 transcript segments" {
		t.Fatalf("expected one newest complete segment, got %#v", result)
	}
}

func TestResolveTranscriptFullFailsInsteadOfDowngrading(t *testing.T) {
	t.Setenv("AI_MAX_CONTEXT_TOKENS", "256000")
	_, err := ResolveTranscript(
		[]TranscriptSegment{{Text: "a very long transcript that cannot fit"}},
		ContextPolicy{Mode: "full", MaxTokens: 1},
	)
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("expected ErrContextTooLarge, got %v", err)
	}
}
