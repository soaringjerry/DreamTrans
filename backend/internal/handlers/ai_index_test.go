package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

func TestSelectKnowledgeEmbeddingBatchKeepsOneSourceAndTokenLimit(t *testing.T) {
	chunks := []models.KnowledgeChunk{
		{SourceID: "one", Content: "first", TokenCount: 60_000},
		{SourceID: "one", Content: "second", TokenCount: 60_000},
		{SourceID: "two", Content: "third", TokenCount: 1},
	}
	selected, err := selectKnowledgeEmbeddingBatch(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].Content != "first" {
		t.Fatalf("unexpected batch: %#v", selected)
	}
}

func TestSelectSessionEmbeddingBatchCapsChunkCount(t *testing.T) {
	chunks := make([]models.SessionAIChunk, maxEmbeddingBatchSize+10)
	for index := range chunks {
		chunks[index] = models.SessionAIChunk{
			Ordinal: index, Content: "content", TokenCount: 1,
		}
	}
	selected, err := selectSessionEmbeddingBatch(chunks)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != maxEmbeddingBatchSize {
		t.Fatalf("batch size = %d, want %d", len(selected), maxEmbeddingBatchSize)
	}
}

func TestSelectEmbeddingBatchRejectsOversizedChunk(t *testing.T) {
	_, err := selectSessionEmbeddingBatch([]models.SessionAIChunk{{
		Content: "too large", TokenCount: maxEmbeddingTokens + 1,
	}})
	if err == nil {
		t.Fatal("oversized embedding chunk was accepted")
	}
}

