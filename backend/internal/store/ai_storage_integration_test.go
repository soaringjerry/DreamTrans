package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

func TestPostgresAIStorageUsageAndDeletionOptIn(t *testing.T) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	postgresStore := &PostgresStore{db: db}
	if err := postgresStore.VerifySchema(t.Context()); err != nil {
		t.Fatalf("verify migrated schema: %v", err)
	}

	tenantID := uuid.NewString()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	projectID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO tenants (
			id, name, slug, plan, api_quota_monthly, storage_quota_gb,
			max_sessions
		) VALUES ($1, 'AI storage integration', $2, 'pro', 1000, 1, 10)
	`, tenantID, "ai-storage-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `
			DELETE FROM tenants WHERE id=$1
		`, tenantID)
	})
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO users (
			id, tenant_id, email, password_hash, name, role, is_active,
			email_verified
		) VALUES (
			$1,$2,$3,'integration-only','AI storage integration',
			'user',true,true
		)
	`, userID, tenantID, "ai-storage-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO sessions (
			id, user_id, tenant_id, title, source_language,
			target_language, status
		) VALUES ($1,$2,$3,'AI storage session','en','zh','active')
	`, sessionID, userID, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO ai_projects (
			id, tenant_id, user_id, name, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,'AI storage project','smart',64000)
	`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.LinkProjectSession(
		t.Context(), projectID, sessionID, userID,
	); err != nil {
		t.Fatalf("link project session: %v", err)
	}
	sourceLanguage, err := postgresStore.GetProjectSessionSourceLanguage(
		t.Context(), projectID, sessionID, tenantID, userID,
	)
	if err != nil {
		t.Fatalf("get linked session source language: %v", err)
	}
	if sourceLanguage != "en" {
		t.Fatalf("linked session source language = %q, want en", sourceLanguage)
	}
	if _, err := postgresStore.GetProjectSessionSourceLanguage(
		t.Context(), uuid.NewString(), sessionID, tenantID, userID,
	); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unlinked project lookup error = %v, want sql.ErrNoRows", err)
	}

	source := &models.KnowledgeSource{
		ProjectID:  projectID,
		TenantID:   tenantID,
		UserID:     userID,
		SourceType: "memory",
		Name:       "Raw memory",
		MediaType:  "text/plain",
		SizeBytes:  4,
		Content:    "raw!",
		Status:     "processing",
	}
	chunks := []models.KnowledgeChunk{{
		Ordinal:    0,
		Content:    "chunk",
		Vector:     []float64{0.25, 0.75},
		TokenCount: 2,
	}}
	if err := postgresStore.CreateMemorySourceWithChunks(
		t.Context(), source, chunks,
	); err != nil {
		t.Fatalf("create memory with chunks: %v", err)
	}

	// A failed replacement must leave both the source row and the old chunks
	// untouched. With a zero-byte quota the replacement cannot commit.
	if _, err := db.ExecContext(t.Context(), `
		UPDATE tenants SET storage_quota_gb=0 WHERE id=$1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	rollbackName := "Must roll back"
	rollbackContent := "replacement content"
	rollbackChunks := []models.KnowledgeChunk{{
		SourceID: source.ID, ProjectID: projectID,
		Ordinal: 0, Content: "replacement chunk",
		Vector: []float64{1, 0}, TokenCount: 2,
	}}
	if _, err := postgresStore.UpdateMemorySourceWithChunks(
		t.Context(), source.ID, projectID, tenantID, userID,
		&rollbackName, &rollbackContent, rollbackChunks,
	); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("quota-rejected memory update error = %v, want ErrStorageQuota", err)
	}
	var (
		storedName, storedContent, storedChunk string
		storedChunkCount                       int
	)
	if err := db.QueryRowContext(t.Context(), `
		SELECT name, memory_content, chunk_count
		FROM knowledge_sources WHERE id=$1
	`, source.ID).Scan(&storedName, &storedContent, &storedChunkCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT content FROM knowledge_chunks
		WHERE source_id=$1 AND ordinal=0
	`, source.ID).Scan(&storedChunk); err != nil {
		t.Fatal(err)
	}
	if storedName != source.Name || storedContent != source.Content ||
		storedChunkCount != 1 || storedChunk != "chunk" {
		t.Fatalf(
			"failed memory update was not atomic: name=%q content=%q count=%d chunk=%q",
			storedName,
			storedContent,
			storedChunkCount,
			storedChunk,
		)
	}

	rejectedSource := &models.KnowledgeSource{
		ProjectID: projectID, TenantID: tenantID, UserID: userID,
		SourceType: "memory", Name: "Rejected memory", Content: "raw",
	}
	rejectedChunks := []models.KnowledgeChunk{{
		Ordinal: 0, Content: "raw", Vector: []float64{1}, TokenCount: 1,
	}}
	if err := postgresStore.CreateMemorySourceWithChunks(
		t.Context(), rejectedSource, rejectedChunks,
	); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("quota-rejected memory create error = %v, want ErrStorageQuota", err)
	}
	var rejectedCount int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM knowledge_sources
		WHERE project_id=$1 AND name='Rejected memory'
	`, projectID).Scan(&rejectedCount); err != nil {
		t.Fatal(err)
	}
	if rejectedCount != 0 {
		t.Fatalf("quota-rejected memory left %d source rows", rejectedCount)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE tenants SET storage_quota_gb=1 WHERE id=$1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	updatedName := "Updated memory"
	updatedContent := "new!"
	updatedChunks := []models.KnowledgeChunk{{
		SourceID: source.ID, ProjectID: projectID,
		Ordinal: 0, Content: "fresh",
		Vector: []float64{0.75, 0.25}, TokenCount: 2,
	}}
	updatedSource, err := postgresStore.UpdateMemorySourceWithChunks(
		t.Context(), source.ID, projectID, tenantID, userID,
		&updatedName, &updatedContent, updatedChunks,
	)
	if err != nil {
		t.Fatalf("atomically update memory: %v", err)
	}
	if updatedSource.Name != updatedName ||
		updatedSource.Content != updatedContent ||
		updatedSource.Status != "ready" ||
		updatedSource.ChunkCount != 1 {
		t.Fatalf("updated memory metadata = %#v", updatedSource)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT content FROM knowledge_chunks
		WHERE source_id=$1 AND ordinal=0
	`, source.ID).Scan(&storedChunk); err != nil {
		t.Fatal(err)
	}
	if storedChunk != "fresh" {
		t.Fatalf("updated memory chunk = %q, want fresh", storedChunk)
	}
	originalName := source.Name
	originalContent := source.Content
	source, err = postgresStore.UpdateMemorySourceWithChunks(
		t.Context(), source.ID, projectID, tenantID, userID,
		&originalName, &originalContent, chunks,
	)
	if err != nil {
		t.Fatalf("restore memory fixture: %v", err)
	}

	artifact := &models.AIArtifact{
		TenantID:        tenantID,
		UserID:          userID,
		SessionID:       &sessionID,
		ProjectID:       &projectID,
		ArtifactType:    "notes",
		Title:           "Notes",
		Content:         "note",
		ContextPolicy:   map[string]any{"mode": "smart"},
		ContextTokens:   4,
		Model:           "mock-model",
		ClientRequestID: uuid.NewString(),
	}
	if err := postgresStore.CreateAIArtifact(t.Context(), artifact); err != nil {
		t.Fatalf("create AI artifact: %v", err)
	}
	sessionChunks := []models.SessionAIChunk{{
		Ordinal:    0,
		Content:    "abc",
		TokenCount: 1,
	}}
	if err := postgresStore.SyncSessionAIChunksFromTranscripts(
		t.Context(), sessionID, tenantID, userID, sessionChunks,
	); err != nil {
		t.Fatalf("store session AI chunks: %v", err)
	}

	summary, err := postgresStore.GetUsageSummary(
		t.Context(), tenantID, time.Now().UTC().Format("2006-01"),
	)
	if err != nil {
		t.Fatalf("get AI storage usage: %v", err)
	}
	// Raw memory (4) + extracted chunk (5) + legacy vector (2*4)
	// + artifact (4) + session chunk (3).
	const expectedBytes = 24
	if math.Abs(summary.StorageMB-float64(expectedBytes)/(1024*1024)) > 1e-12 {
		t.Fatalf(
			"reported AI storage = %.12f MiB, want %d bytes",
			summary.StorageMB, expectedBytes,
		)
	}

	if _, err := db.ExecContext(t.Context(), `
		UPDATE tenants SET storage_quota_gb=0 WHERE id=$1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	sourcePreview, err := postgresStore.PreviewAIIndex(
		t.Context(),
		"source",
		source.ID,
		tenantID,
		userID,
		"text-embedding-3-small",
	)
	if err != nil {
		t.Fatalf("preview source index: %v", err)
	}
	indexJob := &models.AIIndexJob{
		TenantID:        tenantID,
		UserID:          userID,
		TargetType:      "source",
		TargetID:        source.ID,
		Model:           "text-embedding-3-small",
		Dimensions:      1536,
		ChunkCount:      sourcePreview.PendingChunks,
		EstimatedTokens: sourcePreview.EstimatedTokens,
		ContentDigest:   sourcePreview.ContentDigest,
		ClientRequestID: uuid.NewString(),
	}
	if _, err := postgresStore.CreateAIIndexJob(
		t.Context(), indexJob,
	); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("index storage preflight error = %v, want ErrStorageQuota", err)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE tenants SET storage_quota_gb=1 WHERE id=$1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	sessionPreview, err := postgresStore.PreviewAIIndex(
		t.Context(),
		"session",
		sessionID,
		tenantID,
		userID,
		"text-embedding-3-small",
	)
	if err != nil {
		t.Fatalf("preview session index: %v", err)
	}
	sessionIndexJob := &models.AIIndexJob{
		TenantID:        tenantID,
		UserID:          userID,
		TargetType:      "session",
		TargetID:        sessionID,
		Model:           "text-embedding-3-small",
		Dimensions:      1536,
		ChunkCount:      sessionPreview.PendingChunks,
		EstimatedTokens: sessionPreview.EstimatedTokens,
		ContentDigest:   sessionPreview.ContentDigest,
		ClientRequestID: uuid.NewString(),
	}
	if created, err := postgresStore.CreateAIIndexJob(
		t.Context(), sessionIndexJob,
	); err != nil || !created {
		t.Fatalf("create session index job: created=%v err=%v", created, err)
	}
	var sessionIndexStatus string
	if err := db.QueryRowContext(t.Context(), `
		SELECT embedding_status
		FROM session_ai_chunks
		WHERE session_id=$1 AND ordinal=0
	`, sessionID).Scan(&sessionIndexStatus); err != nil {
		t.Fatal(err)
	}
	if sessionIndexStatus != models.AIIndexStatusQueued {
		t.Fatalf("session chunk status = %q, want queued", sessionIndexStatus)
	}
	if _, err := postgresStore.CancelAIIndexJob(
		t.Context(), sessionIndexJob.ID, tenantID, userID,
	); err != nil {
		t.Fatalf("cancel session index job: %v", err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT embedding_status
		FROM session_ai_chunks
		WHERE session_id=$1 AND ordinal=0
	`, sessionID).Scan(&sessionIndexStatus); err != nil {
		t.Fatal(err)
	}
	if sessionIndexStatus != models.AIIndexStatusUnindexed {
		t.Fatalf("cancelled session chunk status = %q, want unindexed", sessionIndexStatus)
	}

	fileSource := &models.KnowledgeSource{
		ProjectID:    projectID,
		TenantID:     tenantID,
		UserID:       userID,
		SourceType:   "file",
		Name:         "delete-me.txt",
		MediaType:    "text/plain",
		SizeBytes:    7,
		SHA256:       strings.Repeat("a", 64),
		BlobPath:     "/app/data/knowledge/" + uuid.NewString() + ".txt",
		OCRLanguages: []string{"eng"},
		Status:       "ready",
	}
	if err := postgresStore.CreateKnowledgeSource(
		t.Context(), fileSource,
	); err != nil {
		t.Fatalf("create deletion-outbox file source: %v", err)
	}
	deletedBlob, err := postgresStore.DeleteKnowledgeSource(
		t.Context(), fileSource.ID, projectID, userID,
	)
	if err != nil {
		t.Fatalf("delete file source into outbox: %v", err)
	}
	if deletedBlob != fileSource.BlobPath {
		t.Fatalf("deleted blob path = %q, want %q", deletedBlob, fileSource.BlobPath)
	}
	deletion, err := postgresStore.ClaimKnowledgeBlobDeletion(
		t.Context(), "integration-delete-worker", time.Minute,
	)
	if err != nil {
		t.Fatalf("claim blob deletion: %v", err)
	}
	if deletion == nil || deletion.BlobPath != fileSource.BlobPath ||
		deletion.TenantID != tenantID || deletion.UserID != userID {
		t.Fatalf("claimed blob deletion = %#v", deletion)
	}
	if err := postgresStore.CompleteKnowledgeBlobDeletion(
		t.Context(), deletion.ID, "integration-delete-worker",
	); err != nil {
		t.Fatalf("complete blob deletion: %v", err)
	}
	var deletionCount int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM knowledge_blob_deletions WHERE blob_path=$1
	`, fileSource.BlobPath).Scan(&deletionCount); err != nil {
		t.Fatal(err)
	}
	if deletionCount != 0 {
		t.Fatalf("completed blob deletion left %d outbox rows", deletionCount)
	}

	if _, err := postgresStore.DeleteKnowledgeSource(
		t.Context(), source.ID, projectID, userID,
	); err != nil {
		t.Fatalf("delete memory source: %v", err)
	}
	if err := postgresStore.DeleteAIArtifact(
		t.Context(), artifact.ID, tenantID, userID,
	); err != nil {
		t.Fatalf("delete AI artifact: %v", err)
	}
	if err := postgresStore.SyncSessionAIChunksFromTranscripts(
		t.Context(), sessionID, tenantID, userID, nil,
	); err != nil {
		t.Fatalf("delete session AI chunks: %v", err)
	}
	summary, err = postgresStore.GetUsageSummary(
		t.Context(), tenantID, time.Now().UTC().Format("2006-01"),
	)
	if err != nil {
		t.Fatalf("get released AI storage usage: %v", err)
	}
	if summary.StorageMB != 0 {
		t.Fatalf("AI storage was not released: %.12f MiB", summary.StorageMB)
	}
}
