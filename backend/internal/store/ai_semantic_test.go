package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

func TestPGVectorTextRoundTrip(t *testing.T) {
	vector := make([]float64, semanticEmbeddingDimensions)
	vector[0] = 0.125
	vector[17] = -2.5
	vector[len(vector)-1] = 1.0 / 3.0

	encoded, err := formatPGVector(vector)
	if err != nil {
		t.Fatalf("format vector: %v", err)
	}
	decoded, err := parsePGVector(encoded)
	if err != nil {
		t.Fatalf("parse vector: %v", err)
	}
	if len(decoded) != semanticEmbeddingDimensions {
		t.Fatalf("decoded dimensions = %d", len(decoded))
	}
	for index := range vector {
		if decoded[index] != vector[index] {
			t.Fatalf("component %d = %v, want %v", index, decoded[index], vector[index])
		}
	}
}

func TestPGVectorTextValidation(t *testing.T) {
	if _, err := formatPGVector(make([]float64, 3)); err == nil {
		t.Fatal("wrong vector dimensions were accepted")
	}
	vector := make([]float64, semanticEmbeddingDimensions)
	vector[9] = math.NaN()
	if _, err := formatPGVector(vector); err == nil {
		t.Fatal("NaN vector component was accepted")
	}
	if _, err := parsePGVector("[1,not-a-number]"); err == nil {
		t.Fatal("invalid pgvector text was accepted")
	}
}

func TestSessionAIChunksContentHashIsStableAndContentBound(t *testing.T) {
	chunks := []models.SessionAIChunk{
		{Ordinal: 0, Content: "first", TokenCount: 1},
		{Ordinal: 1, Content: "second", TokenCount: 2},
	}
	base := sessionAIChunksContentHash(chunks)
	if len(base) != 64 || base != sessionAIChunksContentHash(chunks) {
		t.Fatalf("unstable session chunk hash %q", base)
	}
	changed := append([]models.SessionAIChunk(nil), chunks...)
	changed[1].Content = "changed"
	if base == sessionAIChunksContentHash(changed) {
		t.Fatal("session chunk hash ignored a content change")
	}
	if base == sessionAIChunksContentHash(chunks[:1]) {
		t.Fatal("session chunk hash ignored a removed chunk")
	}
}

func TestReciprocalRankFusionDeduplicatesAndCombinesRanks(t *testing.T) {
	got := rrfRanks(
		[]string{"semantic-first", "shared-best", "semantic-only"},
		[]string{"shared-best", "lexical-only", "semantic-first"},
		4,
	)
	ids := make([]string, len(got))
	for index := range got {
		ids[index] = got[index].id
	}
	want := []string{
		"shared-best",
		"semantic-first",
		"lexical-only",
		"semantic-only",
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("fused ids = %v, want %v", ids, want)
	}
	if got[0].score <= got[1].score {
		t.Fatalf("combined rank did not promote shared result: %#v", got)
	}
}

func TestEmptySearchCandidatesReportNoRetrieval(t *testing.T) {
	if got := retrievalMode(0, 0); got != models.AIRetrievalModeNone {
		t.Fatalf("empty candidate retrieval mode = %q, want none", got)
	}
	search := &models.KnowledgeSearchResult{
		Chunks:        fuseKnowledgeCandidates(nil, nil, 5),
		RetrievalMode: retrievalMode(0, 0),
	}
	if len(search.Chunks) != 0 ||
		search.RetrievalMode != models.AIRetrievalModeNone {
		t.Fatalf("unexpected empty PostgreSQL search metadata: %#v", search)
	}
}