func TestSelectEmbeddingBatchRepairsPositiveCJKUndercount(t *testing.T) {
	const content = "新加坡语言"
	selected, err := selectSessionEmbeddingBatch([]models.SessionAIChunk{{
		Content: content, TokenCount: 1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || selected[0].TokenCount != len(content) {
		t.Fatalf("conservative CJK batch estimate = %#v", selected)
	}
}

func TestMakeSessionAIChunksAreContiguousAndBounded(t *testing.T) {
	end := 2.0
	chunks, err := makeSessionAIChunks([]models.Transcript{
		{
			Speaker: "A", Text: strings.Repeat("长", 2_900),
			StartTime: 1, EndTime: &end,
		},
		{Speaker: "B", Text: "final"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) < 3 {
		t.Fatalf("expected split session chunks, got %d", len(chunks))
	}
	for index, chunk := range chunks {
		if chunk.Ordinal != index {
			t.Fatalf("chunk ordinal = %d, want %d", chunk.Ordinal, index)
		}
		if len([]rune(chunk.Content)) > 1_400 {
			t.Fatalf("chunk %d exceeds rune limit", index)
		}
		if chunk.TokenCount <= 0 {
			t.Fatalf("chunk %d has no token estimate", index)
		}
	}
}

func TestMakeSessionAIChunksCoalescesProviderMicroFinals(t *testing.T) {
	firstEnd := 1.4
	secondEnd := 2.0
	thirdEnd := 2.5
	chunks, err := makeSessionAIChunks([]models.Transcript{
		{
			ClientSegmentID: "one", Speaker: "A", Text: "that's",
			StartTime: 1, EndTime: &firstEnd, Status: "confirmed",
		},
		{
			ClientSegmentID: "two", Speaker: "A", Text: "unique to",
			StartTime: 1.5, EndTime: &secondEnd, Status: "confirmed",
		},
		{
			ClientSegmentID: "three", Speaker: "A", Text: "us.",
			StartTime: 2.1, EndTime: &thirdEnd, Status: "confirmed",
		},
		{
			ClientSegmentID: "partial", Speaker: "A", Text: "stale preview",
			StartTime: 2.2, EndTime: &thirdEnd, Status: "partial",
			IsPartial: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 1 {
		t.Fatalf("session chunks = %d, want 1: %#v", len(chunks), chunks)
	}
	if !strings.Contains(chunks[0].Content, "A: that's unique to us.") {
		t.Fatalf("micro-finals were not coalesced: %q", chunks[0].Content)
	}
	if strings.Contains(chunks[0].Content, "stale preview") {
		t.Fatalf("partial transcript leaked into index: %q", chunks[0].Content)
	}
}

func TestSessionAIChunkBuilderPreservesCrossPageCoalescing(t *testing.T) {
	transcripts := make([]models.Transcript, aiContextTranscriptPageSize+80)
	for index := range transcripts {
		end := float64(index) * 0.1
		transcripts[index] = models.Transcript{
			ClientSegmentID: fmt.Sprintf("segment-%d", index),
			Speaker:         "A",
			Text:            fmt.Sprintf("word-%d", index),
			StartTime:       end,
			EndTime:         &end,
			Status:          "confirmed",
		}
	}
	want, err := makeSessionAIChunks(transcripts)
	if err != nil {
		t.Fatal(err)
	}
	builder := newSessionAIChunkBuilder()
	if err := builder.AddTranscripts(
		transcripts[:aiContextTranscriptPageSize],
	); err != nil {
		t.Fatal(err)
	}
	if err := builder.AddTranscripts(
		transcripts[aiContextTranscriptPageSize:],
	); err != nil {
		t.Fatal(err)
	}
	got, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paged chunks differ from one-shot chunks:\ngot  %#v\nwant %#v", got, want)
	}
}

func TestSessionAIChunkBuilderFailsExplicitlyAtBound(t *testing.T) {
	builder := newSessionAIChunkBuilder()
	for index := 0; index < maxSessionAIChunks; index++ {
		if err := builder.appendChunk("chunk"); err != nil {
			t.Fatalf("append chunk %d: %v", index, err)
		}
	}
	err := builder.appendChunk("one too many")
	if !errors.Is(err, store.ErrSessionAIChunkLimit) {
		t.Fatalf("chunk limit error = %v, want ErrSessionAIChunkLimit", err)
	}
}

func TestCancelActiveAIIndexJobsCancelsEveryOwningPool(t *testing.T) {
	firstHandler := &RAGHandler{}
	secondHandler := &RAGHandler{}
	firstPool := &aiIndexPool{}
	secondPool := &aiIndexPool{}
	firstContext, firstCancel := context.WithCancel(context.Background())
	secondContext, secondCancel := context.WithCancel(context.Background())
	unrelatedContext, unrelatedCancel := context.WithCancel(context.Background())
	defer unrelatedCancel()
	firstPool.active.Store("job-one", &aiIndexActiveRun{
		cancel: firstCancel, done: make(chan struct{}),
	})
	secondPool.active.Store("job-two", &aiIndexActiveRun{
		cancel: secondCancel, done: make(chan struct{}),
	})
	secondPool.active.Store("unrelated", &aiIndexActiveRun{
		cancel: unrelatedCancel, done: make(chan struct{}),
	})
	aiIndexPools.Store(firstHandler, firstPool)
	aiIndexPools.Store(secondHandler, secondPool)
	t.Cleanup(func() {
		aiIndexPools.Delete(firstHandler)
		aiIndexPools.Delete(secondHandler)
	})

	cancelActiveAIIndexJobs([]string{"job-one", "job-two"})
	for name, done := range map[string]<-chan struct{}{
		"first":  firstContext.Done(),
		"second": secondContext.Done(),
	} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("%s active job was not cancelled", name)
		}
	}
	select {
	case <-unrelatedContext.Done():
		t.Fatal("unrelated active job was cancelled")
	default:
	}
}

func TestAIIndexActiveRunCleanupDoesNotDeleteNewGeneration(t *testing.T) {
	pool := &aiIndexPool{}
	oldContext, oldCancel := context.WithCancel(context.Background())
	newContext, newCancel := context.WithCancel(context.Background())
	defer oldCancel()
	defer newCancel()
	oldRun := &aiIndexActiveRun{cancel: oldCancel, done: make(chan struct{})}
	newRun := &aiIndexActiveRun{cancel: newCancel, done: make(chan struct{})}

	pool.active.Store("same-job", oldRun)
	pool.active.Store("same-job", newRun)
	pool.finishActiveRun("same-job", oldRun)

	value, ok := pool.active.Load("same-job")
	if !ok || value != newRun {
		t.Fatalf("active run = %#v, want the new generation", value)
	}
	value.(*aiIndexActiveRun).cancel()
	select {
	case <-newContext.Done():
	case <-time.After(time.Second):
		t.Fatal("new generation cancel handle was not retained")
	}
	select {
	case <-oldContext.Done():
	case <-time.After(time.Second):
		t.Fatal("old generation cleanup did not cancel its own context")
	}
}

func TestNormalizeAIIndexTargetCompatibilityFields(t *testing.T) {
	req := aiIndexTargetRequest{ProjectID: "11111111-1111-4111-8111-111111111111"}
	if err := normalizeAIIndexTarget(&req); err != nil {
		t.Fatal(err)
	}
	if req.TargetType != "project" || req.TargetID != req.ProjectID {
		t.Fatalf("unexpected normalized request: %#v", req)
	}
}

func TestIndexBatchOperationIDBindsOrderedIDsAndContent(t *testing.T) {
	base := indexBatchOperationID(
		"text-embedding-3-small",
		[]string{"chunk-1", "chunk-2"},
		[]string{"first", "second"},
	)
	if base != indexBatchOperationID(
		"text-embedding-3-small",
		[]string{"chunk-1", "chunk-2"},
		[]string{"first", "second"},
	) {
		t.Fatal("same index batch produced a different operation id")
	}
	if base == indexBatchOperationID(
		"text-embedding-3-small",
		[]string{"chunk-2", "chunk-1"},
		[]string{"second", "first"},
	) {
		t.Fatal("ordered batch identity ignored chunk order")
	}
	if base == indexBatchOperationID(
		"text-embedding-3-small",
		[]string{"chunk-1", "chunk-2"},
		[]string{"first", "changed"},
	) {
		t.Fatal("batch identity ignored content changes")
	}
}

func TestAIIndexConfirmationBindsPendingSnapshot(t *testing.T) {
	handler := &RAGHandler{}
	preview := &models.AIIndexPreview{
		TargetType: "project", TargetID: "target",
		Model: "text-embedding-3-small", Dimensions: 1536,
		ChunkCount: 9, PendingChunks: 3, EstimatedTokens: 42,
		EstimatedDP: 0.25, ContentDigest: strings.Repeat("a", 64),
	}
	token, err := handler.signAIIndexConfirmation(preview, "tenant", "user")
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.verifyAIIndexConfirmation(
		token, preview, "tenant", "user",
	); err != nil {
		t.Fatalf("valid confirmation rejected: %v", err)
	}
	changed := *preview
	changed.PendingChunks++
	if err := handler.verifyAIIndexConfirmation(
		token, &changed, "tenant", "user",
	); err == nil {
		t.Fatal("changed pending work reused an old confirmation")
	}
	signatureStart := strings.LastIndexByte(token, '.') + 1
	replacement := byte('A')
	if token[signatureStart] == replacement {
		replacement = 'B'
	}
	tampered := token[:signatureStart] + string(replacement) +
		token[signatureStart+1:]
	if err := handler.verifyAIIndexConfirmation(
		tampered, preview, "tenant", "user",
	); err == nil {
		t.Fatal("tampered confirmation was accepted")
	}
}

func TestAIIndexConfirmationExpires(t *testing.T) {
	handler := &RAGHandler{}
	preview := &models.AIIndexPreview{
		TargetType: "session", TargetID: "target",
		Model: "text-embedding-3-small", Dimensions: 1536,
		ChunkCount: 2, PendingChunks: 2, EstimatedTokens: 20,
		EstimatedDP: 0.1, ContentDigest: strings.Repeat("b", 64),
	}
	confirmation := aiIndexConfirmation{
		TenantID: "tenant", UserID: "user",
		TargetType: preview.TargetType, TargetID: preview.TargetID,
		Model: preview.Model, Dimensions: preview.Dimensions,
		ChunkCount: preview.ChunkCount, PendingChunks: preview.PendingChunks,
		EstimatedTokens: preview.EstimatedTokens,
		EstimatedDP:     preview.EstimatedDP, ContentDigest: preview.ContentDigest,
		ExpiresAt: time.Now().Add(-time.Minute).Unix(),
	}
	payload, err := json.Marshal(confirmation)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, handler.aiIndexConfirmationKey())
	_, _ = mac.Write(payload)
	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if err := handler.verifyAIIndexConfirmation(
		token, preview, "tenant", "user",
	); err == nil {
		t.Fatal("expired confirmation was accepted")
	}
}

func TestValidateAIIndexRetryModel(t *testing.T) {
	if err := validateAIIndexRetryModel(
		"text-embedding-3-small",
		"text-embedding-3-small",
	); err != nil {
		t.Fatalf("unchanged model was rejected: %v", err)
	}
	if err := validateAIIndexRetryModel(
		"text-embedding-3-small",
		"text-embedding-4-small",
	); !errors.Is(err, errAIIndexModelChanged) {
		t.Fatalf("changed model error = %v, want errAIIndexModelChanged", err)
	}
}
