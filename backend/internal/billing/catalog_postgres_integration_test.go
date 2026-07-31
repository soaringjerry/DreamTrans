package billing

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// This test deliberately mutates global billing configuration and therefore
// only runs against the caller-provided, disposable, fully migrated database.
func TestCostAttributionHistoricalEstimateAndResetIntegration(t *testing.T) {
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
	var migrationInstalled bool
	if err := db.QueryRowContext(t.Context(), `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.columns
		  WHERE table_name = 'usage_logs' AND column_name = 'cost_attribution'
		)
	`).Scan(&migrationInstalled); err != nil {
		t.Fatal(err)
	}
	if !migrationInstalled {
		t.Fatal("migration 020 is not installed")
	}

	var previousBillingEnabled string
	if err := db.QueryRowContext(t.Context(), `
		SELECT value::text FROM system_settings WHERE key = 'billing_enabled'
	`).Scan(&previousBillingEnabled); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE system_settings SET value = 'true'::jsonb
		WHERE key = 'billing_enabled'
	`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `
			UPDATE system_settings SET value = $1::jsonb
			WHERE key = 'billing_enabled'
		`, previousBillingEnabled)
	})

	service := NewService(db)
	if err := service.EnsureBuiltinCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE billing_config
		SET catalog_version = 'legacy-compatible',
		    pricing_state = 'legacy_active',
		    pending_config = NULL,
		    updated_by = NULL
		WHERE singleton = TRUE;
		UPDATE pricing_rules SET is_active = FALSE WHERE source = 'managed';
	`); err != nil {
		t.Fatal(err)
	}
	var (
		appliedDPBeforeDraft, appliedMarkupBeforeDraft float64
		activeRuleCountBeforeDraft                     int
		activeRulePriceBeforeDraft                     float64
	)
	if err := db.QueryRowContext(t.Context(), `
		SELECT dp_per_usd, default_markup_percent
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&appliedDPBeforeDraft, &appliedMarkupBeforeDraft); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*), COALESCE(SUM(price_per_unit), 0)
		FROM pricing_rules WHERE is_active = TRUE
	`).Scan(&activeRuleCountBeforeDraft, &activeRulePriceBeforeDraft); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateBillingConfig(
		t.Context(),
		BillingConfigInput{
			DPPerUSD: 2, DefaultMarkupPercent: 75,
			Overrides: []MarkupOverride{},
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	var legacyState, legacyVersion, pendingDP string
	var activeManaged, activeRuleCountAfterDraft int
	var (
		appliedDPAfterDraft, appliedMarkupAfterDraft float64
		activeRulePriceAfterDraft                    float64
	)
	if err := db.QueryRowContext(t.Context(), `
		SELECT pricing_state, catalog_version, dp_per_usd,
		       default_markup_percent,
		       pending_config->>'dp_per_usd'
		FROM billing_config WHERE singleton = TRUE
	`).Scan(
		&legacyState,
		&legacyVersion,
		&appliedDPAfterDraft,
		&appliedMarkupAfterDraft,
		&pendingDP,
	); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM pricing_rules
		WHERE source = 'managed' AND is_active = TRUE
	`).Scan(&activeManaged); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*), COALESCE(SUM(price_per_unit), 0)
		FROM pricing_rules WHERE is_active = TRUE
	`).Scan(&activeRuleCountAfterDraft, &activeRulePriceAfterDraft); err != nil {
		t.Fatal(err)
	}
	if legacyState != pricingStateLegacy ||
		legacyVersion != "legacy-compatible" ||
		activeManaged != 0 ||
		appliedDPAfterDraft != appliedDPBeforeDraft ||
		appliedMarkupAfterDraft != appliedMarkupBeforeDraft ||
		pendingDP != "2" ||
		activeRuleCountAfterDraft != activeRuleCountBeforeDraft ||
		activeRulePriceAfterDraft != activeRulePriceBeforeDraft {
		t.Fatalf(
			"draft changed applied billing: state=%q version=%q active=%d applied=%v/%v pending=%q rules=%d/%v",
			legacyState,
			legacyVersion,
			activeManaged,
			appliedDPAfterDraft,
			appliedMarkupAfterDraft,
			pendingDP,
			activeRuleCountAfterDraft,
			activeRulePriceAfterDraft,
		)
	}
	staleApplyPreview, err := service.PreviewBuiltinCatalogApply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateBillingConfig(
		t.Context(),
		BillingConfigInput{
			DPPerUSD: 1, DefaultMarkupPercent: 50,
			Overrides: []MarkupOverride{},
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyBuiltinCatalog(
		t.Context(),
		"",
		DefaultCatalogVersion,
		billingApplyConfirmation,
		staleApplyPreview.CurrentRevision,
	); !errors.Is(err, ErrBillingPreviewStale) {
		t.Fatalf("stale apply preview error = %v", err)
	}
	applyPreview, err := service.PreviewBuiltinCatalogApply(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyBuiltinCatalog(
		t.Context(),
		"",
		DefaultCatalogVersion,
		billingApplyConfirmation,
		applyPreview.CurrentRevision,
	); err != nil {
		t.Fatal(err)
	}
	var pendingAfterApply bool
	if err := db.QueryRowContext(t.Context(), `
		SELECT pending_config IS NOT NULL
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&pendingAfterApply); err != nil {
		t.Fatal(err)
	}
	if pendingAfterApply {
		t.Fatal("catalog apply did not clear pending billing configuration")
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE billing_config
		SET catalog_version = 'outdated-for-reset'
		WHERE singleton = TRUE
	`); err != nil {
		t.Fatal(err)
	}
	if err := service.UpdateBillingConfig(
		t.Context(),
		BillingConfigInput{
			DPPerUSD: 3, DefaultMarkupPercent: 80,
			Overrides: []MarkupOverride{},
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO provider_model_sync_status (provider, status, updated_at)
		VALUES ('openai-compatible', 'builtin_unverified', NOW())
		ON CONFLICT (provider) DO UPDATE SET
			status = EXCLUDED.status,
			updated_at = EXCLUDED.updated_at;
		UPDATE provider_models
		SET provider_available = FALSE
		WHERE provider = 'openai-compatible'
		  AND model_id IN (
		    'gpt-5.6-luna',
		    'gpt-5.6-sol',
		    'text-embedding-3-small'
		  )
	`); err != nil {
		t.Fatal(err)
	}
	resetPreview, err := service.PreviewBillingReset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetBillingDefaults(
		t.Context(),
		"",
		billingResetConfirmation,
		resetPreview.CurrentRevision,
	); err != nil {
		t.Fatal(err)
	}
	var pendingAfterReset bool
	if err := db.QueryRowContext(t.Context(), `
		SELECT pending_config IS NOT NULL
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&pendingAfterReset); err != nil {
		t.Fatal(err)
	}
	if pendingAfterReset {
		t.Fatal("full reset did not clear pending billing configuration")
	}
	var restoredUnverifiedModels int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*)
		FROM provider_models
		WHERE provider = 'openai-compatible'
		  AND provider_available = TRUE
		  AND model_id IN (
		    'gpt-5.6-luna',
		    'gpt-5.6-sol',
		    'text-embedding-3-small'
		  )
	`).Scan(&restoredUnverifiedModels); err != nil {
		t.Fatal(err)
	}
	if restoredUnverifiedModels != 3 {
		t.Fatalf(
			"unverified reset restored %d shipped models, want 3",
			restoredUnverifiedModels,
		)
	}

	var tenantID, userID string
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO tenants
			(name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
		VALUES (
			'Billing Reliability Integration',
			'bri-' || gen_random_uuid(),
			'enterprise',
			-1,
			-1,
			100
		)
		RETURNING id
	`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(
			context.Background(),
			`DELETE FROM tenants WHERE id = $1`,
			tenantID,
		)
	})
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO users
			(tenant_id, email, password_hash, name, role, is_active,
			 email_verified, dreampoints)
		VALUES (
			$1,
			gen_random_uuid() || '@example.invalid',
			'integration-only',
			'Billing Reliability User',
			'user',
			TRUE,
			TRUE,
			100
		)
		RETURNING id
	`, tenantID).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	platformCost, err := service.RecordUsage(t.Context(), &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "transcription", Model: "speechmatics-realtime-enhanced",
		Quantity: 60, IdempotencyKey: "billing-reliability-platform-" + userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(platformCost-0.645) > 1e-9 {
		t.Fatalf("platform charge = %v, want 0.645", platformCost)
	}
	byokKey := "billing-reliability-byok-" + userID
	byokReservation, err := service.RecordUsage(t.Context(), &UsageRecord{
		UserID: userID, TenantID: tenantID,
		Action: "transcription", Model: "speechmatics-realtime-enhanced",
		Quantity: 30, CustomerFunded: true,
		IdempotencyKey: byokKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(byokReservation-0.1075) > 1e-9 {
		t.Fatalf("BYOK reservation = %v, want 0.1075", byokReservation)
	}
	if _, err := service.UpsertProviderCostOverride(
		t.Context(),
		ProviderCostOverrideInput{
			Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced",
			Service: "transcription", UnitType: "hour",
			CostPerUnitUSD: 0.2, SourceLabel: "mid-flight-change",
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettleUsageReservation(
		t.Context(),
		byokKey,
		&UsageRecord{
			UserID: userID, TenantID: tenantID,
			Action: "transcription", Model: "speechmatics-realtime-enhanced",
			Quantity: 60,
		},
	); err == nil {
		t.Fatal("BYOK reservation accepted a platform-funded settlement")
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE system_settings SET value = 'false'::jsonb
		WHERE key = 'billing_enabled'
	`); err != nil {
		t.Fatal(err)
	}
	byokCost, err := service.SettleUsageReservation(
		t.Context(),
		byokKey,
		&UsageRecord{
			UserID: userID, TenantID: tenantID,
			Action: "transcription", Model: "speechmatics-response-2026-07-31",
			Quantity: 60, CustomerFunded: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(byokCost-0.215) > 1e-9 {
		t.Fatalf("BYOK charge = %v, want 0.215", byokCost)
	}
	repeatedBYOKCost, err := service.SettleUsageReservation(
		t.Context(),
		byokKey,
		&UsageRecord{
			UserID: userID, TenantID: tenantID,
			Action: "transcription", Model: "different-retry-model",
			Quantity: 120, CustomerFunded: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(repeatedBYOKCost-0.215) > 1e-9 {
		t.Fatalf(
			"repeated BYOK settlement = %v, want finalized 0.215",
			repeatedBYOKCost,
		)
	}
	var finalizedQuantity float64
	var finalizedAt sql.NullTime
	if err := db.QueryRowContext(t.Context(), `
		SELECT quantity, settled_at
		FROM usage_logs
		WHERE idempotency_key = $1
	`, byokKey).Scan(&finalizedQuantity, &finalizedAt); err != nil {
		t.Fatal(err)
	}
	if finalizedQuantity != 60 || !finalizedAt.Valid {
		t.Fatalf(
			"repeated settlement mutated finalized usage: quantity=%v settled=%v",
			finalizedQuantity,
			finalizedAt.Valid,
		)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE system_settings SET value = 'true'::jsonb
		WHERE key = 'billing_enabled'
	`); err != nil {
		t.Fatal(err)
	}

	var attribution, billedModel string
	var upstream, fee float64
	if err := db.QueryRowContext(t.Context(), `
		SELECT cost_attribution, upstream_cost_usd, service_fee_dp,
		       COALESCE(model, '')
		FROM usage_logs
		WHERE idempotency_key = $1
	`, "billing-reliability-platform-"+userID).Scan(
		&attribution,
		&upstream,
		&fee,
		&billedModel,
	); err != nil {
		t.Fatal(err)
	}
	if attribution != AttributionProviderPriced ||
		math.Abs(upstream-0.43) > 1e-9 ||
		math.Abs(fee-0.215) > 1e-9 {
		t.Fatalf(
			"platform ledger = %q/%v/%v",
			attribution,
			upstream,
			fee,
		)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT cost_attribution, upstream_cost_usd, service_fee_dp,
		       COALESCE(model, '')
		FROM usage_logs
		WHERE idempotency_key = $1
	`, byokKey).Scan(
		&attribution,
		&upstream,
		&fee,
		&billedModel,
	); err != nil {
		t.Fatal(err)
	}
	if attribution != AttributionBYOK ||
		upstream != 0 ||
		math.Abs(fee-0.215) > 1e-9 ||
		billedModel != "speechmatics-realtime-enhanced" {
		t.Fatalf(
			"BYOK ledger = %q/%v/%v model=%q",
			attribution,
			upstream,
			fee,
			billedModel,
		)
	}
	if _, err := service.DeleteProviderCostOverride(
		t.Context(),
		"speechmatics",
		"speechmatics-realtime-enhanced",
		"transcription",
		"hour",
		"",
	); err != nil {
		t.Fatal(err)
	}
	beforeLegacy, err := service.GetBillingAnalytics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO usage_logs
			(tenant_id, user_id, action, quantity, model, cost, month_key,
			 cost_attribution)
		VALUES (
			$1, $2, 'transcription', 60, 'speechmatics', 3.67,
			TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM'), 'legacy_unknown'
		)
	`, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	afterLegacy, err := service.GetBillingAnalytics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if afterLegacy.LegacyUnknownCount-beforeLegacy.LegacyUnknownCount != 1 ||
		afterLegacy.EstimateEligibleCount-beforeLegacy.EstimateEligibleCount != 1 ||
		math.Abs(
			(afterLegacy.EstimatedLegacyUpstreamCostUSD-
				beforeLegacy.EstimatedLegacyUpstreamCostUSD)-0.43,
		) > 1e-9 {
		t.Fatalf(
			"legacy estimate delta = unknown %d, eligible %d, upstream %v",
			afterLegacy.LegacyUnknownCount-beforeLegacy.LegacyUnknownCount,
			afterLegacy.EstimateEligibleCount-beforeLegacy.EstimateEligibleCount,
			afterLegacy.EstimatedLegacyUpstreamCostUSD-
				beforeLegacy.EstimatedLegacyUpstreamCostUSD,
		)
	}

	if _, err := service.UpsertProviderCostOverride(
		t.Context(),
		ProviderCostOverrideInput{
			Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced",
			Service: "transcription", UnitType: "hour",
			CostPerUnitUSD: 0.2, SourceLabel: "integration-contract",
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManualModelCost(
		t.Context(),
		ManualModelCostInput{
			ModelID: "integration-manual-model", Service: "llm",
			InputPerMillionUSD: 1, OutputPerMillionUSD: 2,
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManualModelCost(
		t.Context(),
		ManualModelCostInput{
			ModelID: "gpt-5.6-luna", Service: "llm",
			InputPerMillionUSD: 2, OutputPerMillionUSD: 8,
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if err := service.UpsertManualModelCost(
		t.Context(),
		ManualModelCostInput{
			ModelID: "gpt-5.6-luna", Service: "llm",
		},
		"",
	); err != nil {
		t.Fatalf("restore built-in model catalog prices: %v", err)
	}
	var restoredModelOverrideCount, restoredModelBuiltinUnits int
	if err := db.QueryRowContext(t.Context(), `
		SELECT
		  (SELECT COUNT(*) FROM provider_cost_overrides
		   WHERE provider = 'openai-compatible' AND sku = 'gpt-5.6-luna'),
		  (SELECT COUNT(*) FROM provider_cost_rates
		   WHERE provider = 'openai-compatible' AND sku = 'gpt-5.6-luna'
		     AND is_builtin = TRUE AND is_active = TRUE)
	`).Scan(&restoredModelOverrideCount, &restoredModelBuiltinUnits); err != nil {
		t.Fatal(err)
	}
	if restoredModelOverrideCount != 0 || restoredModelBuiltinUnits < 2 {
		t.Fatalf(
			"restored model price state = overrides %d, builtin units %d",
			restoredModelOverrideCount,
			restoredModelBuiltinUnits,
		)
	}
	for _, item := range []struct {
		provider string
		cost     float64
	}{
		{provider: "integration-provider-a", cost: 1},
		{provider: "integration-provider-b", cost: 2},
	} {
		if _, err := service.UpsertProviderCostOverride(
			t.Context(),
			ProviderCostOverrideInput{
				Provider: item.provider, SKU: "shared-model",
				Service: "llm", UnitType: "input_token",
				CostPerUnitUSD: item.cost, SourceLabel: "provider-isolation-test",
			},
			"",
		); err != nil {
			t.Fatal(err)
		}
	}
	providerACost, err := service.calculateUsageCost(&UsageRecord{
		Action: "chat", Provider: "integration-provider-a",
		Model: "shared-model", InputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	providerBCost, err := service.calculateUsageCost(&UsageRecord{
		Action: "chat", Provider: "integration-provider-b",
		Model: "shared-model", InputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(providerACost-15) > 1e-9 ||
		math.Abs(providerBCost-30) > 1e-9 {
		t.Fatalf(
			"provider-specific retail leaked: provider-a=%v provider-b=%v",
			providerACost,
			providerBCost,
		)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE provider_model_sync_status
		SET status = 'provider_confirmed', updated_at = NOW()
		WHERE provider = 'openai-compatible';
		UPDATE provider_models
		SET provider_available = FALSE
		WHERE provider = 'openai-compatible'
		  AND model_id IN (
		    'gpt-5.6-luna',
		    'gpt-5.6-sol',
		    'text-embedding-3-small'
		  );
		UPDATE pricing_rules
		SET rule_type = 'chat',
		    provider = 'corrupted-provider',
		    model = 'corrupted-model',
		    unit_type = 'hour',
		    priority = -500,
		    is_active = FALSE
		WHERE managed_key =
		  'openai-compatible:gpt-5.6-sol:translation:input_token'
	`); err != nil {
		t.Fatal(err)
	}

	var balanceBeforeReset, ledgerCostBeforeReset float64
	var ledgerCountBeforeReset int
	if err := db.QueryRowContext(t.Context(), `
		SELECT dreampoints FROM users WHERE id = $1
	`, userID).Scan(&balanceBeforeReset); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*), COALESCE(SUM(cost), 0)
		FROM usage_logs WHERE user_id = $1
	`, userID).Scan(&ledgerCountBeforeReset, &ledgerCostBeforeReset); err != nil {
		t.Fatal(err)
	}
	resetPreview, err = service.PreviewBillingReset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetBillingDefaults(
		t.Context(),
		"",
		billingResetConfirmation,
		resetPreview.CurrentRevision,
	); err != nil {
		t.Fatal(err)
	}
	var (
		balanceAfterReset, ledgerCostAfterReset float64
		ledgerCountAfterReset                   int
		overrideCount, manualCount              int
		dpPerUSD, markup                        float64
		state                                   string
		validDefaultPurposes                    int
		unavailablePreferredModels              int
		rebuiltRuleType, rebuiltRuleProvider    string
		rebuiltRuleModel, rebuiltRuleUnit       string
		rebuiltRulePriority                     int
		rebuiltRuleActive                       bool
	)
	if err := db.QueryRowContext(t.Context(), `
		SELECT dreampoints FROM users WHERE id = $1
	`, userID).Scan(&balanceAfterReset); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*), COALESCE(SUM(cost), 0)
		FROM usage_logs WHERE user_id = $1
	`, userID).Scan(&ledgerCountAfterReset, &ledgerCostAfterReset); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM provider_cost_overrides
	`).Scan(&overrideCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM provider_cost_rates
		WHERE is_builtin = FALSE
	`).Scan(&manualCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT dp_per_usd, default_markup_percent, pricing_state
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&dpPerUSD, &markup, &state); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(DISTINCT p.purpose)
		FROM model_policies p
		JOIN provider_models pm
		  ON pm.provider = 'openai-compatible'
		 AND pm.model_id = p.model_id
		 AND pm.provider_available = TRUE
		WHERE p.is_approved = TRUE
		  AND p.is_default = TRUE
		  AND p.cost_confirmed = TRUE
	`).Scan(&validDefaultPurposes); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*)
		FROM provider_models
		WHERE provider = 'openai-compatible'
		  AND provider_available = FALSE
		  AND model_id IN (
		    'gpt-5.6-luna',
		    'gpt-5.6-sol',
		    'text-embedding-3-small'
		  )
	`).Scan(&unavailablePreferredModels); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT rule_type, provider, model, unit_type, priority, is_active
		FROM pricing_rules
		WHERE managed_key =
		  'openai-compatible:gpt-5.6-sol:translation:input_token'
	`).Scan(
		&rebuiltRuleType,
		&rebuiltRuleProvider,
		&rebuiltRuleModel,
		&rebuiltRuleUnit,
		&rebuiltRulePriority,
		&rebuiltRuleActive,
	); err != nil {
		t.Fatal(err)
	}
	if balanceAfterReset != balanceBeforeReset ||
		ledgerCountAfterReset != ledgerCountBeforeReset ||
		ledgerCostAfterReset != ledgerCostBeforeReset {
		t.Fatal("billing reset changed balance or immutable usage ledger")
	}
	if overrideCount != 0 || manualCount != 0 ||
		dpPerUSD != 1 || markup != 50 || state != pricingStateManaged ||
		validDefaultPurposes != 4 || unavailablePreferredModels != 3 ||
		rebuiltRuleType != "translation" ||
		rebuiltRuleProvider != "openai-compatible" ||
		rebuiltRuleModel != "gpt-5.6-sol" ||
		rebuiltRuleUnit != "input_token" ||
		rebuiltRulePriority != 100 ||
		!rebuiltRuleActive {
		t.Fatalf(
			"reset defaults = override=%d manual=%d dp=%v markup=%v state=%q valid_defaults=%d unavailable_preferred=%d rebuilt_rule=%s/%s/%s/%s/%d/%v",
			overrideCount,
			manualCount,
			dpPerUSD,
			markup,
			state,
			validDefaultPurposes,
			unavailablePreferredModels,
			rebuiltRuleType,
			rebuiltRuleProvider,
			rebuiltRuleModel,
			rebuiltRuleUnit,
			rebuiltRulePriority,
			rebuiltRuleActive,
		)
	}

	if _, err := service.UpsertProviderCostOverride(
		t.Context(),
		ProviderCostOverrideInput{
			Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced",
			Service: "transcription", UnitType: "hour",
			CostPerUnitUSD: 0.2, SourceLabel: "rollback-sentinel",
		},
		"",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE provider_model_sync_status
		SET status = 'temporarily_unavailable', updated_at = NOW()
		WHERE provider = 'openai-compatible';
		UPDATE provider_models
		SET provider_available = FALSE
		WHERE provider = 'openai-compatible'
	`); err != nil {
		t.Fatal(err)
	}
	resetPreview, err = service.PreviewBillingReset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResetBillingDefaults(
		t.Context(),
		"",
		billingResetConfirmation,
		resetPreview.CurrentRevision,
	); err == nil {
		t.Fatal("temporarily unavailable provider with no valid fallback allowed reset")
	}
	var rollbackSentinelCount int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*)
		FROM provider_cost_overrides
		WHERE provider = 'speechmatics'
		  AND sku = 'speechmatics-realtime-enhanced'
		  AND service = 'transcription'
		  AND unit_type = 'hour'
		  AND source_label = 'rollback-sentinel'
	`).Scan(&rollbackSentinelCount); err != nil {
		t.Fatal(err)
	}
	if rollbackSentinelCount != 1 {
		t.Fatal("failed reset did not roll back cost-override deletion")
	}
}

func TestResetPreviewRejectsConcurrentCostOverrideIntegration(t *testing.T) {
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
	service := NewService(db)
	if err := service.EnsureBuiltinCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
	const (
		provider    = "integration-provider"
		sku         = "revision-lock-test"
		serviceName = "llm"
		unitType    = "input_token"
	)
	t.Cleanup(func() {
		tx, beginErr := db.BeginTx(context.Background(), nil)
		if beginErr != nil {
			return
		}
		defer func() { _ = tx.Rollback() }()
		if lockBillingRevisionTx(context.Background(), tx) != nil {
			return
		}
		_, _ = tx.Exec(`
			DELETE FROM provider_cost_overrides
			WHERE provider = $1 AND sku = $2
			  AND service = $3 AND unit_type = $4
		`, provider, sku, serviceName, unitType)
		_, _ = tx.Exec(`
			DELETE FROM provider_cost_rates
			WHERE provider = $1 AND sku = $2
			  AND service = $3 AND unit_type = $4
		`, provider, sku, serviceName, unitType)
		_ = tx.Commit()
	})
	if _, err := db.Exec(`
		DELETE FROM provider_cost_overrides
		WHERE provider = $1 AND sku = $2
		  AND service = $3 AND unit_type = $4
	`, provider, sku, serviceName, unitType); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		DELETE FROM provider_cost_rates
		WHERE provider = $1 AND sku = $2
		  AND service = $3 AND unit_type = $4
	`, provider, sku, serviceName, unitType); err != nil {
		t.Fatal(err)
	}

	preview, err := service.PreviewBillingReset(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	overrideTx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = overrideTx.Rollback() }()
	if err := lockBillingRevisionTx(t.Context(), overrideTx); err != nil {
		t.Fatal(err)
	}
	if _, err := overrideTx.ExecContext(t.Context(), `
		INSERT INTO provider_cost_rates
			(provider, sku, service, unit_type, cost_per_unit_usd,
			 catalog_version, source_url, effective_at, is_builtin, is_active)
		VALUES ($1, $2, $3, $4, 1, 'integration', '', NOW(), FALSE, TRUE)
	`, provider, sku, serviceName, unitType); err != nil {
		t.Fatal(err)
	}
	if _, err := overrideTx.ExecContext(t.Context(), `
		INSERT INTO provider_cost_overrides
			(provider, sku, service, unit_type, cost_per_unit_usd,
			 source_label, effective_at)
		VALUES ($1, $2, $3, $4, 0.5, 'integration', NOW())
	`, provider, sku, serviceName, unitType); err != nil {
		t.Fatal(err)
	}

	resetDone := make(chan error, 1)
	go func() {
		_, resetErr := service.ResetBillingDefaults(
			context.Background(),
			"",
			billingResetConfirmation,
			preview.CurrentRevision,
		)
		resetDone <- resetErr
	}()
	if err := overrideTx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case resetErr := <-resetDone:
		if !errors.Is(resetErr, ErrBillingPreviewStale) {
			t.Fatalf("concurrent reset error = %v", resetErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent reset did not resume after revision lock was released")
	}
	var overrideCount int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM provider_cost_overrides
		WHERE provider = $1 AND sku = $2
		  AND service = $3 AND unit_type = $4
	`, provider, sku, serviceName, unitType).Scan(&overrideCount); err != nil {
		t.Fatal(err)
	}
	if overrideCount != 1 {
		t.Fatal("stale reset partially deleted the concurrent override")
	}
}
