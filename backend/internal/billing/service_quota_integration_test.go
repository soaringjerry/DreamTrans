package billing

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq"
)

// This test is opt-in because it creates rows in a fully migrated disposable
// PostgreSQL database. The tenant row lock is observable only with real
// concurrent transactions, not a SQL mock.
//
// It must not depend on global seed retail prices: other PostgreSQL integration
// tests in this package apply the managed cost-plus catalog and leave shared
// pricing_rules at markup-adjusted retail values.
func TestRecordUsageHardPlanQuotaIsConcurrentAndSettlementSafe(t *testing.T) {
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

	// Keep monetary settlement paths exercised even if a prior integration test
	// flipped billing_enabled for its own scenario.
	if _, err := db.ExecContext(t.Context(), `
		UPDATE system_settings
		SET value = 'true'::jsonb
		WHERE key = 'billing_enabled'
	`); err != nil {
		t.Fatal(err)
	}

	var tenantID, userID string
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO tenants
			(name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
		VALUES ('Billing Quota Integration', 'bqi-' || gen_random_uuid(), 'free', -1, -1, 10)
		RETURNING id
	`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	modelID := "speechmatics-test-" + strings.ReplaceAll(tenantID, "-", "")
	// Own both upstream cost and retail rules for this model so the test works
	// whether the shared DB is still on legacy seeds or a managed catalog.
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO provider_cost_rates
			(provider, sku, service, unit_type, cost_per_unit_usd,
			 catalog_version, source_url, is_builtin, is_active)
		VALUES ('speechmatics', $1, 'transcription', 'hour', 0.1,
		        'integration-test', '', FALSE, TRUE)
		ON CONFLICT (provider, sku, service, unit_type) DO UPDATE SET
			cost_per_unit_usd = EXCLUDED.cost_per_unit_usd,
			is_active = TRUE
	`, modelID); err != nil {
		t.Fatal(err)
	}
	var transcriptionRuleID, translationInputRuleID, translationOutputRuleID string
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO pricing_rules
			(rule_type, provider, model, price_per_unit, unit_type, description,
			 is_active, priority, source)
		VALUES (
			'transcription', 'speechmatics', $1, 0.15, 'hour',
			'quota integration transcription', TRUE, 200, 'legacy'
		)
		RETURNING id
	`, modelID).Scan(&transcriptionRuleID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO pricing_rules
			(rule_type, provider, model, price_per_unit, unit_type, description,
			 is_active, priority, source)
		VALUES (
			'translation', 'openai-compatible', 'gpt-4o-mini', 0.00000015,
			'input_token', 'quota integration translation input', TRUE, 200, 'legacy'
		)
		RETURNING id
	`).Scan(&translationInputRuleID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO pricing_rules
			(rule_type, provider, model, price_per_unit, unit_type, description,
			 is_active, priority, source)
		VALUES (
			'translation', 'openai-compatible', 'gpt-4o-mini', 0.0000006,
			'output_token', 'quota integration translation output', TRUE, 200, 'legacy'
		)
		RETURNING id
	`).Scan(&translationOutputRuleID); err != nil {
		t.Fatal(err)
	}
	// gpt-4o-mini upstream rates are required by the provider-priced path even
	// when a retail rule already exists. EnsureBuiltinCatalog is safe and
	// idempotent; it does not rewrite applied retail rules.
	bootstrap := NewService(db)
	if err := bootstrap.EnsureBuiltinCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `
			DELETE FROM pricing_rules
			WHERE id IN ($1, $2, $3)
		`, transcriptionRuleID, translationInputRuleID, translationOutputRuleID)
		_, _ = db.ExecContext(context.Background(), `
			DELETE FROM provider_cost_rates
			WHERE provider = 'speechmatics' AND sku = $1
			  AND catalog_version = 'integration-test'
		`, modelID)
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO users
			(tenant_id, email, password_hash, name, role, is_active, email_verified, dreampoints)
		VALUES (
			$1,
			gen_random_uuid() || '@example.invalid',
			'integration-only',
			'Billing Quota User',
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
	const attempts = 20
	var allowed atomic.Int64
	var rejected atomic.Int64
	var unexpected atomic.Int64
	var waitGroup sync.WaitGroup
	for range attempts {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, recordErr := service.RecordUsage(t.Context(), &UsageRecord{
				UserID: userID, TenantID: tenantID,
				Action: "transcription", Model: modelID, Quantity: 10,
			})
			switch {
			case recordErr == nil:
				allowed.Add(1)
			case errors.Is(recordErr, ErrPlanQuotaExceeded):
				rejected.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	if allowed.Load() != 6 || rejected.Load() != attempts-6 || unexpected.Load() != 0 {
		t.Fatalf(
			"quota results: allowed=%d rejected=%d unexpected=%d",
			allowed.Load(),
			rejected.Load(),
			unexpected.Load(),
		)
	}

	var minutes float64
	if err := db.QueryRowContext(t.Context(), `
		SELECT COALESCE(SUM(quantity), 0)
		FROM usage_logs
		WHERE tenant_id = $1 AND action = 'transcription'
	`, tenantID).Scan(&minutes); err != nil {
		t.Fatal(err)
	}
	if minutes != 60 {
		t.Fatalf("persisted transcription minutes = %v, want 60", minutes)
	}

	reservationKey := "billing-quota-integration:" + tenantID
	if _, err := service.RecordUsage(t.Context(), &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "transcription", Model: modelID, Quantity: 0,
		IdempotencyKey: reservationKey,
	}); err != nil {
		t.Fatalf("create zero-minute reservation at limit: %v", err)
	}
	if _, err := service.SettleUsageReservation(t.Context(), reservationKey, &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "transcription", Model: modelID, Quantity: 1,
	}); !errors.Is(err, ErrPlanQuotaExceeded) {
		t.Fatalf("upward settlement error = %v, want ErrPlanQuotaExceeded", err)
	}
	var reservedMinutes float64
	if err := db.QueryRowContext(t.Context(), `
		SELECT quantity
		FROM usage_logs
		WHERE idempotency_key = $1
	`, reservationKey).Scan(&reservedMinutes); err != nil {
		t.Fatal(err)
	}
	if reservedMinutes != 0 {
		t.Fatalf("quota-rejected settlement changed reservation to %v minutes", reservedMinutes)
	}

	// A known provider failure may refund a durable logical operation. A retry
	// must be able to reserve that same key again, while a crash after a
	// successful settlement must not create another debit.
	var balanceBefore float64
	if err := db.QueryRowContext(t.Context(), `
		SELECT dreampoints FROM users WHERE id=$1
	`, userID).Scan(&balanceBefore); err != nil {
		t.Fatal(err)
	}
	reusableKey := "billing-reusable-integration:" + tenantID
	firstReservation := &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "translation", Model: "gpt-4o-mini",
		InputTokens: 100, IdempotencyKey: reusableKey,
		ReuseRefundedReservation: true,
	}
	if _, err := service.RecordUsage(t.Context(), firstReservation); err != nil {
		t.Fatalf("create reusable reservation: %v", err)
	}
	if err := service.RefundUsage(
		t.Context(), reusableKey, "known provider failure",
	); err != nil {
		t.Fatalf("refund reusable reservation: %v", err)
	}
	secondReservation := &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "translation", Model: "gpt-4o-mini",
		InputTokens: 100, IdempotencyKey: reusableKey,
		ReuseRefundedReservation: true,
	}
	if _, err := service.RecordUsage(t.Context(), secondReservation); err != nil {
		t.Fatalf("reuse refunded reservation: %v", err)
	}
	if secondReservation.IdempotencyDuplicate {
		t.Fatal("refunded reservation was not reopened")
	}
	actual := &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "translation", Model: "gpt-4o-mini",
		InputTokens: 50,
	}
	actualCost, err := service.SettleUsageReservation(
		t.Context(), reusableKey, actual,
	)
	if err != nil {
		t.Fatalf("settle reused reservation: %v", err)
	}
	crashReplay := &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "translation", Model: "gpt-4o-mini",
		InputTokens: 100, IdempotencyKey: reusableKey,
		ReuseRefundedReservation: true,
	}
	if _, err := service.RecordUsage(t.Context(), crashReplay); err != nil {
		t.Fatalf("replay settled reservation: %v", err)
	}
	if !crashReplay.IdempotencyDuplicate {
		t.Fatal("settled reservation replay created a new debit")
	}
	var balanceAfter float64
	if err := db.QueryRowContext(t.Context(), `
		SELECT dreampoints FROM users WHERE id=$1
	`, userID).Scan(&balanceAfter); err != nil {
		t.Fatal(err)
	}
	if difference := balanceBefore - balanceAfter; math.Abs(
		difference-actualCost,
	) > 1e-9 {
		t.Fatalf(
			"reusable reservation balance delta = %v, want %v",
			difference,
			actualCost,
		)
	}
}
