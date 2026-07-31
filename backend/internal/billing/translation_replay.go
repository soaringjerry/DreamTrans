package billing

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

// TranslationRequestDisposition describes whether the caller owns new
// provider work, should wait for an existing owner, or can replay a completed
// result without touching provider quota or billing again.
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

// TranslationReplayResult is the minimum provider result required to satisfy a
// reconnect. Source text and timestamps come from the fingerprint-verified
// retry, avoiding an unnecessary second copy of the transcript in the billing
// database.
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
	return (char >= 'a' && char <= 'z') ||
		(char >= 'A' && char <= 'Z') ||
		(char >= '0' && char <= '9') ||
		char == ':' || char == '-' || char == '.'
}

func isLowerHexChar(char rune) bool {
	return (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')
}

func validateTranslationClaimInput(
	requestKey string,
	fingerprint string,
	record *UsageRecord,
	staleAfter time.Duration,
	retention time.Duration,
) error {
	if strings.TrimSpace(requestKey) != requestKey ||
		!strings.HasPrefix(requestKey, "ws-translation:") ||
		len(requestKey) > 220 {
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
			WHERE state IN ('completed', 'failed', 'expired')
			  AND expires_at <= $1
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

func latestTranslationUsageTx(
	ctx context.Context,
	tx *sql.Tx,
	requestKey string,
	record *UsageRecord,
) (translationUsageHistory, error) {
	var (
		history                  translationUsageHistory
		refundedAt               sql.NullTime
		userID, tenantID, action string
	)
	prefix := requestKey + ":attempt:"
	err := tx.QueryRowContext(ctx, `
		SELECT idempotency_key, refunded_at, user_id, tenant_id, action
		FROM usage_logs
		WHERE idempotency_key LIKE $1
		  AND idempotency_key LIKE 'ws-translation:%:attempt:%'
		ORDER BY idempotency_key DESC
		LIMIT 1
	`, prefix+"%").Scan(
		&history.usageKey,
		&refundedAt,
		&userID,
		&tenantID,
		&action,
	)
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
	requestKey string,
	fingerprint string,
	record *UsageRecord,
	staleAfter time.Duration,
	retention time.Duration,
) (*TranslationRequestClaim, error) {
	if err := validateTranslationClaimInput(
		requestKey,
		fingerprint,
		record,
		staleAfter,
		retention,
	); err != nil {
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
			// The replay payload has aged out, but its durable accounting row
			// proves that this request was already paid. Recreate only a bounded
			// tombstone, never provider ownership or a second debit.
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
			return &TranslationRequestClaim{
				Disposition: TranslationRequestExpired,
				Attempt:     firstAttempt,
			}, nil
		}
		return &TranslationRequestClaim{
			Disposition:         TranslationRequestOwner,
			Attempt:             firstAttempt,
			UsageIdempotencyKey: firstUsageKey,
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
	`, requestKey).Scan(
		&storedTenantID,
		&storedUserID,
		&storedSessionID,
		&storedFingerprint,
		&state,
		&attempt,
		&usageKey,
		&content,
		&model,
		&latencyMs,
		&startedAt,
		&expiresAt,
	); err != nil {
		return nil, err
	}
	if storedTenantID != record.TenantID ||
		storedUserID != record.UserID ||
		storedSessionID.String != normalizedSessionID(record.SessionID) ||
		storedFingerprint != fingerprint {
		return nil, ErrTranslationRequestConflict
	}

	switch state {
	case "completed":
		if !expiresAt.After(now) {
			if _, err := tx.ExecContext(ctx, `
					UPDATE translation_request_results
					SET state = 'expired', content = NULL, model = NULL,
					    latency_ms = NULL, completed_at = NULL,
					    expires_at = $3, updated_at = $2
					WHERE request_key = $1
				`, requestKey, now, now.Add(retention)); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &TranslationRequestClaim{
				Disposition: TranslationRequestExpired,
				Attempt:     attempt,
			}, nil
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &TranslationRequestClaim{
			Disposition: TranslationRequestReplay,
			Attempt:     attempt,
			Result: TranslationReplayResult{
				Content:   content.String,
				Model:     model.String,
				LatencyMs: latencyMs.Int64,
			},
		}, nil
	case "expired":
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &TranslationRequestClaim{
			Disposition: TranslationRequestExpired,
			Attempt:     attempt,
		}, nil
	case "reserved":
		if now.Sub(startedAt) < staleAfter {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return &TranslationRequestClaim{
				Disposition:         TranslationRequestProcessing,
				Attempt:             attempt,
				UsageIdempotencyKey: usageKey,
			}, nil
		}
	case "failed":
		// The failing owner already refunded its usage in the same transaction
		// that recorded this state. Keep the marker long enough to advance the
		// next attempt instead of reusing a refunded idempotency key.
	default:
		return nil, fmt.Errorf("unsupported translation request state %q", state)
	}

	if state == "reserved" {
		if err := refundTranslationUsageIfExistsTx(
			ctx,
			tx,
			usageKey,
			record.UserID,
			"stale real-time translation reservation recovered",
		); err != nil {
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
		    started_at = $4, completed_at = NULL, expires_at = $5,
		    updated_at = $4
		WHERE request_key = $1
	`, requestKey, attempt, nextUsageKey, now, now.Add(retention)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &TranslationRequestClaim{
		Disposition:         TranslationRequestOwner,
		Attempt:             attempt,
		UsageIdempotencyKey: nextUsageKey,
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

func validateTranslationSettlementRecord(actual *UsageRecord) error {
	if actual == nil {
		return fmt.Errorf("actual usage is required for settlement")
	}
	actual.UserID = strings.TrimSpace(actual.UserID)
	actual.TenantID = strings.TrimSpace(actual.TenantID)
	actual.Action = strings.TrimSpace(actual.Action)
	actual.Provider = strings.ToLower(strings.TrimSpace(actual.Provider))
	actual.Model = strings.TrimSpace(actual.Model)
	if actual.UserID == "" || actual.TenantID == "" || actual.Action != "translation" {
		return fmt.Errorf("translation settlement owner is invalid")
	}
	if actual.Quantity < 0 || actual.Quantity > maxUsageQuantity ||
		math.IsNaN(actual.Quantity) || math.IsInf(actual.Quantity, 0) ||
		actual.InputTokens < 0 || actual.InputTokens > maxDatabaseTokenCount ||
		actual.CachedInputTokens < 0 ||
		actual.CachedInputTokens > actual.InputTokens ||
		actual.CacheWriteTokens < 0 ||
		actual.CacheWriteTokens > actual.InputTokens ||
		actual.CachedInputTokens > actual.InputTokens-actual.CacheWriteTokens ||
		actual.OutputTokens < 0 || actual.OutputTokens > maxDatabaseTokenCount ||
		len(actual.Provider) > 60 || len(actual.Model) > 200 {
		return fmt.Errorf("translation settlement usage is invalid")
	}
	return nil
}

// SettleTranslationRequest stores the replay result in the exact transaction
// that reconciles the reservation. Once the provider succeeds, a reconnect can
// therefore either replay the result or observe no committed settlement.
//
//nolint:gocyclo // Transactional state machine stays linear for atomic auditability.
func (s *Service) SettleTranslationRequest(
	ctx context.Context,
	requestKey string,
	attempt int,
	usageKey string,
	actual *UsageRecord,
	replay *TranslationReplayResult,
	retention time.Duration,
) (float64, error) {
	if err := validateTranslationSettlementRecord(actual); err != nil {
		return 0, err
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
	`, requestKey).Scan(
		&state,
		&storedAttempt,
		&storedUsageKey,
		&storedTenantID,
		&storedUserID,
	); err != nil {
		return 0, err
	}
	if storedAttempt != attempt || storedUsageKey != usageKey ||
		storedTenantID != actual.TenantID || storedUserID != actual.UserID {
		return 0, ErrTranslationRequestConflict
	}
	if state == "completed" {
		var cost float64
		if err := tx.QueryRowContext(ctx, `
			SELECT cost FROM usage_logs WHERE idempotency_key = $1
		`, usageKey).Scan(&cost); err != nil {
			return 0, err
		}
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return cost, nil
	}
	if state != "reserved" {
		return 0, fmt.Errorf("translation request is not reserved")
	}

	var currentBalance float64
	var userTenantID string
	if err := tx.QueryRowContext(ctx, `
		SELECT dreampoints, tenant_id
		FROM users
		WHERE id = $1
		FOR UPDATE
	`, actual.UserID).Scan(&currentBalance, &userTenantID); err != nil {
		return 0, err
	}
	if userTenantID != actual.TenantID {
		return 0, fmt.Errorf("usage tenant does not match user tenant")
	}

	var (
		usageID                          string
		reservedUserID, reservedTenantID string
		reservedAction                   string
		reservedModel                    string
		reservedAttribution              string
		reservedCost                     float64
		reservedUpstream, reservedFee    float64
		reservedSnapshot                 []byte
		refundedAt                       sql.NullTime
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT id, user_id, tenant_id, action, COALESCE(model, ''),
		       cost_attribution, cost,
		       upstream_cost_usd, service_fee_dp, pricing_snapshot, refunded_at
		FROM usage_logs
		WHERE idempotency_key = $1
		FOR UPDATE
	`, usageKey).Scan(
		&usageID,
		&reservedUserID,
		&reservedTenantID,
		&reservedAction,
		&reservedModel,
		&reservedAttribution,
		&reservedCost,
		&reservedUpstream,
		&reservedFee,
		&reservedSnapshot,
		&refundedAt,
	); err != nil {
		return 0, err
	}
	if refundedAt.Valid {
		return 0, fmt.Errorf("translation usage reservation was already refunded")
	}
	if reservedUserID != actual.UserID ||
		reservedTenantID != actual.TenantID ||
		reservedAction != actual.Action {
		return 0, fmt.Errorf("translation settlement does not match reservation")
	}
	if (reservedAttribution == AttributionBYOK) != actual.CustomerFunded {
		return 0, fmt.Errorf(
			"translation settlement funding source does not match reservation",
		)
	}
	actual.Model = reservedModel
	breakdown, snapshotErr := resolveUsageCostFromSnapshot(
		reservedSnapshot,
		actual,
		reservedAttribution,
	)
	if snapshotErr != nil {
		if !errors.Is(snapshotErr, ErrPricingSnapshotIncomplete) {
			return 0, snapshotErr
		}
		breakdown = usageCostBreakdown{
			ChargeDP: reservedCost, UpstreamCostUSD: reservedUpstream,
			ServiceFeeDP: reservedFee, PricingSnapshot: reservedSnapshot,
			Attribution: reservedAttribution,
		}
	}
	actualCost := breakdown.ChargeDP
	if actualCost < 0 ||
		actualCost >= maxStoredUsageCost ||
		math.IsNaN(actualCost) ||
		math.IsInf(actualCost, 0) {
		return 0, fmt.Errorf("translation settlement calculated an invalid cost")
	}
	enabled, policySnapshotted := billingPolicyFromPricingSnapshot(
		reservedSnapshot,
	)
	if !policySnapshotted {
		currentEnabled, settingErr := boolSettingTx(
			ctx,
			tx,
			"billing_enabled",
			true,
		)
		if settingErr != nil {
			return 0, settingErr
		}
		enabled = currentEnabled
	}
	if !enabled {
		breakdown.ServiceFeeDP -= actualCost
		actualCost = 0
	}
	delta := actualCost - reservedCost
	if delta > 0 {
		allowNegative, policySnapshotted :=
			negativeBalancePolicyFromPricingSnapshot(reservedSnapshot)
		var allowErr error
		if !policySnapshotted {
			allowNegative, allowErr = boolSettingTx(
				ctx,
				tx,
				"allow_negative_balance",
				false,
			)
		}
		if allowErr != nil || (!allowNegative && currentBalance < delta) {
			// The reservation is deliberately conservative. If an exceptional
			// response exceeds it, deliver at the already collected reservation
			// rather than charging without returning the successful result.
			if allowErr != nil {
				log.Printf("translation settlement balance policy fallback: %v", allowErr)
			}
			breakdown.ServiceFeeDP += reservedCost - actualCost
			actualCost = reservedCost
			delta = 0
		}
	}
	breakdown.ChargeDP = actualCost
	if err := validateUsageCostBreakdown(breakdown); err != nil {
		return 0, fmt.Errorf("translation settlement pricing: %w", err)
	}
	breakdown.PricingSnapshot = annotatePricingSnapshot(
		breakdown.PricingSnapshot,
		actualCost,
	)

	newBalance := currentBalance - delta
	if delta != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users
			SET dreampoints = $1,
			    dreampoints_used = GREATEST(0, dreampoints_used + $2)
			WHERE id = $3
		`, newBalance, delta, actual.UserID); err != nil {
			return 0, err
		}
		transactionType := "debit"
		if delta < 0 {
			transactionType = "refund"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO balance_transactions
				(user_id, amount, balance_after, transaction_type,
				 reference_type, reference_id, description)
			VALUES ($1, $2, $3, $4, 'usage', $5, $6)
		`, actual.UserID, -delta, newBalance, transactionType, usageID,
			actual.Action+" usage settlement"); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE usage_logs
		SET quantity = $1, session_id = $2, model = $3,
		    input_tokens = $4, cached_input_tokens = $5,
		    cache_write_tokens = $6, output_tokens = $7, cost = $8,
		    upstream_cost_usd = $9, service_fee_dp = $10,
		    pricing_snapshot = $11, cost_attribution = $12,
		    settled_at = NOW()
		WHERE id = $13
	`, actual.Quantity, actual.SessionID, actual.Model, actual.InputTokens,
		actual.CachedInputTokens, actual.CacheWriteTokens, actual.OutputTokens,
		actualCost, breakdown.UpstreamCostUSD, breakdown.ServiceFeeDP,
		breakdown.PricingSnapshot, breakdown.Attribution, usageID); err != nil {
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
	return actualCost, nil
}

// CancelTranslationRequest removes an uncharged owner claim. If a usage row
// exists (including an ambiguous commit), the reservation is retained for the
// stale-recovery path instead of risking a second debit.
func (s *Service) CancelTranslationRequest(
	ctx context.Context,
	requestKey string,
	attempt int,
	usageKey string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state, storedUsageKey string
	var storedAttempt int
	if err := tx.QueryRowContext(ctx, `
		SELECT state, attempt, usage_idempotency_key
		FROM translation_request_results
		WHERE request_key = $1
		FOR UPDATE
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
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM usage_logs WHERE idempotency_key = $1
		)
	`, usageKey).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM translation_request_results WHERE request_key = $1
		`, requestKey); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// FailTranslationRequest atomically refunds a failed provider attempt and
// retains a bounded marker so a deliberate retry receives a fresh,
// monotonically increasing accounting key.
func (s *Service) FailTranslationRequest(
	ctx context.Context,
	requestKey string,
	attempt int,
	usageKey string,
	description string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var state, storedUsageKey, userID string
	var storedAttempt int
	if err := tx.QueryRowContext(ctx, `
		SELECT state, attempt, usage_idempotency_key, user_id
		FROM translation_request_results
		WHERE request_key = $1
		FOR UPDATE
	`, requestKey).Scan(&state, &storedAttempt, &storedUsageKey, &userID); err != nil {
		return err
	}
	if state != "reserved" || storedAttempt != attempt || storedUsageKey != usageKey {
		return ErrTranslationRequestConflict
	}
	if err := refundTranslationUsageIfExistsTx(
		ctx,
		tx,
		usageKey,
		userID,
		description,
	); err != nil {
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

func refundTranslationUsageIfExistsTx(
	ctx context.Context,
	tx *sql.Tx,
	usageKey string,
	expectedUserID string,
	description string,
) error {
	var lockedUserID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM users WHERE id = $1 FOR UPDATE
	`, expectedUserID).Scan(&lockedUserID); err != nil {
		return err
	}
	var usageID, userID string
	var cost float64
	var refundedAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT id, user_id, cost, refunded_at
		FROM usage_logs
		WHERE idempotency_key = $1
		FOR UPDATE
	`, usageKey).Scan(&usageID, &userID, &cost, &refundedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if userID != expectedUserID || lockedUserID != expectedUserID {
		return fmt.Errorf("translation usage reservation owner changed")
	}
	if refundedAt.Valid {
		return nil
	}
	if cost > 0 {
		var newBalance float64
		if err := tx.QueryRowContext(ctx, `
			UPDATE users
			SET dreampoints = dreampoints + $1,
			    dreampoints_used = GREATEST(0, dreampoints_used - $1)
			WHERE id = $2
			RETURNING dreampoints
		`, cost, userID).Scan(&newBalance); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO balance_transactions
				(user_id, amount, balance_after, transaction_type,
				 reference_type, reference_id, description)
			VALUES ($1, $2, $3, 'refund', 'usage', $4, $5)
		`, userID, cost, newBalance, usageID, description); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE usage_logs
		SET cost = 0, quantity = 0, refunded_at = NOW()
		WHERE id = $1
	`, usageID)
	return err
}
