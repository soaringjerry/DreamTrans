package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TranslationRequestDisposition describes whether the caller owns new
// provider work, should wait for an existing owner, or can replay a completed
// result without touching billing again.
type TranslationRequestDisposition uint8

const (
	TranslationRequestOwner TranslationRequestDisposition = iota
	TranslationRequestProcessing
	TranslationRequestReplay
	TranslationRequestExpired
)

var ErrTranslationRequestConflict = errors.New(
	"translation request key belongs to different content or owner",
)

// TranslationReplayResult is the minimum provider result required to satisfy
// a reconnect.
type TranslationReplayResult struct {
	Content   string
	Model     string
	LatencyMs int64
}

type TranslationRequestClaim struct {
	Disposition         TranslationRequestDisposition
	Attempt             int
	UsageIdempotencyKey string
	Result              TranslationReplayResult
}

const (
	translationRequestCleanupBatch = 128
	maxTranslationRequestAttempt   = 2_147_483_647
)

func translationAttemptUsageKey(requestKey string, attempt int) (string, error) {
	if attempt < 1 || attempt > maxTranslationRequestAttempt {
		return "", fmt.Errorf("translation request attempt is out of range")
	}
	key := requestKey + ":attempt:" + fmt.Sprintf("%010d", attempt)
	if len(key) > 255 {
		return "", fmt.Errorf("translation usage idempotency key is too long")
	}
	return key, nil
}

func translationAttemptFromUsageKey(requestKey string, usageKey string) (int, error) {
	prefix := requestKey + ":attempt:"
	if !strings.HasPrefix(usageKey, prefix) {
		return 0, fmt.Errorf("translation usage key has the wrong request prefix")
	}
	rawAttempt := strings.TrimPrefix(usageKey, prefix)
	if len(rawAttempt) != 10 {
		return 0, fmt.Errorf("translation usage key has an invalid attempt")
	}
	attempt, err := strconv.Atoi(rawAttempt)
	if err != nil || attempt < 1 || attempt > maxTranslationRequestAttempt {
		return 0, fmt.Errorf("translation usage key has an invalid attempt")
	}
	return attempt, nil
}

func isTranslationRequestKeyChar(char rune) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') || char == ':' || char == '-' || char == '.'
}

func isLowerHexChar(char rune) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')
}

func validateTranslationClaimInput(
	requestKey, fingerprint string,
	record *UsageRecord,
	staleAfter, retention time.Duration,
) error {
	if strings.TrimSpace(requestKey) != requestKey ||
		!strings.HasPrefix(requestKey, "ws-translation:") || len(requestKey) > 220 {
		return fmt.Errorf("invalid translation request key")
	}
	for _, char := range requestKey {
		if !isTranslationRequestKeyChar(char) {
			return fmt.Errorf("invalid translation request key")
		}
	}
	if len(fingerprint) != 64 {
		return fmt.Errorf("invalid translation request fingerprint")
	}
	for _, char := range fingerprint {
		if !isLowerHexChar(char) {
			return fmt.Errorf("invalid translation request fingerprint")
		}
	}
	if record == nil {
		return fmt.Errorf("translation usage record is required")
	}
	record.UserID = strings.TrimSpace(record.UserID)
	record.TenantID = strings.TrimSpace(record.TenantID)
	record.Action = strings.TrimSpace(record.Action)
	if record.UserID == "" || record.TenantID == "" || record.Action != "translation" {
		return fmt.Errorf("translation request requires a translation usage owner")
	}
	if staleAfter < time.Minute || retention < staleAfter {
		return fmt.Errorf("invalid translation replay time budget")
	}
	return nil
}

func cleanupTranslationRequestsTx(ctx context.Context, tx *sql.Tx, now time.Time) error {
	_, err := tx.ExecContext(ctx, `
		WITH expired AS (
			SELECT request_key
			FROM translation_request_results
			WHERE state IN ('completed', 'failed', 'expired') AND expires_at <= $1
			ORDER BY expires_at, request_key
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		DELETE FROM translation_request_results AS requests
		USING expired
		WHERE requests.request_key = expired.request_key
	`, now, translationRequestCleanupBatch)
	return err
}

