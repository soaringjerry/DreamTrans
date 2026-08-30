package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/lib/pq"
)

const (
	maxEmbeddingBatchChunks = 64
	maxEmbeddingBatchTokens = 100_000
	defaultRRFConstant      = 60.0
	maxSessionAIChunks      = 20_000
)

func validateEmbeddingBatch(chunkCount, tokenCount int) error {
	if chunkCount <= 0 || chunkCount > maxEmbeddingBatchChunks {
		return fmt.Errorf("embedding batches must contain between 1 and 64 chunks")
	}
	if tokenCount < 0 || tokenCount > maxEmbeddingBatchTokens {
		return fmt.Errorf("embedding batches must not exceed 100000 estimated tokens")
	}
	return nil
}

func formatPGVector(values []float64) (string, error) {
	if len(values) != semanticEmbeddingDimensions {
		return "", fmt.Errorf(
			"embedding has %d dimensions, expected %d",
			len(values),
			semanticEmbeddingDimensions,
		)
	}
	buffer := make([]byte, 0, len(values)*12)
	buffer = append(buffer, '[')
	for index, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return "", fmt.Errorf("embedding contains a non-finite value")
		}
		if index > 0 {
			buffer = append(buffer, ',')
		}
		buffer = strconv.AppendFloat(buffer, value, 'g', -1, 64)
	}
	buffer = append(buffer, ']')
	return string(buffer), nil
}

func parsePGVector(value string) ([]float64, error) {
	value = strings.TrimSpace(value)
	if len(value) < 2 || value[0] != '[' || value[len(value)-1] != ']' {
		return nil, fmt.Errorf("invalid pgvector text")
	}
	body := strings.TrimSpace(value[1 : len(value)-1])
	if body == "" {
		return []float64{}, nil
	}
	parts := strings.Split(body, ",")
	vector := make([]float64, len(parts))
	for index, part := range parts {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return nil, fmt.Errorf("invalid pgvector component at index %d", index)
		}
		vector[index] = parsed
	}
	return vector, nil
}

// UpsertKnowledgeChunkEmbeddings stores one provider batch. Calls are limited
// to the provider contract (64 chunks / 100k estimated tokens).
func (s *PostgresStore) UpsertKnowledgeChunkEmbeddings(
	ctx context.Context,
	sourceID, projectID, tenantID, userID, model string,
	chunks []models.KnowledgeChunk,
) error {
	return s.upsertKnowledgeChunkEmbeddings(
		ctx, "", "", sourceID, projectID, tenantID, userID, model, chunks, 0,
	)
}

