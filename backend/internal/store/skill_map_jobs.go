package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

const skillMapJobSelect = `
	id, tenant_id, user_id, project_id, status, model, reasoning_effort,
	request_hash, client_request_id, chunk_count, processed_chunks,
	error_message, lease_owner, lease_expires_at, attempt_count, max_attempts,
	started_at, finished_at, created_at, updated_at
`

// Same columns qualified for the claim query, whose UPDATE … FROM joins a
// CTE that also has an "id". Keep in sync with skillMapJobSelect.
const skillMapJobSelectJobs = `
	jobs.id, jobs.tenant_id, jobs.user_id, jobs.project_id, jobs.status,
	jobs.model, jobs.reasoning_effort, jobs.request_hash, jobs.client_request_id,
	jobs.chunk_count, jobs.processed_chunks, jobs.error_message, jobs.lease_owner,
	jobs.lease_expires_at, jobs.attempt_count, jobs.max_attempts,
	jobs.started_at, jobs.finished_at, jobs.created_at, jobs.updated_at
`

func scanSkillMapJob(scanner rowScanner, job *models.SkillMapJob) error {
	return scanner.Scan(
		&job.ID, &job.TenantID, &job.UserID, &job.ProjectID, &job.Status,
		&job.Model, &job.ReasoningEffort, &job.RequestHash, &job.ClientRequestID,
		&job.ChunkCount, &job.ProcessedChunks, &job.ErrorMessage, &job.LeaseOwner,
		&job.LeaseExpiresAt, &job.AttemptCount, &job.MaxAttempts, &job.StartedAt,
		&job.FinishedAt, &job.CreatedAt, &job.UpdatedAt,
	)
}

func validateSkillMapJob(job *models.SkillMapJob) error {
	if job == nil {
		return fmt.Errorf("skill map job is required")
	}
	if strings.TrimSpace(job.ID) == "" {
		job.ID = uuid.NewString()
	} else if uuid.Validate(job.ID) != nil {
		return fmt.Errorf("skill map job id must be a UUID")
	}
	if uuid.Validate(job.TenantID) != nil || uuid.Validate(job.UserID) != nil ||
		uuid.Validate(job.ProjectID) != nil {
		return fmt.Errorf("skill map job owner ids must be UUIDs")
	}
	job.ClientRequestID = strings.TrimSpace(job.ClientRequestID)
	if job.ClientRequestID == "" || len(job.ClientRequestID) > 128 {
		return fmt.Errorf("client_request_id is required and must be at most 128 characters")
	}
	job.RequestHash = strings.ToLower(strings.TrimSpace(job.RequestHash))
	hash, err := hex.DecodeString(job.RequestHash)
	if err != nil || len(hash) != 32 {
		return fmt.Errorf("request_hash must be a lowercase SHA-256 digest")
	}
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 3
	}
	if strings.TrimSpace(job.ReasoningEffort) == "" {
		job.ReasoningEffort = "low"
	}
	if job.Status == "" {
		job.Status = "queued"
	}
	return nil
}

func isSkillMapJobUniqueViolation(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "23505"
}

