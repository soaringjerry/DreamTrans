package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

const semanticEmbeddingDimensions = 1536

const aiIndexEstimatedDPScale = 100_000_000

func scanAIIndexJob(scanner rowScanner, job *models.AIIndexJob) error {
	return scanner.Scan(
		&job.ID, &job.TenantID, &job.UserID, &job.TargetType, &job.TargetID,
		&job.Model, &job.Dimensions, &job.Status, &job.ChunkCount,
		&job.ProcessedChunks, &job.EstimatedTokens, &job.ActualTokens,
		&job.EstimatedDP, &job.ContentDigest,
		&job.ClientRequestID, &job.ErrorMessage,
		&job.LeaseOwner, &job.LeaseExpiresAt, &job.AttemptCount,
		&job.MaxAttempts, &job.CancelRequestedAt, &job.StartedAt,
		&job.FinishedAt, &job.CreatedAt, &job.UpdatedAt,
	)
}

func validateAIIndexJob(job *models.AIIndexJob) error {
	job.TargetType = strings.ToLower(strings.TrimSpace(job.TargetType))
	job.TargetID = strings.TrimSpace(job.TargetID)
	job.Model = strings.TrimSpace(job.Model)
	job.ContentDigest = strings.ToLower(strings.TrimSpace(job.ContentDigest))
	job.ClientRequestID = strings.TrimSpace(job.ClientRequestID)
	switch job.TargetType {
	case "project", "source", "session":
	default:
		return fmt.Errorf("invalid index target type")
	}
	if job.TargetID == "" {
		return fmt.Errorf("index target id is required")
	}
	if job.Model == "" || len(job.Model) > 200 {
		return fmt.Errorf("embedding model is required and must be at most 200 characters")
	}
	digestBytes, digestErr := hex.DecodeString(job.ContentDigest)
	if digestErr != nil || len(digestBytes) != sha256DigestBytes ||
		hex.EncodeToString(digestBytes) != job.ContentDigest {
		return fmt.Errorf("content digest must be a lowercase SHA-256 digest")
	}
	if job.Dimensions == 0 {
		job.Dimensions = semanticEmbeddingDimensions
	}
	if job.Dimensions != semanticEmbeddingDimensions {
		return fmt.Errorf("embedding dimensions must be 1536")
	}
	if len(job.ClientRequestID) > 128 {
		return fmt.Errorf("client_request_id must be at most 128 characters")
	}
	if math.IsNaN(job.EstimatedDP) || math.IsInf(job.EstimatedDP, 0) {
		return fmt.Errorf("invalid index job estimates")
	}
	canonicalDP := math.Round(job.EstimatedDP*aiIndexEstimatedDPScale) /
		aiIndexEstimatedDPScale
	if math.IsInf(canonicalDP, 0) {
		return fmt.Errorf("invalid index job estimates")
	}
	job.EstimatedDP = canonicalDP
	if job.ChunkCount < 0 || job.ProcessedChunks < 0 ||
		job.ProcessedChunks > job.ChunkCount || job.EstimatedTokens < 0 ||
		job.EstimatedDP < 0 {
		return fmt.Errorf("invalid index job estimates")
	}
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 3
	}
	if job.MaxAttempts < 1 || job.MaxAttempts > 20 {
		return fmt.Errorf("max attempts must be between 1 and 20")
	}
	return nil
}

func sameAIIndexConfirmedRequest(
	existing, requested *models.AIIndexJob,
) bool {
	return existing.TargetType == requested.TargetType &&
		existing.TargetID == requested.TargetID &&
		existing.Model == requested.Model &&
		existing.Dimensions == requested.Dimensions &&
		existing.ChunkCount == requested.ChunkCount &&
		existing.EstimatedTokens == requested.EstimatedTokens &&
		existing.EstimatedDP == requested.EstimatedDP &&
		existing.ContentDigest == requested.ContentDigest
}

const sha256DigestBytes = 32

type aiIndexTargetSnapshot struct {
	TotalChunks   int
	PendingChunks int
	PendingTokens int64
	ContentDigest string
}

func aiIndexTargetSnapshotTx(
	ctx context.Context,
	tx *sql.Tx,
	job *models.AIIndexJob,
) (aiIndexTargetSnapshot, error) {
	var snapshot aiIndexTargetSnapshot
	switch job.TargetType {
	case "project", "source":
		err := tx.QueryRowContext(ctx, `
			SELECT
			  COUNT(c.id),
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
			  encode(digest(COALESCE(string_agg(
			    c.id::text || ':' || encode(digest(c.content, 'sha256'), 'hex'),
			    '|' ORDER BY c.source_id, c.ordinal, c.id
			  ), ''), 'sha256'), 'hex')
			FROM knowledge_sources s
			LEFT JOIN knowledge_chunks c ON c.source_id=s.id
			WHERE s.tenant_id=$1 AND s.user_id=$2 AND s.status='ready'
			  AND (
			    ($3='project' AND s.project_id=$4)
			    OR ($3='source' AND s.id=$4)
			  )
		`, job.TenantID, job.UserID, job.TargetType, job.TargetID, job.Model).Scan(
			&snapshot.TotalChunks,
			&snapshot.PendingChunks,
			&snapshot.PendingTokens,
			&snapshot.ContentDigest,
		)
		return snapshot, err
	case "session":
		err := tx.QueryRowContext(ctx, `
			SELECT
			  COUNT(*),
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
			  encode(digest(COALESCE(string_agg(
			    id::text || ':' || encode(digest(content, 'sha256'), 'hex'),
			    '|' ORDER BY ordinal, id
			  ), ''), 'sha256'), 'hex')
			FROM session_ai_chunks
			WHERE session_id=$1 AND tenant_id=$2 AND user_id=$3
		`, job.TargetID, job.TenantID, job.UserID, job.Model).Scan(
			&snapshot.TotalChunks,
			&snapshot.PendingChunks,
			&snapshot.PendingTokens,
			&snapshot.ContentDigest,
		)
		return snapshot, err
	default:
		return aiIndexTargetSnapshot{}, fmt.Errorf("invalid index target type")
	}
}

func indexJobTargetIDs(job *models.AIIndexJob) (projectID, sourceID, sessionID any) {
	switch job.TargetType {
	case "project":
		projectID = job.TargetID
	case "source":
		sourceID = job.TargetID
	case "session":
		sessionID = job.TargetID
	}
	return projectID, sourceID, sessionID
}