type translationUsageHistory struct {
	attempt  int
	usageKey string
	refunded bool
	found    bool
}

func latestTranslationUsageTx(ctx context.Context, tx *sql.Tx, requestKey string, record *UsageRecord) (translationUsageHistory, error) {
	var (
		history                  translationUsageHistory
		refundedAt               sql.NullTime
		userID, tenantID, action string
	)
	prefix := requestKey + ":attempt:"
	err := tx.QueryRowContext(ctx, `
		SELECT idempotency_key, refunded_at, user_id, tenant_id, action
		FROM usage_logs
		WHERE idempotency_key LIKE $1 AND idempotency_key LIKE 'ws-translation:%:attempt:%'
		ORDER BY idempotency_key DESC
		LIMIT 1
	`, prefix+"%").Scan(&history.usageKey, &refundedAt, &userID, &tenantID, &action)
	if errors.Is(err, sql.ErrNoRows) {
		return history, nil
	}
	if err != nil {
		return history, err
	}
	if userID != record.UserID || tenantID != record.TenantID || action != "translation" {
		return history, ErrTranslationRequestConflict
	}
	history.attempt, err = translationAttemptFromUsageKey(requestKey, history.usageKey)
	if err != nil {
		return history, err
	}
	history.refunded = refundedAt.Valid
	history.found = true
	return history, nil
}