// CreateSkillMapJob enqueues one generation. The same client_request_id with
// the same hash replays; a different hash conflicts. A new request cancels any
// other queued/processing job for the course so regenerate replaces in-flight work.
func (s *PostgresStore) CreateSkillMapJob(
	ctx context.Context, job *models.SkillMapJob,
) (bool, error) {
	if err := validateSkillMapJob(job); err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := getSkillMapJobByClientRequestIDTx(
		ctx, tx, job.TenantID, job.UserID, job.ClientRequestID,
	)
	if err != nil {
		return false, err
	}
	if existing != nil {
		if existing.RequestHash != job.RequestHash ||
			existing.ProjectID != job.ProjectID {
			return false, ErrIdempotencyConflict
		}
		*job = *existing
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE skill_map_jobs
		SET status='cancelled',
		    error_message='replaced by a newer generation request',
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW()
		WHERE project_id=$1 AND user_id=$2
		  AND status IN ('queued', 'processing')
	`, job.ProjectID, job.UserID); err != nil {
		return false, err
	}

	if err := scanSkillMapJob(tx.QueryRowContext(ctx, `
		INSERT INTO skill_map_jobs (
			id, tenant_id, user_id, project_id, status, model, reasoning_effort,
			request_hash, client_request_id, chunk_count, max_attempts
		) VALUES ($1,$2,$3,$4,'queued',$5,$6,$7,$8,$9,$10)
		RETURNING `+skillMapJobSelect, job.ID, job.TenantID, job.UserID, job.ProjectID,
		job.Model, job.ReasoningEffort, job.RequestHash, job.ClientRequestID,
		job.ChunkCount, job.MaxAttempts,
	), job); err != nil {
		if isSkillMapJobUniqueViolation(err) {
			return false, ErrIdempotencyConflict
		}
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func getSkillMapJobByClientRequestIDTx(
	ctx context.Context, tx *sql.Tx, tenantID, userID, clientRequestID string,
) (*models.SkillMapJob, error) {
	var job models.SkillMapJob
	err := scanSkillMapJob(tx.QueryRowContext(ctx, `
		SELECT `+skillMapJobSelect+`
		FROM skill_map_jobs
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

// CancelSkillMapJobs cancels every queued or processing job for a course.
// A running worker notices on its next lease renewal and aborts.
func (s *PostgresStore) CancelSkillMapJobs(
	ctx context.Context, userID, projectID string,
) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		UPDATE skill_map_jobs
		SET status='cancelled',
		    error_message='cancelled by the learner',
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW()
		WHERE user_id=$1 AND project_id=$2
		  AND status IN ('queued', 'processing')
	`, userID, projectID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetActiveSkillMapJob returns the queued or processing job for a course.
func (s *PostgresStore) GetActiveSkillMapJob(
	ctx context.Context, userID, projectID string,
) (*models.SkillMapJob, error) {
	var job models.SkillMapJob
	err := scanSkillMapJob(s.db.QueryRowContext(ctx, `
		SELECT `+skillMapJobSelect+`
		FROM skill_map_jobs
		WHERE user_id=$1 AND project_id=$2
		  AND status IN ('queued', 'processing')
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID, projectID), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// GetLatestSkillMapJob returns the newest job for a course, including errors.
func (s *PostgresStore) GetLatestSkillMapJob(
	ctx context.Context, userID, projectID string,
) (*models.SkillMapJob, error) {
	var job models.SkillMapJob
	err := scanSkillMapJob(s.db.QueryRowContext(ctx, `
		SELECT `+skillMapJobSelect+`
		FROM skill_map_jobs
		WHERE user_id=$1 AND project_id=$2
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID, projectID), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// ClaimSkillMapJobs leases queued or expired processing jobs.
func (s *PostgresStore) ClaimSkillMapJobs(
	ctx context.Context, workerID string, limit int, leaseDuration time.Duration,
) ([]models.SkillMapJob, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	if limit <= 0 || limit > 16 {
		limit = 1
	}
	if leaseDuration <= 0 || leaseDuration > time.Hour {
		leaseDuration = 3 * time.Minute
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE skill_map_jobs
		SET status='error',
		    error_message='skill map retry limit reached',
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW()
		WHERE status='processing'
		  AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		  AND attempt_count >= max_attempts
	`); err != nil {
		return nil, err
	}

	rows, err := tx.QueryContext(ctx, `
		WITH candidates AS (
		  SELECT id
		  FROM skill_map_jobs
		  WHERE status='queued'
		     OR (
		       status='processing'
		       AND (lease_expires_at IS NULL OR lease_expires_at < NOW())
		       AND attempt_count < max_attempts
		     )
		  ORDER BY created_at ASC, id ASC
		  FOR UPDATE SKIP LOCKED
		  LIMIT $1
		)
		UPDATE skill_map_jobs AS jobs
		SET status='processing',
		    lease_owner=$2,
		    lease_expires_at=NOW() + ($3 * INTERVAL '1 second'),
		    attempt_count=jobs.attempt_count + 1,
		    started_at=COALESCE(jobs.started_at, NOW()),
		    error_message=''
		FROM candidates
		WHERE jobs.id=candidates.id
		RETURNING `+skillMapJobSelectJobs, limit, workerID, leaseDuration.Seconds())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	jobs := make([]models.SkillMapJob, 0)
	for rows.Next() {
		var job models.SkillMapJob
		if err := scanSkillMapJob(rows, &job); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (s *PostgresStore) RenewSkillMapJobLease(
	ctx context.Context, jobID, leaseOwner string, leaseDuration time.Duration,
) (bool, error) {
	if leaseDuration <= 0 || leaseDuration > 30*time.Minute {
		leaseDuration = 3 * time.Minute
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE skill_map_jobs
		SET lease_expires_at=NOW() + ($3 * INTERVAL '1 second')
		WHERE id=$1 AND lease_owner=$2 AND status='processing'
		  AND lease_expires_at > NOW()
	`, jobID, leaseOwner, leaseDuration.Seconds())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *PostgresStore) UpdateSkillMapJobProgress(
	ctx context.Context, jobID, leaseOwner string, processed, chunkCount int,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE skill_map_jobs
		SET processed_chunks=$3, chunk_count=$4
		WHERE id=$1 AND lease_owner=$2 AND status='processing'
	`, jobID, leaseOwner, processed, chunkCount)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) CompleteSkillMapJob(
	ctx context.Context, jobID, leaseOwner string,
) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE skill_map_jobs
		SET status='ready',
		    processed_chunks=GREATEST(processed_chunks, chunk_count),
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW(),
		    error_message=''
		WHERE id=$1 AND lease_owner=$2 AND status='processing'
	`, jobID, leaseOwner)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *PostgresStore) FailSkillMapJob(
	ctx context.Context, jobID, leaseOwner, message string, retryable bool,
) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "skill map generation failed"
	}
	query := `
		UPDATE skill_map_jobs
		SET status='error',
		    error_message=$3,
		    lease_owner='',
		    lease_expires_at=NULL,
		    finished_at=NOW()
		WHERE id=$1 AND lease_owner=$2 AND status='processing'
	`
	if retryable {
		query = `
			UPDATE skill_map_jobs
			SET status=CASE
			      WHEN attempt_count >= max_attempts THEN 'error'
			      ELSE 'queued'
			    END,
			    error_message=$3,
			    lease_owner='',
			    lease_expires_at=NULL,
			    finished_at=CASE
			      WHEN attempt_count >= max_attempts THEN NOW()
			      ELSE NULL
			    END
			WHERE id=$1 AND lease_owner=$2 AND status='processing'
		`
	}
	result, err := s.db.ExecContext(ctx, query, jobID, leaseOwner, message)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrLeaseLost
	}
	return nil
}