func insertAIIndexJobChunkSnapshotTx(
	ctx context.Context,
	tx *sql.Tx,
	job *models.AIIndexJob,
) error {
	var (
		result sql.Result
		err    error
	)
	switch job.TargetType {
	case "project", "source":
		result, err = tx.ExecContext(ctx, `
			INSERT INTO ai_index_job_chunks (
			  job_id, chunk_id, chunk_order, content_hash
			)
			SELECT
			  $1,
			  c.id,
			  (ROW_NUMBER() OVER (
			    ORDER BY c.source_id, c.ordinal, c.id
			  ) - 1)::int,
			  encode(digest(c.content, 'sha256'), 'hex')
			FROM knowledge_sources s
			JOIN knowledge_chunks c ON c.source_id=s.id
			WHERE s.tenant_id=$2 AND s.user_id=$3 AND s.status='ready'
			  AND (
			    ($4='project' AND s.project_id=$5)
			    OR ($4='source' AND s.id=$5)
			  )
			  AND (
			    c.embedding IS NULL
			    OR c.embedding_model<>$6
			    OR c.embedding_status<>'ready'
			  )
			ORDER BY c.source_id, c.ordinal, c.id
		`, job.ID, job.TenantID, job.UserID, job.TargetType, job.TargetID, job.Model)
	case "session":
		result, err = tx.ExecContext(ctx, `
			INSERT INTO ai_index_job_chunks (
			  job_id, chunk_id, chunk_order, content_hash
			)
			SELECT
			  $1,
			  c.id,
			  (ROW_NUMBER() OVER (ORDER BY c.ordinal, c.id) - 1)::int,
			  encode(digest(c.content, 'sha256'), 'hex')
			FROM session_ai_chunks c
			JOIN sessions s ON s.id=c.session_id
			WHERE c.session_id=$2 AND c.tenant_id=$3 AND c.user_id=$4
			  AND s.tenant_id=$3 AND s.user_id=$4
			  AND (
			    c.embedding IS NULL
			    OR c.embedding_model<>$5
			    OR c.embedding_status<>'ready'
			  )
			ORDER BY c.ordinal, c.id
		`, job.ID, job.TargetID, job.TenantID, job.UserID, job.Model)
	default:
		return fmt.Errorf("invalid index target type")
	}
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != int64(job.ChunkCount) {
		return ErrIndexContentChanged
	}
	return nil
}

func loadAIIndexJobForBatchTx(
	ctx context.Context,
	tx *sql.Tx,
	jobID, workerID, targetType, targetID, tenantID, userID, model string,
) (*models.AIIndexJob, error) {
	var job models.AIIndexJob
	err := scanAIIndexJob(tx.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, target_type,
		       COALESCE(project_id, source_id, session_id)::text,
		       model, dimensions, status, chunk_count, processed_chunks,
		       estimated_tokens, actual_tokens, estimated_dp, content_digest,
		       client_request_id, error_message, lease_owner,
		       lease_expires_at, attempt_count, max_attempts,
		       cancel_requested_at, started_at, finished_at,
		       created_at, updated_at
		FROM ai_index_jobs
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND target_type=$4
		  AND COALESCE(project_id, source_id, session_id)=$5
		  AND model=$6 AND status='processing' AND lease_owner=$7
		  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
		FOR UPDATE
	`, jobID, tenantID, userID, targetType, targetID, model, workerID), &job)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func verifyAIIndexJobContentDigestTx(
	ctx context.Context,
	tx *sql.Tx,
	job *models.AIIndexJob,
) error {
	current, err := aiIndexTargetSnapshotTx(ctx, tx, job)
	if err != nil {
		return err
	}
	if current.ContentDigest != job.ContentDigest {
		return ErrIndexContentChanged
	}
	return nil
}

func resolveAIIndexScopeTx(
	ctx context.Context, tx *sql.Tx, job *models.AIIndexJob,
) (string, string, error) {
	var scopeID string
	switch job.TargetType {
	case "project":
		err := tx.QueryRowContext(ctx, `
			SELECT id::text FROM ai_projects
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		`, job.TargetID, job.TenantID, job.UserID).Scan(&scopeID)
		return "project", scopeID, err
	case "source":
		err := tx.QueryRowContext(ctx, `
			SELECT project_id::text FROM knowledge_sources
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		`, job.TargetID, job.TenantID, job.UserID).Scan(&scopeID)
		return "project", scopeID, err
	case "session":
		err := tx.QueryRowContext(ctx, `
			SELECT id::text FROM sessions
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		`, job.TargetID, job.TenantID, job.UserID).Scan(&scopeID)
		return "session", scopeID, err
	default:
		return "", "", fmt.Errorf("invalid index target type")
	}
}

func getActiveAIIndexJobForScopeTx(
	ctx context.Context,
	db queryRower,
	tenantID, userID, scopeType, scopeID string,
) (*models.AIIndexJob, error) {
	var job models.AIIndexJob
	err := scanAIIndexJob(db.QueryRowContext(ctx, `
		SELECT j.id, j.tenant_id, j.user_id, j.target_type,
		       COALESCE(j.project_id, j.source_id, j.session_id)::text,
		       j.model, j.dimensions, j.status, j.chunk_count,
		       j.processed_chunks, j.estimated_tokens, j.actual_tokens,
		       j.estimated_dp, j.content_digest,
		       j.client_request_id, j.error_message,
		       j.lease_owner, j.lease_expires_at, j.attempt_count,
		       j.max_attempts, j.cancel_requested_at, j.started_at,
		       j.finished_at, j.created_at, j.updated_at
		FROM ai_index_jobs j
		WHERE j.tenant_id=$1 AND j.user_id=$2
		  AND j.status IN ('queued', 'processing')
		  AND (
		    (
		      $3='session' AND j.target_type='session' AND j.session_id=$4
		    )
		    OR
		    (
		      $3='project' AND (
		        (j.target_type='project' AND j.project_id=$4)
		        OR (
		          j.target_type='source' AND EXISTS (
		            SELECT 1 FROM knowledge_sources source
		            WHERE source.id=j.source_id AND source.project_id=$4
		              AND source.tenant_id=$1 AND source.user_id=$2
		          )
		        )
		      )
		    )
		  )
		ORDER BY j.created_at ASC
		LIMIT 1
	`, tenantID, userID, scopeType, scopeID), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// ensureAIIndexStorageCapacityTx rejects a paid indexing job before its first
// provider request when the new vectors cannot fit. Existing non-nil vectors
// are overwritten in place on a model refresh, so only currently-null vectors
// add projected storage.
func ensureAIIndexStorageCapacityTx(
	ctx context.Context,
	tx *sql.Tx,
	job *models.AIIndexJob,
) error {
	var quotaGB int
	if err := tx.QueryRowContext(ctx, `
		SELECT storage_quota_gb
		FROM tenants
		WHERE id=$1
		FOR UPDATE
	`, job.TenantID).Scan(&quotaGB); err != nil {
		return err
	}
	if quotaGB < 0 {
		return nil
	}

	var missingVectors int64
	switch job.TargetType {
	case "project", "source":
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM knowledge_chunks c
			JOIN knowledge_sources s ON s.id=c.source_id
			WHERE s.tenant_id=$1 AND s.user_id=$2 AND s.status='ready'
			  AND c.embedding IS NULL
			  AND (
			    ($3='project' AND s.project_id=$4)
			    OR ($3='source' AND s.id=$4)
			  )
		`, job.TenantID, job.UserID, job.TargetType, job.TargetID).Scan(
			&missingVectors,
		); err != nil {
			return err
		}
	case "session":
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM session_ai_chunks c
			JOIN sessions se ON se.id=c.session_id
			WHERE c.session_id=$1 AND c.tenant_id=$2 AND c.user_id=$3
			  AND se.tenant_id=$2 AND se.user_id=$3
			  AND c.embedding IS NULL
		`, job.TargetID, job.TenantID, job.UserID).Scan(&missingVectors); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid index target type")
	}
	if missingVectors == 0 {
		return nil
	}

	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((
		    SELECT transcript_bytes
		    FROM tenant_storage_usage
		    WHERE tenant_id=$1
		  ), 0)
		  + COALESCE((
		      SELECT SUM(size_bytes + extracted_text_bytes + vector_bytes)
		      FROM knowledge_sources
		      WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(content_bytes)
		      FROM ai_artifacts
		      WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(
		        octet_length(content)::bigint
		        + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		      )
		      FROM session_ai_chunks
		      WHERE tenant_id=$1
		    ), 0)
	`, job.TenantID).Scan(&usedBytes); err != nil {
		return err
	}
	const vectorBytes = int64(semanticEmbeddingDimensions * 4)
	if missingVectors > (math.MaxInt64-usedBytes)/vectorBytes {
		return ErrStorageQuota
	}
	if exceedsStorageQuota(quotaGB, usedBytes+missingVectors*vectorBytes) {
		return ErrStorageQuota
	}
	return nil
}

