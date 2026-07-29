package rag

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

type recordingEmbedder struct {
	inputs []string
}

func (e *recordingEmbedder) Embed(_ context.Context, input string) ([]float32, error) {
	e.inputs = append(e.inputs, input)
	return []float32{1, 2, 3}, nil
}

func newIngestTestService(t *testing.T) (*Service, *recordingEmbedder) {
	t.Helper()
	t.Setenv("OPENAI_API_KEY", "test-key")
	st, err := NewStore(filepath.Join(t.TempDir(), "rag.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	embedder := &recordingEmbedder{}
	return &Service{
		store:                  st,
		embedder:               embedder,
		ingestSummarizeEnabled: false,
		embedEnabled:           true,
		live:                   make(map[string]*liveBuffer),
		liveLastUsed:           make(map[string]time.Time),
		liveMaxEntries:         18,
		liveMaxSessions:        20,
		liveMaxAge:             time.Minute,
	}, embedder
}

func TestIngestParagraphWithResultSkipsFilteredText(t *testing.T) {
	service, embedder := newIngestTestService(t)

	result, err := service.IngestParagraphWithResult(context.Background(), "session-1", "speaker", "uh.", 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Embedded || result.Duplicate {
		t.Fatalf("unexpected ingest result: %#v", result)
	}
	if len(embedder.inputs) != 0 {
		t.Fatalf("embedder called %d times for filtered text", len(embedder.inputs))
	}
}

func TestIngestParagraphWithResultDeduplicatesCanonicalRetry(t *testing.T) {
	service, embedder := newIngestTestService(t)
	const canonical = "this is a sufficiently long paragraph about testing"

	first, err := service.IngestParagraphWithResult(
		context.Background(), "session-1", "speaker",
		"This is a sufficiently long paragraph about testing.", 1, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Embedded || first.Duplicate {
		t.Fatalf("unexpected first ingest result: %#v", first)
	}
	if first.CanonicalText != canonical || first.EmbeddedText != canonical {
		t.Fatalf("unexpected canonical result: %#v", first)
	}

	retry, err := service.IngestParagraphWithResult(
		context.Background(), "session-1", "speaker",
		"  THIS  is a sufficiently long paragraph about testing!  ", 1, 2,
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Embedded || !retry.Duplicate {
		t.Fatalf("unexpected retry result: %#v", retry)
	}
	if len(embedder.inputs) != 1 {
		t.Fatalf("embedder called %d times, want 1", len(embedder.inputs))
	}
}