func TestIndexAndOCRValidation(t *testing.T) {
	job := &models.AIIndexJob{
		TargetType:    "PROJECT",
		TargetID:      "project-id",
		Model:         "text-embedding-3-small",
		ChunkCount:    4,
		ContentDigest: strings.Repeat("a", sha256DigestBytes*2),
	}
	if err := validateAIIndexJob(job); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	if job.TargetType != "project" ||
		job.Dimensions != semanticEmbeddingDimensions ||
		job.MaxAttempts != 3 {
		t.Fatalf("job defaults were not normalized: %#v", job)
	}
	if job.EstimatedDP != 0 {
		t.Fatalf("estimated DP = %v, want canonical zero", job.EstimatedDP)
	}
	job.Dimensions = 3072
	if err := validateAIIndexJob(job); err == nil {
		t.Fatal("unsupported embedding dimensions were accepted")
	}
	if err := validateOCRLanguages([]string{"eng", "chi_sim", "jpn", "kor"}); err != nil {
		t.Fatalf("supported OCR languages rejected: %v", err)
	}
	if err := validateOCRLanguages([]string{"eng", "eng"}); err == nil {
		t.Fatal("duplicate OCR languages were accepted")
	}
	if err := validateOCRLanguages([]string{"fra"}); err == nil {
		t.Fatal("unsupported OCR language was accepted")
	}
	generation := &models.AIGenerationRequest{
		TenantID:        "tenant-id",
		UserID:          "user-id",
		ClientRequestID: "request-id",
		RequestKind:     "Artifact",
		RequestHash:     strings.Repeat("a", 64),
		LeaseOwner:      "worker-id",
	}
	if err := validateAIGenerationRequest(generation); err != nil {
		t.Fatalf("valid generation reservation rejected: %v", err)
	}
	if generation.RequestKind != "artifact" {
		t.Fatalf("generation request kind = %q, want artifact", generation.RequestKind)
	}
	generation.RequestHash = "not-sha256"
	if err := validateAIGenerationRequest(generation); err == nil {
		t.Fatal("invalid generation request hash was accepted")
	}
}

func TestAIIndexConfirmedRequestComparisonCoversPaidSnapshot(t *testing.T) {
	base := models.AIIndexJob{
		TargetType:      "project",
		TargetID:        "project-id",
		Model:           "text-embedding-3-small",
		Dimensions:      semanticEmbeddingDimensions,
		ChunkCount:      4,
		EstimatedTokens: 120,
		EstimatedDP:     0.123456789,
		ContentDigest:   strings.Repeat("a", sha256DigestBytes*2),
	}
	if err := validateAIIndexJob(&base); err != nil {
		t.Fatalf("validate base request: %v", err)
	}
	if base.EstimatedDP != 0.12345679 {
		t.Fatalf("canonical estimated DP = %.10f", base.EstimatedDP)
	}
	if !sameAIIndexConfirmedRequest(&base, &base) {
		t.Fatal("identical confirmed request did not replay")
	}

	tests := []struct {
		name   string
		mutate func(*models.AIIndexJob)
	}{
		{"target type", func(job *models.AIIndexJob) { job.TargetType = "source" }},
		{"target id", func(job *models.AIIndexJob) { job.TargetID = "other" }},
		{"model", func(job *models.AIIndexJob) { job.Model += "-v2" }},
		{"dimensions", func(job *models.AIIndexJob) { job.Dimensions++ }},
		{"chunk count", func(job *models.AIIndexJob) { job.ChunkCount++ }},
		{"estimated tokens", func(job *models.AIIndexJob) { job.EstimatedTokens++ }},
		{"estimated DP", func(job *models.AIIndexJob) { job.EstimatedDP += 0.00000001 }},
		{"content digest", func(job *models.AIIndexJob) {
			job.ContentDigest = strings.Repeat("b", sha256DigestBytes*2)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			test.mutate(&changed)
			if sameAIIndexConfirmedRequest(&base, &changed) {
				t.Fatalf("changed %s was accepted as an idempotent replay", test.name)
			}
		})
	}
}

func TestConservativeTokenCountUsesUTF8Bytes(t *testing.T) {
	const content = "新加坡语言"
	if got, want := conservativeTokenCount(content, 1), len(content); got != want {
		t.Fatalf("CJK token estimate = %d, want %d UTF-8 bytes", got, want)
	}
	if got := conservativeTokenCount(content, len(content)+5); got != len(content)+5 {
		t.Fatalf("larger caller estimate was reduced to %d", got)
	}
}

