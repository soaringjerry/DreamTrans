package handlers

import (
	"context"
	"testing"

	aicontext "github.com/dreamtrans/backend/internal/ai"
	"github.com/dreamtrans/backend/internal/models"
)

func TestMergeProjectContextPolicyAppliesDefaultsPerField(t *testing.T) {
	project := &models.AIProject{
		ContextMode:      "retrieval",
		MaxContextTokens: 32_000,
	}
	tests := []struct {
		name  string
		input aicontext.ContextPolicy
		want  aicontext.ContextPolicy
	}{
		{
			name: "both defaults",
			want: aicontext.ContextPolicy{
				Mode: "retrieval", MaxTokens: 32_000,
			},
		},
		{
			name:  "mode override inherits tokens",
			input: aicontext.ContextPolicy{Mode: "full"},
			want:  aicontext.ContextPolicy{Mode: "full", MaxTokens: 32_000},
		},
		{
			name:  "token override inherits mode",
			input: aicontext.ContextPolicy{MaxTokens: 8_000},
			want:  aicontext.ContextPolicy{Mode: "retrieval", MaxTokens: 8_000},
		},
		{
			name:  "both override",
			input: aicontext.ContextPolicy{Mode: "smart", MaxTokens: 16_000},
			want:  aicontext.ContextPolicy{Mode: "smart", MaxTokens: 16_000},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mergeProjectContextPolicy(test.input, project); got != test.want {
				t.Fatalf("policy = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestSelectedRAGSourcesReflectsOnlyBudgetedSources(t *testing.T) {
	if selectedRAGSources([]aicontext.Source{{Kind: "transcript"}}) {
		t.Fatal("transcript-only context reported RAG usage")
	}
	if !selectedRAGSources([]aicontext.Source{{Kind: "knowledge"}}) {
		t.Fatal("selected knowledge did not report RAG usage")
	}
	if !selectedRAGSources([]aicontext.Source{{Kind: "rag"}}) {
		t.Fatal("selected session retrieval did not report RAG usage")
	}
}

func TestNormalizeContextTopKBoundsAllocationAndSearch(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{input: -100, want: 5},
		{input: 0, want: 5},
		{input: 1, want: 1},
		{input: 20, want: 20},
		{input: 1_000_000, want: 20},
	}
	for _, test := range tests {
		if got := normalizeContextTopK(test.input); got != test.want {
			t.Fatalf("normalizeContextTopK(%d) = %d, want %d", test.input, got, test.want)
		}
	}
}

func TestSemanticPreviewUsesUniqueProviderOperationID(t *testing.T) {
	base := context.Background()
	stableA := semanticQueryProviderOperationID(base, "model", "question", "project", "session")
	stableB := semanticQueryProviderOperationID(base, "model", "question", "project", "session")
	if stableA != stableB {
		t.Fatalf("normal request operation IDs differ: %q != %q", stableA, stableB)
	}

	previewA := semanticQueryProviderOperationID(
		withSemanticPreviewNonce(base), "model", "question", "project", "session",
	)
	previewB := semanticQueryProviderOperationID(
		withSemanticPreviewNonce(base), "model", "question", "project", "session",
	)
	if previewA == stableA || previewB == stableA || previewA == previewB {
		t.Fatalf(
			"semantic preview operation IDs must be request-unique: stable=%q first=%q second=%q",
			stableA,
			previewA,
			previewB,
		)
	}
}

func TestEffectiveGenerationHashTracksResolvedContext(t *testing.T) {
	base := effectiveAIGenerationIdentity{
		RequestKind: "chat",
		SessionID:   "session",
		Project: &aiGenerationProjectContextIdentity{
			ID: "project", ContentDigest: "digest-a",
			IndexStatus:  models.AIIndexStatusReady,
			CurrentModel: "text-embedding-3-small", ChunkCount: 2,
		},
		Question:     "What happened?",
		History:      "user: earlier question",
		SystemPrompt: "Answer from context.",
		Segments: []aicontext.TranscriptSegment{{
			ID: "segment", Text: "Original transcript",
		}},
		SessionTranscriptDigest: "session-digest",
		ContextPolicy: aicontext.ContextPolicy{
			Mode: "smart", MaxTokens: 8_000,
		},
		RetrievalPreference: "auto",
		TopK:                5,
		EmbeddingModel:      "text-embedding-3-small",
		Config: aiGenerationConfigIdentity{
			Model: "gpt-5.6-sol",
		},
	}
	baseHash, err := hashAIGenerationPayload(base)
	if err != nil {
		t.Fatal(err)
	}

	changedHistory := base
	changedHistory.History = "user: changed history"
	changedHistoryHash, err := hashAIGenerationPayload(changedHistory)
	if err != nil {
		t.Fatal(err)
	}
	if changedHistoryHash == baseHash {
		t.Fatal("resolved server history did not change the generation hash")
	}

	changedProject := base
	projectCopy := *base.Project
	projectCopy.ContentDigest = "digest-b"
	changedProject.Project = &projectCopy
	changedProjectHash, err := hashAIGenerationPayload(changedProject)
	if err != nil {
		t.Fatal(err)
	}
	if changedProjectHash == baseHash {
		t.Fatal("resolved project content did not change the generation hash")
	}

	changedModel := base
	changedModel.Config.Model = "gpt-5.6-terra"
	changedModelHash, err := hashAIGenerationPayload(changedModel)
	if err != nil {
		t.Fatal(err)
	}
	if changedModelHash == baseHash {
		t.Fatal("effective model did not change the generation hash")
	}

	changedTranscript := base
	changedTranscript.Segments = append(
		[]aicontext.TranscriptSegment(nil),
		base.Segments...,
	)
	changedTranscript.Segments[0].Text = "Changed transcript"
	changedTranscriptHash, err := hashAIGenerationPayload(changedTranscript)
	if err != nil {
		t.Fatal(err)
	}
	if changedTranscriptHash == baseHash {
		t.Fatal("resolved transcript did not change the generation hash")
	}
}

func TestAIGenerationConfigIdentityNeverStoresRawAPIKey(t *testing.T) {
	const secret = "sk-user-secret"
	identity := aiGenerationConfigIdentityFor(
		&askConfig{APIKey: secret},
		"fallback-model",
	)
	if identity.Model != "fallback-model" {
		t.Fatalf("model = %q, want fallback-model", identity.Model)
	}
	if identity.APIKeyDigest == "" || identity.APIKeyDigest == secret {
		t.Fatalf("API key digest was not safely derived: %q", identity.APIKeyDigest)
	}
}

func TestMergeRetrievalModeReportsMixedSemanticAndLexicalAsHybrid(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  string
	}{
		{
			left:  models.AIRetrievalModeSemantic,
			right: models.AIRetrievalModeLexicalFallback,
			want:  models.AIRetrievalModeHybrid,
		},
		{
			left:  models.AIRetrievalModeLexicalFallback,
			right: models.AIRetrievalModeSemantic,
			want:  models.AIRetrievalModeHybrid,
		},
		{
			left:  models.AIRetrievalModeLegacy,
			right: models.AIRetrievalModeSemantic,
			want:  models.AIRetrievalModeSemantic,
		},
		{
			left:  models.AIRetrievalModeNone,
			right: models.AIRetrievalModeLexicalFallback,
			want:  models.AIRetrievalModeLexicalFallback,
		},
	}
	for _, test := range tests {
		if got := mergeRetrievalMode(test.left, test.right); got != test.want {
			t.Fatalf(
				"mergeRetrievalMode(%q, %q) = %q, want %q",
				test.left,
				test.right,
				got,
				test.want,
			)
		}
	}
}
