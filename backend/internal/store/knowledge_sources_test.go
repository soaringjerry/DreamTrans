package store

import (
	"errors"
	"math"
	"testing"

	"github.com/dreamtrans/backend/internal/models"
)

func TestNormalizeMemorySourceAndChunks(t *testing.T) {
	source := &models.KnowledgeSource{
		ProjectID:  "project-1",
		TenantID:   "tenant-1",
		UserID:     "user-1",
		SourceType: " memory ",
		Name:       " Memory name ",
		Content:    " Memory body ",
	}
	chunks := []models.KnowledgeChunk{{
		ProjectID:  "project-1",
		Ordinal:    0,
		Content:    " Memory body ",
		Vector:     []float64{0.5, 0.25},
		TokenCount: 3,
	}}

	storage, err := normalizeMemorySource(source, chunks)
	if err != nil {
		t.Fatal(err)
	}
	if source.Name != "Memory name" || source.Content != "Memory body" {
		t.Fatalf("memory was not normalized: %#v", source)
	}
	if source.SizeBytes != int64(len("Memory body")) ||
		source.MediaType != "text/plain" {
		t.Fatalf("memory metadata was not derived: %#v", source)
	}
	if len(source.OCRLanguages) != 2 ||
		source.OCRLanguages[0] != "eng" ||
		source.OCRLanguages[1] != "chi_sim" {
		t.Fatalf("default OCR languages = %#v", source.OCRLanguages)
	}
	if storage.extractedTextBytes != int64(len("Memory body")) ||
		storage.vectorBytes != 8 {
		t.Fatalf("memory storage = %#v", storage)
	}
	if chunks[0].Content != "Memory body" {
		t.Fatalf("chunk content was not normalized: %q", chunks[0].Content)
	}
	if chunks[0].TokenCount != len("Memory body") {
		t.Fatalf(
			"memory token estimate = %d, want %d UTF-8 bytes",
			chunks[0].TokenCount,
			len("Memory body"),
		)
	}
}

func TestValidateMemoryChunksRejectsNonAtomicReplacementInputs(t *testing.T) {
	tests := []struct {
		name   string
		chunks []models.KnowledgeChunk
	}{
		{name: "empty"},
		{
			name: "ordinal gap",
			chunks: []models.KnowledgeChunk{{
				Ordinal: 1, Content: "chunk",
			}},
		},
		{
			name: "empty content",
			chunks: []models.KnowledgeChunk{{
				Ordinal: 0, Content: " ",
			}},
		},
		{
			name: "wrong project",
			chunks: []models.KnowledgeChunk{{
				ProjectID: "other-project", Ordinal: 0, Content: "chunk",
			}},
		},
		{
			name: "wrong source",
			chunks: []models.KnowledgeChunk{{
				SourceID: "other-source", Ordinal: 0, Content: "chunk",
			}},
		},
		{
			name: "negative tokens",
			chunks: []models.KnowledgeChunk{{
				Ordinal: 0, Content: "chunk", TokenCount: -1,
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := validateMemoryChunks(
				"source-1", "project-1", test.chunks,
			); err == nil {
				t.Fatal("invalid memory chunks were accepted")
			}
		})
	}
}

func TestEnsureMemoryStorageQuotaRejectsOverflowBeforeDatabaseRead(t *testing.T) {
	err := ensureMemoryStorageQuotaTx(
		t.Context(),
		nil,
		"tenant-1",
		nil,
		1,
		math.MaxInt64,
		memoryChunkStorage{extractedTextBytes: 1},
	)
	if !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("overflow error = %v, want ErrStorageQuota", err)
	}
}