func TestMigration019DefinesProductionIndexingAndMetadataBackfill(t *testing.T) {
	data, err := os.ReadFile("../../migrations/019_ai_knowledge_production.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	migration := string(data)
	required := []string{
		"CREATE EXTENSION IF NOT EXISTS vector",
		"CREATE EXTENSION IF NOT EXISTS pg_trgm",
		"ADD COLUMN IF NOT EXISTS embedding VECTOR(1536)",
		"CREATE TABLE IF NOT EXISTS session_ai_chunks",
		"CREATE TABLE IF NOT EXISTS ai_index_jobs",
		"CREATE TABLE IF NOT EXISTS ai_generation_requests",
		"session_id UUID REFERENCES sessions(id) ON DELETE CASCADE",
		"expires_at TIMESTAMP WITH TIME ZONE NOT NULL",
		"idx_ai_generation_requests_expiry",
		"USING hnsw (embedding vector_cosine_ops)",
		"USING gin (content gin_trgm_ops)",
		"client_request_id",
		"idx_ai_index_jobs_active_target",
		"lease_expires_at",
		"ocr_languages TEXT[]",
		"memory_content TEXT",
		"ai_chunks_content_hash CHAR(64)",
		"content_digest CHAR(64)",
		"SET token_count=GREATEST(token_count, octet_length(content))",
	}
	for _, fragment := range required {
		if !strings.Contains(migration, fragment) {
			t.Errorf("migration is missing %q", fragment)
		}
	}
	forbidden := []string{
		"UPDATE knowledge_chunks SET embedding",
		"INSERT INTO ai_index_jobs SELECT",
	}
	for _, fragment := range forbidden {
		if strings.Contains(migration, fragment) {
			t.Errorf("migration contains automatic indexing/backfill: %q", fragment)
		}
	}
}

func TestPostgresAISemanticSchemaOptIn(t *testing.T) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &PostgresStore{db: db}
	if err := store.VerifySchema(t.Context()); err != nil {
		t.Fatalf("verify migrated schema: %v", err)
	}
	var extensions int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM pg_extension WHERE extname IN ('vector', 'pg_trgm')
	`).Scan(&extensions); err != nil {
		t.Fatal(err)
	}
	if extensions != 2 {
		t.Fatalf("AI search extensions installed = %d, want 2", extensions)
	}
	var indexes int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*)
		FROM pg_indexes
		WHERE indexname IN (
		  'idx_knowledge_chunks_embedding_hnsw',
		  'idx_knowledge_chunks_content_trgm',
		  'idx_session_ai_chunks_embedding_hnsw',
		  'idx_session_ai_chunks_content_trgm',
		  'idx_ai_index_jobs_claim',
		  'idx_ai_index_jobs_active_target',
		  'idx_ai_index_jobs_user_request'
		)
	`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if indexes != 7 {
		t.Fatalf("production AI indexes installed = %d, want 7", indexes)
	}
	var activeIndexDefinition string
	if err := db.QueryRowContext(t.Context(), `
		SELECT indexdef FROM pg_indexes
		WHERE indexname='idx_ai_index_jobs_active_target'
	`).Scan(&activeIndexDefinition); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(activeIndexDefinition), "model") ||
		!strings.Contains(strings.ToLower(activeIndexDefinition), "processing") {
		t.Fatalf("unexpected active-target unique index: %s", activeIndexDefinition)
	}
}

