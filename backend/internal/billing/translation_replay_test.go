package billing

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

func TestTranslationAttemptUsageKeysSortAndRoundTrip(t *testing.T) {
	const requestKey = "ws-translation:unit"
	previous := ""
	for _, attempt := range []int{1, 2, 9, 10, 99, 100, 1_000_000} {
		key, err := translationAttemptUsageKey(requestKey, attempt)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if previous != "" && key <= previous {
			t.Fatalf("attempt key %q does not sort after %q", key, previous)
		}
		parsed, err := translationAttemptFromUsageKey(requestKey, key)
		if err != nil {
			t.Fatalf("parse attempt %d: %v", attempt, err)
		}
		if parsed != attempt {
			t.Fatalf("parsed attempt = %d, want %d", parsed, attempt)
		}
		previous = key
	}
}

func TestValidateTranslationClaimRejectsWildcardRequestKeys(t *testing.T) {
	record := &UsageRecord{
		UserID: "user", TenantID: "tenant", Action: "translation",
	}
	for _, requestKey := range []string{
		"other:request",
		"ws-translation:percent%",
		"ws-translation:underscore_",
	} {
		if err := validateTranslationClaimInput(
			requestKey,
			strings.Repeat("a", 64),
			record,
			time.Minute,
			2*time.Minute,
		); err == nil {
			t.Fatalf("unsafe request key %q was accepted", requestKey)
		}
	}
}

