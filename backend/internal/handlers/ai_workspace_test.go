package handlers

import (
	"strings"
	"testing"

	"github.com/dreamtrans/backend/internal/models"
)

func TestKnowledgeChunksAreBoundedAndIndexed(t *testing.T) {
	source := &models.KnowledgeSource{ID: "source-1", ProjectID: "project-1"}
	text := strings.Repeat("long knowledge paragraph ", 200)
	chunks := makeKnowledgeChunks(source, text)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if got := len([]rune(chunk.Content)); got > 1_400 {
			t.Fatalf("chunk has %d runes", got)
		}
		if len(chunk.Vector) != knowledgeVectorDimensions {
			t.Fatalf("vector dimensions = %d", len(chunk.Vector))
		}
	}
}

func TestExtractWorksheetResolvesSharedStrings(t *testing.T) {
	xmlText := `<worksheet><sheetData><row>` +
		`<c t="s"><v>0</v></c><c><v>42</v></c>` +
		`</row></sheetData></worksheet>`
	text, err := extractWorksheetText(strings.NewReader(xmlText), []string{"Heading"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(text) != "Heading | 42" {
		t.Fatalf("worksheet text = %q", text)
	}
}

func TestRetrieveKnowledgeRanksMatchingMultilingualChunk(t *testing.T) {
	chunks := []models.KnowledgeChunk{
		{ID: "unrelated", Content: "Quarterly financial planning and budget.", Vector: localKnowledgeVector("Quarterly financial planning and budget.")},
		{ID: "matching", Content: "新加坡独有的语言文化值得自豪。", Vector: localKnowledgeVector("新加坡独有的语言文化值得自豪。")},
	}
	result := retrieveKnowledge("新加坡语言文化", chunks, 1)
	if len(result) != 1 || result[0].ID != "matching" {
		t.Fatalf("unexpected retrieval result: %#v", result)
	}
}