// CreateAIIndexJob inserts an idempotent, durable semantic indexing job.
// Existing knowledge is only marked queued; this method never calls a provider.
//
//nolint:gocyclo // The transaction deliberately keeps validation, locking, and idempotency atomic.
func (s *PostgresStore) CreateAIIndexJob(
	ctx context.Context, job *models.AIIndexJob,
) (bool, error) {
	if err := validateAIIndexJob(job); err != nil {
		return false, err
	}
	projectID, sourceID, sessionID := indexJobTargetIDs(job)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	scopeType, scopeID, err := resolveAIIndexScopeTx(ctx, tx, job)
	if err != nil {
		return false, err
	}
	if err := lockAIIndexScopeTx(
		ctx, tx, job.UserID, scopeType, scopeID,
	); err != nil {
		return false, err
	}
	if err := lockAIStorageOwnerFKGateTx(
		ctx,
		tx,
		job.TenantID,
		job.UserID,
	); err != nil {
		return false, err
	}
	if job.ClientRequestID != "" {
		existing, loadErr := getAIIndexJobByClientRequestIDTx(
			ctx, tx, job.TenantID, job.UserID, job.ClientRequestID,
		)
		if loadErr != nil {
			return false, loadErr
		}
		if existing != nil {
			if !sameAIIndexConfirmedRequest(existing, job) {
				return false, ErrIdempotencyConflict
			}
			*job = *existing
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return false, nil
		}
	}
	active, err := getActiveAIIndexJobForScopeTx(
		ctx, tx, job.TenantID, job.UserID, scopeType, scopeID,
	)
	if err != nil {
		return false, err
	}
	if active != nil {
		if job.ClientRequestID == "" && active.TargetType == job.TargetType &&
			active.TargetID == job.TargetID && active.Model == job.Model {
			*job = *active
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return false, nil
		}
		return false, ErrIndexTargetBusy
	}
	currentSnapshot, err := aiIndexTargetSnapshotTx(ctx, tx, job)
	if err != nil {
		return false, err
	}
	if currentSnapshot.PendingChunks != job.ChunkCount ||
		currentSnapshot.PendingTokens != job.EstimatedTokens ||
		currentSnapshot.ContentDigest != job.ContentDigest {
		return false, ErrIndexContentChanged
	}
	if err := ensureAIIndexStorageCapacityTx(ctx, tx, job); err != nil {
		return false, err
	}

	err = scanAIIndexJob(tx.QueryRowContext(ctx, `
		INSERT INTO ai_index_jobs (
			tenant_id, user_id, target_type, project_id, source_id, session_id,
			model, dimensions, status, chunk_count, processed_chunks,
			estimated_tokens, estimated_dp, content_digest,
			client_request_id, max_attempts
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,'queued',$9,0,$10,$11,$12,$13,$14
		WHERE
		  (
		    $3='project' AND EXISTS (
		      SELECT 1 FROM ai_projects
		      WHERE id=$4 AND tenant_id=$1 AND user_id=$2
		    )
		  )
		  OR
		  (
		    $3='source' AND EXISTS (
		      SELECT 1 FROM knowledge_sources
		      WHERE id=$5 AND tenant_id=$1 AND user_id=$2
		    )
		  )
		  OR
		  (
		    $3='session' AND EXISTS (
		      SELECT 1 FROM sessions
		      WHERE id=$6 AND tenant_id=$1 AND user_id=$2
		    )
		  )
		ON CONFLICT DO NOTHING
		RETURNING id, tenant_id, user_id, target_type,
		          COALESCE(project_id, source_id, session_id)::text,
		          model, dimensions, status, chunk_count, processed_chunks,
		          estimated_tokens, actual_tokens, estimated_dp, content_digest,
		          client_request_id, error_message, lease_owner,
		          lease_expires_at, attempt_count, max_attempts,
		          cancel_requested_at, started_at, finished_at,
		          created_at, updated_at
	`, job.TenantID, job.UserID, job.TargetType, projectID, sourceID, sessionID,
		job.Model, job.Dimensions, job.ChunkCount, job.EstimatedTokens,
		job.EstimatedDP, job.ContentDigest, job.ClientRequestID,
		job.MaxAttempts), job)
	if errors.Is(err, sql.ErrNoRows) {
		if job.ClientRequestID != "" {
			existing, loadErr := getAIIndexJobByClientRequestIDTx(
				ctx, tx, job.TenantID, job.UserID, job.ClientRequestID,
			)
			if loadErr != nil {
				return false, loadErr
			}
			if existing != nil {
				if !sameAIIndexConfirmedRequest(existing, job) {
					return false, ErrIdempotencyConflict
				}
				*job = *existing
				if err := tx.Commit(); err != nil {
					return false, err
				}
				return false, nil
			}
		}
		active, loadErr := getActiveAIIndexJobForTargetTx(
			ctx, tx, job.TenantID, job.UserID,
			job.TargetType, job.TargetID, job.Model,
		)
		if loadErr != nil {
			return false, loadErr
		}
		if active == nil {
			active, loadErr = getActiveAIIndexJobForScopeTx(
				ctx, tx, job.TenantID, job.UserID, scopeType, scopeID,
			)
			if loadErr != nil {
				return false, loadErr
			}
			if active != nil {
				return false, ErrIndexTargetBusy
			}
			return false, sql.ErrNoRows
		}
		if job.ClientRequestID != "" &&
			active.ClientRequestID != job.ClientRequestID {
			return false, ErrIndexTargetBusy
		}
		*job = *active
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if err := insertAIIndexJobChunkSnapshotTx(ctx, tx, job); err != nil {
		return false, err
	}
	if err := setIndexTargetStatusTx(
		ctx, tx, job.TargetType, job.TargetID, job.TenantID, job.UserID,
		models.AIIndexStatusQueued, job.Model, "",
	); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *PostgresStore) GetAIIndexJob(
	ctx context.Context, jobID, tenantID, userID string,
) (*models.AIIndexJob, error) {
	var job models.AIIndexJob
	err := scanAIIndexJob(s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, target_type,
		       COALESCE(project_id, source_id, session_id)::text,
		       model, dimensions, status, chunk_count, processed_chunks,
		       estimated_tokens, actual_tokens, estimated_dp, content_digest,
		       client_request_id, error_message, lease_owner,
		       lease_expires_at, attempt_count, max_attempts,
		       cancel_requested_at, started_at, finished_at,
		       created_at, updated_at
		FROM ai_index_jobs
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
	`, jobID, tenantID, userID), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *PostgresStore) GetAIIndexJobByClientRequestID(
	ctx context.Context, tenantID, userID, clientRequestID string,
) (*models.AIIndexJob, error) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return nil, nil
	}
	if len(clientRequestID) > 128 {
		return nil, fmt.Errorf("client_request_id must be at most 128 characters")
	}
	return getAIIndexJobByClientRequestIDTx(
		ctx, s.db, tenantID, userID, clientRequestID,
	)
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getAIIndexJobByClientRequestIDTx(
	ctx context.Context,
	db queryRower,
	tenantID, userID, clientRequestID string,
) (*models.AIIndexJob, error) {
	var job models.AIIndexJob
	err := scanAIIndexJob(db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, target_type,
		       COALESCE(project_id, source_id, session_id)::text,
		       model, dimensions, status, chunk_count, processed_chunks,
		       estimated_tokens, actual_tokens, estimated_dp, content_digest,
		       client_request_id, error_message, lease_owner,
		       lease_expires_at, attempt_count, max_attempts,
		       cancel_requested_at, started_at, finished_at,
		       created_at, updated_at
		FROM ai_index_jobs
		WHERE tenant_id=$1 AND user_id=$2 AND client_request_id=$3
	`, tenantID, userID, clientRequestID), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *PostgresStore) GetActiveAIIndexJobForTarget(
	ctx context.Context,
	tenantID, userID, targetType, targetID, model string,
) (*models.AIIndexJob, error) {
	targetType = strings.ToLower(strings.TrimSpace(targetType))
	targetID = strings.TrimSpace(targetID)
	model = strings.TrimSpace(model)
	if targetType != "project" && targetType != "source" && targetType != "session" {
		return nil, fmt.Errorf("invalid index target type")
	}
	return getActiveAIIndexJobForTargetTx(
		ctx, s.db, tenantID, userID, targetType, targetID, model,
	)
}

func getActiveAIIndexJobForTargetTx(
	ctx context.Context,
	db queryRower,
	tenantID, userID, targetType, targetID, model string,
) (*models.AIIndexJob, error) {
	var job models.AIIndexJob
	err := scanAIIndexJob(db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, target_type,
		       COALESCE(project_id, source_id, session_id)::text,
		       model, dimensions, status, chunk_count, processed_chunks,
		       estimated_tokens, actual_tokens, estimated_dp, content_digest,
		       client_request_id, error_message, lease_owner,
		       lease_expires_at, attempt_count, max_attempts,
		       cancel_requested_at, started_at, finished_at,
		       created_at, updated_at
		FROM ai_index_jobs
		WHERE tenant_id=$1 AND user_id=$2 AND target_type=$3
		  AND COALESCE(project_id, source_id, session_id)=$4
		  AND model=$5 AND status IN ('queued', 'processing')
		ORDER BY created_at ASC
		LIMIT 1
	`, tenantID, userID, targetType, targetID, model), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *PostgresStore) ClaimAIIndexJobs(
	ctx context.Context, workerID string, limit int, leaseDuration time.Duration,
) ([]models.AIIndexJob, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	if limit <= 0 || limit > 128 {
		limit = 16
	}
	if leaseDuration <= 0 || leaseDuration > time.Hour {
		leaseDuration = 3 * time.Minute
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	expiredRows, err := tx.QueryContext(ctx, `
		UPDATE ai_index_jobs
		SET status='error',
		    error_message='index retry limit reached',
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW()
		WHERE status='processing'
		  AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		  AND attempt_count >= max_attempts
		RETURNING tenant_id, user_id, target_type,
		          COALESCE(project_id, source_id, session_id)::text, model
	`)
	if err != nil {
		return nil, err
	}
	type expiredTarget struct {
		tenantID, userID, targetType, targetID, model string
	}
	expiredTargets := make([]expiredTarget, 0)
	for expiredRows.Next() {
		var target expiredTarget
		if err := expiredRows.Scan(
			&target.tenantID, &target.userID, &target.targetType,
			&target.targetID, &target.model,
		); err != nil {
			_ = expiredRows.Close()
			return nil, err
		}
		expiredTargets = append(expiredTargets, target)
	}
	if err := expiredRows.Err(); err != nil {
		_ = expiredRows.Close()
		return nil, err
	}
	if err := expiredRows.Close(); err != nil {
		return nil, err
	}
	for _, target := range expiredTargets {
		if err := setIndexTargetStatusTx(
			ctx, tx, target.targetType, target.targetID,
			target.tenantID, target.userID, models.AIIndexStatusError,
			target.model, "index retry limit reached",
		); err != nil {
			return nil, err
		}
	}

	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
		  SELECT id
		  FROM ai_index_jobs
		  WHERE (
		      status='queued'
		      OR (
		        status='processing'
		        AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		      )
		    )
		    AND cancel_requested_at IS NULL
		    AND attempt_count < max_attempts
		  ORDER BY created_at ASC
		  FOR UPDATE SKIP LOCKED
		  LIMIT $1
		)
		UPDATE ai_index_jobs j
		SET status='processing',
		    error_message='',
		    lease_owner=$2,
		    lease_expires_at=NOW() + ($3 * INTERVAL '1 second'),
		    attempt_count=j.attempt_count + 1,
		    started_at=COALESCE(j.started_at, NOW())
		FROM candidates c
		WHERE j.id=c.id
		RETURNING j.id, j.tenant_id, j.user_id, j.target_type,
		          COALESCE(j.project_id, j.source_id, j.session_id)::text,
		          j.model, j.dimensions, j.status, j.chunk_count,
		          j.processed_chunks, j.estimated_tokens, j.actual_tokens,
		          j.estimated_dp, j.content_digest,
		          j.client_request_id, j.error_message,
		          j.lease_owner, j.lease_expires_at, j.attempt_count,
		          j.max_attempts, j.cancel_requested_at, j.started_at,
		          j.finished_at, j.created_at, j.updated_at
	`, limit, workerID, leaseDuration.Seconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]models.AIIndexJob, 0, limit)
	for rows.Next() {
		var job models.AIIndexJob
		if err := scanAIIndexJob(rows, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range jobs {
		job := &jobs[index]
		if err := setIndexTargetStatusTx(
			ctx, tx, job.TargetType, job.TargetID, job.TenantID, job.UserID,
			models.AIIndexStatusProcessing, job.Model, "",
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *PostgresStore) RenewAIIndexJobLease(
	ctx context.Context, jobID, workerID string, leaseDuration time.Duration,
) (bool, error) {
	if leaseDuration <= 0 || leaseDuration > time.Hour {
		leaseDuration = 3 * time.Minute
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_index_jobs
		SET lease_expires_at=NOW() + ($3 * INTERVAL '1 second')
		WHERE id=$1 AND status='processing' AND lease_owner=$2
		  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
	`, jobID, workerID, leaseDuration.Seconds())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *PostgresStore) UpdateAIIndexJobProgress(
	ctx context.Context, jobID, workerID string, processedChunks int,
) (bool, error) {
	return s.UpdateAIIndexJobProgressAndTokens(
		ctx, jobID, workerID, processedChunks, 0,
	)
}

func (s *PostgresStore) UpdateAIIndexJobProgressAndTokens(
	ctx context.Context,
	jobID, workerID string,
	processedChunks int,
	actualTokenDelta int64,
) (bool, error) {
	if processedChunks < 0 {
		return false, fmt.Errorf("processed chunk count cannot be negative")
	}
	if actualTokenDelta < 0 {
		return false, fmt.Errorf("actual token delta cannot be negative")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_index_jobs
		SET processed_chunks=GREATEST(processed_chunks, $3),
		    actual_tokens=actual_tokens + CASE
		      WHEN $3 > processed_chunks THEN $4
		      ELSE 0
		    END
		WHERE id=$1 AND status='processing' AND lease_owner=$2
		  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
		  AND $3 <= chunk_count
	`, jobID, workerID, processedChunks, actualTokenDelta)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *PostgresStore) CompleteAIIndexJob(
	ctx context.Context, jobID, workerID string, actualTokens int64,
) error {
	if actualTokens < 0 {
		return fmt.Errorf("actual token count cannot be negative")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var job models.AIIndexJob
	err = scanAIIndexJob(tx.QueryRowContext(ctx, `
		UPDATE ai_index_jobs
		SET status='ready',
		    processed_chunks=chunk_count,
		    actual_tokens=GREATEST(actual_tokens, $3),
		    error_message='',
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW()
		WHERE id=$1 AND status='processing' AND lease_owner=$2
		  AND lease_expires_at > NOW() AND cancel_requested_at IS NULL
		RETURNING id, tenant_id, user_id, target_type,
		          COALESCE(project_id, source_id, session_id)::text,
		          model, dimensions, status, chunk_count, processed_chunks,
		          estimated_tokens, actual_tokens, estimated_dp, content_digest,
		          client_request_id, error_message, lease_owner,
		          lease_expires_at, attempt_count, max_attempts,
		          cancel_requested_at, started_at, finished_at,
		          created_at, updated_at
	`, jobID, workerID, actualTokens), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	if err := validateAIIndexTargetSnapshotTx(ctx, tx, &job); err != nil {
		return err
	}
	if err := setIndexTargetStatusTx(
		ctx, tx, job.TargetType, job.TargetID, job.TenantID, job.UserID,
		models.AIIndexStatusReady, job.Model, "",
	); err != nil {
		return err
	}
	return tx.Commit()
}

func validateAIIndexTargetSnapshotTx(
	ctx context.Context, tx *sql.Tx, job *models.AIIndexJob,
) error {
	var expectedDigest string
	if err := tx.QueryRowContext(ctx, `
		SELECT content_digest
		FROM ai_index_jobs
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
	`, job.ID, job.TenantID, job.UserID).Scan(&expectedDigest); err != nil {
		return err
	}
	currentSnapshot, err := aiIndexTargetSnapshotTx(
		ctx,
		tx,
		job,
	)
	if err != nil {
		return err
	}
	var readyChunks int
	switch job.TargetType {
	case "source":
		err = tx.QueryRowContext(ctx, `
			SELECT COUNT(c.id) FILTER (
			         WHERE c.embedding IS NOT NULL
			           AND c.embedding_model=$4
			           AND c.embedding_status='ready'
			       )
			FROM knowledge_sources s
			LEFT JOIN knowledge_chunks c ON c.source_id=s.id
			WHERE s.id=$1 AND s.tenant_id=$2 AND s.user_id=$3
			  AND s.status='ready'
			GROUP BY s.id
		`, job.TargetID, job.TenantID, job.UserID, job.Model).Scan(&readyChunks)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrIndexContentChanged
		}
		if err != nil {
			return err
		}
	case "project":
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(c.id) FILTER (
			         WHERE c.embedding IS NOT NULL
			           AND c.embedding_model=$4
			           AND c.embedding_status='ready'
			       )
			FROM knowledge_sources s
			LEFT JOIN knowledge_chunks c ON c.source_id=s.id
			WHERE s.project_id=$1 AND s.tenant_id=$2 AND s.user_id=$3
			  AND s.status='ready'
		`, job.TargetID, job.TenantID, job.UserID, job.Model).Scan(&readyChunks); err != nil {
			return err
		}
	case "session":
		if err := tx.QueryRowContext(ctx, `
			SELECT COUNT(c.id) FILTER (
			         WHERE c.embedding IS NOT NULL
			           AND c.embedding_model=$4
			           AND c.embedding_status='ready'
			       )
			FROM sessions se
			LEFT JOIN session_ai_chunks c
			  ON c.session_id=se.id
			  AND c.tenant_id=se.tenant_id
			  AND c.user_id=se.user_id
			WHERE se.id=$1 AND se.tenant_id=$2 AND se.user_id=$3
		`, job.TargetID, job.TenantID, job.UserID, job.Model).Scan(&readyChunks); err != nil {
			return err
		}
	default:
		return fmt.Errorf("invalid index target type")
	}
	if readyChunks != currentSnapshot.TotalChunks ||
		currentSnapshot.ContentDigest != expectedDigest {
		return ErrIndexContentChanged
	}
	return nil
}

func (s *PostgresStore) CountQueuedAIIndexJobs(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ai_index_jobs WHERE status='queued'
	`).Scan(&count)
	return count, err
}

func (s *PostgresStore) FailAIIndexJob(
	ctx context.Context,
	jobID, workerID, errorMessage string,
	retryable bool,
	actualTokens int64,
) error {
	if actualTokens < 0 {
		return fmt.Errorf("actual token count cannot be negative")
	}
	errorMessage = truncateRunes(strings.TrimSpace(errorMessage))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var job models.AIIndexJob
	err = scanAIIndexJob(tx.QueryRowContext(ctx, `
		UPDATE ai_index_jobs
		SET status=CASE
		      WHEN $4 AND attempt_count < max_attempts THEN 'queued'
		      ELSE 'error'
		    END,
		    error_message=$3,
		    actual_tokens=GREATEST(actual_tokens, $5),
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=CASE
		      WHEN $4 AND attempt_count < max_attempts THEN NULL
		      ELSE NOW()
		    END
		WHERE id=$1 AND status='processing' AND lease_owner=$2
		  AND lease_expires_at > NOW()
		RETURNING id, tenant_id, user_id, target_type,
		          COALESCE(project_id, source_id, session_id)::text,
		          model, dimensions, status, chunk_count, processed_chunks,
		          estimated_tokens, actual_tokens, estimated_dp, content_digest,
		          client_request_id, error_message, lease_owner,
		          lease_expires_at, attempt_count, max_attempts,
		          cancel_requested_at, started_at, finished_at,
		          created_at, updated_at
	`, jobID, workerID, errorMessage, retryable, actualTokens), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return err
	}
	targetStatus := models.AIIndexStatusError
	if job.Status == models.AIIndexJobStatusQueued {
		targetStatus = models.AIIndexStatusQueued
	}
	if err := setIndexTargetStatusTx(
		ctx, tx, job.TargetType, job.TargetID, job.TenantID, job.UserID,
		targetStatus, job.Model, errorMessage,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) RetryAIIndexJob(
	ctx context.Context, jobID, tenantID, userID string,
) (*models.AIIndexJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var (
		targetType, targetID string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT target_type, COALESCE(project_id, source_id, session_id)::text
		FROM ai_index_jobs
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
	`, jobID, tenantID, userID).Scan(
		&targetType,
		&targetID,
	); err != nil {
		return nil, err
	}
	scopeType, scopeID, err := resolveAIIndexScopeTx(
		ctx, tx, &models.AIIndexJob{
			TenantID: tenantID, UserID: userID,
			TargetType: targetType, TargetID: targetID,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := lockAIIndexScopeTx(
		ctx, tx, userID, scopeType, scopeID,
	); err != nil {
		return nil, err
	}
	var existing models.AIIndexJob
	if err := scanAIIndexJob(tx.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, target_type,
		       COALESCE(project_id, source_id, session_id)::text,
		       model, dimensions, status, chunk_count, processed_chunks,
		       estimated_tokens, actual_tokens, estimated_dp, content_digest,
		       client_request_id, error_message, lease_owner,
		       lease_expires_at, attempt_count, max_attempts,
		       cancel_requested_at, started_at, finished_at,
		       created_at, updated_at
		FROM ai_index_jobs
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		FOR UPDATE
	`, jobID, tenantID, userID), &existing); err != nil {
		return nil, err
	}
	switch existing.Status {
	case models.AIIndexJobStatusQueued, models.AIIndexJobStatusProcessing:
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &existing, nil
	case models.AIIndexJobStatusError, models.AIIndexJobStatusCancelled:
	default:
		return nil, ErrIndexJobNotRetryable
	}
	if existing.ErrorMessage == ErrIndexContentChanged.Error() ||
		existing.ErrorMessage == "index target changed" {
		return nil, ErrIndexContentChanged
	}
	if existing.Status == models.AIIndexJobStatusCancelled &&
		existing.LeaseOwner != "" {
		var draining bool
		if err := tx.QueryRowContext(ctx, `
			SELECT lease_expires_at IS NOT NULL AND lease_expires_at > NOW()
			FROM ai_index_jobs
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		`, jobID, tenantID, userID).Scan(&draining); err != nil {
			return nil, err
		}
		if draining {
			return nil, ErrIndexTargetBusy
		}
	}
	active, err := getActiveAIIndexJobForScopeTx(
		ctx, tx, tenantID, userID, scopeType, scopeID,
	)
	if err != nil {
		return nil, err
	}
	if active != nil {
		if active.TargetType != targetType || active.TargetID != targetID ||
			active.Model != existing.Model {
			return nil, ErrIndexTargetBusy
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return active, nil
	}
	snapshotJob := &models.AIIndexJob{
		TenantID: tenantID, UserID: userID,
		TargetType: targetType, TargetID: targetID, Model: existing.Model,
	}
	currentSnapshot, err := aiIndexTargetSnapshotTx(ctx, tx, snapshotJob)
	if err != nil {
		return nil, err
	}
	var snapshottedChunks int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM ai_index_job_chunks WHERE job_id=$1
	`, jobID).Scan(&snapshottedChunks); err != nil {
		return nil, err
	}
	if snapshottedChunks != existing.ChunkCount ||
		currentSnapshot.ContentDigest != existing.ContentDigest {
		return nil, ErrIndexContentChanged
	}
	var job models.AIIndexJob
	err = scanAIIndexJob(tx.QueryRowContext(ctx, `
		UPDATE ai_index_jobs
		SET status='queued',
		    error_message='',
		    lease_owner='',
		    lease_expires_at=NULL,
		    attempt_count=0,
		    cancel_requested_at=NULL,
		    started_at=NULL,
		    finished_at=NULL
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND status IN ('error', 'cancelled')
		RETURNING id, tenant_id, user_id, target_type,
		          COALESCE(project_id, source_id, session_id)::text,
		          model, dimensions, status, chunk_count, processed_chunks,
		          estimated_tokens, actual_tokens, estimated_dp, content_digest,
		          client_request_id, error_message, lease_owner,
		          lease_expires_at, attempt_count, max_attempts,
		          cancel_requested_at, started_at, finished_at,
		          created_at, updated_at
	`, jobID, tenantID, userID), &job)
	if err != nil {
		return nil, err
	}
	if err := setIndexTargetStatusTx(
		ctx, tx, job.TargetType, job.TargetID, tenantID, userID,
		models.AIIndexStatusQueued, job.Model, "",
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

// ReleaseCancelledAIIndexJobLease acknowledges that the cancelled worker has
// returned from its provider call. CancelAIIndexJob deliberately retains a
// live lease until this acknowledgement so a retry cannot overlap paid work
// still running under the old fencing token.
func (s *PostgresStore) ReleaseCancelledAIIndexJobLease(
	ctx context.Context, jobID, workerID string,
) (bool, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_index_jobs
		SET lease_owner='',
		    lease_expires_at=NULL
		WHERE id=$1 AND status='cancelled' AND lease_owner=$2
	`, jobID, workerID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func lockAIIndexScopeTx(
	ctx context.Context,
	tx *sql.Tx,
	userID, scopeType, scopeID string,
) error {
	_, err := tx.ExecContext(ctx, `
		SELECT pg_advisory_xact_lock(
		  hashtextextended($1 || ':' || $2 || ':' || $3, 0)
		)
	`, userID, scopeType, scopeID)
	return err
}

type aiIndexScopeMutationImpact string

const (
	aiIndexMutationLockOnly aiIndexScopeMutationImpact = "lock_only"
	aiIndexMutationProject  aiIndexScopeMutationImpact = "project"
	aiIndexMutationSource   aiIndexScopeMutationImpact = "source"
	aiIndexMutationWhole    aiIndexScopeMutationImpact = "whole"
)

// coordinateAIIndexScopeMutationTx serializes content mutations with job
// creation/retry, takes the owner gate used by account deletion, then locks
// active jobs before touching any target or tenant-quota row. Workers preserve
// the same job -> target order, so a DELETE or chunk replacement cannot
// deadlock with a paid embedding batch. Affected leases are fenced inside this
// transaction before the target is changed; the returned IDs let handlers also
// cancel in-memory provider calls.
func coordinateAIIndexScopeMutationTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, userID, scopeType, scopeID string,
	impact aiIndexScopeMutationImpact,
	affectedSourceID string,
) ([]string, error) {
	if scopeType != "project" && scopeType != "session" {
		return nil, fmt.Errorf("invalid AI index scope type")
	}
	switch impact {
	case aiIndexMutationLockOnly,
		aiIndexMutationProject,
		aiIndexMutationSource,
		aiIndexMutationWhole:
	default:
		return nil, fmt.Errorf("invalid AI index mutation impact")
	}
	if err := lockAIIndexScopeTx(
		ctx,
		tx,
		userID,
		scopeType,
		scopeID,
	); err != nil {
		return nil, err
	}
	if err := lockAIStorageOwnerMutationGateTx(
		ctx,
		tx,
		tenantID,
		userID,
	); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT j.id::text, j.target_type,
		       COALESCE(j.project_id, j.source_id, j.session_id)::text
		FROM ai_index_jobs j
		WHERE j.tenant_id=$1 AND j.user_id=$2
		  AND j.status IN ('queued', 'processing')
		  AND (
		    (
		      $3='session'
		      AND j.target_type='session'
		      AND j.session_id=$4
		    )
		    OR
		    (
		      $3='project'
		      AND (
		        (j.target_type='project' AND j.project_id=$4)
		        OR (
		          j.target_type='source'
		          AND EXISTS (
		            SELECT 1
		            FROM knowledge_sources source
		            WHERE source.id=j.source_id
		              AND source.project_id=$4
		              AND source.tenant_id=$1
		              AND source.user_id=$2
		          )
		        )
		      )
		    )
		  )
		ORDER BY j.id
		FOR UPDATE OF j
	`, tenantID, userID, scopeType, scopeID)
	if err != nil {
		return nil, err
	}
	type activeJob struct {
		id         string
		targetType string
		targetID   string
	}
	activeJobs := make([]activeJob, 0, 1)
	for rows.Next() {
		var job activeJob
		if err := rows.Scan(&job.id, &job.targetType, &job.targetID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		activeJobs = append(activeJobs, job)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	jobIDs := make([]string, 0, len(activeJobs))
	for _, job := range activeJobs {
		cancel := impact == aiIndexMutationWhole
		if scopeType == "project" {
			switch impact {
			case aiIndexMutationProject:
				cancel = job.targetType == "project"
			case aiIndexMutationSource:
				cancel = job.targetType == "project" ||
					(job.targetType == "source" &&
						job.targetID == affectedSourceID)
			case aiIndexMutationLockOnly:
				cancel = false
			}
		}
		if cancel {
			jobIDs = append(jobIDs, job.id)
		}
	}
	for _, jobID := range jobIDs {
		var job models.AIIndexJob
		err := scanAIIndexJob(tx.QueryRowContext(ctx, `
			UPDATE ai_index_jobs
			SET status='cancelled',
			    cancel_requested_at=NOW(),
			    error_message='index target changed',
			    lease_owner='',
			    lease_expires_at=NULL,
			    finished_at=NOW()
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
			  AND status IN ('queued', 'processing')
			RETURNING id, tenant_id, user_id, target_type,
			          COALESCE(project_id, source_id, session_id)::text,
			          model, dimensions, status, chunk_count, processed_chunks,
			          estimated_tokens, actual_tokens, estimated_dp,
			          content_digest, client_request_id, error_message,
			          lease_owner, lease_expires_at, attempt_count,
			          max_attempts, cancel_requested_at, started_at,
			          finished_at, created_at, updated_at
		`, jobID, tenantID, userID), &job)
		if err != nil {
			return nil, err
		}
	}
	return jobIDs, nil
}

func resetCancelledAIIndexTargetsTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, userID string,
	jobIDs []string,
) error {
	for _, jobID := range jobIDs {
		var job models.AIIndexJob
		err := scanAIIndexJob(tx.QueryRowContext(ctx, `
			SELECT id, tenant_id, user_id, target_type,
			       COALESCE(project_id, source_id, session_id)::text,
			       model, dimensions, status, chunk_count, processed_chunks,
			       estimated_tokens, actual_tokens, estimated_dp,
			       content_digest, client_request_id, error_message,
			       lease_owner, lease_expires_at, attempt_count,
			       max_attempts, cancel_requested_at, started_at,
			       finished_at, created_at, updated_at
			FROM ai_index_jobs
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
			  AND status='cancelled'
		`, jobID, tenantID, userID), &job)
		if err != nil {
			return err
		}
		if err := setIndexTargetStatusTx(
			ctx,
			tx,
			job.TargetType,
			job.TargetID,
			tenantID,
			userID,
			models.AIIndexStatusUnindexed,
			job.Model,
			"",
		); err != nil {
			return err
		}
	}
	return nil
}

// cancelActiveAIIndexJobsForUserTx is used while the caller holds the target
// users row FOR UPDATE. That row is the FK gate preventing new jobs from being
// inserted while all existing active jobs are fenced in deterministic order.
func cancelActiveAIIndexJobsForUserTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, userID string,
) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id::text
		FROM ai_index_jobs
		WHERE tenant_id=$1 AND user_id=$2
		  AND status IN ('queued', 'processing')
		ORDER BY id
		FOR UPDATE
	`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	jobIDs := make([]string, 0)
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		jobIDs = append(jobIDs, jobID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, jobID := range jobIDs {
		result, err := tx.ExecContext(ctx, `
			UPDATE ai_index_jobs
			SET status='cancelled',
			    cancel_requested_at=NOW(),
			    error_message='index owner deleted',
			    lease_owner='',
			    lease_expires_at=NULL,
			    finished_at=NOW()
			WHERE id=$1 AND tenant_id=$2 AND user_id=$3
			  AND status IN ('queued', 'processing')
		`, jobID, tenantID, userID)
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected != 1 {
			return nil, ErrLeaseLost
		}
	}
	return jobIDs, nil
}

func (s *PostgresStore) CancelAIIndexJob(
	ctx context.Context, jobID, tenantID, userID string,
) (*models.AIIndexJob, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var job models.AIIndexJob
	err = scanAIIndexJob(tx.QueryRowContext(ctx, `
		UPDATE ai_index_jobs
		SET status='cancelled',
		    cancel_requested_at=NOW(),
		    lease_owner=CASE
		      WHEN status='processing' THEN lease_owner
		      ELSE ''
		    END,
		    lease_expires_at=CASE
		      WHEN status='processing' THEN lease_expires_at
		      ELSE NULL
		    END,
		    finished_at=NOW()
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND status IN ('queued', 'processing')
		RETURNING id, tenant_id, user_id, target_type,
		          COALESCE(project_id, source_id, session_id)::text,
		          model, dimensions, status, chunk_count, processed_chunks,
		          estimated_tokens, actual_tokens, estimated_dp, content_digest,
		          client_request_id, error_message, lease_owner,
		          lease_expires_at, attempt_count, max_attempts,
		          cancel_requested_at, started_at, finished_at,
		          created_at, updated_at
	`, jobID, tenantID, userID), &job)
	if err != nil {
		return nil, err
	}
	if err := setIndexTargetStatusTx(
		ctx, tx, job.TargetType, job.TargetID, tenantID, userID,
		models.AIIndexStatusUnindexed, job.Model, "",
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func setIndexTargetStatusTx(
	ctx context.Context,
	tx *sql.Tx,
	targetType, targetID, tenantID, userID, status, model, errorMessage string,
) error {
	if !validAIIndexStatus(status) {
		return fmt.Errorf("invalid index status")
	}
	switch targetType {
	case "source":
		result, err := tx.ExecContext(ctx, `
			UPDATE knowledge_sources
			SET index_status=$1,
			    embedding_model=CASE WHEN $2='' THEN embedding_model ELSE $2 END,
			    embedding_dimensions=CASE WHEN $2='' THEN embedding_dimensions ELSE 1536 END,
			    index_error_message=$3,
			    indexed_at=CASE WHEN $1='ready' THEN NOW() ELSE indexed_at END
			WHERE id=$4 AND tenant_id=$5 AND user_id=$6
		`, status, model, errorMessage, targetID, tenantID, userID)
		return requireOneRow(result, err)
	case "project":
		result, err := tx.ExecContext(ctx, `
			UPDATE knowledge_sources
			SET index_status=$1,
			    embedding_model=CASE WHEN $2='' THEN embedding_model ELSE $2 END,
			    embedding_dimensions=CASE WHEN $2='' THEN embedding_dimensions ELSE 1536 END,
			    index_error_message=$3,
			    indexed_at=CASE WHEN $1='ready' THEN NOW() ELSE indexed_at END
			WHERE project_id=$4 AND tenant_id=$5 AND user_id=$6
			  AND status='ready'
		`, status, model, errorMessage, targetID, tenantID, userID)
		if err != nil {
			return err
		}
		_, err = result.RowsAffected()
		return err
	case "session":
		// Preserve chunks that are already ready for this model so a partial
		// resume never pays to embed them twice. The remaining rows carry the
		// target-level queue/error state used by context previews.
		result, err := tx.ExecContext(ctx, `
			UPDATE session_ai_chunks c
			SET embedding_status=$1,
			    embedding_error=$3
			FROM sessions se
			WHERE c.session_id=$4
			  AND c.tenant_id=$5 AND c.user_id=$6
			  AND se.id=c.session_id
			  AND se.tenant_id=$5 AND se.user_id=$6
			  AND (
			    c.embedding IS NULL
			    OR c.embedding_model<>$2
			    OR c.embedding_status<>'ready'
			  )
		`, status, model, errorMessage, targetID, tenantID, userID)
		if err != nil {
			return err
		}
		_, err = result.RowsAffected()
		return err
	default:
		return fmt.Errorf("invalid index target type")
	}
}

func requireOneRow(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}
