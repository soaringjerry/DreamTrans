package ai

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestAssembleFullBudgetsFixedAndContextTogether(t *testing.T) {
	t.Setenv("AI_MAX_CONTEXT_TOKENS", "256000")
	_, err := Assemble(AssemblyInput{
		Policy:    ContextPolicy{Mode: "full", MaxTokens: 8},
		FixedText: "system question history",
		Transcript: []TranscriptSegment{{
			Speaker: "A",
			Text:    "a transcript that cannot fit in the remaining budget",
		}},
	})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("expected total-input context error, got %v", err)
	}
}

func TestAssembleRetrievalNeverIncludesTranscript(t *testing.T) {
	result, err := Assemble(AssemblyInput{
		Policy:     ContextPolicy{Mode: "retrieval", MaxTokens: 100},
		FixedText:  "question",
		Transcript: []TranscriptSegment{{Text: "secret full transcript"}},
		Blocks: []ContextBlock{{
			Text: "relevant result", Section: "Project knowledge",
			Source: Source{Kind: "knowledge", ID: "one"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, "secret full transcript") ||
		!strings.Contains(result.Text, "relevant result") {
		t.Fatalf("unexpected retrieval context: %q", result.Text)
	}
	if result.EstimatedTokens > 100 {
		t.Fatalf("estimated tokens exceeded budget: %d", result.EstimatedTokens)
	}
}

func TestAssembleSmartKeepsRankedBlocksAndNewestSegments(t *testing.T) {
	segments := []TranscriptSegment{
		{ID: "old", Speaker: "A", Text: strings.Repeat("old ", 20)},
		{ID: "new", Speaker: "B", Text: "newest important words"},
	}
	result, err := Assemble(AssemblyInput{
		Policy:     ContextPolicy{Mode: "smart", MaxTokens: 120},
		FixedText:  "system question",
		Transcript: segments,
		Blocks: []ContextBlock{{
			Text: "ranked project fact", Section: "Project knowledge",
			Source: Source{Kind: "knowledge", ID: "fact"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.EffectiveMode != "smart" || !result.Truncated ||
		!strings.Contains(result.Text, "ranked project fact") ||
		!strings.Contains(result.Text, "newest important words") ||
		strings.Contains(result.Text, strings.Repeat("old ", 10)) {
		t.Fatalf("unexpected smart result: %#v", result)
	}
	if result.EstimatedTokens > 120 {
		t.Fatalf("estimated tokens exceeded budget: %d", result.EstimatedTokens)
	}
}

func TestAssembleSmartNeverSplitsTranscriptSegment(t *testing.T) {
	result, err := Assemble(AssemblyInput{
		Policy:    ContextPolicy{Mode: "smart", MaxTokens: 60},
		FixedText: "system question",
		Transcript: []TranscriptSegment{{
			ID: "oversized", Speaker: "A", Text: strings.Repeat("segment ", 40),
		}},
		Blocks: []ContextBlock{{
			Text: "small fact", Section: "Project knowledge",
			Source: Source{Kind: "knowledge", ID: "fact"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Text, "segment") {
		t.Fatalf("smart mode returned a partial transcript segment: %q", result.Text)
	}
	if !strings.Contains(result.Text, "small fact") || !result.Truncated {
		t.Fatalf("unexpected smart result: %#v", result)
	}
}

func TestAssembleRenderedByteAccountingAndSourceOrder(t *testing.T) {
	t.Setenv("AI_MAX_CONTEXT_TOKENS", "256000")
	t.Setenv("AI_MODEL_CONTEXT_WINDOW_TOKENS", "260096")
	t.Setenv("AI_CONTEXT_OUTPUT_RESERVE_TOKENS", "4096")
	input := AssemblyInput{
		Policy:    ContextPolicy{Mode: "full", MaxTokens: 1_000},
		FixedText: "system and question",
		Transcript: []TranscriptSegment{{
			ID: "transcript", Speaker: "A", Text: "spoken words",
		}},
		Blocks: []ContextBlock{
			{
				Text: "first fact", Section: "Project knowledge",
				Source: Source{Kind: "knowledge", ID: "first"},
			},
			{
				Text: "second fact", Section: "Project knowledge",
				Source: Source{Kind: "knowledge", ID: "second"},
			},
			{
				Text: "memory", Section: "",
				Source: Source{Kind: "memory", ID: "third"},
			},
		},
	}
	result, err := Assemble(input)
	if err != nil {
		t.Fatal(err)
	}
	wantTokens := EstimateTokens(input.FixedText) + EstimateTokens(result.Text)
	if result.EstimatedTokens != wantTokens {
		t.Fatalf("estimated tokens = %d, want exact rendered bytes %d", result.EstimatedTokens, wantTokens)
	}
	if len(result.Sources) != 4 ||
		result.Sources[0].Kind != "transcript" ||
		result.Sources[1].ID != "first" ||
		result.Sources[2].ID != "second" ||
		result.Sources[3].ID != "third" {
		t.Fatalf("unexpected source order: %#v", result.Sources)
	}
}

func TestAssembleSmartLargeBlockSetKeepsLinearSelectionSemantics(t *testing.T) {
	t.Setenv("AI_MAX_CONTEXT_TOKENS", "256000")
	t.Setenv("AI_MODEL_CONTEXT_WINDOW_TOKENS", "260096")
	t.Setenv("AI_CONTEXT_OUTPUT_RESERVE_TOKENS", "4096")
	const blockCount = 10_000
	blocks := make([]ContextBlock, blockCount)
	for index := range blocks {
		blocks[index] = ContextBlock{
			Text:    "x",
			Section: "Project knowledge",
			Source:  Source{Kind: "knowledge", ID: fmt.Sprintf("%d", index)},
		}
	}
	fullBlockBytes := EstimateTokens(formatBlocks(blocks))
	input := AssemblyInput{
		Policy: ContextPolicy{
			Mode:      "smart",
			MaxTokens: fullBlockBytes - 2,
		},
		Blocks: blocks,
	}
	var result ContextResult
	var err error
	allocations := testing.AllocsPerRun(1, func() {
		result, err = Assemble(input)
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocations > 256 {
		t.Fatalf("smart selection allocated %.0f objects, want linear incremental assembly", allocations)
	}
	if result.EffectiveMode != "smart" || !result.Truncated {
		t.Fatalf("expected truncated smart result, got %#v", result)
	}
	if len(result.Sources) != blockCount-1 {
		t.Fatalf("selected %d sources, want %d", len(result.Sources), blockCount-1)
	}
	if result.EstimatedTokens > fullBlockBytes-2 {
		t.Fatalf("estimated tokens exceeded budget: %d", result.EstimatedTokens)
	}
}

func TestAssembleRetrievalDoesNotFormatLargeTranscript(t *testing.T) {
	transcript := make([]TranscriptSegment, 10_000)
	for index := range transcript {
		transcript[index] = TranscriptSegment{
			Speaker: "Speaker",
			Text:    "retrieval mode must not format this transcript segment",
		}
	}
	input := AssemblyInput{
		Policy:     ContextPolicy{Mode: "retrieval", MaxTokens: 1_000},
		FixedText:  "question",
		Transcript: transcript,
		Blocks: []ContextBlock{{
			Text: "retrieved fact", Section: "Project knowledge",
			Source: Source{Kind: "knowledge", ID: "fact"},
		}},
	}
	var result ContextResult
	var err error
	allocations := testing.AllocsPerRun(3, func() {
		result, err = Assemble(input)
	})
	if err != nil {
		t.Fatal(err)
	}
	if allocations > 64 {
		t.Fatalf("retrieval assembly allocated %.0f objects for an unused transcript", allocations)
	}
	if strings.Contains(result.Text, "must not format") {
		t.Fatalf("retrieval context leaked the transcript: %q", result.Text)
	}
}

func TestAssembleFullOverflowStopsBeforeTranscriptTail(t *testing.T) {
	transcript := make([]TranscriptSegment, 10_001)
	transcript[0] = TranscriptSegment{Speaker: "A", Text: "already over budget"}
	for index := 1; index < len(transcript); index++ {
		transcript[index] = TranscriptSegment{
			Speaker: "Speaker",
			Text:    "the known-overflow tail must not be formatted",
		}
	}
	input := AssemblyInput{
		Policy:     ContextPolicy{Mode: "full", MaxTokens: 1},
		Transcript: transcript,
	}
	var err error
	allocations := testing.AllocsPerRun(3, func() {
		_, err = Assemble(input)
	})
	if !errors.Is(err, ErrContextTooLarge) {
		t.Fatalf("expected ErrContextTooLarge, got %v", err)
	}
	if allocations > 64 {
		t.Fatalf("known-overflow full assembly allocated %.0f objects", allocations)
	}
}

func BenchmarkAssembleRetrievalWithLargeTranscript(b *testing.B) {
	transcript := make([]TranscriptSegment, 50_000)
	for index := range transcript {
		transcript[index] = TranscriptSegment{
			Speaker: "Speaker",
			Text:    "retrieval mode must not inspect or format this transcript segment",
		}
	}
	input := AssemblyInput{
		Policy:     ContextPolicy{Mode: "retrieval", MaxTokens: 1_000},
		FixedText:  "question",
		Transcript: transcript,
		Blocks: []ContextBlock{{
			Text: "retrieved fact", Section: "Project knowledge",
			Source: Source{Kind: "knowledge", ID: "fact"},
		}},
	}
	b.ResetTimer()
	for range b.N {
		if _, err := Assemble(input); err != nil {
			b.Fatal(err)
		}
	}
}