// This path depends on PostgreSQL row locks and transaction atomicity, so it
// intentionally shares the repository's opt-in disposable database contract.
func TestTranslationReplayFailureCleanupAndAtomicSettlement(t *testing.T) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	var replayTable sql.NullString
	if err := db.QueryRowContext(
		t.Context(),
		`SELECT to_regclass('public.translation_request_results')::text`,
	).Scan(&replayTable); err != nil {
		t.Fatal(err)
	}
	if !replayTable.Valid {
		t.Fatal("translation_request_results migration is not installed")
	}

	var tenantID, userID string
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO tenants
			(name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
		VALUES (
			'Translation Replay Integration',
			'tri-' || gen_random_uuid(),
			'free',
			-1,
			-1,
			10
		)
		RETURNING id
	`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO users
			(tenant_id, email, password_hash, name, role, is_active, email_verified, dreampoints)
		VALUES (
			$1,
			gen_random_uuid() || '@example.invalid',
			'integration-only',
			'Translation Replay User',
			'user',
			true,
			true,
			1000
		)
		RETURNING id
	`, tenantID).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	service := NewService(db)
	requestKey := "ws-translation:integration-" + strings.ReplaceAll(tenantID, "-", "")
	fingerprint := strings.Repeat("a", 64)
	owner := &UsageRecord{
		UserID: userID, TenantID: tenantID, Action: "translation",
	}
	claim1, err := service.ClaimTranslationRequest(
		t.Context(), requestKey, fingerprint, owner, time.Minute, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim1.Disposition != TranslationRequestOwner || claim1.Attempt != 1 {
		t.Fatalf("first claim = %#v, want owner attempt 1", claim1)
	}
	if _, err := service.RecordUsage(t.Context(), &UsageRecord{
		UserID: userID, TenantID: tenantID, Action: "translation",
		Model: "gpt-4o-mini", InputTokens: 10, OutputTokens: 10,
		IdempotencyKey: claim1.UsageIdempotencyKey,
	}); err != nil {
		t.Fatalf("reserve first attempt: %v", err)
	}
	if err := service.FailTranslationRequest(
		t.Context(),
		requestKey,
		claim1.Attempt,
		claim1.UsageIdempotencyKey,
		"integration provider failure",
	); err != nil {
		t.Fatalf("fail first attempt: %v", err)
	}
	var failedState string
	var refundedAt sql.NullTime
	if err := db.QueryRowContext(t.Context(), `
		SELECT request.state, usage.refunded_at
		FROM translation_request_results AS request
		JOIN usage_logs AS usage
		  ON usage.idempotency_key = request.usage_idempotency_key
		WHERE request.request_key = $1
	`, requestKey).Scan(&failedState, &refundedAt); err != nil {
		t.Fatal(err)
	}
	if failedState != "failed" || !refundedAt.Valid {
		t.Fatalf("failed state/refund = %q/%v", failedState, refundedAt.Valid)
	}

	// Simulate bounded lazy cleanup before the retry. The accounting history
	// must still advance to attempt 2 instead of colliding with refunded attempt 1.
	if _, err := db.ExecContext(t.Context(), `
		UPDATE translation_request_results
		SET expires_at = NOW() - INTERVAL '1 second'
		WHERE request_key = $1
	`, requestKey); err != nil {
		t.Fatal(err)
	}
	claim2, err := service.ClaimTranslationRequest(
		t.Context(), requestKey, fingerprint, owner, time.Minute, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if claim2.Disposition != TranslationRequestOwner || claim2.Attempt != 2 ||
		claim2.UsageIdempotencyKey == claim1.UsageIdempotencyKey {
		t.Fatalf("post-cleanup retry claim = %#v, want fresh attempt 2", claim2)
	}
	if _, err := service.RecordUsage(t.Context(), &UsageRecord{
		UserID: userID, TenantID: tenantID, Action: "translation",
		Model: "gpt-4o-mini", InputTokens: 10, OutputTokens: 10,
		IdempotencyKey: claim2.UsageIdempotencyKey,
	}); err != nil {
		t.Fatalf("reserve second attempt: %v", err)
	}
	var balanceBeforeFailedSettlement float64
	if err := db.QueryRowContext(t.Context(), `
		SELECT dreampoints FROM users WHERE id = $1
	`, userID).Scan(&balanceBeforeFailedSettlement); err != nil {
		t.Fatal(err)
	}
	dropRejectTrigger := func(ctx context.Context) {
		_, _ = db.ExecContext(ctx, `
			DROP TRIGGER IF EXISTS translation_replay_test_reject_completion
				ON translation_request_results;
			DROP FUNCTION IF EXISTS translation_replay_test_reject_completion_fn();
		`)
	}
	dropRejectTrigger(t.Context())
	if _, err := db.ExecContext(t.Context(), `
		CREATE FUNCTION translation_replay_test_reject_completion_fn()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $$
		BEGIN
			IF NEW.state = 'completed' THEN
				RAISE EXCEPTION 'intentional replay persistence failure';
			END IF;
			RETURN NEW;
		END;
		$$;
		CREATE TRIGGER translation_replay_test_reject_completion
		BEFORE UPDATE ON translation_request_results
		FOR EACH ROW
		EXECUTE FUNCTION translation_replay_test_reject_completion_fn();
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dropRejectTrigger(context.Background())
	})
	if _, err := service.SettleTranslationRequest(
		t.Context(),
		requestKey,
		claim2.Attempt,
		claim2.UsageIdempotencyKey,
		&UsageRecord{
			UserID: userID, TenantID: tenantID, Action: "translation",
			Model: "gpt-4o-mini", InputTokens: 8, OutputTokens: 8,
		},
		&TranslationReplayResult{
			Content: "durable translation", Model: "gpt-4o-mini", LatencyMs: 42,
		},
		5*time.Minute,
	); err == nil {
		t.Fatal("forced replay persistence failure unexpectedly committed")
	}
	var (
		stateAfterFailedSettlement                     string
		inputAfterFailedSettlement, outputAfterFailure int
		balanceAfterFailedSettlement                   float64
	)
	if err := db.QueryRowContext(t.Context(), `
		SELECT request.state, usage.input_tokens, usage.output_tokens, users.dreampoints
		FROM translation_request_results AS request
		JOIN usage_logs AS usage
		  ON usage.idempotency_key = request.usage_idempotency_key
		JOIN users ON users.id = request.user_id
		WHERE request.request_key = $1
	`, requestKey).Scan(
		&stateAfterFailedSettlement,
		&inputAfterFailedSettlement,
		&outputAfterFailure,
		&balanceAfterFailedSettlement,
	); err != nil {
		t.Fatal(err)
	}
	if stateAfterFailedSettlement != "reserved" ||
		inputAfterFailedSettlement != 10 ||
		outputAfterFailure != 10 ||
		balanceAfterFailedSettlement != balanceBeforeFailedSettlement {
		t.Fatalf(
			"failed replay write partially settled billing: state=%q tokens=%d/%d balance=%v want=%v",
			stateAfterFailedSettlement,
			inputAfterFailedSettlement,
			outputAfterFailure,
			balanceAfterFailedSettlement,
			balanceBeforeFailedSettlement,
		)
	}
	dropRejectTrigger(t.Context())
	if _, err := service.SettleTranslationRequest(
		t.Context(),
		requestKey,
		claim2.Attempt,
		claim2.UsageIdempotencyKey,
		&UsageRecord{
			UserID: userID, TenantID: tenantID, Action: "translation",
			Model: "gpt-4o-mini", InputTokens: 8, OutputTokens: 8,
		},
		&TranslationReplayResult{
			Content: "durable translation", Model: "gpt-4o-mini", LatencyMs: 42,
		},
		5*time.Minute,
	); err != nil {
		t.Fatalf("settle translation: %v", err)
	}
	replay, err := service.ClaimTranslationRequest(
		t.Context(), requestKey, fingerprint, owner, time.Minute, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Disposition != TranslationRequestReplay ||
		replay.Result.Content != "durable translation" {
		t.Fatalf("completed claim = %#v, want durable replay", replay)
	}

	// Once the short-lived payload is gone, usage history must return an
	// expired tombstone instead of assigning provider work and charging again.
	if _, err := db.ExecContext(t.Context(), `
		UPDATE translation_request_results
		SET expires_at = NOW() - INTERVAL '1 second'
		WHERE request_key = $1
	`, requestKey); err != nil {
		t.Fatal(err)
	}
	expired, err := service.ClaimTranslationRequest(
		t.Context(), requestKey, fingerprint, owner, time.Minute, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Disposition != TranslationRequestExpired {
		t.Fatalf("aged replay claim = %#v, want expired without new ownership", expired)
	}
	var usageCount int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM usage_logs WHERE idempotency_key LIKE $1
	`, requestKey+":attempt:%").Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if usageCount != 2 {
		t.Fatalf("usage attempts = %d, want exactly 2", usageCount)
	}

	// Cleanup is deliberately capped so one request cannot turn into an
	// unbounded delete transaction.
	cleanupPrefix := "ws-translation:cleanup-" + strings.ReplaceAll(tenantID, "-", "")
	for index := 0; index < translationRequestCleanupBatch+2; index++ {
		key := fmt.Sprintf("%s-%03d", cleanupPrefix, index)
		usageKey, keyErr := translationAttemptUsageKey(key, 1)
		if keyErr != nil {
			t.Fatal(keyErr)
		}
		if _, err := db.ExecContext(t.Context(), `
			INSERT INTO translation_request_results (
				request_key, tenant_id, user_id, request_fingerprint, state,
				attempt, usage_idempotency_key, started_at, expires_at, updated_at
			)
			VALUES (
				$1, $2, $3, $4, 'failed', 1, $5,
				TIMESTAMPTZ '2000-01-01', TIMESTAMPTZ '2000-01-02', TIMESTAMPTZ '2000-01-01'
			)
		`, key, tenantID, userID, fingerprint, usageKey); err != nil {
			t.Fatal(err)
		}
	}
	triggerKey := "ws-translation:cleanup-trigger-" + strings.ReplaceAll(tenantID, "-", "")
	triggerClaim, err := service.ClaimTranslationRequest(
		t.Context(), triggerKey, strings.Repeat("b", 64), owner, time.Minute, 5*time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CancelTranslationRequest(
		t.Context(),
		triggerKey,
		triggerClaim.Attempt,
		triggerClaim.UsageIdempotencyKey,
	); err != nil {
		t.Fatal(err)
	}
	var cleanupRemaining int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*)
		FROM translation_request_results
		WHERE request_key LIKE $1
	`, cleanupPrefix+"-%").Scan(&cleanupRemaining); err != nil {
		t.Fatal(err)
	}
	if cleanupRemaining != 2 {
		t.Fatalf(
			"bounded cleanup left %d rows, want 2 after deleting exactly %d",
			cleanupRemaining,
			translationRequestCleanupBatch,
		)
	}
}
