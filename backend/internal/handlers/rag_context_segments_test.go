package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	aicontext "github.com/dreamtrans/backend/internal/ai"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/dreamtrans/backend/internal/store"
)

func TestCoalesceAIContextSegmentsBuildsReadableUtterances(t *testing.T) {
	segments := []aicontext.TranscriptSegment{
		{
			ID: "one", Speaker: "Speaker 1", Text: "that's",
			StartTime: 1, EndTime: 1.4,
		},
		{
			ID: "two", Speaker: "Speaker 1", Text: "unique to",
			StartTime: 1.5, EndTime: 2,
		},
		{
			ID: "three", Speaker: "Speaker 1", Text: "us.",
			StartTime: 2.1, EndTime: 2.5,
		},
		{
			ID: "four", Speaker: "Speaker 2", Text: "Exactly.",
			StartTime: 2.6, EndTime: 3,
		},
	}

	got := coalesceAIContextSegments(segments)
	if len(got) != 2 {
		t.Fatalf("coalesced segments = %d, want 2: %#v", len(got), got)
	}
	if got[0].Text != "that's unique to us." {
		t.Fatalf("first utterance = %q", got[0].Text)
	}
	if got[0].ID != "one" || got[0].StartTime != 1 || got[0].EndTime != 2.5 {
		t.Fatalf("first utterance attribution = %#v", got[0])
	}
	if got[1].Text != "Exactly." {
		t.Fatalf("second utterance = %q", got[1].Text)
	}
}

func TestCoalesceAIContextSegmentsJoinsCJKWithoutArtificialSpaces(t *testing.T) {
	got := coalesceAIContextSegments([]aicontext.TranscriptSegment{
		{
			Speaker: "A", Text: "新加坡人",
			StartTime: 0, EndTime: 0.5,
		},
		{
			Speaker: "A", Text: "可以为此",
			StartTime: 0.6, EndTime: 1,
		},
		{
			Speaker: "A", Text: "感到自豪。",
			StartTime: 1.1, EndTime: 1.5,
		},
	})
	if len(got) != 1 || got[0].Text != "新加坡人可以为此感到自豪。" {
		t.Fatalf("CJK utterance = %#v", got)
	}
}

func TestCoalesceAIContextSegmentsKeepsReadableBounds(t *testing.T) {
	long := strings.Repeat("a", aiContextSentenceBreakMinRunes) + "."
	got := coalesceAIContextSegments([]aicontext.TranscriptSegment{
		{
			Speaker: "A", Text: long,
			StartTime: 0, EndTime: 1,
		},
		{
			Speaker: "A", Text: "Next sentence.",
			StartTime: 1.1, EndTime: 2,
		},
	})
	if len(got) != 2 {
		t.Fatalf("completed long sentence should start a new utterance: %#v", got)
	}
}

type countingAIContextTranscriptReader struct {
	rows            []models.Transcript
	ascendingReads  int
	descendingReads int
	watermarkReads  int
}

func (reader *countingAIContextTranscriptReader) GetTranscriptsPageBySession(
	_ context.Context,
	_ string,
	limit int,
	after *store.TranscriptPageCursor,
) ([]models.Transcript, bool, error) {
	reader.ascendingReads++
	page := make([]models.Transcript, 0, limit+1)
	for index := range reader.rows {
		row := reader.rows[index]
		if after != nil &&
			(row.StartTime < after.StartTime ||
				(row.StartTime == after.StartTime && row.ID <= after.ID)) {
			continue
		}
		page = append(page, row)
		if len(page) == limit+1 {
			break
		}
	}
	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}
	return page, hasMore, nil
}

func (reader *countingAIContextTranscriptReader) GetTranscriptsPageBySessionDescending(
	_ context.Context,
	_ string,
	limit int,
	before *store.TranscriptPageCursor,
) ([]models.Transcript, bool, error) {
	reader.descendingReads++
	page := make([]models.Transcript, 0, limit+1)
	for index := len(reader.rows) - 1; index >= 0; index-- {
		row := reader.rows[index]
		if before != nil &&
			(row.StartTime > before.StartTime ||
				(row.StartTime == before.StartTime && row.ID >= before.ID)) {
			continue
		}
		page = append(page, row)
		if len(page) == limit+1 {
			break
		}
	}
	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}
	return page, hasMore, nil
}

func (reader *countingAIContextTranscriptReader) GetLatestCompleteTranscriptEnd(
	_ context.Context,
	_ string,
) (float64, bool, error) {
	reader.watermarkReads++
	var (
		latest   float64
		hasValue bool
	)
	for index := range reader.rows {
		row := &reader.rows[index]
		if row.IsPartial ||
			strings.EqualFold(strings.TrimSpace(row.Status), "partial") {
			continue
		}
		end := row.StartTime
		if row.EndTime != nil {
			end = *row.EndTime
		}
		if !hasValue || end > latest {
			latest = end
			hasValue = true
		}
	}
	return latest, hasValue, nil
}

func completeTranscriptRows(count int, text func(int) string) []models.Transcript {
	rows := make([]models.Transcript, count)
	for index := range rows {
		end := float64(index) + 0.5
		rows[index] = models.Transcript{
			ID:              fmt.Sprintf("%08d", index),
			ClientSegmentID: fmt.Sprintf("segment-%08d", index),
			Speaker:         "A",
			Text:            text(index),
			StartTime:       float64(index),
			EndTime:         &end,
			Status:          "confirmed",
		}
	}
	return rows
}