func normalizedSessionID(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// ClaimTranslationRequest serializes provider ownership across processes.
// A stale reservation is refunded inside the same transaction before a new,
// independently idempotent attempt is issued.
//
//nolint:gocyclo // Transactional state machine stays linear for atomic auditability.
func (s *Service) ClaimTranslationRequest(
	ctx context.Context,
	requestKey, fingerprint string,
	record *UsageRecord,
	staleAfter, retention time.Duration,
) (*TranslationRequestClaim, error) {
	if err := validateTranslationClaimInput(requestKey, fingerprint, record, staleAfter, retention); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()
	if err := cleanupTranslationRequestsTx(ctx, tx, now); err != nil {
		return nil, err
	}
	history, err := latestTranslationUsageTx(ctx, tx, requestKey, record)
	if err != nil {
		return nil, err
	}
	firstAttempt := 1
	firstState := "reserved"
	if history.found {
		firstAttempt = history.attempt
		if history.refunded {
			if firstAttempt == maxTranslationRequestAttempt {
				return nil, fmt.Errorf("translation request retry limit reached")
			}
			firstAttempt++
		} else {
			// The replay payload aged out, but the durable accounting row proves
			// this request was already paid. Recreate only a bounded tombstone.
			firstState = "expired"
		}
	}
	firstUsageKey, err := translationAttemptUsageKey(requestKey, firstAttempt)
	if err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO translation_request_results (
			request_key, tenant_id, user_id, session_id, request_fingerprint,
			state, attempt, usage_idempotency_key, started_at, expires_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $9)
		ON CONFLICT (request_key) DO NOTHING
	`, requestKey, record.TenantID, record.UserID, record.SessionID, fingerprint,
		firstState, firstAttempt, firstUsageKey, now, now.Add(retention))
	if err != nil {
		return nil, err
	}
	if inserted, rowsErr := result.RowsAffected(); rowsErr != nil {
		return nil, rowsErr
	} else if inserted == 1 {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if firstState == "expired" {
			return &TranslationRequestClaim{Disposition: TranslationRequestExpired, Attempt: firstAttempt}, nil
		}
		return &TranslationRequestClaim{
			Disposition: TranslationRequestOwner, Attempt: firstAttempt, UsageIdempotencyKey: firstUsageKey,
		}, nil
	}

	var (
		storedTenantID, storedUserID, storedFingerprint string
		storedSessionID                                 sql.NullString
		state, usageKey                                 string
		attempt                                         int
		content, model                                  sql.NullString
		latencyMs                                       sql.NullInt64
		startedAt, expiresAt                            time.Time
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT tenant_id, user_id, session_id, request_fingerprint, state,
		       attempt, usage_idempotency_key, content, model, latency_ms,
		       started_at, expires_at
		FROM translation_request_results
		WHERE request_key = $1
		FOR UPDATE
	`, requestKey).Scan(&storedTenantID, &storedUserID, &storedSessionID, &storedFingerprint,
		&state, &attempt, &usageKey, &content, &model, &latencyMs, &startedAt, &expiresAt); err != nil {
		return nil, err
	}
	if storedTenantID != record.TenantID || storedUserID != record.UserID ||
		storedSessionID.String != normalizedSessionID(record.SessionID) || storedFingerprint != fingerprint {
		return nil, ErrTranslationRequestConflict
	}

	switch state {
	case "completed":
		if !expiresAt.After(now) {
			if _, err := tx.ExecContext(ctx, `
				UPDATE translation_request_results
				SET state = 'expired', content = NULL, model = NULL, latency_ms = NULL,
				    completed_at = NULL, expires_at = $3, updated_at = $2
				WHERE request_key = $1
			`, requestKey, now, now.Add(retention)); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &TranslationRequestClaim{Disposition: TranslationRequestExpired, Attempt: attempt}, nil
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &TranslationRequestClaim{
			Disposition: TranslationRequestReplay, Attempt: attempt,
			Result: TranslationReplayResult{Content: content.String, Model: model.String, LatencyMs: latencyMs.Int64},
		}, nil
	case "expired":
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &TranslationRequestClaim{Disposition: TranslationRequestExpired, Attempt: attempt}, nil
	case "reserved":
		if now.Sub(startedAt) < staleAfter {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &TranslationRequestClaim{
				Disposition: TranslationRequestProcessing, Attempt: attempt, UsageIdempotencyKey: usageKey,
			}, nil
		}
	case "failed":
		// The failing owner already refunded its usage. Keep the marker long
		// enough to advance the next attempt instead of reusing its key.
	default:
		return nil, fmt.Errorf("unsupported translation request state %q", state)
	}

	if state == "reserved" {
		if err := refundTranslationUsageIfExistsTx(ctx, tx, usageKey, record.UserID,
			"stale real-time translation reservation recovered"); err != nil {
			return nil, err
		}
	}
	if attempt == maxTranslationRequestAttempt {
		return nil, fmt.Errorf("translation request retry limit reached")
	}
	attempt++
	nextUsageKey, err := translationAttemptUsageKey(requestKey, attempt)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE translation_request_results
		SET state = 'reserved', attempt = $2, usage_idempotency_key = $3,
		    content = NULL, model = NULL, latency_ms = NULL,
		    started_at = $4, completed_at = NULL, expires_at = $5, updated_at = $4
		WHERE request_key = $1
	`, requestKey, attempt, nextUsageKey, now, now.Add(retention)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TranslationRequestClaim{
		Disposition: TranslationRequestOwner, Attempt: attempt, UsageIdempotencyKey: nextUsageKey,
	}, nil
}

func validateTranslationReplayResult(result *TranslationReplayResult) error {
	if result == nil || strings.TrimSpace(result.Content) == "" {
		return fmt.Errorf("translation replay content is required")
	}
	if len(result.Model) > 200 || result.LatencyMs < 0 {
		return fmt.Errorf("invalid translation replay metadata")
	}
	return nil
}

// SettleTranslationRequest stores the replay result in the exact transaction
// that reconciles the reservation. The result has been delivered, so a
// shortfall is absorbed rather than failing the settlement.
func (s *Service) SettleTranslationRequest(
	ctx context.Context,
	requestKey string,
	attempt int,
	usageKey string,
	actual *UsageRecord,
	replay *TranslationReplayResult,
	retention time.Duration,
) (float64, error) {
	if actual == nil || strings.TrimSpace(actual.Action) != "translation" {
		return 0, fmt.Errorf("translation settlement owner is invalid")
	}
	if err := validateTranslationReplayResult(replay); err != nil {
		return 0, err
	}
	if retention < time.Minute {
		return 0, fmt.Errorf("invalid translation replay retention")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		state, storedTenantID, storedUserID, storedUsageKey string
		storedAttempt                                       int
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT state, attempt, usage_idempotency_key, tenant_id, user_id
		FROM translation_request_results
		WHERE request_key = $1
		FOR UPDATE
	`, requestKey).Scan(&state, &storedAttempt, &storedUsageKey, &storedTenantID, &storedUserID); err != nil {
		return 0, err
	}
	if storedAttempt != attempt || storedUsageKey != usageKey ||
		storedTenantID != strings.TrimSpace(actual.TenantID) || storedUserID != strings.TrimSpace(actual.UserID) {
		return 0, ErrTranslationRequestConflict
	}
	if state == "completed" {
		var charge float64
		if err := tx.QueryRowContext(ctx, `SELECT charge_usd FROM usage_logs WHERE idempotency_key = $1`, usageKey).Scan(&charge); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return charge, nil
	}
	if state != "reserved" {
		return 0, fmt.Errorf("translation request is not reserved")
	}
	actual.AbsorbSettlementShortfall = true
	charge, err := settleUsageTx(ctx, tx, usageKey, actual)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
		UPDATE translation_request_results
		SET state = 'completed', content = $2, model = $3, latency_ms = $4,
		    completed_at = $5, expires_at = $6, updated_at = $5
		WHERE request_key = $1
	`, requestKey, strings.TrimSpace(replay.Content), strings.TrimSpace(replay.Model),
		replay.LatencyMs, now, now.Add(retention)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return charge, nil
}

// CancelTranslationRequest removes an uncharged owner claim. If a usage row
// exists (including an ambiguous commit), the reservation is retained for the
// stale-recovery path instead of risking a second debit.
func (s *Service) CancelTranslationRequest(ctx context.Context, requestKey string, attempt int, usageKey string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state, storedUsageKey string
	var storedAttempt int
	if err := tx.QueryRowContext(ctx, `
		SELECT state, attempt, usage_idempotency_key
		FROM translation_request_results WHERE request_key = $1 FOR UPDATE
	`, requestKey).Scan(&state, &storedAttempt, &storedUsageKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return tx.Commit()
		}
		return err
	}
	if state != "reserved" || storedAttempt != attempt || storedUsageKey != usageKey {
		return tx.Commit()
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM usage_logs WHERE idempotency_key = $1)`, usageKey).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `DELETE FROM translation_request_results WHERE request_key = $1`, requestKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FailTranslationRequest atomically refunds a failed provider attempt and
// retains a bounded marker so a deliberate retry receives a fresh key.
func (s *Service) FailTranslationRequest(ctx context.Context, requestKey string, attempt int, usageKey, description string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state, storedUsageKey, userID string
	var storedAttempt int
	if err := tx.QueryRowContext(ctx, `
		SELECT state, attempt, usage_idempotency_key, user_id
		FROM translation_request_results WHERE request_key = $1 FOR UPDATE
	`, requestKey).Scan(&state, &storedAttempt, &storedUsageKey, &userID); err != nil {
		return err
	}
	if state != "reserved" || storedAttempt != attempt || storedUsageKey != usageKey {
		return ErrTranslationRequestConflict
	}
	if err := refundTranslationUsageIfExistsTx(ctx, tx, usageKey, userID, description); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE translation_request_results
		SET state = 'failed', content = NULL, model = NULL, latency_ms = NULL,
		    completed_at = NULL, updated_at = NOW()
		WHERE request_key = $1
	`, requestKey); err != nil {
		return err
	}
	return tx.Commit()
}

func refundTranslationUsageIfExistsTx(ctx context.Context, tx *sql.Tx, usageKey, expectedUserID, description string) error {
	err := refundUsageByKeyTx(ctx, tx, usageKey, expectedUserID, description)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
