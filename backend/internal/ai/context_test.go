package ai

import (
	"errors"
	"testing"
)

func TestEstimateTokensDoesNotUndercountCJKOrShortWords(t *testing.T) {
	if got := EstimateTokens("新加坡人使用这种语言"); got < 10 {
		t.Fatalf("CJK estimate = %d, want at least one token per rune", got)
	}
	if got := EstimateTokens("a b c d e f"); got < 6 {
		t.Fatalf("short-word estimate = %d, want at least one token per word", got)
	}
}

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
	newestTokens := EstimateTokens(FormatTranscript(segments[2:]))
	result, err := ResolveTranscript(
		segments,
		ContextPolicy{Mode: "smart", MaxTokens: newestTokens},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.EffectiveMode != "smart" {
		t.Fatalf("expected truncated smart context, got %#v", result)
	}
	if result.Text == "" || result.Sources[0].Label != "1 transcript segments" ||
		result.Text != FormatTranscript(segments[2:]) {
		t.Fatalf("expected one newest complete segment, got %#v", result)
	}
}

func TestResolveTranscriptSmartDoesNotSplitOversizedSegment(t *testing.T) {
	result, err := ResolveTranscript(
		[]TranscriptSegment{{
			ID: "oversized", Speaker: "A",
			Text: "this complete segment cannot fit in the configured budget",
		}},
		ContextPolicy{Mode: "smart", MaxTokens: 8},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "" || !result.Truncated || len(result.Sources) != 0 {
		t.Fatalf("oversized segment was partially returned: %#v", result)
	}
}

func TestResolveTranscriptSmartCountsSeparatorsInBudget(t *testing.T) {
	segments := []TranscriptSegment{
		{ID: "one", Speaker: "A", Text: "x"},
		{ID: "two", Speaker: "B", Text: "y"},
	}
	lineBudget := EstimateTokens(FormatTranscript(segments[:1])) +
		EstimateTokens(FormatTranscript(segments[1:]))
	result, err := ResolveTranscript(
		segments,
		ContextPolicy{Mode: "smart", MaxTokens: lineBudget},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.EstimatedTokens > lineBudget {
		t.Fatalf(
			"smart context used %d tokens with limit %d",
			result.EstimatedTokens,
			lineBudget,
		)
	}
	if result.Text != FormatTranscript(segments[1:]) {
		t.Fatalf("expected only newest segment, got %q", result.Text)
	}
}

func TestMaxContextTokensNeverExceedsWindowAfterOutputReserve(t *testing.T) {
	t.Setenv("AI_MAX_CONTEXT_TOKENS", "256000")
	t.Setenv("AI_MODEL_CONTEXT_WINDOW_TOKENS", "8192")
	t.Setenv("AI_CONTEXT_OUTPUT_RESERVE_TOKENS", "2048")
	if got := MaxContextTokens(); got != 6144 {
		t.Fatalf("max input tokens = %d, want 6144", got)
	}

	t.Setenv("AI_MODEL_CONTEXT_WINDOW_TOKENS", "1024")
	t.Setenv("AI_CONTEXT_OUTPUT_RESERVE_TOKENS", "4096")
	if got := MaxContextTokens(); got != 1 {
		t.Fatalf("invalid window/reserve must fail closed, got %d", got)
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

func BenchmarkResolveTranscriptSmartLargeSuffix(b *testing.B) {
	segments := make([]TranscriptSegment, 50_000)
	for index := range segments {
		segments[index] = TranscriptSegment{
			ID:      "segment",
			Speaker: "Speaker",
			Text:    "a complete transcript segment",
		}
	}
	lineBytes := EstimateTokens(FormatTranscript(segments[:1]))
	policy := ContextPolicy{
		Mode:      "smart",
		MaxTokens: (lineBytes+1)*10_000 - 1,
	}
	b.ResetTimer()
	for range b.N {
		if _, err := ResolveTranscript(segments, policy); err != nil {
			b.Fatal(err)
		}
	}
}
