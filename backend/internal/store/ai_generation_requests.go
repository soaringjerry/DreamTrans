package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

func scanAIGenerationRequest(
	scanner rowScanner, request *models.AIGenerationRequest,
) error {
	var responseJSON []byte
	err := scanner.Scan(
		&request.ID, &request.TenantID, &request.UserID,
		&request.SessionID, &request.ClientRequestID, &request.RequestKind,
		&request.RequestHash, &request.Status, &responseJSON,
		&request.LeaseOwner, &request.LeaseExpiresAt, &request.AttemptCount,
		&request.ErrorMessage, &request.CompletedAt, &request.ExpiresAt,
		&request.CreatedAt, &request.UpdatedAt,
	)
	if err != nil {
		return err
	}
	if len(responseJSON) > 0 {
		request.ResponseJSON = append(request.ResponseJSON[:0], responseJSON...)
	} else {
		request.ResponseJSON = nil
	}
	return nil
}

func validateAIGenerationRequest(request *models.AIGenerationRequest) error {
	request.ClientRequestID = strings.TrimSpace(request.ClientRequestID)
	request.RequestKind = strings.ToLower(strings.TrimSpace(request.RequestKind))
	request.RequestHash = strings.ToLower(strings.TrimSpace(request.RequestHash))
	request.LeaseOwner = strings.TrimSpace(request.LeaseOwner)
	if request.TenantID == "" || request.UserID == "" {
		return fmt.Errorf("tenant and user ids are required")
	}
	if request.SessionID != nil {
		sessionID := strings.TrimSpace(*request.SessionID)
		if sessionID == "" {
			request.SessionID = nil
		} else {
			request.SessionID = &sessionID
		}
	}
	if request.ClientRequestID == "" || len(request.ClientRequestID) > 128 {
		return fmt.Errorf("client_request_id is required and must be at most 128 characters")
	}
	if request.RequestKind == "" || len(request.RequestKind) > 32 {
		return fmt.Errorf("request_kind is required and must be at most 32 characters")
	}
	for _, character := range request.RequestKind {
		if (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return fmt.Errorf("request_kind contains an unsupported character")
		}
	}
	hashBytes, err := hex.DecodeString(request.RequestHash)
	if err != nil || len(hashBytes) != 32 {
		return fmt.Errorf("request_hash must be a lowercase SHA-256 hex digest")
	}
	if request.LeaseOwner == "" || len(request.LeaseOwner) > 200 {
		return fmt.Errorf("lease_owner is required and must be at most 200 characters")
	}
	return nil
}