func TestLoadFullContextSegmentsStopsAtFirstOverBudgetPage(t *testing.T) {
	reader := &countingAIContextTranscriptReader{
		rows: completeTranscriptRows(1_000, func(index int) string {
			return fmt.Sprintf("row-%d-%s", index, strings.Repeat("x", 100))
		}),
	}
	_, statusCode, err := loadAuthorizedAIContextSegments(
		context.Background(),
		reader,
		"session",
		nil,
		aicontext.ContextPolicy{Mode: "full", MaxTokens: 80},
	)
	if !errors.Is(err, aicontext.ErrContextTooLarge) {
		t.Fatalf("full context error = %v, want ErrContextTooLarge", err)
	}
	if statusCode != http.StatusUnprocessableEntity {
		t.Fatalf("full context status = %d, want 422", statusCode)
	}
	if reader.ascendingReads != 1 {
		t.Fatalf("full context read %d pages, want one-page early stop", reader.ascendingReads)
	}
	if reader.descendingReads != 0 || reader.watermarkReads != 0 {
		t.Fatalf(
			"full context used unexpected reads: descending=%d watermark=%d",
			reader.descendingReads,
			reader.watermarkReads,
		)
	}
}

func TestLoadSmartContextSegmentsKeepsBoundedNewestSuffix(t *testing.T) {
	reader := &countingAIContextTranscriptReader{
		rows: completeTranscriptRows(600, func(index int) string {
			return fmt.Sprintf("row-%03d-%s", index, strings.Repeat("x", 24))
		}),
	}
	loaded, err := loadPersistedAIContextSegments(
		context.Background(),
		reader,
		"session",
		nil,
		aicontext.ContextPolicy{Mode: "smart", MaxTokens: 1_000},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.StoredTruncated {
		t.Fatal("bounded newest suffix was reported as a complete stored transcript")
	}
	if reader.descendingReads != 1 {
		t.Fatalf("smart context read %d pages, want one bounded page", reader.descendingReads)
	}
	if reader.ascendingReads != 0 || reader.watermarkReads != 1 {
		t.Fatalf(
			"smart context used unexpected reads: ascending=%d watermark=%d",
			reader.ascendingReads,
			reader.watermarkReads,
		)
	}
	rendered := aicontext.FormatTranscript(loaded.Segments)
	if !strings.Contains(rendered, "row-599-") {
		t.Fatalf("newest transcript row is missing: %q", rendered)
	}
	if strings.Contains(rendered, "row-000-") {
		t.Fatal("smart context materialized the oldest transcript row")
	}
}

func TestRetrievalContextDoesNotReadStoredTranscript(t *testing.T) {
	reader := &countingAIContextTranscriptReader{
		rows: completeTranscriptRows(10, func(index int) string {
			return fmt.Sprintf("stored-%d", index)
		}),
	}
	client := []aicontext.TranscriptSegment{{
		ID: "client", Speaker: "A", Text: "unsynced client text",
	}}
	loaded, err := loadPersistedAIContextSegments(
		context.Background(),
		reader,
		"session",
		client,
		aicontext.ContextPolicy{Mode: "retrieval", MaxTokens: 1_000},
	)
	if err != nil {
		t.Fatal(err)
	}
	if reader.ascendingReads != 0 ||
		reader.descendingReads != 0 ||
		reader.watermarkReads != 0 {
		t.Fatalf(
			"retrieval mode read stored rows: ascending=%d descending=%d watermark=%d",
			reader.ascendingReads,
			reader.descendingReads,
			reader.watermarkReads,
		)
	}
	if len(loaded.Segments) != 1 ||
		loaded.Segments[0].Text != "unsynced client text" {
		t.Fatalf("retrieval client context = %#v", loaded.Segments)
	}
}

func TestProjectDefaultPolicyIsAppliedBeforeTranscriptRead(t *testing.T) {
	reader := &countingAIContextTranscriptReader{
		rows: completeTranscriptRows(10, func(index int) string {
			return fmt.Sprintf("stored-%d", index)
		}),
	}
	project := &models.AIProject{
		ContextMode:      "retrieval",
		MaxContextTokens: 2_000,
	}
	policy, err := aicontext.NormalizePolicy(
		mergeProjectContextPolicy(aicontext.ContextPolicy{}, project),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadPersistedAIContextSegments(
		context.Background(),
		reader,
		"session",
		nil,
		policy,
	); err != nil {
		t.Fatal(err)
	}
	if reader.ascendingReads != 0 ||
		reader.descendingReads != 0 ||
		reader.watermarkReads != 0 {
		t.Fatal("project retrieval default was applied after stored transcript loading")
	}
}

func TestAssembleSmartContextNeverClaimsBoundedSuffixIsFull(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-only")
	t.Setenv("RAG_DB_PATH", filepath.Join(t.TempDir(), "rag.db"))
	service, err := rag.NewServiceFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()

	segments := []aicontext.TranscriptSegment{{
		ID: "newest", Speaker: "A", Text: "newest bounded suffix",
	}}
	handler := &RAGHandler{svc: service}
	assembled, err := handler.assembleModelContext(
		context.Background(),
		"scoped-session",
		"",
		"Summarize.",
		"",
		"Answer from context.",
		segments,
		nil,
		aicontext.ContextPolicy{Mode: "smart", MaxTokens: 1_000},
		5,
		"lexical_only",
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Result.EffectiveMode != "smart" ||
		!assembled.Result.Truncated {
		t.Fatalf(
			"bounded suffix metadata = mode %q truncated %v, want smart/true",
			assembled.Result.EffectiveMode,
			assembled.Result.Truncated,
		)
	}
}
