package handlers

import (
	"path/filepath"
	"testing"

	"github.com/dreamtrans/backend/internal/store"
)

func TestNewRAGHandlerDoesNotBoxNilQuotaStore(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-only")
	t.Setenv("RAG_DB_PATH", filepath.Join(t.TempDir(), "rag.db"))

	var postgresStore *store.PostgresStore
	handler, err := NewRAGHandler(nil, postgresStore)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	if handler.billing != nil {
		t.Fatal("typed nil billing service enabled RAG billing mode")
	}
}
