package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type KnowledgeBlobDeletion struct {
	ID           string
	TenantID     string
	UserID       string
	BlobPath     string
	LeaseOwner   string
	AttemptCount int
	MaxAttempts  int
}

func enqueueKnowledgeBlobDeletionTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, userID, blobPath string,
) error {
	blobPath = strings.TrimSpace(blobPath)
	if blobPath == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO knowledge_blob_deletions (
		  tenant_id, user_id, blob_path
		) VALUES ($1,$2,$3)
		ON CONFLICT (blob_path) DO UPDATE
		SET status='queued',
		    lease_owner='',
		    lease_expires_at=NULL,
		    error_message=''
	`, tenantID, userID, blobPath)
	return err
}

func (s *PostgresStore) ClaimKnowledgeBlobDeletion(
	ctx context.Context,
	workerID string,
	leaseDuration time.Duration,
) (*KnowledgeBlobDeletion, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	if leaseDuration <= 0 || leaseDuration > time.Hour {
		leaseDuration = 3 * time.Minute
	}
	var deletion KnowledgeBlobDeletion
	err := s.db.QueryRowContext(ctx, `
		WITH candidate AS (
		  SELECT id
		  FROM knowledge_blob_deletions
		  WHERE (
		      status='queued'
		      OR (
		        status='processing'
		        AND (lease_expires_at IS NULL OR lease_expires_at <= NOW())
		      )
		    )
		    AND attempt_count < max_attempts
		  ORDER BY created_at
		  FOR UPDATE SKIP LOCKED
		  LIMIT 1
		)
		UPDATE knowledge_blob_deletions deletion
		SET status='processing',
		    lease_owner=$1,
		    lease_expires_at=NOW() + ($2 * INTERVAL '1 second'),
		    attempt_count=attempt_count + 1,
		    error_message=''
		FROM candidate
		WHERE deletion.id=candidate.id
		RETURNING deletion.id, deletion.tenant_id, deletion.user_id,
		          deletion.blob_path, deletion.lease_owner,
		          deletion.attempt_count, deletion.max_attempts
	`, workerID, leaseDuration.Seconds()).Scan(
		&deletion.ID,
		&deletion.TenantID,
		&deletion.UserID,
		&deletion.BlobPath,
		&deletion.LeaseOwner,
		&deletion.AttemptCount,
		&deletion.MaxAttempts,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &deletion, nil
}

func (s *PostgresStore) CompleteKnowledgeBlobDeletion(
	ctx context.Context,
	deletionID, workerID string,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM knowledge_blob_deletions
		WHERE id=$1 AND status='processing' AND lease_owner=$2
		  AND lease_expires_at > NOW()
	`, deletionID, workerID)
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

func (s *PostgresStore) FailKnowledgeBlobDeletion(
	ctx context.Context,
	deletionID, workerID, errorMessage string,
) error {
	errorMessage = truncateRunes(strings.TrimSpace(errorMessage))
	result, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_blob_deletions
		SET status=CASE
		      WHEN attempt_count < max_attempts THEN 'queued'
		      ELSE 'error'
		    END,
		    lease_owner='',
		    lease_expires_at=NULL,
		    error_message=$3
		WHERE id=$1 AND status='processing' AND lease_owner=$2
		  AND lease_expires_at > NOW()
	`, deletionID, workerID, errorMessage)
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