func TestPostgresKnowledgeChunkEmbeddingUpsertJobModesOptIn(t *testing.T) {
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
	ctx := t.Context()
	store := &PostgresStore{db: db}

	tenantID := uuid.NewString()
	userID := uuid.NewString()
	projectID := uuid.NewString()
	sourceID := uuid.NewString()
	chunkID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO tenants (
			id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions
		) VALUES ($1, 'AI jobless upsert test', $2, 'pro', 1000, 1, 10)
	`, tenantID, "ai-jobless-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(
			context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID,
		)
	})
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (
			id, tenant_id, email, password_hash, name, role,
			is_active, email_verified
		) VALUES ($1,$2,$3,'not-a-login','AI jobless upsert test','user',true,true)
	`, userID, tenantID, "ai-jobless-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO ai_projects (
			id, tenant_id, user_id, name, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,'AI jobless project','smart',64000)
	`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	const content = "jobless embedding update"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO knowledge_sources (
			id, project_id, tenant_id, user_id, source_type, name, media_type,
			size_bytes, status, chunk_count, extracted_text_bytes, vector_bytes,
			index_status, embedded_chunk_count
		) VALUES (
			$1,$2,$3,$4,'memory','Jobless memory','text/plain',
			$5,'ready',1,$5,0,'unindexed',0
		)
	`, sourceID, projectID, tenantID, userID, len(content)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO knowledge_chunks (
			id, source_id, project_id, ordinal, content, vector, token_count
		) VALUES ($1,$2,$3,0,$4,'{}'::real[],1)
	`, chunkID, sourceID, projectID, content); err != nil {
		t.Fatal(err)
	}

	vector := make([]float64, semanticEmbeddingDimensions)
	vector[0] = 1
	chunks := []models.KnowledgeChunk{{
		ID:         chunkID,
		SourceID:   sourceID,
		ProjectID:  projectID,
		Ordinal:    0,
		Content:    content,
		TokenCount: 1,
		Embedding:  vector,
	}}
	const model = "text-embedding-3-small"
	if err := store.UpsertKnowledgeChunkEmbeddings(
		ctx, sourceID, projectID, tenantID, userID, model, chunks,
	); err != nil {
		t.Fatalf("jobless embedding upsert: %v", err)
	}

	var chunkStatus, embeddedModel, sourceStatus string
	var dimensions, embeddedChunks int
	if err := db.QueryRowContext(ctx, `
		SELECT c.embedding_status, c.embedding_model, vector_dims(c.embedding),
		       s.index_status, s.embedded_chunk_count
		FROM knowledge_chunks c
		JOIN knowledge_sources s ON s.id=c.source_id
		WHERE c.id=$1
	`, chunkID).Scan(
		&chunkStatus,
		&embeddedModel,
		&dimensions,
		&sourceStatus,
		&embeddedChunks,
	); err != nil {
		t.Fatal(err)
	}
	if chunkStatus != models.AIIndexStatusReady ||
		embeddedModel != model ||
		dimensions != semanticEmbeddingDimensions ||
		sourceStatus != models.AIIndexStatusReady ||
		embeddedChunks != 1 {
		t.Fatalf(
			"jobless embedding state = chunk %q, model %q, dimensions %d, source %q, embedded %d",
			chunkStatus,
			embeddedModel,
			dimensions,
			sourceStatus,
			embeddedChunks,
		)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE knowledge_chunks
		SET embedding=NULL, embedding_model='', embedding_status='unindexed',
		    embedded_at=NULL
		WHERE id=$1
	`, chunkID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE knowledge_sources
		SET index_status='unindexed', embedding_model='',
		    embedded_chunk_count=0, vector_bytes=0, indexed_at=NULL
		WHERE id=$1
	`, sourceID); err != nil {
		t.Fatal(err)
	}
	const jobModel = "text-embedding-3-small-v2"
	preview, err := store.PreviewAIIndex(
		ctx, "project", projectID, tenantID, userID, jobModel,
	)
	if err != nil {
		t.Fatalf("preview job-backed upsert: %v", err)
	}
	job := &models.AIIndexJob{
		TenantID:        tenantID,
		UserID:          userID,
		TargetType:      "project",
		TargetID:        projectID,
		Model:           jobModel,
		ChunkCount:      preview.PendingChunks,
		EstimatedTokens: preview.EstimatedTokens,
		ContentDigest:   preview.ContentDigest,
		ClientRequestID: uuid.NewString(),
	}
	if created, err := store.CreateAIIndexJob(ctx, job); err != nil || !created {
		t.Fatalf("create job-backed upsert: created=%v err=%v", created, err)
	}
	claimed, err := store.ClaimAIIndexJobs(
		ctx, "upsert-"+uuid.NewString(), 128, time.Minute,
	)
	if err != nil {
		t.Fatalf("claim job-backed upsert: %v", err)
	}
	var claimedJob *models.AIIndexJob
	for index := range claimed {
		if claimed[index].ID == job.ID {
			claimedJob = &claimed[index]
			break
		}
	}
	if claimedJob == nil {
		t.Fatal("job-backed upsert was not claimed")
	}
	chunks[0].EmbeddingModel = ""
	chunks[0].EmbeddingStatus = ""
	chunks[0].EmbeddedAt = nil
	if err := store.UpsertKnowledgeChunkEmbeddingsForJob(
		ctx,
		claimedJob.ID,
		claimedJob.LeaseOwner,
		sourceID,
		projectID,
		tenantID,
		userID,
		jobModel,
		chunks,
		7,
	); err != nil {
		t.Fatalf("job-backed embedding upsert: %v", err)
	}
	var processedChunks int
	var actualTokens int64
	if err := db.QueryRowContext(ctx, `
		SELECT processed_chunks, actual_tokens
		FROM ai_index_jobs
		WHERE id=$1
	`, claimedJob.ID).Scan(&processedChunks, &actualTokens); err != nil {
		t.Fatal(err)
	}
	if processedChunks != 1 || actualTokens != 7 {
		t.Fatalf(
			"job-backed progress = chunks %d, tokens %d",
			processedChunks,
			actualTokens,
		)
	}
}

func TestPostgresAISemanticIsolationAndFencingOptIn(t *testing.T) {
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
	ctx := t.Context()
	store := &PostgresStore{db: db}

	tenantID := uuid.NewString()
	userID := uuid.NewString()
	projectID := uuid.NewString()
	sourceID := uuid.NewString()
	chunkID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO tenants (
			id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions
		) VALUES ($1, 'AI store test', $2, 'pro', 1000, 1, 10)
	`, tenantID, "ai-store-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(
			context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID,
		)
	})
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (
			id, tenant_id, email, password_hash, name, role,
			is_active, email_verified
		) VALUES ($1,$2,$3,'not-a-login','AI store test','user',true,true)
	`, userID, tenantID, "ai-store-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO ai_projects (
			id, tenant_id, user_id, name, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,'AI store project','smart',64000)
	`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}

	vector := make([]float64, semanticEmbeddingDimensions)
	vector[0] = 1
	encodedVector, err := formatPGVector(vector)
	if err != nil {
		t.Fatal(err)
	}
	const initialContent = "tenant-private semantic knowledge"
	const legacyCJKContent = "新加坡语言是独特的文化记忆"
	legacyChunkID := uuid.NewString()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO knowledge_sources (
			id, project_id, tenant_id, user_id, source_type, name, media_type,
			size_bytes, status, chunk_count, extracted_text_bytes, vector_bytes,
			index_status, embedding_model, embedding_dimensions,
			embedded_chunk_count
		) VALUES (
			$1,$2,$3,$4,'memory','Private memory','text/plain',
			$5,'ready',1,$5,6144,'ready','text-embedding-3-small',1536,1
		)
	`, sourceID, projectID, tenantID, userID, len(initialContent)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO knowledge_chunks (
			id, source_id, project_id, ordinal, content, vector, embedding,
			embedding_model, embedding_status, token_count, embedded_at
		) VALUES (
			$1,$2,$3,0,$4,'{}'::real[],$5::vector(1536),
			'text-embedding-3-small','ready',8,NOW()
		)
	`, chunkID, sourceID, projectID, initialContent, encodedVector); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO knowledge_chunks (
			id, source_id, project_id, ordinal, content, vector, token_count
		) VALUES ($1,$2,$3,1,$4,'{}'::real[],1)
	`, legacyChunkID, sourceID, projectID, legacyCJKContent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE knowledge_sources
		SET chunk_count=2,
		    extracted_text_bytes=extracted_text_bytes+$2
		WHERE id=$1
	`, sourceID, len(legacyCJKContent)); err != nil {
		t.Fatal(err)
	}

	search, err := store.HybridProjectKnowledgeChunks(
		ctx, projectID, tenantID, userID, "",
		"text-embedding-3-small", vector, 5,
	)
	if err != nil {
		t.Fatalf("semantic search: %v", err)
	}
	if len(search.Chunks) != 1 || search.Chunks[0].ID != chunkID ||
		search.RetrievalMode != models.AIRetrievalModeSemantic {
		t.Fatalf("unexpected semantic result: %#v", search)
	}
	isolated, err := store.HybridProjectKnowledgeChunks(
		ctx, projectID, uuid.NewString(), uuid.NewString(), "",
		"text-embedding-3-small", vector, 5,
	)
	if err != nil {
		t.Fatalf("isolated semantic search: %v", err)
	}
	if len(isolated.Chunks) != 0 {
		t.Fatalf("cross-tenant semantic rows leaked: %#v", isolated.Chunks)
	}
	if isolated.RetrievalMode != models.AIRetrievalModeNone {
		t.Fatalf(
			"empty isolated search mode = %q, want none",
			isolated.RetrievalMode,
		)
	}
	unmatched, err := store.HybridProjectKnowledgeChunks(
		ctx, projectID, tenantID, userID, "zzzz-unmatched-query-zzzz",
		"text-embedding-3-small", nil, 5,
	)
	if err != nil {
		t.Fatalf("unmatched lexical search: %v", err)
	}
	if len(unmatched.Chunks) != 0 ||
		unmatched.RetrievalMode != models.AIRetrievalModeNone {
		t.Fatalf("unmatched PostgreSQL search = %#v, want empty/none", unmatched)
	}
	shortCJK, err := store.HybridProjectKnowledgeChunks(
		ctx, projectID, tenantID, userID, "新加坡",
		"text-embedding-3-small", nil, 5,
	)
	if err != nil {
		t.Fatalf("short CJK lexical search: %v", err)
	}
	if len(shortCJK.Chunks) == 0 ||
		shortCJK.Chunks[0].ID != legacyChunkID ||
		shortCJK.RetrievalMode != models.AIRetrievalModeLexicalFallback {
		t.Fatalf("short CJK PostgreSQL search = %#v", shortCJK)
	}

	preview, err := store.PreviewAIIndex(
		ctx, "project", projectID, tenantID, userID,
		"text-embedding-3-small-v2",
	)
	if err != nil {
		t.Fatalf("preview changed model: %v", err)
	}
	if preview.IndexStatus != models.AIIndexStatusStale {
		t.Fatalf("changed-model status = %q, want stale", preview.IndexStatus)
	}
	minimumEstimate := int64(len(initialContent) + len(legacyCJKContent))
	if preview.EstimatedTokens < minimumEstimate {
		t.Fatalf(
			"CJK preview estimate = %d, want at least %d UTF-8 bytes",
			preview.EstimatedTokens,
			minimumEstimate,
		)
	}
	var chunkStatus, sourceStatus string
	if err := db.QueryRowContext(ctx, `
		SELECT c.embedding_status, s.index_status
		FROM knowledge_chunks c
		JOIN knowledge_sources s ON s.id=c.source_id
		WHERE c.id=$1
	`, chunkID).Scan(&chunkStatus, &sourceStatus); err != nil {
		t.Fatal(err)
	}
	if chunkStatus != models.AIIndexStatusStale ||
		sourceStatus != models.AIIndexStatusStale {
		t.Fatalf("persistent stale status = chunk %q, source %q", chunkStatus, sourceStatus)
	}

	job := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: "project", TargetID: projectID,
		Model:      "text-embedding-3-small-v2",
		ChunkCount: preview.PendingChunks, EstimatedTokens: preview.EstimatedTokens,
		EstimatedDP:   0.123456789,
		ContentDigest: preview.ContentDigest, ClientRequestID: uuid.NewString(),
	}
	created, err := store.CreateAIIndexJob(ctx, job)
	if err != nil || !created {
		t.Fatalf("create project index job: created=%v err=%v", created, err)
	}
	replay := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: "project", TargetID: projectID,
		Model: job.Model, ChunkCount: job.ChunkCount,
		EstimatedTokens: job.EstimatedTokens, ContentDigest: job.ContentDigest,
		EstimatedDP:     job.EstimatedDP,
		ClientRequestID: job.ClientRequestID,
	}
	created, err = store.CreateAIIndexJob(ctx, replay)
	if err != nil || created || replay.ID != job.ID {
		t.Fatalf("index replay: created=%v job=%q err=%v", created, replay.ID, err)
	}
	changedReplay := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: "project", TargetID: projectID,
		Model: job.Model, Dimensions: job.Dimensions,
		ChunkCount: job.ChunkCount, EstimatedTokens: job.EstimatedTokens,
		EstimatedDP:     job.EstimatedDP,
		ContentDigest:   strings.Repeat("b", sha256DigestBytes*2),
		ClientRequestID: job.ClientRequestID,
	}
	if _, err := store.CreateAIIndexJob(ctx, changedReplay); !errors.Is(
		err, ErrIdempotencyConflict,
	) {
		t.Fatalf("changed-content idempotency replay error = %v", err)
	}
	duplicateExact := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: "project", TargetID: projectID,
		Model: job.Model, ChunkCount: job.ChunkCount,
		EstimatedTokens: job.EstimatedTokens, ContentDigest: job.ContentDigest,
		ClientRequestID: uuid.NewString(),
	}
	if _, err := store.CreateAIIndexJob(ctx, duplicateExact); !errors.Is(
		err, ErrIndexTargetBusy,
	) {
		t.Fatalf("second request id for active target error = %v", err)
	}
	duplicate := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: "source", TargetID: sourceID,
		Model: job.Model, ChunkCount: job.ChunkCount,
		EstimatedTokens: job.EstimatedTokens, ContentDigest: job.ContentDigest,
		ClientRequestID: uuid.NewString(),
	}
	if _, err := store.CreateAIIndexJob(ctx, duplicate); !errors.Is(
		err, ErrIndexTargetBusy,
	) {
		t.Fatalf("overlapping project/source job error = %v", err)
	}

	claimed, err := store.ClaimAIIndexJobs(
		ctx, "integration-"+uuid.NewString(), 128, time.Minute,
	)
	if err != nil {
		t.Fatalf("claim index jobs: %v", err)
	}
	var claimedJob *models.AIIndexJob
	for index := range claimed {
		if claimed[index].ID == job.ID {
			claimedJob = &claimed[index]
			break
		}
	}
	if claimedJob == nil {
		t.Fatal("created index job was not claimed")
	}
	cancelledJob, err := store.CancelAIIndexJob(
		ctx,
		claimedJob.ID,
		tenantID,
		userID,
	)
	if err != nil {
		t.Fatalf("cancel processing index job: %v", err)
	}
	if cancelledJob.Status != models.AIIndexJobStatusCancelled ||
		cancelledJob.LeaseOwner != claimedJob.LeaseOwner ||
		cancelledJob.LeaseExpiresAt == nil {
		t.Fatalf(
			"cancelled provider drain marker = status %q, owner %q, expiry %v",
			cancelledJob.Status,
			cancelledJob.LeaseOwner,
			cancelledJob.LeaseExpiresAt,
		)
	}
	if _, err := store.RetryAIIndexJob(
		ctx,
		claimedJob.ID,
		tenantID,
		userID,
	); !errors.Is(err, ErrIndexTargetBusy) {
		t.Fatalf("retry while cancelled provider is draining error = %v", err)
	}
	released, err := store.ReleaseCancelledAIIndexJobLease(
		ctx,
		claimedJob.ID,
		claimedJob.LeaseOwner,
	)
	if err != nil || !released {
		t.Fatalf("release cancelled provider drain: released=%v err=%v", released, err)
	}

	type retryResult struct {
		job *models.AIIndexJob
		err error
	}
	retryResults := make(chan retryResult, 2)
	retryStart := make(chan struct{})
	for range 2 {
		go func() {
			<-retryStart
			retriedJob, retryErr := store.RetryAIIndexJob(
				context.Background(),
				claimedJob.ID,
				tenantID,
				userID,
			)
			retryResults <- retryResult{job: retriedJob, err: retryErr}
		}()
	}
	close(retryStart)
	for range 2 {
		result := <-retryResults
		if result.err != nil {
			t.Fatalf("concurrent retry returned an error: %v", result.err)
		}
		if result.job == nil || result.job.ID != claimedJob.ID ||
			result.job.Status != models.AIIndexJobStatusQueued {
			t.Fatalf("concurrent retry returned %#v", result.job)
		}
	}
	reclaimed, err := store.ClaimAIIndexJobs(
		ctx,
		"integration-retry-"+uuid.NewString(),
		128,
		time.Minute,
	)
	if err != nil {
		t.Fatalf("reclaim retried index job: %v", err)
	}
	claimedJob = nil
	for index := range reclaimed {
		if reclaimed[index].ID == job.ID {
			claimedJob = &reclaimed[index]
			break
		}
	}
	if claimedJob == nil {
		t.Fatal("retried index job was not reclaimed")
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE knowledge_chunks SET content='edited while provider was running'
		WHERE id=$1
	`, chunkID); err != nil {
		t.Fatal(err)
	}
	staleBatch := []models.KnowledgeChunk{{
		ID: chunkID, SourceID: sourceID, ProjectID: projectID,
		Ordinal: 0, Content: initialContent, TokenCount: 8,
		Embedding: vector,
	}}
	err = store.UpsertKnowledgeChunkEmbeddingsForJob(
		ctx, claimedJob.ID, claimedJob.LeaseOwner,
		sourceID, projectID, tenantID, userID, claimedJob.Model, staleBatch,
		8,
	)
	if !errors.Is(err, ErrIndexContentChanged) {
		t.Fatalf("stale provider result error = %v", err)
	}
	if err := store.FailAIIndexJob(
		ctx, claimedJob.ID, claimedJob.LeaseOwner,
		ErrIndexContentChanged.Error(), false, 8,
	); err != nil {
		t.Fatalf("persist non-retryable index failure: %v", err)
	}
	var failedStatus, failedSourceStatus string
	var failedTokens int64
	if err := db.QueryRowContext(ctx, `
		SELECT j.status, j.actual_tokens, s.index_status
		FROM ai_index_jobs j
		JOIN knowledge_sources s ON s.id=$2
		WHERE j.id=$1
	`, claimedJob.ID, sourceID).Scan(
		&failedStatus, &failedTokens, &failedSourceStatus,
	); err != nil {
		t.Fatal(err)
	}
	if failedStatus != models.AIIndexJobStatusError || failedTokens != 8 ||
		failedSourceStatus != models.AIIndexStatusError {
		t.Fatalf(
			"failed job state = status %q, tokens %d, source %q",
			failedStatus, failedTokens, failedSourceStatus,
		)
	}

	exhaustedPreview, err := store.PreviewAIIndex(
		ctx, "project", projectID, tenantID, userID,
		"text-embedding-3-small-v3",
	)
	if err != nil {
		t.Fatalf("preview exhausted-attempt fixture: %v", err)
	}
	exhausted := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: "project", TargetID: projectID,
		Model:           "text-embedding-3-small-v3",
		ChunkCount:      exhaustedPreview.PendingChunks,
		EstimatedTokens: exhaustedPreview.EstimatedTokens,
		ContentDigest:   exhaustedPreview.ContentDigest,
		ClientRequestID: uuid.NewString(),
	}
	if created, err := store.CreateAIIndexJob(ctx, exhausted); err != nil || !created {
		t.Fatalf("create exhausted-attempt fixture: created=%v err=%v", created, err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE ai_index_jobs
		SET status='processing', lease_owner='expired-worker',
		    lease_expires_at=NOW()-INTERVAL '1 minute',
		    attempt_count=max_attempts
		WHERE id=$1
	`, exhausted.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `
		UPDATE knowledge_sources SET index_status='processing'
		WHERE id=$1
	`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAIIndexJobs(
		ctx, "cleanup-"+uuid.NewString(), 128, time.Minute,
	); err != nil {
		t.Fatalf("cleanup exhausted index jobs: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT j.status, s.index_status
		FROM ai_index_jobs j
		JOIN knowledge_sources s ON s.id=$2
		WHERE j.id=$1
	`, exhausted.ID, sourceID).Scan(
		&failedStatus, &failedSourceStatus,
	); err != nil {
		t.Fatal(err)
	}
	if failedStatus != models.AIIndexJobStatusError ||
		failedSourceStatus != models.AIIndexStatusError {
		t.Fatalf(
			"exhausted job state = job %q, source %q",
			failedStatus, failedSourceStatus,
		)
	}

	if _, err := db.ExecContext(ctx, `
		UPDATE tenants SET storage_quota_gb=0 WHERE id=$1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	artifact := &models.AIArtifact{
		TenantID: tenantID, UserID: userID,
		ArtifactType: "summary", Content: "quota-controlled output",
		ClientRequestID: uuid.NewString(),
	}
	if _, err := store.CreateAIArtifactIdempotent(
		ctx, artifact,
	); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("AI artifact quota error = %v", err)
	}
}
