package billing

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	_ "github.com/lib/pq"
)

// This test is opt-in because it creates rows in a fully migrated disposable
// PostgreSQL database. The tenant row lock is observable only with real
// concurrent transactions, not a SQL mock.
func TestRecordUsageHardPlanQuotaIsConcurrentAndSettlementSafe(t *testing.T) {
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
	for unitType, expected := range map[string]string{
		"input_token":  "0.0000001500",
		"output_token": "0.0000006000",
	} {
		var price string
		if err := db.QueryRowContext(t.Context(), `
			SELECT price_per_unit::text
			FROM pricing_rules
			WHERE rule_type = 'translation'
			  AND model = 'gpt-4o-mini'
			  AND unit_type = $1
			  AND is_active = true
			ORDER BY priority DESC
			LIMIT 1
		`, unitType).Scan(&price); err != nil {
			t.Fatal(err)
		}
		if price != expected {
			t.Fatalf("gpt-4o-mini %s seed price = %s, want %s", unitType, price, expected)
		}
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
				Action: "transcription", Model: "speechmatics-test", Quantity: 10,
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
		Action: "transcription", Model: "speechmatics-test", Quantity: 0,
		IdempotencyKey: reservationKey,
	}); err != nil {
		t.Fatalf("create zero-minute reservation at limit: %v", err)
	}
	if _, err := service.SettleUsageReservation(t.Context(), reservationKey, &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "transcription", Model: "speechmatics-test", Quantity: 1,
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
}