func (s *PostgresStore) UpsertKnowledgeChunkEmbeddingsForJob(
	ctx context.Context,
	jobID, workerID, sourceID, projectID, tenantID, userID, model string,
	chunks []models.KnowledgeChunk,
	actualTokenDelta int64,
) error {
	jobID = strings.TrimSpace(jobID)
	workerID = strings.TrimSpace(workerID)
	if jobID == "" || workerID == "" {
		return fmt.Errorf("job id and worker id are required")
	}
	err := s.upsertKnowledgeChunkEmbeddings(
		ctx, jobID, workerID, sourceID, projectID, tenantID, userID, model,
		chunks, actualTokenDelta,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

//nolint:gocyclo // The transaction validates every lease, quota, vector, and content fence atomically.
func (s *PostgresStore) upsertKnowledgeChunkEmbeddings(
	ctx context.Context,
	jobID, workerID, sourceID, projectID, tenantID, userID, model string,
	chunks []models.KnowledgeChunk,
	actualTokenDelta int64,
) error {
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 200 {
		return fmt.Errorf("embedding model is required and must be at most 200 characters")
	}
	if actualTokenDelta < 0 {
		return fmt.Errorf("actual token delta cannot be negative")
	}
	totalTokens := 0
	vectors := make([]string, len(chunks))
	for index := range chunks {
		chunk := &chunks[index]
		if chunk.TokenCount < 0 {
			return fmt.Errorf("invalid embedding token count")
		}
		chunk.TokenCount = conservativeTokenCount(chunk.Content, chunk.TokenCount)
		if totalTokens > maxEmbeddingBatchTokens-chunk.TokenCount {
			return fmt.Errorf("invalid embedding token count")
		}
		totalTokens += chunk.TokenCount
		vector, err := formatPGVector(chunk.Embedding)
		if err != nil {
			return err
		}
		vectors[index] = vector
	}
	if err := validateEmbeddingBatch(len(chunks), totalTokens); err != nil {
		return err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if jobID != "" {
		var processedChunks, expectedChunks int
		if err := tx.QueryRowContext(ctx, `
			SELECT processed_chunks, chunk_count
			FROM ai_index_jobs
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
			  AND status='processing' AND lease_owner=$4
			  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
			  AND model=$5
			  AND (
			    (target_type='project' AND project_id=$6)
			    OR (target_type='source' AND source_id=$7)
			)
			FOR UPDATE
		`, jobID, tenantID, userID, workerID, model,
			projectID, sourceID).Scan(&processedChunks, &expectedChunks); err != nil {
			return err
		}
		if len(chunks) > expectedChunks-processedChunks {
			return ErrIndexContentChanged
		}
	}
	var sourceChunkCount, quotaGB int
	if err := tx.QueryRowContext(ctx, `
		SELECT ks.chunk_count, t.storage_quota_gb
		FROM knowledge_sources ks
		JOIN tenants t ON t.id=ks.tenant_id
		WHERE ks.id=$1 AND ks.project_id=$2 AND ks.tenant_id=$3
		  AND ks.user_id=$4 AND ks.status='ready'
		FOR UPDATE
	`, sourceID, projectID, tenantID, userID).Scan(
		&sourceChunkCount, &quotaGB,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_chunks
		SET embedding_status='stale'
		WHERE source_id=$1 AND embedding IS NOT NULL
		  AND embedding_model<>$2 AND embedding_status='ready'
	`, sourceID, model); err != nil {
		return err
	}
	for index := range chunks {
		chunk := &chunks[index]
		if chunk.SourceID != "" && chunk.SourceID != sourceID {
			return fmt.Errorf("knowledge chunk source does not match target")
		}
		if chunk.ProjectID != "" && chunk.ProjectID != projectID {
			return fmt.Errorf("knowledge chunk project does not match target")
		}
		if strings.TrimSpace(chunk.ID) == "" || chunk.Ordinal < 0 ||
			strings.TrimSpace(chunk.Content) == "" {
			return fmt.Errorf("invalid knowledge chunk")
		}
		err := tx.QueryRowContext(ctx, `
			WITH input AS (
			  SELECT NULLIF($11::TEXT, '')::UUID AS job_id
			)
			UPDATE knowledge_chunks c
			SET embedding=$6::vector(1536),
			    embedding_model=$7,
			    embedding_status='ready',
			    embedding_error='',
			    token_count=$8,
			    embedded_at=NOW()
			FROM knowledge_sources s, input
			WHERE c.id=$5 AND c.source_id=$1 AND c.project_id=$2
			  AND c.ordinal=$3 AND c.content=$4
			  AND s.id=c.source_id AND s.project_id=$2
			  AND s.tenant_id=$9 AND s.user_id=$10 AND s.status='ready'
			  AND (
			    input.job_id IS NULL
			    OR EXISTS (
			      SELECT 1 FROM ai_index_job_chunks snapshot
			      WHERE snapshot.job_id=input.job_id
			        AND snapshot.chunk_id=c.id
			        AND snapshot.content_hash=
			          encode(digest(c.content, 'sha256'), 'hex')
			    )
			  )
			RETURNING c.id
		`, sourceID, projectID, chunk.Ordinal, chunk.Content, chunk.ID,
			vectors[index], model, chunk.TokenCount, tenantID, userID, jobID,
		).Scan(&chunk.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIndexContentChanged
		}
		if err != nil {
			return err
		}
		chunk.SourceID = sourceID
		chunk.ProjectID = projectID
		chunk.EmbeddingModel = model
		chunk.EmbeddingStatus = models.AIIndexStatusReady
		now := time.Now()
		chunk.EmbeddedAt = &now
	}

	var embeddedCount int
	var vectorBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COUNT(*) FILTER (
		    WHERE embedding IS NOT NULL
		      AND embedding_model=$2 AND embedding_status='ready'
		  ),
		  COALESCE(SUM(
		    COALESCE(cardinality(vector), 0) * 4
		    + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		  ), 0)
		FROM knowledge_chunks
		WHERE source_id=$1
	`, sourceID, model).Scan(&embeddedCount, &vectorBytes); err != nil {
		return err
	}
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((
		    SELECT COALESCE(SUM(a.storage_bytes), 0) FROM billing_accounts a JOIN users u ON u.billing_account_id = a.id WHERE u.tenant_id = $1
		  ), 0)
		  + COALESCE((
		      SELECT SUM(
		        size_bytes + extracted_text_bytes
		        + CASE WHEN id=$2 THEN $3 ELSE vector_bytes END
		      )
		      FROM knowledge_sources WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(content_bytes) FROM ai_artifacts WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(
		        octet_length(content)::bigint
		        + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		      )
		      FROM session_ai_chunks WHERE tenant_id=$1
		    ), 0)
	`, tenantID, sourceID, vectorBytes).Scan(&usedBytes); err != nil {
		return err
	}
	if exceedsStorageQuota(quotaGB, usedBytes) {
		return ErrStorageQuota
	}
	indexStatus := models.AIIndexStatusProcessing
	if sourceChunkCount > 0 && embeddedCount == sourceChunkCount {
		indexStatus = models.AIIndexStatusReady
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_sources
		SET index_status=$1::TEXT,
		    embedding_model=$2,
		    embedding_dimensions=1536,
		    embedded_chunk_count=$3,
		    vector_bytes=$4,
		    index_error_message='',
		    indexed_at=CASE
		      WHEN $1::TEXT='ready' THEN NOW()
		      ELSE indexed_at
		    END
		WHERE id=$5 AND project_id=$6 AND tenant_id=$7 AND user_id=$8
	`, indexStatus, model, embeddedCount, vectorBytes,
		sourceID, projectID, tenantID, userID); err != nil {
		return err
	}
	if jobID != "" {
		var updatedJobID string
		if err := tx.QueryRowContext(ctx, `
			UPDATE ai_index_jobs
			SET processed_chunks=processed_chunks + $3,
			    actual_tokens=actual_tokens + $4
			WHERE id=$1 AND status='processing' AND lease_owner=$2
			  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
			  AND processed_chunks + $3 <= chunk_count
			RETURNING id
		`, jobID, workerID, len(chunks), actualTokenDelta).Scan(
			&updatedJobID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) ListKnowledgeChunksForIndex(
	ctx context.Context,
	jobID, workerID string,
	targetType, targetID, tenantID, userID, model string,
	limit int,
) ([]models.KnowledgeChunk, error) {
	if targetType != "project" && targetType != "source" {
		return nil, fmt.Errorf("knowledge index target must be project or source")
	}
	if limit <= 0 || limit > maxEmbeddingBatchChunks {
		limit = maxEmbeddingBatchChunks
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := loadAIIndexJobForBatchTx(
		ctx,
		tx,
		jobID,
		workerID,
		targetType,
		targetID,
		tenantID,
		userID,
		model,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLeaseLost
		}
		return nil, err
	}
	if err := verifyAIIndexJobContentDigestTx(ctx, tx, job); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.source_id, c.project_id, c.ordinal, c.content, c.vector,
		       c.embedding_model, c.embedding_status, c.embedding_error,
		       c.token_count, c.embedded_at, s.name
		FROM ai_index_job_chunks snapshot
		JOIN knowledge_chunks c ON c.id=snapshot.chunk_id
		  AND snapshot.content_hash=encode(digest(c.content, 'sha256'), 'hex')
		JOIN knowledge_sources s ON s.id=c.source_id
		WHERE snapshot.job_id=$1
		  AND s.tenant_id=$2 AND s.user_id=$3 AND s.status='ready'
		  AND (
		    ($4='project' AND c.project_id=$5)
		    OR ($4='source' AND c.source_id=$5)
		  )
		  AND (
		    c.embedding IS NULL
		    OR c.embedding_model<>$6
		    OR c.embedding_status<>'ready'
		  )
		ORDER BY snapshot.chunk_order
		LIMIT $7
		FOR SHARE OF c, s
	`, jobID, tenantID, userID, targetType, targetID, model, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	chunks := make([]models.KnowledgeChunk, 0, limit)
	for rows.Next() {
		var chunk models.KnowledgeChunk
		if err := rows.Scan(
			&chunk.ID, &chunk.SourceID, &chunk.ProjectID, &chunk.Ordinal,
			&chunk.Content, pq.Array(&chunk.Vector), &chunk.EmbeddingModel,
			&chunk.EmbeddingStatus, &chunk.EmbeddingError, &chunk.TokenCount,
			&chunk.EmbeddedAt, &chunk.SourceName,
		); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return chunks, nil
}

// SyncSessionAIChunksFromTranscripts materializes a complete, contiguous set
// of free transcript chunks before previewing or creating a semantic job.
// Unchanged content keeps its embedding; changed content drops the old vector
// and becomes stale (or unindexed if it was never embedded).
func (s *PostgresStore) SyncSessionAIChunksFromTranscripts(
	ctx context.Context,
	sessionID, tenantID, userID string,
	chunks []models.SessionAIChunk,
) error {
	_, err := s.SyncSessionAIChunksFromTranscriptsAndCancelIndexJobs(
		ctx,
		sessionID,
		tenantID,
		userID,
		chunks,
	)
	return err
}

func (s *PostgresStore) SyncSessionAIChunksFromTranscriptsAndCancelIndexJobs(
	ctx context.Context,
	sessionID, tenantID, userID string,
	chunks []models.SessionAIChunk,
) ([]string, error) {
	if len(chunks) > maxSessionAIChunks {
		return nil, fmt.Errorf(
			"%w: session contains more than %d AI chunks",
			ErrSessionAIChunkLimit,
			maxSessionAIChunks,
		)
	}
	for index := range chunks {
		if chunks[index].Ordinal != index {
			return nil, fmt.Errorf("session AI chunk ordinals must be contiguous from zero")
		}
		if chunks[index].TokenCount < 0 ||
			strings.TrimSpace(chunks[index].Content) == "" {
			return nil, fmt.Errorf("invalid session AI chunk")
		}
		chunks[index].TokenCount = conservativeTokenCount(
			chunks[index].Content,
			chunks[index].TokenCount,
		)
	}
	contentHash := sessionAIChunksContentHash(chunks)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var storedContentHash string
	if err := tx.QueryRowContext(ctx, `
		SELECT ai_chunks_content_hash
		FROM sessions
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
	`, sessionID, tenantID, userID).Scan(&storedContentHash); err != nil {
		return nil, err
	}
	if storedContentHash == contentHash {
		return nil, tx.Commit()
	}
	cancelledJobIDs, err := coordinateAIIndexScopeMutationTx(
		ctx,
		tx,
		tenantID,
		userID,
		"session",
		sessionID,
		aiIndexMutationWhole,
		"",
	)
	if err != nil {
		return nil, err
	}
	quotaGB, err := lockTenantStorageQuota(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	if err := resetCancelledAIIndexTargetsTx(
		ctx,
		tx,
		tenantID,
		userID,
		cancelledJobIDs,
	); err != nil {
		return nil, err
	}
	if err := tx.QueryRowContext(ctx, `
		SELECT ai_chunks_content_hash
		FROM sessions
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		FOR UPDATE
	`, sessionID, tenantID, userID).Scan(&storedContentHash); err != nil {
		return nil, err
	}
	if storedContentHash == contentHash {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return cancelledJobIDs, nil
	}
	for index := range chunks {
		chunk := &chunks[index]
		err := tx.QueryRowContext(ctx, `
			INSERT INTO session_ai_chunks (
				tenant_id, user_id, session_id, ordinal, content, token_count,
				embedding, embedding_model, embedding_status, embedding_error,
				embedded_at
			) VALUES($1,$2,$3,$4,$5,$6,NULL,'','unindexed','',NULL)
			ON CONFLICT (session_id, ordinal) DO UPDATE
			SET content=excluded.content,
			    token_count=excluded.token_count,
			    embedding=CASE
			      WHEN session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedding
			      ELSE NULL
			    END,
			    embedding_model=CASE
			      WHEN session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedding_model
			      ELSE session_ai_chunks.embedding_model
			    END,
			    embedding_status=CASE
			      WHEN session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedding_status
			      WHEN session_ai_chunks.embedding IS NOT NULL THEN 'stale'
			      ELSE 'unindexed'
			    END,
			    embedding_error='',
			    embedded_at=CASE
			      WHEN session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedded_at
			      ELSE NULL
			    END
			WHERE session_ai_chunks.tenant_id=$1
			  AND session_ai_chunks.user_id=$2
			RETURNING id, embedding_model, embedding_status, embedded_at,
			          created_at, updated_at
		`, tenantID, userID, sessionID, chunk.Ordinal, chunk.Content,
			chunk.TokenCount).Scan(
			&chunk.ID, &chunk.EmbeddingModel, &chunk.EmbeddingStatus,
			&chunk.EmbeddedAt, &chunk.CreatedAt, &chunk.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		chunk.TenantID = tenantID
		chunk.UserID = userID
		chunk.SessionID = sessionID
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM session_ai_chunks
		WHERE session_id=$1 AND tenant_id=$2 AND user_id=$3
		  AND ordinal >= $4
	`, sessionID, tenantID, userID, len(chunks)); err != nil {
		return nil, err
	}
	if err := enforceTenantAIStorageQuota(ctx, tx, tenantID, quotaGB); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sessions
		SET ai_chunks_content_hash=$4
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
	`, sessionID, tenantID, userID, contentHash); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cancelledJobIDs, nil
}

func sessionAIChunksContentHash(chunks []models.SessionAIChunk) string {
	hasher := sha256.New()
	for index := range chunks {
		_, _ = hasher.Write([]byte(strconv.Itoa(chunks[index].Ordinal)))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(chunks[index].Content))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(strconv.Itoa(chunks[index].TokenCount)))
		_, _ = hasher.Write([]byte{0xff})
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// UpsertSessionAIChunks stores transcript-derived chunks. Rows without an
// embedding remain unindexed and can be fetched by ListSessionAIChunksForIndex.
func (s *PostgresStore) UpsertSessionAIChunks(
	ctx context.Context,
	sessionID, tenantID, userID, model string,
	chunks []models.SessionAIChunk,
) error {
	return s.upsertSessionAIChunks(
		ctx, "", "", sessionID, tenantID, userID, model, chunks, 0,
	)
}

func (s *PostgresStore) UpsertSessionAIChunksForJob(
	ctx context.Context,
	jobID, workerID, sessionID, tenantID, userID, model string,
	chunks []models.SessionAIChunk,
	actualTokenDelta int64,
) error {
	jobID = strings.TrimSpace(jobID)
	workerID = strings.TrimSpace(workerID)
	if jobID == "" || workerID == "" {
		return fmt.Errorf("job id and worker id are required")
	}
	err := s.upsertSessionAIChunks(
		ctx, jobID, workerID, sessionID, tenantID, userID, model,
		chunks, actualTokenDelta,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

func (s *PostgresStore) upsertSessionAIChunks(
	ctx context.Context,
	jobID, workerID, sessionID, tenantID, userID, model string,
	chunks []models.SessionAIChunk,
	actualTokenDelta int64,
) error {
	if len(chunks) == 0 || len(chunks) > maxEmbeddingBatchChunks {
		return fmt.Errorf("session chunk batches must contain between 1 and 64 chunks")
	}
	model = strings.TrimSpace(model)
	if actualTokenDelta < 0 {
		return fmt.Errorf("actual token delta cannot be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
		  SELECT 1 FROM sessions
		  WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		)
	`, sessionID, tenantID, userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return sql.ErrNoRows
	}
	if jobID != "" {
		// Session content mutations and deletion fence the active job before
		// taking the tenant quota lock. Keep the paid worker in the same
		// job -> tenant order so neither side can hold one row while waiting
		// for the other.
		var processedChunks, expectedChunks int
		if err := tx.QueryRowContext(ctx, `
			SELECT processed_chunks, chunk_count
			FROM ai_index_jobs
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
			  AND status='processing' AND lease_owner=$4
			  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
			  AND model=$5 AND target_type='session' AND session_id=$6
			FOR UPDATE
		`, jobID, tenantID, userID, workerID, model, sessionID).Scan(
			&processedChunks, &expectedChunks,
		); err != nil {
			return err
		}
		if len(chunks) > expectedChunks-processedChunks {
			return ErrIndexContentChanged
		}
	}
	quotaGB, err := lockTenantStorageQuota(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	for index := range chunks {
		chunk := &chunks[index]
		if chunk.Ordinal < 0 || chunk.TokenCount < 0 ||
			strings.TrimSpace(chunk.Content) == "" {
			return fmt.Errorf("invalid session AI chunk")
		}
		chunk.TokenCount = conservativeTokenCount(chunk.Content, chunk.TokenCount)
		var vector any
		status := models.AIIndexStatusUnindexed
		embeddedModel := ""
		if len(chunk.Embedding) > 0 {
			encoded, err := formatPGVector(chunk.Embedding)
			if err != nil {
				return err
			}
			vector = encoded
			status = models.AIIndexStatusReady
			embeddedModel = model
			if embeddedModel == "" {
				return fmt.Errorf("embedding model is required")
			}
		}
		if jobID != "" {
			if strings.TrimSpace(chunk.ID) == "" || vector == nil {
				return fmt.Errorf("indexed session chunks require an id and embedding")
			}
			err := tx.QueryRowContext(ctx, `
				UPDATE session_ai_chunks c
				SET embedding=$7::vector(1536),
				    embedding_model=$8,
				    embedding_status='ready',
				    embedding_error='',
				    token_count=$6,
				    embedded_at=NOW()
				FROM sessions se
				WHERE c.id=$9 AND c.tenant_id=$1 AND c.user_id=$2
				  AND c.session_id=$3 AND c.ordinal=$4 AND c.content=$5
				  AND se.id=c.session_id
				  AND se.tenant_id=$1 AND se.user_id=$2
				  AND EXISTS (
				    SELECT 1 FROM ai_index_job_chunks snapshot
				    WHERE snapshot.job_id=$10
				      AND snapshot.chunk_id=c.id
				      AND snapshot.content_hash=
				        encode(digest(c.content, 'sha256'), 'hex')
				  )
				RETURNING c.id, c.created_at, c.updated_at
			`, tenantID, userID, sessionID, chunk.Ordinal, chunk.Content,
				chunk.TokenCount, vector, embeddedModel, chunk.ID, jobID,
			).Scan(&chunk.ID, &chunk.CreatedAt, &chunk.UpdatedAt)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIndexContentChanged
			}
			if err != nil {
				return err
			}
		} else {
			err := tx.QueryRowContext(ctx, `
			INSERT INTO session_ai_chunks (
				tenant_id, user_id, session_id, ordinal, content, token_count,
				embedding, embedding_model, embedding_status, embedded_at
			) VALUES(
				$1,$2,$3,$4,$5,$6,
				CASE
				  WHEN $7::vector(1536) IS NULL THEN NULL
				  ELSE $7::vector(1536)
				END,
				$8,$9,
				CASE WHEN $7::vector(1536) IS NULL THEN NULL ELSE NOW() END
			)
			ON CONFLICT (session_id, ordinal) DO UPDATE
			SET content=excluded.content,
			    token_count=excluded.token_count,
			    embedding=CASE
			      WHEN excluded.embedding IS NULL
			        AND session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedding
			      ELSE excluded.embedding
			    END,
			    embedding_model=CASE
			      WHEN excluded.embedding IS NULL
			        AND session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedding_model
			      ELSE excluded.embedding_model
			    END,
			    embedding_status=CASE
			      WHEN excluded.embedding IS NULL
			        AND session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedding_status
			      WHEN excluded.embedding IS NULL
			        AND session_ai_chunks.embedding IS NOT NULL
			      THEN 'stale'
			      ELSE excluded.embedding_status
			    END,
			    embedding_error='',
			    embedded_at=CASE
			      WHEN excluded.embedding IS NULL
			        AND session_ai_chunks.content=excluded.content
			      THEN session_ai_chunks.embedded_at
			      ELSE excluded.embedded_at
			    END
			WHERE session_ai_chunks.tenant_id=$1
			  AND session_ai_chunks.user_id=$2
			RETURNING id, created_at, updated_at
		`, tenantID, userID, sessionID, chunk.Ordinal, chunk.Content,
				chunk.TokenCount, vector, embeddedModel, status).Scan(
				&chunk.ID, &chunk.CreatedAt, &chunk.UpdatedAt,
			)
			if err != nil {
				return err
			}
		}
		chunk.TenantID = tenantID
		chunk.UserID = userID
		chunk.SessionID = sessionID
		chunk.EmbeddingModel = embeddedModel
		chunk.EmbeddingStatus = status
	}
	if err := enforceTenantAIStorageQuota(ctx, tx, tenantID, quotaGB); err != nil {
		return err
	}
	if jobID != "" {
		var updatedJobID string
		if err := tx.QueryRowContext(ctx, `
			UPDATE ai_index_jobs
			SET processed_chunks=processed_chunks + $3,
			    actual_tokens=actual_tokens + $4
			WHERE id=$1 AND status='processing' AND lease_owner=$2
			  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
			  AND processed_chunks + $3 <= chunk_count
			RETURNING id
		`, jobID, workerID, len(chunks), actualTokenDelta).Scan(
			&updatedJobID,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func lockTenantStorageQuota(
	ctx context.Context, tx *sql.Tx, tenantID string,
) (int, error) {
	var quotaGB int
	err := tx.QueryRowContext(ctx, `
		SELECT storage_quota_gb FROM tenants WHERE id=$1 FOR UPDATE
	`, tenantID).Scan(&quotaGB)
	return quotaGB, err
}

func enforceTenantAIStorageQuota(
	ctx context.Context, tx *sql.Tx, tenantID string, quotaGB int,
) error {
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((
		    SELECT COALESCE(SUM(a.storage_bytes), 0) FROM billing_accounts a JOIN users u ON u.billing_account_id = a.id WHERE u.tenant_id = $1
		  ), 0)
		  + COALESCE((
		      SELECT SUM(size_bytes + extracted_text_bytes + vector_bytes)
		      FROM knowledge_sources WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(content_bytes) FROM ai_artifacts WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(
		        octet_length(content)::bigint
		        + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		      )
		      FROM session_ai_chunks WHERE tenant_id=$1
		    ), 0)
	`, tenantID).Scan(&usedBytes); err != nil {
		return err
	}
	if exceedsStorageQuota(quotaGB, usedBytes) {
		return ErrStorageQuota
	}
	return nil
}

func (s *PostgresStore) ListSessionAIChunksForIndex(
	ctx context.Context,
	jobID, workerID string,
	sessionID, tenantID, userID, model string,
	limit int,
) ([]models.SessionAIChunk, error) {
	if limit <= 0 || limit > maxEmbeddingBatchChunks {
		limit = maxEmbeddingBatchChunks
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	job, err := loadAIIndexJobForBatchTx(
		ctx,
		tx,
		jobID,
		workerID,
		"session",
		sessionID,
		tenantID,
		userID,
		model,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrLeaseLost
		}
		return nil, err
	}
	if err := verifyAIIndexJobContentDigestTx(ctx, tx, job); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.tenant_id, c.user_id, c.session_id, c.ordinal,
		       c.content, c.token_count, c.embedding_model,
		       c.embedding_status, c.embedding_error, c.embedded_at,
		       c.created_at, c.updated_at
		FROM ai_index_job_chunks snapshot
		JOIN session_ai_chunks c ON c.id=snapshot.chunk_id
		  AND snapshot.content_hash=encode(digest(c.content, 'sha256'), 'hex')
		JOIN sessions se ON se.id=c.session_id
		WHERE snapshot.job_id=$1
		  AND c.session_id=$2 AND c.tenant_id=$3 AND c.user_id=$4
		  AND se.tenant_id=$3 AND se.user_id=$4
		  AND (
		    c.embedding IS NULL
		    OR c.embedding_model<>$5
		    OR c.embedding_status<>'ready'
		  )
		ORDER BY snapshot.chunk_order
		LIMIT $6
		FOR SHARE OF c, se
	`, jobID, sessionID, tenantID, userID, model, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	chunks := make([]models.SessionAIChunk, 0, limit)
	for rows.Next() {
		var chunk models.SessionAIChunk
		if err := rows.Scan(
			&chunk.ID, &chunk.TenantID, &chunk.UserID, &chunk.SessionID,
			&chunk.Ordinal, &chunk.Content, &chunk.TokenCount,
			&chunk.EmbeddingModel, &chunk.EmbeddingStatus,
			&chunk.EmbeddingError, &chunk.EmbeddedAt,
			&chunk.CreatedAt, &chunk.UpdatedAt,
		); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return chunks, nil
}

func (s *PostgresStore) DeleteSessionAIChunksAfterOrdinal(
	ctx context.Context,
	sessionID, tenantID, userID string,
	lastOrdinal int,
) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM session_ai_chunks c
		USING sessions se
		WHERE c.session_id=se.id
		  AND c.session_id=$1 AND c.tenant_id=$2 AND c.user_id=$3
		  AND se.tenant_id=$2 AND se.user_id=$3
		  AND c.ordinal>$4
	`, sessionID, tenantID, userID, lastOrdinal)
	return err
}

// MarkMismatchedAIIndexesStale persists model invalidation before preview or
// retrieval. Existing vectors are retained for accounting/migration purposes,
// but ready-only semantic queries will no longer use them.
func (s *PostgresStore) MarkMismatchedAIIndexesStale(
	ctx context.Context,
	targetType, targetID, tenantID, userID, model string,
) (int64, error) {
	targetType = strings.ToLower(strings.TrimSpace(targetType))
	targetID = strings.TrimSpace(targetID)
	model = strings.TrimSpace(model)
	if targetID == "" || tenantID == "" || userID == "" || model == "" {
		return 0, fmt.Errorf("index target, owner, and model are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var affected int64
	switch targetType {
	case "project", "source":
		result, err := tx.ExecContext(ctx, `
			UPDATE knowledge_sources
			SET index_status='stale', index_error_message=''
			WHERE tenant_id=$1 AND user_id=$2
			  AND (
			    ($3='project' AND project_id=$4)
			    OR ($3='source' AND id=$4)
			  )
			  AND embedding_model<>$5
			  AND index_status='ready'
		`, tenantID, userID, targetType, targetID, model)
		if err != nil {
			return 0, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return 0, err
		}
		result, err = tx.ExecContext(ctx, `
			UPDATE knowledge_chunks c
			SET embedding_status='stale', embedding_error=''
			FROM knowledge_sources s
			WHERE c.source_id=s.id
			  AND s.tenant_id=$1 AND s.user_id=$2
			  AND (
			    ($3='project' AND s.project_id=$4)
			    OR ($3='source' AND s.id=$4)
			  )
			  AND c.embedding IS NOT NULL
			  AND c.embedding_model<>$5
			  AND c.embedding_status='ready'
		`, tenantID, userID, targetType, targetID, model)
		if err != nil {
			return 0, err
		}
		chunkCount, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		affected += chunkCount
	case "session":
		result, err := tx.ExecContext(ctx, `
			UPDATE session_ai_chunks c
			SET embedding_status='stale', embedding_error=''
			FROM sessions se
			WHERE c.session_id=se.id
			  AND c.session_id=$1 AND c.tenant_id=$2 AND c.user_id=$3
			  AND se.tenant_id=$2 AND se.user_id=$3
			  AND c.embedding IS NOT NULL
			  AND c.embedding_model<>$4
			  AND c.embedding_status='ready'
		`, targetID, tenantID, userID, model)
		if err != nil {
			return 0, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("invalid index target type")
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *PostgresStore) PreviewAIIndex(
	ctx context.Context,
	targetType, targetID, tenantID, userID, model string,
) (*models.AIIndexPreview, error) {
	targetType = strings.ToLower(strings.TrimSpace(targetType))
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("embedding model is required")
	}
	if _, err := s.MarkMismatchedAIIndexesStale(
		ctx, targetType, targetID, tenantID, userID, model,
	); err != nil {
		return nil, err
	}
	preview := &models.AIIndexPreview{
		TargetType:  targetType,
		TargetID:    targetID,
		Model:       model,
		Dimensions:  semanticEmbeddingDimensions,
		IndexStatus: models.AIIndexStatusUnindexed,
	}
	var errorCount, processingCount, queuedCount, staleCount, mismatchedCount int
	switch targetType {
	case "project", "source":
		if targetType == "project" {
			var allowed bool
			if err := s.db.QueryRowContext(ctx, `
				SELECT EXISTS(
				  SELECT 1 FROM ai_projects
				  WHERE id=$1 AND tenant_id=$2 AND user_id=$3
				)
			`, targetID, tenantID, userID).Scan(&allowed); err != nil {
				return nil, err
			}
			if !allowed {
				return nil, sql.ErrNoRows
			}
		}
		err := s.db.QueryRowContext(ctx, `
			SELECT
			  COUNT(DISTINCT s.id),
			  COUNT(c.id),
			  COUNT(c.id) FILTER (
			    WHERE c.embedding IS NOT NULL
			      AND c.embedding_model=$5
			      AND c.embedding_status='ready'
			  ),
			  COUNT(c.id) FILTER (
			    WHERE c.embedding IS NULL
			       OR c.embedding_model<>$5
			       OR c.embedding_status<>'ready'
			  ),
			  COALESCE(SUM(
			    CASE
			      WHEN c.id IS NULL THEN 0
			      ELSE GREATEST(
			        c.token_count::bigint,
			        octet_length(c.content)::bigint
			      )
			    END
			  ) FILTER (
			    WHERE c.id IS NOT NULL
			      AND (
			        c.embedding IS NULL
			        OR c.embedding_model<>$5
			        OR c.embedding_status<>'ready'
			      )
			  ), 0),
			  COUNT(DISTINCT s.id) FILTER (WHERE s.index_status='error'),
			  COUNT(DISTINCT s.id) FILTER (WHERE s.index_status='processing'),
			  COUNT(DISTINCT s.id) FILTER (WHERE s.index_status='queued'),
			  COUNT(DISTINCT s.id) FILTER (WHERE s.index_status='stale'),
			  COUNT(c.id) FILTER (
			    WHERE c.embedding IS NOT NULL AND c.embedding_model<>$5
			  ),
			  COALESCE(MAX(NULLIF(s.embedding_model, '')), ''),
			  encode(digest(COALESCE(string_agg(
			    c.id::text || ':' || encode(digest(c.content, 'sha256'), 'hex'),
			    '|' ORDER BY c.source_id, c.ordinal, c.id
			  ), ''), 'sha256'), 'hex')
			FROM knowledge_sources s
			LEFT JOIN knowledge_chunks c ON c.source_id=s.id
			WHERE s.tenant_id=$1 AND s.user_id=$2
			  AND s.status='ready'
			  AND (
			    ($3='project' AND s.project_id=$4)
			    OR ($3='source' AND s.id=$4)
			  )
		`, tenantID, userID, targetType, targetID, model).Scan(
			&preview.SourceCount, &preview.ChunkCount, &preview.IndexedChunks,
			&preview.PendingChunks, &preview.EstimatedTokens,
			&errorCount, &processingCount,
			&queuedCount, &staleCount, &mismatchedCount, &preview.CurrentModel,
			&preview.ContentDigest,
		)
		if err != nil {
			return nil, err
		}
		if targetType == "source" && preview.SourceCount == 0 {
			return nil, sql.ErrNoRows
		}
	case "session":
		var allowed bool
		if err := s.db.QueryRowContext(ctx, `
			SELECT EXISTS(
			  SELECT 1 FROM sessions
			  WHERE id=$1 AND tenant_id=$2 AND user_id=$3
			)
		`, targetID, tenantID, userID).Scan(&allowed); err != nil {
			return nil, err
		}
		if !allowed {
			return nil, sql.ErrNoRows
		}
		preview.SourceCount = 1
		if err := s.db.QueryRowContext(ctx, `
			SELECT
			  COUNT(*),
			  COUNT(*) FILTER (
			    WHERE embedding IS NOT NULL
			      AND embedding_model=$4 AND embedding_status='ready'
			  ),
			  COUNT(*) FILTER (
			    WHERE embedding IS NULL
			       OR embedding_model<>$4
			       OR embedding_status<>'ready'
			  ),
			  COALESCE(SUM(
			    GREATEST(
			      token_count::bigint,
			      octet_length(content)::bigint
			    )
			  ) FILTER (
			    WHERE embedding IS NULL
			       OR embedding_model<>$4
			       OR embedding_status<>'ready'
			  ), 0),
			  COUNT(*) FILTER (WHERE embedding_status='error'),
			  COUNT(*) FILTER (WHERE embedding_status='processing'),
			  COUNT(*) FILTER (WHERE embedding_status='queued'),
			  COUNT(*) FILTER (WHERE embedding_status='stale'),
			  COUNT(*) FILTER (
			    WHERE embedding IS NOT NULL AND embedding_model<>$4
			  ),
			  COALESCE(MAX(NULLIF(embedding_model, '')), ''),
			  encode(digest(COALESCE(string_agg(
			    id::text || ':' || encode(digest(content, 'sha256'), 'hex'),
			    '|' ORDER BY ordinal, id
			  ), ''), 'sha256'), 'hex')
			FROM session_ai_chunks
			WHERE session_id=$1 AND tenant_id=$2 AND user_id=$3
		`, targetID, tenantID, userID, model).Scan(
			&preview.ChunkCount, &preview.IndexedChunks,
			&preview.PendingChunks, &preview.EstimatedTokens,
			&errorCount, &processingCount,
			&queuedCount, &staleCount, &mismatchedCount,
			&preview.CurrentModel,
			&preview.ContentDigest,
		); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("invalid index target type")
	}

	switch {
	case preview.ChunkCount > 0 && preview.IndexedChunks == preview.ChunkCount:
		preview.IndexStatus = models.AIIndexStatusReady
	case errorCount > 0:
		preview.IndexStatus = models.AIIndexStatusError
	case processingCount > 0:
		preview.IndexStatus = models.AIIndexStatusProcessing
	case queuedCount > 0:
		preview.IndexStatus = models.AIIndexStatusQueued
	case staleCount > 0 || mismatchedCount > 0:
		preview.IndexStatus = models.AIIndexStatusStale
	default:
		preview.IndexStatus = models.AIIndexStatusUnindexed
	}
	preview.RequiresIndexing = preview.PendingChunks > 0
	return preview, nil
}

type knowledgeCandidate struct {
	chunk models.KnowledgeChunk
}

type sessionCandidate struct {
	chunk models.SessionAIChunk
}

func (s *PostgresStore) HybridProjectKnowledgeChunks(
	ctx context.Context,
	projectID, tenantID, userID, query, model string,
	queryEmbedding []float64,
	topK int,
) (*models.KnowledgeSearchResult, error) {
	topK, candidateLimit := normalizeSearchLimits(topK)
	query = strings.TrimSpace(query)
	lexical, err := s.lexicalKnowledgeCandidates(
		ctx, projectID, tenantID, userID, query, candidateLimit,
	)
	if err != nil {
		return nil, err
	}
	semantic := make([]knowledgeCandidate, 0)
	if len(queryEmbedding) > 0 {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("embedding model is required for semantic retrieval")
		}
		vector, err := formatPGVector(queryEmbedding)
		if err != nil {
			return nil, err
		}
		semantic, err = s.semanticKnowledgeCandidates(
			ctx, projectID, tenantID, userID, model, vector, candidateLimit,
		)
		if err != nil {
			return nil, err
		}
	}
	chunks := fuseKnowledgeCandidates(semantic, lexical, topK)
	return &models.KnowledgeSearchResult{
		Chunks:        chunks,
		RetrievalMode: retrievalMode(len(semantic), len(lexical)),
	}, nil
}

func (s *PostgresStore) lexicalKnowledgeCandidates(
	ctx context.Context,
	projectID, tenantID, userID, query string,
	limit int,
) ([]knowledgeCandidate, error) {
	if query == "" {
		return []knowledgeCandidate{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.source_id, c.project_id, c.ordinal, c.content, s.name
		FROM knowledge_chunks c
		JOIN knowledge_sources s ON s.id=c.source_id
		WHERE c.project_id=$1 AND s.tenant_id=$2 AND s.user_id=$3
		  AND s.status='ready'
		  AND (
		    c.content % $4
		    OR $4 <% c.content
		    OR strpos(lower(c.content), lower($4)) > 0
		  )
		ORDER BY
		  CASE WHEN strpos(lower(c.content), lower($4)) > 0 THEN 1 ELSE 0 END DESC,
		  GREATEST(
		    similarity(c.content, $4),
		    word_similarity($4, c.content)
		  ) DESC,
		  c.id
		LIMIT $5
	`, projectID, tenantID, userID, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := make([]knowledgeCandidate, 0, limit)
	for rows.Next() {
		var candidate knowledgeCandidate
		if err := rows.Scan(
			&candidate.chunk.ID, &candidate.chunk.SourceID,
			&candidate.chunk.ProjectID, &candidate.chunk.Ordinal,
			&candidate.chunk.Content, &candidate.chunk.SourceName,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (s *PostgresStore) semanticKnowledgeCandidates(
	ctx context.Context,
	projectID, tenantID, userID, model, queryVector string,
	limit int,
) ([]knowledgeCandidate, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		SET LOCAL hnsw.iterative_scan = 'strict_order'
	`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.source_id, c.project_id, c.ordinal, c.content, s.name
		FROM knowledge_chunks c
		JOIN knowledge_sources s ON s.id=c.source_id
		WHERE c.project_id=$1 AND s.tenant_id=$2 AND s.user_id=$3
		  AND s.status='ready'
		  AND c.embedding IS NOT NULL
		  AND c.embedding_model=$4 AND c.embedding_status='ready'
		ORDER BY c.embedding <=> $5::vector(1536)
		LIMIT $6
	`, projectID, tenantID, userID, model, queryVector, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]knowledgeCandidate, 0, limit)
	for rows.Next() {
		var candidate knowledgeCandidate
		if err := rows.Scan(
			&candidate.chunk.ID, &candidate.chunk.SourceID,
			&candidate.chunk.ProjectID, &candidate.chunk.Ordinal,
			&candidate.chunk.Content, &candidate.chunk.SourceName,
		); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func (s *PostgresStore) HybridSessionAIChunks(
	ctx context.Context,
	sessionID, tenantID, userID, query, model string,
	queryEmbedding []float64,
	topK int,
) (*models.SessionSearchResult, error) {
	topK, candidateLimit := normalizeSearchLimits(topK)
	query = strings.TrimSpace(query)
	lexical, err := s.lexicalSessionCandidates(
		ctx, sessionID, tenantID, userID, query, candidateLimit,
	)
	if err != nil {
		return nil, err
	}
	semantic := make([]sessionCandidate, 0)
	if len(queryEmbedding) > 0 {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("embedding model is required for semantic retrieval")
		}
		vector, err := formatPGVector(queryEmbedding)
		if err != nil {
			return nil, err
		}
		semantic, err = s.semanticSessionCandidates(
			ctx, sessionID, tenantID, userID, model, vector, candidateLimit,
		)
		if err != nil {
			return nil, err
		}
	}
	chunks := fuseSessionCandidates(semantic, lexical, topK)
	return &models.SessionSearchResult{
		Chunks:        chunks,
		RetrievalMode: retrievalMode(len(semantic), len(lexical)),
	}, nil
}

func (s *PostgresStore) lexicalSessionCandidates(
	ctx context.Context,
	sessionID, tenantID, userID, query string,
	limit int,
) ([]sessionCandidate, error) {
	if query == "" {
		return []sessionCandidate{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.tenant_id, c.user_id, c.session_id, c.ordinal,
		       c.content, c.token_count, c.embedding_model,
		       c.embedding_status, c.embedding_error, c.embedded_at,
		       c.created_at, c.updated_at
		FROM session_ai_chunks c
		JOIN sessions se ON se.id=c.session_id
		WHERE c.session_id=$1 AND c.tenant_id=$2 AND c.user_id=$3
		  AND se.tenant_id=$2 AND se.user_id=$3
		  AND (
		    c.content % $4
		    OR $4 <% c.content
		    OR strpos(lower(c.content), lower($4)) > 0
		  )
		ORDER BY
		  CASE WHEN strpos(lower(c.content), lower($4)) > 0 THEN 1 ELSE 0 END DESC,
		  GREATEST(
		    similarity(c.content, $4),
		    word_similarity($4, c.content)
		  ) DESC,
		  c.id
		LIMIT $5
	`, sessionID, tenantID, userID, query, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	candidates := make([]sessionCandidate, 0, limit)
	for rows.Next() {
		var candidate sessionCandidate
		if err := scanSessionCandidate(rows, &candidate); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	return candidates, rows.Err()
}

func (s *PostgresStore) semanticSessionCandidates(
	ctx context.Context,
	sessionID, tenantID, userID, model, queryVector string,
	limit int,
) ([]sessionCandidate, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		SET LOCAL hnsw.iterative_scan = 'strict_order'
	`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.tenant_id, c.user_id, c.session_id, c.ordinal,
		       c.content, c.token_count, c.embedding_model,
		       c.embedding_status, c.embedding_error, c.embedded_at,
		       c.created_at, c.updated_at
		FROM session_ai_chunks c
		JOIN sessions se ON se.id=c.session_id
		WHERE c.session_id=$1 AND c.tenant_id=$2 AND c.user_id=$3
		  AND se.tenant_id=$2 AND se.user_id=$3
		  AND c.embedding IS NOT NULL
		  AND c.embedding_model=$4 AND c.embedding_status='ready'
		ORDER BY c.embedding <=> $5::vector(1536)
		LIMIT $6
	`, sessionID, tenantID, userID, model, queryVector, limit)
	if err != nil {
		return nil, err
	}
	candidates := make([]sessionCandidate, 0, limit)
	for rows.Next() {
		var candidate sessionCandidate
		if err := scanSessionCandidate(rows, &candidate); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return candidates, nil
}

func scanSessionCandidate(scanner rowScanner, candidate *sessionCandidate) error {
	return scanner.Scan(
		&candidate.chunk.ID, &candidate.chunk.TenantID, &candidate.chunk.UserID,
		&candidate.chunk.SessionID, &candidate.chunk.Ordinal,
		&candidate.chunk.Content, &candidate.chunk.TokenCount,
		&candidate.chunk.EmbeddingModel, &candidate.chunk.EmbeddingStatus,
		&candidate.chunk.EmbeddingError, &candidate.chunk.EmbeddedAt,
		&candidate.chunk.CreatedAt, &candidate.chunk.UpdatedAt,
	)
}

func normalizeSearchLimits(topK int) (int, int) {
	if topK <= 0 {
		topK = 6
	}
	if topK > 50 {
		topK = 50
	}
	candidates := topK * 4
	if candidates < 20 {
		candidates = 20
	}
	if candidates > 200 {
		candidates = 200
	}
	return topK, candidates
}

func retrievalMode(semanticCount, lexicalCount int) string {
	switch {
	case semanticCount > 0 && lexicalCount > 0:
		return models.AIRetrievalModeHybrid
	case semanticCount > 0:
		return models.AIRetrievalModeSemantic
	case lexicalCount > 0:
		return models.AIRetrievalModeLexicalFallback
	default:
		return models.AIRetrievalModeNone
	}
}

type fusedRank struct {
	id       string
	score    float64
	bestRank int
}

func rrfRanks(semanticIDs, lexicalIDs []string, topK int) []fusedRank {
	ranks := make(map[string]*fusedRank, len(semanticIDs)+len(lexicalIDs))
	add := func(ids []string) {
		for index, id := range ids {
			rank := index + 1
			item, ok := ranks[id]
			if !ok {
				item = &fusedRank{id: id, bestRank: rank}
				ranks[id] = item
			}
			item.score += 1 / (defaultRRFConstant + float64(rank))
			if rank < item.bestRank {
				item.bestRank = rank
			}
		}
	}
	add(semanticIDs)
	add(lexicalIDs)
	values := make([]fusedRank, 0, len(ranks))
	for _, item := range ranks {
		values = append(values, *item)
	}
	sort.Slice(values, func(left, right int) bool {
		if values[left].score != values[right].score {
			return values[left].score > values[right].score
		}
		if values[left].bestRank != values[right].bestRank {
			return values[left].bestRank < values[right].bestRank
		}
		return values[left].id < values[right].id
	})
	if len(values) > topK {
		values = values[:topK]
	}
	return values
}

func fuseKnowledgeCandidates(
	semantic, lexical []knowledgeCandidate,
	topK int,
) []models.KnowledgeChunk {
	byID := make(map[string]models.KnowledgeChunk, len(semantic)+len(lexical))
	semanticIDs := make([]string, 0, len(semantic))
	lexicalIDs := make([]string, 0, len(lexical))
	for index := range semantic {
		candidate := &semantic[index]
		byID[candidate.chunk.ID] = candidate.chunk
		semanticIDs = append(semanticIDs, candidate.chunk.ID)
	}
	for index := range lexical {
		candidate := &lexical[index]
		if _, exists := byID[candidate.chunk.ID]; !exists {
			byID[candidate.chunk.ID] = candidate.chunk
		}
		lexicalIDs = append(lexicalIDs, candidate.chunk.ID)
	}
	ranks := rrfRanks(semanticIDs, lexicalIDs, topK)
	chunks := make([]models.KnowledgeChunk, 0, len(ranks))
	for _, rank := range ranks {
		chunk := byID[rank.id]
		chunk.RetrievalScore = rank.score
		chunks = append(chunks, chunk)
	}
	return chunks
}

func fuseSessionCandidates(
	semantic, lexical []sessionCandidate,
	topK int,
) []models.SessionAIChunk {
	byID := make(map[string]models.SessionAIChunk, len(semantic)+len(lexical))
	semanticIDs := make([]string, 0, len(semantic))
	lexicalIDs := make([]string, 0, len(lexical))
	for index := range semantic {
		candidate := &semantic[index]
		byID[candidate.chunk.ID] = candidate.chunk
		semanticIDs = append(semanticIDs, candidate.chunk.ID)
	}
	for index := range lexical {
		candidate := &lexical[index]
		if _, exists := byID[candidate.chunk.ID]; !exists {
			byID[candidate.chunk.ID] = candidate.chunk
		}
		lexicalIDs = append(lexicalIDs, candidate.chunk.ID)
	}
	ranks := rrfRanks(semanticIDs, lexicalIDs, topK)
	chunks := make([]models.SessionAIChunk, 0, len(ranks))
	for _, rank := range ranks {
		chunk := byID[rank.id]
		chunk.RetrievalScore = rank.score
		chunks = append(chunks, chunk)
	}
	return chunks
}

func (s *PostgresStore) MarkMismatchedKnowledgeIndexesStale(
	ctx context.Context,
	projectID, tenantID, userID, model string,
) (int64, error) {
	return s.MarkMismatchedAIIndexesStale(
		ctx, "project", projectID, tenantID, userID, model,
	)
}