// BeginAIGenerationRequest reserves one authenticated generation request before
// a provider call. The result outcome is one of acquired, ready_replay,
// in_progress, or hash_conflict.
func (s *PostgresStore) BeginAIGenerationRequest(
	ctx context.Context,
	request *models.AIGenerationRequest,
	leaseDuration time.Duration,
) (*models.AIGenerationBeginResult, error) {
	if err := validateAIGenerationRequest(request); err != nil {
		return nil, err
	}
	if leaseDuration <= 0 || leaseDuration > 30*time.Minute {
		leaseDuration = 2 * time.Minute
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	// Ready responses are a bounded idempotency cache, not a second permanent
	// copy of generated user data. Reusing a key after expiry first removes its
	// old response within the same transaction.
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM ai_generation_requests
		WHERE tenant_id=$1 AND user_id=$2 AND client_request_id=$3
		  AND expires_at <= NOW()
	`, request.TenantID, request.UserID, request.ClientRequestID); err != nil {
		return nil, err
	}

	var inserted models.AIGenerationRequest
	err = scanAIGenerationRequest(tx.QueryRowContext(ctx, `
		INSERT INTO ai_generation_requests (
			tenant_id, user_id, session_id, client_request_id, request_kind,
			request_hash, status, lease_owner, lease_expires_at
		)
		SELECT $1,$2,$3,$4,$5,$6,'processing',$7,
		       NOW() + ($8 * INTERVAL '1 second')
		WHERE EXISTS (
		  SELECT 1 FROM users
		  WHERE id=$2 AND tenant_id=$1 AND is_active=true
		)
		  AND (
		    $3::uuid IS NULL
		    OR EXISTS (
		      SELECT 1 FROM sessions
		      WHERE id=$3 AND tenant_id=$1 AND user_id=$2
		    )
		  )
		ON CONFLICT DO NOTHING
		RETURNING id, tenant_id, user_id, session_id, client_request_id,
		          request_kind, request_hash, status, response_json, lease_owner,
		          lease_expires_at, attempt_count, error_message,
		          completed_at, expires_at, created_at, updated_at
	`, request.TenantID, request.UserID, request.SessionID,
		request.ClientRequestID, request.RequestKind, request.RequestHash,
		request.LeaseOwner, leaseDuration.Seconds()), &inserted)
	if err == nil {
		*request = inserted
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.AIGenerationBeginResult{
			Outcome: models.AIGenerationOutcomeAcquired,
			Created: true,
			Request: inserted,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var existing models.AIGenerationRequest
	err = scanAIGenerationRequest(tx.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, session_id, client_request_id,
		       request_kind, request_hash, status, response_json, lease_owner,
		       lease_expires_at, attempt_count, error_message,
		       completed_at, expires_at, created_at, updated_at
		FROM ai_generation_requests
		WHERE tenant_id=$1 AND user_id=$2 AND client_request_id=$3
		FOR UPDATE
	`, request.TenantID, request.UserID, request.ClientRequestID), &existing)
	if errors.Is(err, sql.ErrNoRows) {
		// No conflict row means the INSERT's ownership predicate failed.
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	if existing.RequestKind != request.RequestKind ||
		existing.RequestHash != request.RequestHash {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.AIGenerationBeginResult{
			Outcome: models.AIGenerationOutcomeHashConflict,
			Created: false,
			Request: existing,
		}, nil
	}
	if existing.Status == models.AIGenerationStatusReady {
		*request = existing
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.AIGenerationBeginResult{
			Outcome: models.AIGenerationOutcomeReplay,
			Created: false,
			Request: existing,
		}, nil
	}
	leaseIsLive := false
	if existing.Status == models.AIGenerationStatusProcessing {
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(lease_expires_at > NOW(), false)
			FROM ai_generation_requests WHERE id=$1
		`, existing.ID).Scan(&leaseIsLive); err != nil {
			return nil, err
		}
	}
	if leaseIsLive {
		*request = existing
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &models.AIGenerationBeginResult{
			Outcome: models.AIGenerationOutcomeInProgress,
			Created: false,
			Request: existing,
		}, nil
	}

	var reclaimed models.AIGenerationRequest
	err = scanAIGenerationRequest(tx.QueryRowContext(ctx, `
		UPDATE ai_generation_requests
		SET status='processing',
		    response_json=NULL,
		    lease_owner=$4,
		    lease_expires_at=NOW() + ($5 * INTERVAL '1 second'),
		    expires_at=NOW() + INTERVAL '24 hours',
		    attempt_count=attempt_count + 1,
		    error_message='',
		    completed_at=NULL
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		RETURNING id, tenant_id, user_id, session_id, client_request_id,
		          request_kind, request_hash, status, response_json, lease_owner,
		          lease_expires_at, attempt_count, error_message,
		          completed_at, expires_at, created_at, updated_at
	`, existing.ID, existing.TenantID, existing.UserID, request.LeaseOwner,
		leaseDuration.Seconds()), &reclaimed)
	if err != nil {
		return nil, err
	}
	*request = reclaimed
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.AIGenerationBeginResult{
		Outcome: models.AIGenerationOutcomeAcquired,
		Created: false,
		Request: reclaimed,
	}, nil
}

func (s *PostgresStore) CompleteAIGenerationRequest(
	ctx context.Context,
	requestID, tenantID, userID, leaseOwner string,
	responseJSON json.RawMessage,
) error {
	if !json.Valid(responseJSON) {
		return fmt.Errorf("generation response must be valid JSON")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_generation_requests
		SET status='ready',
		    response_json=$5::jsonb,
		    lease_owner='',
		    lease_expires_at=NULL,
		    error_message='',
		    completed_at=NOW(),
		    expires_at=NOW() + INTERVAL '24 hours'
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND status='processing' AND lease_owner=$4
		  AND lease_expires_at > NOW()
	`, requestID, tenantID, userID, leaseOwner, string(responseJSON))
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

func (s *PostgresStore) FailAIGenerationRequest(
	ctx context.Context,
	requestID, tenantID, userID, leaseOwner, errorMessage string,
) error {
	errorMessage = truncateRunes(strings.TrimSpace(errorMessage), 1_000)
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_generation_requests
		SET status='error',
		    lease_owner='',
		    lease_expires_at=NULL,
		    error_message=$5,
		    completed_at=NOW()
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND status='processing' AND lease_owner=$4
		  AND lease_expires_at > NOW()
	`, requestID, tenantID, userID, leaseOwner, errorMessage)
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

func (s *PostgresStore) RenewAIGenerationRequestLease(
	ctx context.Context,
	requestID, tenantID, userID, leaseOwner string,
	leaseDuration time.Duration,
) (bool, error) {
	if leaseDuration <= 0 || leaseDuration > 30*time.Minute {
		leaseDuration = 2 * time.Minute
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_generation_requests
		SET lease_expires_at=NOW() + ($5 * INTERVAL '1 second')
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND status='processing' AND lease_owner=$4
		  AND lease_expires_at > NOW()
	`, requestID, tenantID, userID, leaseOwner, leaseDuration.Seconds())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

// ReleaseAIGenerationRequest removes an in-flight reservation without storing
// a response copy. Artifact rows are already durable and idempotent, so their
// full response must not also be retained in this cache.
func (s *PostgresStore) ReleaseAIGenerationRequest(
	ctx context.Context,
	requestID, tenantID, userID, leaseOwner string,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM ai_generation_requests
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		  AND status='processing' AND lease_owner=$4
	`, requestID, tenantID, userID, leaseOwner)
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

func (s *PostgresStore) DeleteAIGenerationRequestByClientRequestID(
	ctx context.Context, tenantID, userID, clientRequestID, requestKind string,
) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM ai_generation_requests
		WHERE tenant_id=$1 AND user_id=$2 AND client_request_id=$3
		  AND request_kind=$4
		  AND (
		    status<>'processing'
		    OR lease_expires_at IS NULL
		    OR lease_expires_at <= NOW()
		  )
	`, tenantID, userID, strings.TrimSpace(clientRequestID),
		strings.TrimSpace(requestKind))
	return err
}

func (s *PostgresStore) PruneExpiredAIGenerationRequests(
	ctx context.Context,
) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM ai_generation_requests WHERE expires_at <= NOW()
	`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
