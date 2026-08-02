package billing

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestApplyRetailPricesUsesSpecificOverridePrecedence(t *testing.T) {
	rates := []CostRate{{
		Provider: "openai-compatible", SKU: "gpt-test",
		Service: "llm", UnitType: "input_token", CostPerUnitUSD: 2,
	}}
	cfg := BillingConfig{
		DPPerUSD: 3, DefaultMarkupPercent: 10,
		Overrides: []MarkupOverride{
			{ScopeType: "sku", ScopeKey: "gpt-test", MarkupPercent: 50},
			{ScopeType: "provider", ScopeKey: "openai-compatible", MarkupPercent: 20},
			{ScopeType: "category", ScopeKey: "llm", MarkupPercent: 30},
		},
	}
	applyRetailPrices(rates, &cfg)
	if got, want := rates[0].RetailDPPerUnit, 9.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("retail = %v, want %v", got, want)
	}
	if rates[0].OverrideSource != "sku" {
		t.Fatalf("override source = %q, want sku", rates[0].OverrideSource)
	}
	if got, want := rates[0].GrossMargin, 100.0/3.0; math.Abs(got-want) > 1e-9 {
		t.Fatalf("gross margin = %v, want %v", got, want)
	}
}

func TestCalculateCostPricesCacheDetailsAndFallsBackConservatively(t *testing.T) {
	model := "gpt-test"
	service := &Service{rulesCache: []PricingRule{
		{RuleType: "chat", Model: &model, UnitType: "input_token", PricePerUnit: 1, IsActive: true},
		{RuleType: "chat", Model: &model, UnitType: "cached_input_token", PricePerUnit: .1, IsActive: true},
		{RuleType: "chat", Model: &model, UnitType: "cache_write_token", PricePerUnit: 1.25, IsActive: true},
		{RuleType: "chat", Model: &model, UnitType: "output_token", PricePerUnit: 10, IsActive: true},
	}}
	got, _ := service.calculateCost("chat", model, 0, 100, 20, 10, 5)
	// 70 ordinary + 2 cached + 12.5 writes + 50 output.
	if want := 134.5; math.Abs(got-want) > 1e-9 {
		t.Fatalf("cost = %v, want %v", got, want)
	}

	service.rulesCache = []PricingRule{
		{RuleType: "chat", Model: &model, UnitType: "input_token", PricePerUnit: 1, IsActive: true},
	}
	got, _ = service.calculateCost("chat", model, 0, 100, 20, 10, 0)
	if got != 100 {
		t.Fatalf("cache fallback cost = %v, want 100", got)
	}
}

func TestBillingResetRequiresExactConfirmation(t *testing.T) {
	service := &Service{}
	if _, err := service.ResetBillingDefaults(
		t.Context(), "", "RESET", "",
	); !errors.Is(err, ErrInvalidBillingInput) {
		t.Fatalf("confirmation mismatch error = %v", err)
	}
}

func TestManualModelCostValidationIsTyped(t *testing.T) {
	service := &Service{}
	if err := service.UpsertManualModelCost(
		t.Context(),
		ManualModelCostInput{ModelID: "", Service: "llm"},
		"",
	); !errors.Is(err, ErrInvalidBillingInput) {
		t.Fatalf("manual model cost validation error = %v", err)
	}
}

func openBillingRevisionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range []string{
		`CREATE TABLE billing_config (
			singleton BOOLEAN PRIMARY KEY,
			dp_per_usd REAL NOT NULL,
			default_markup_percent REAL NOT NULL,
			catalog_version TEXT NOT NULL,
			pricing_state TEXT NOT NULL,
			pending_config BLOB,
			updated_at TIMESTAMP NOT NULL,
			updated_by TEXT
		)`,
		`INSERT INTO billing_config
			(singleton, dp_per_usd, default_markup_percent, catalog_version,
			 pricing_state, pending_config, updated_at)
		 VALUES
			(TRUE, 1, 50, '` + DefaultCatalogVersion + `',
			 'managed_active', NULL, '2026-07-31T00:00:00Z')`,
		`CREATE TABLE billing_markup_overrides (
			scope_type TEXT, scope_key TEXT, markup_percent REAL
		)`,
		`CREATE TABLE provider_cost_overrides (
			id TEXT PRIMARY KEY,
			provider TEXT, sku TEXT, service TEXT, unit_type TEXT,
			cost_per_unit_usd REAL, source_label TEXT, effective_at TIMESTAMP
		)`,
		`CREATE TABLE provider_cost_rates (
			id TEXT PRIMARY KEY,
			provider TEXT, sku TEXT, service TEXT, unit_type TEXT,
			cost_per_unit_usd REAL, catalog_version TEXT, source_url TEXT,
			effective_at TIMESTAMP, is_builtin BOOLEAN, is_active BOOLEAN
		)`,
		`CREATE TABLE pricing_rules (
			id TEXT PRIMARY KEY,
			rule_type TEXT, provider TEXT, model TEXT, price_per_unit REAL,
			unit_type TEXT, description TEXT, is_active BOOLEAN, priority INTEGER,
			managed_key TEXT, source TEXT, catalog_version TEXT,
			updated_at TIMESTAMP
		)`,
		`CREATE TABLE provider_models (
			provider TEXT, model_id TEXT, source TEXT, provider_available BOOLEAN
		)`,
		`CREATE TABLE model_policies (
			purpose TEXT, model_id TEXT, is_approved BOOLEAN,
			is_default BOOLEAN, cost_confirmed BOOLEAN
		)`,
		`CREATE TABLE provider_model_sync_status (
			provider TEXT, status TEXT
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("create billing revision schema: %v", err)
		}
	}
	return db
}

func TestBillingRevisionCoversEveryPreviewRelevantCategory(t *testing.T) {
	mutations := map[string]string{
		"pending config": `UPDATE billing_config
			SET pending_config =
				'{"dp_per_usd":2,"default_markup_percent":50,"overrides":[]}'`,
		"markup override": `INSERT INTO billing_markup_overrides
			VALUES ('provider', 'speechmatics', 75)`,
		"cost override": `INSERT INTO provider_cost_overrides
			VALUES ('o1', 'speechmatics', 'speechmatics-realtime-enhanced',
			        'transcription', 'hour', 0.2, 'contract',
			        '2026-07-01T00:00:00Z')`,
		"cost rate": `INSERT INTO provider_cost_rates
			VALUES ('c1', 'speechmatics', 'speechmatics-realtime-enhanced',
			        'transcription', 'hour', 0.43, 'catalog', '',
			        '2026-07-01T00:00:00Z', TRUE, TRUE)`,
		"active pricing rule": `INSERT INTO pricing_rules
			VALUES ('r1', 'translation', 'openai-compatible', 'gpt-test',
			        0.5, 'input_token', '', TRUE, 1, 'managed:r1',
			        'managed', 'catalog', '2026-07-31T00:00:00Z')`,
		"provider availability": `INSERT INTO provider_models
			VALUES ('openai-compatible', 'gpt-test', 'provider', TRUE)`,
		"model policy": `INSERT INTO model_policies
			VALUES ('translation', 'gpt-test', TRUE, TRUE, TRUE)`,
		"provider sync state": `INSERT INTO provider_model_sync_status
			VALUES ('openai-compatible', 'provider_confirmed')`,
	}
	for name, mutation := range mutations {
		t.Run(name, func(t *testing.T) {
			db := openBillingRevisionTestDB(t)
			before, err := billingRevisionFrom(t.Context(), db)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			after, err := billingRevisionFrom(t.Context(), db)
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Fatalf("revision did not change after %s", name)
			}
		})
	}
}

func TestPendingBillingConfigChangesOnlyProposedRetail(t *testing.T) {
	db := openBillingRevisionTestDB(t)
	if _, err := db.Exec(`
		UPDATE billing_config
		SET pending_config =
			'{"dp_per_usd":2,"default_markup_percent":100,"overrides":[]}'
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO provider_cost_rates
		VALUES (
			'c1', 'openai-compatible', 'gpt-test', 'llm', 'input_token',
			1, '` + DefaultCatalogVersion + `', '',
			'2026-07-31T00:00:00Z', TRUE, TRUE
		);
		INSERT INTO pricing_rules
		VALUES (
			'r1', 'translation', 'openai-compatible', 'gpt-test', 1.5,
			'input_token', '', TRUE, 1, 'managed:r1', 'managed',
			'` + DefaultCatalogVersion + `', '2026-07-31T00:00:00Z'
		)
	`); err != nil {
		t.Fatal(err)
	}

	catalog, err := getBillingCatalogFrom(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Config.DPPerUSD != 1 ||
		catalog.Config.DefaultMarkupPercent != 50 ||
		catalog.PendingConfig == nil ||
		catalog.PendingConfig.DPPerUSD != 2 {
		t.Fatalf("applied/pending config = %#v / %#v", catalog.Config, catalog.PendingConfig)
	}
	if len(catalog.Rates) != 1 {
		t.Fatalf("catalog rates = %#v", catalog.Rates)
	}
	rate := catalog.Rates[0]
	if rate.RetailDPPerUnit != 1.5 ||
		rate.EffectiveRetailDPPerUnit == nil ||
		*rate.EffectiveRetailDPPerUnit != 1.5 ||
		rate.ProposedRetailDPPerUnit != 4 {
		t.Fatalf("pending config leaked into actual pricing: %#v", rate)
	}
}

func TestPendingBillingConfigRejectsUnknownOrUnsafeFields(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte(`{"dp_per_usd":1,"default_markup_percent":50,"overrides":[],"typo":true}`),
		[]byte(`{"dp_per_usd":0,"default_markup_percent":50,"overrides":[]}`),
		[]byte(`{"dp_per_usd":1,"default_markup_percent":50,"overrides":[]} trailing`),
	} {
		if _, err := strictBillingConfigInput(raw); err == nil {
			t.Fatalf("unsafe pending config was accepted: %s", raw)
		}
	}
}

func TestProviderCostOverrideRejectsFutureEffectiveDate(t *testing.T) {
	input := ProviderCostOverrideInput{
		Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced",
		Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.2,
		EffectiveAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	}
	if _, err := validateProviderCostOverride(&input); err == nil ||
		!errors.Is(err, ErrInvalidBillingInput) ||
		!strings.Contains(err.Error(), "future") {
		t.Fatalf("future effective date error = %v", err)
	}
}

func TestProviderCostOverrideToleratesSmallClientClockSkew(t *testing.T) {
	input := ProviderCostOverrideInput{
		Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced",
		Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.2,
		EffectiveAt: time.Now().UTC().Add(time.Minute).Format(time.RFC3339),
	}
	effectiveAt, err := validateProviderCostOverride(&input)
	if err != nil {
		t.Fatalf("small client clock skew rejected: %v", err)
	}
	if effectiveAt.After(time.Now().UTC()) {
		t.Fatalf("small future effective time was not clamped: %v", effectiveAt)
	}
}

func TestDerivedRetailPriceRejectsStoredCostOverflow(t *testing.T) {
	rates := []CostRate{{
		Provider: "speechmatics", SKU: "overflow", UnitType: "hour",
		RetailDPPerUnit: maxStoredUsageCost, IsActive: true,
	}}
	err := validateDerivedRetailPrices(rates)
	if !errors.Is(err, ErrInvalidBillingInput) {
		t.Fatalf("derived retail overflow error = %v", err)
	}
}

func TestBillingConfigPathsRejectDerivedPriceOverflow(t *testing.T) {
	db := openBillingRevisionTestDB(t)
	if _, err := db.Exec(`
		INSERT INTO provider_cost_rates
			(id, provider, sku, service, unit_type, cost_per_unit_usd,
			 catalog_version, source_url, effective_at, is_builtin, is_active)
		VALUES
			('overflow-rate', 'speechmatics', 'overflow', 'transcription',
			 'hour', 0.65, 'test', '', NULL, TRUE, TRUE)
	`); err != nil {
		t.Fatalf("seed overflow rate: %v", err)
	}
	service := NewService(db)
	input := BillingConfigInput{
		DPPerUSD: 1_000_000, DefaultMarkupPercent: 100_000,
		Overrides: []MarkupOverride{},
	}
	if _, err := service.PreviewBillingConfig(t.Context(), input); !errors.Is(
		err,
		ErrInvalidBillingInput,
	) {
		t.Fatalf("preview overflow error = %v", err)
	}
	if _, err := service.UpdateBillingConfig(t.Context(), input, ""); !errors.Is(
		err,
		ErrInvalidBillingInput,
	) {
		t.Fatalf("save overflow error = %v", err)
	}
	var pendingSaved bool
	if err := db.QueryRow(`
		SELECT pending_config IS NOT NULL
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&pendingSaved); err != nil {
		t.Fatalf("load pending config after rejected save: %v", err)
	}
	if pendingSaved {
		t.Fatal("rejected billing configuration was persisted")
	}
}

func TestCatalogApplyRejectsStalePreviewBeforeDatabaseWork(t *testing.T) {
	service := &Service{}
	if _, err := service.ApplyBuiltinCatalog(
		t.Context(),
		"",
		"stale-version",
		billingApplyConfirmation,
		"",
	); !errors.Is(err, ErrBillingPreviewStale) {
		t.Fatalf("stale catalog apply error = %v", err)
	}
	if _, err := service.ApplyBuiltinCatalog(
		t.Context(),
		"",
		DefaultCatalogVersion,
		"wrong confirmation",
		"",
	); err == nil || !strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("confirmation error = %v", err)
	}
}

func TestDefaultRealtimeEnhancedCostPlusAndBYOKGoldenValues(t *testing.T) {
	var rate CostRate
	for _, candidate := range builtinCostRates {
		if candidate.SKU == "speechmatics-realtime-enhanced" &&
			candidate.UnitType == "hour" {
			rate = candidate
			break
		}
	}
	if rate.SKU == "" {
		t.Fatal("realtime enhanced public rate is missing")
	}
	rates := []CostRate{rate}
	cfg := BillingConfig{
		DPPerUSD: DefaultDPPerUSD, DefaultMarkupPercent: DefaultMarkupPercent,
	}
	applyRetailPrices(rates, &cfg)
	if got, want := rates[0].CostPerUnitUSD, 0.43; math.Abs(got-want) > 1e-12 {
		t.Fatalf("public upstream cost = %v, want %v", got, want)
	}
	if got, want := rates[0].RetailDPPerUnit, 0.645; math.Abs(got-want) > 1e-12 {
		t.Fatalf("platform-funded retail = %v DP, want %v", got, want)
	}
	if got, want := customerFundedServiceFee(
		rates[0].CostPerUnitUSD,
		&cfg,
		DefaultMarkupPercent,
	), 0.215; math.Abs(got-want) > 1e-12 {
		t.Fatalf("BYOK service fee = %v DP, want %v", got, want)
	}
}

func TestBuiltinGPT56RatesMatchOfficialPerTokenPrices(t *testing.T) {
	want := map[string]map[string]float64{
		"gpt-5.6-sol": {
			"input_token": 5e-6, "cached_input_token": 0.5e-6,
			"cache_write_token": 6.25e-6, "output_token": 30e-6,
		},
		"gpt-5.6-terra": {
			"input_token": 2e-6, "cached_input_token": 0.2e-6,
			"cache_write_token": 2.5e-6, "output_token": 12e-6,
		},
		"gpt-5.6-luna": {
			"input_token": 0.2e-6, "cached_input_token": 0.02e-6,
			"cache_write_token": 0.25e-6, "output_token": 1.2e-6,
		},
	}
	got := make(map[string]map[string]float64)
	for _, rate := range builtinCostRates {
		if _, exists := want[rate.SKU]; !exists {
			continue
		}
		if got[rate.SKU] == nil {
			got[rate.SKU] = make(map[string]float64)
		}
		got[rate.SKU][rate.UnitType] = rate.CostPerUnitUSD
	}
	for model, units := range want {
		for unit, expected := range units {
			if math.Abs(got[model][unit]-expected) > 1e-15 {
				t.Fatalf(
					"%s %s = %.12g, want %.12g",
					model,
					unit,
					got[model][unit],
					expected,
				)
			}
		}
	}
}

func TestCanonicalSKUCoversSpeechmaticsCompatibilityAliases(t *testing.T) {
	tests := map[string]string{
		"speechmatics":               "speechmatics-realtime-enhanced",
		"speechmatics-realtime":      "speechmatics-realtime-enhanced",
		"speechmatics-classic-token": "speechmatics-realtime-enhanced",
		"speechmatics-batch":         "speechmatics-batch-enhanced",
	}
	for alias, want := range tests {
		provider, got := CanonicalSKU("", alias, "transcription")
		if provider != "speechmatics" || got != want {
			t.Fatalf(
				"CanonicalSKU(%q) = %q/%q, want speechmatics/%q",
				alias,
				provider,
				got,
				want,
			)
		}
	}
}

func TestCalculateProviderUpstreamResolvesClassicAliasAndFailsClosed(t *testing.T) {
	rates := []CostRate{{
		Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced",
		Service: "transcription", UnitType: "hour",
		CostPerUnitUSD: 0.43, IsActive: true,
	}}
	cfg := BillingConfig{
		DPPerUSD: 1, DefaultMarkupPercent: 50,
	}
	upstream, applied, markup, err := calculateProviderUpstream(&UsageRecord{
		Action: "transcription", Model: "speechmatics-classic-token",
		Quantity: 60,
	}, rates, &cfg)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(upstream-0.43) > 1e-12 || applied["hour"] != 0.43 || markup != 50 {
		t.Fatalf(
			"resolved upstream=%v applied=%v markup=%v",
			upstream,
			applied,
			markup,
		)
	}
	_, _, _, err = calculateProviderUpstream(&UsageRecord{
		Action: "transcription", Model: "unknown-provider-sku", Quantity: 1,
	}, rates, &cfg)
	if !errors.Is(err, ErrProviderCostNotFound) {
		t.Fatalf("unpriced provider error = %v, want ErrProviderCostNotFound", err)
	}
}

func TestSpeechmaticsAddonDurationIsNeverEstimatedAsZero(t *testing.T) {
	rates := []CostRate{{
		Provider: "speechmatics", SKU: "speechmatics-translation",
		Service: "translation_addon", UnitType: "hour",
		CostPerUnitUSD: 0.65, IsActive: true,
	}}
	upstream, applied, _, err := calculateProviderUpstream(&UsageRecord{
		Action: "translation", Model: "speechmatics-translation", Quantity: 60,
	}, rates, &BillingConfig{DPPerUSD: 1, DefaultMarkupPercent: 50})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(upstream-0.65) > 1e-12 || applied["hour"] != 0.65 {
		t.Fatalf("add-on upstream=%v rates=%v, want 0.65/hour", upstream, applied)
	}
}

func TestSettlementUsesReservationPricingSnapshotAndBillingSKU(t *testing.T) {
	raw, err := json.Marshal(usagePricingSnapshot{
		SnapshotVersion: 2,
		CatalogVersion:  "reservation-version",
		DPPerUSD:        1,
		MarkupPercent:   50,
		Provider:        "speechmatics",
		CanonicalSKU:    "speechmatics-realtime-enhanced",
		Action:          "transcription",
		Attribution:     AttributionProviderPriced,
		RatesUSD:        map[string]float64{"hour": 0.43},
		RetailRatesDP:   map[string]float64{"hour": 0.645},
	})
	if err != nil {
		t.Fatal(err)
	}
	actual := &UsageRecord{
		Action: "transcription",
		// A provider response may report a dated/resolved model. It must not
		// alter the snapshotted billing SKU.
		Model:    "speechmatics-response-model-2026-07-31",
		Quantity: 60,
	}
	platform, err := resolveUsageCostFromSnapshot(
		raw,
		actual,
		AttributionProviderPriced,
	)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(platform.ChargeDP-0.645) > 1e-12 ||
		math.Abs(platform.UpstreamCostUSD-0.43) > 1e-12 ||
		math.Abs(platform.ServiceFeeDP-0.215) > 1e-12 {
		t.Fatalf("platform snapshot settlement = %#v", platform)
	}
	byokRaw, err := json.Marshal(usagePricingSnapshot{
		SnapshotVersion: 2,
		CatalogVersion:  "reservation-version",
		DPPerUSD:        1,
		MarkupPercent:   50,
		Provider:        "speechmatics",
		CanonicalSKU:    "speechmatics-realtime-enhanced",
		Action:          "transcription",
		Attribution:     AttributionBYOK,
		RatesUSD:        map[string]float64{"hour": 0.43},
		RetailRatesDP:   map[string]float64{"hour": 999},
	})
	if err != nil {
		t.Fatal(err)
	}
	byok, err := resolveUsageCostFromSnapshot(byokRaw, actual, AttributionBYOK)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(byok.ChargeDP-0.215) > 1e-12 ||
		byok.UpstreamCostUSD != 0 ||
		math.Abs(byok.ServiceFeeDP-0.215) > 1e-12 {
		t.Fatalf("BYOK snapshot settlement = %#v", byok)
	}
}

func TestPricingSnapshotPreservesNewRequestBillingPolicy(t *testing.T) {
	raw := annotatePricingSnapshot(
		[]byte(`{"snapshot_version":2}`),
		0,
		false,
		true,
	)
	enabled, ok := billingPolicyFromPricingSnapshot(raw)
	if !ok || enabled {
		t.Fatalf("disabled reservation policy = %v/%v, want false/true", enabled, ok)
	}
	allowNegative, ok := negativeBalancePolicyFromPricingSnapshot(raw)
	if !ok || !allowNegative {
		t.Fatalf("negative-balance policy = %v/%v, want true/true", allowNegative, ok)
	}
	raw = annotatePricingSnapshot(raw, 12.5)
	enabled, ok = billingPolicyFromPricingSnapshot(raw)
	if !ok || enabled {
		t.Fatalf("settlement overwrote reservation policy = %v/%v", enabled, ok)
	}
	allowNegative, ok = negativeBalancePolicyFromPricingSnapshot(raw)
	if !ok || !allowNegative {
		t.Fatalf(
			"settlement overwrote negative-balance policy = %v/%v",
			allowNegative,
			ok,
		)
	}
	if _, ok := billingPolicyFromPricingSnapshot(
		[]byte(`{"snapshot_version":2}`),
	); ok {
		t.Fatal("legacy snapshot unexpectedly has an explicit billing policy")
	}
	if _, ok := negativeBalancePolicyFromPricingSnapshot(
		[]byte(`{"snapshot_version":2}`),
	); ok {
		t.Fatal("legacy snapshot unexpectedly has an explicit balance policy")
	}
}

func TestPriceUsageUsesOneImmutableCatalogView(t *testing.T) {
	provider := "provider-a"
	model := "shared-model"
	record := &UsageRecord{
		Action: "chat", Provider: provider, Model: model, InputTokens: 1,
	}
	for _, test := range []struct {
		name         string
		upstreamRate float64
		retailRate   float64
	}{
		{name: "version one", upstreamRate: 1, retailRate: 1.5},
		{name: "version two", upstreamRate: 2, retailRate: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := &usagePricingView{
				Config: BillingConfig{
					DPPerUSD: 1, DefaultMarkupPercent: 50,
					CatalogVersion: test.name, PricingState: pricingStateManaged,
				},
				Rates: []CostRate{{
					Provider: provider, SKU: model, Service: "llm",
					UnitType: "input_token", CostPerUnitUSD: test.upstreamRate,
					IsActive: true,
				}},
				Rules: []PricingRule{{
					RuleType: "chat", Provider: &provider, Model: &model,
					UnitType: "input_token", PricePerUnit: test.retailRate,
					IsActive: true,
				}},
			}
			breakdown, err := priceUsageWithView(record, view)
			if err != nil {
				t.Fatal(err)
			}
			if breakdown.ChargeDP != test.retailRate ||
				breakdown.UpstreamCostUSD != test.upstreamRate ||
				breakdown.ServiceFeeDP != test.retailRate-test.upstreamRate {
				t.Fatalf("inconsistent breakdown = %#v", breakdown)
			}
			var snapshot usagePricingSnapshot
			if err := json.Unmarshal(breakdown.PricingSnapshot, &snapshot); err != nil {
				t.Fatal(err)
			}
			if snapshot.RatesUSD["input_token"] != test.upstreamRate ||
				snapshot.RetailRatesDP["input_token"] != test.retailRate ||
				snapshot.CatalogVersion != test.name {
				t.Fatalf("mixed pricing snapshot = %#v", snapshot)
			}
		})
	}
}

func TestLegacyPricingSnapshotCannotBeRepricedFromCurrentCatalog(t *testing.T) {
	_, err := resolveUsageCostFromSnapshot(
		[]byte(`{"mode":"legacy","rates_usd":{}}`),
		&UsageRecord{Action: "transcription", Quantity: 60},
		AttributionLegacyUnknown,
	)
	if !errors.Is(err, ErrPricingSnapshotIncomplete) {
		t.Fatalf("legacy snapshot error = %v", err)
	}
}

func TestQuotaOnlyUsageIsKnownZeroWithoutProviderCatalog(t *testing.T) {
	service := &Service{}
	breakdown, err := service.resolveUsageCost(t.Context(), &UsageRecord{
		Action: "rag_query", Quantity: 1,
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if breakdown.ChargeDP != 0 ||
		breakdown.UpstreamCostUSD != 0 ||
		breakdown.ServiceFeeDP != 0 ||
		breakdown.Attribution != AttributionNonProvider {
		t.Fatalf("quota-only breakdown = %#v, want known zero", breakdown)
	}
}

func TestCatalogPricingStateDistinguishesActualFromProposed(t *testing.T) {
	if got := catalogPricingState(&BillingConfig{
		CatalogVersion: DefaultCatalogVersion,
		PricingState:   pricingStateLegacy,
	}); got != PricingStateLegacy {
		t.Fatalf("legacy state = %q", got)
	}
	if got := catalogPricingState(&BillingConfig{
		CatalogVersion: "old",
		PricingState:   pricingStateManaged,
	}); got != PricingStateOutdated {
		t.Fatalf("outdated state = %q", got)
	}
	if got := catalogPricingState(&BillingConfig{
		CatalogVersion: DefaultCatalogVersion,
		PricingState:   pricingStateManaged,
	}); got != PricingStateCurrent {
		t.Fatalf("current state = %q", got)
	}
}

func TestBYOKRequiresCurrentAppliedCatalog(t *testing.T) {
	provider := "speechmatics"
	model := "speechmatics-realtime-enhanced"
	record := &UsageRecord{
		Action: "transcription", Provider: provider, Model: model,
		Quantity: 60, CustomerFunded: true,
	}
	baseView := usagePricingView{
		Config: BillingConfig{
			DPPerUSD: 1, DefaultMarkupPercent: 50,
		},
		Rates: []CostRate{{
			Provider: provider, SKU: model, Service: "transcription",
			UnitType: "hour", CostPerUnitUSD: 0.43, IsActive: true,
		}},
	}
	for _, test := range []struct {
		name    string
		state   string
		version string
		wantErr bool
	}{
		{
			name: "legacy", state: pricingStateLegacy,
			version: "legacy-compatible", wantErr: true,
		},
		{
			name: "outdated", state: pricingStateManaged,
			version: "older", wantErr: true,
		},
		{
			name: "current", state: pricingStateManaged,
			version: DefaultCatalogVersion,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			view := baseView
			view.Config.PricingState = test.state
			view.Config.CatalogVersion = test.version
			breakdown, err := priceUsageWithView(record, &view)
			if test.wantErr {
				if !errors.Is(err, ErrPricingCatalogNotApplied) {
					t.Fatalf("BYOK catalog error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(breakdown.ChargeDP-0.215) > 1e-12 ||
				breakdown.UpstreamCostUSD != 0 ||
				math.Abs(breakdown.ServiceFeeDP-0.215) > 1e-12 {
				t.Fatalf("current BYOK charge = %#v", breakdown)
			}
		})
	}
}

func TestBillingResetOnlyRestoresAvailabilityBeforeFirstProviderSync(t *testing.T) {
	tests := []struct {
		status string
		want   bool
	}{
		{status: providerSyncUnverified, want: true},
		{status: providerSyncConfirmed, want: false},
		{status: providerSyncUnavailable, want: false},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			if got := shouldRestoreBuiltinAvailability(test.status); got != test.want {
				t.Fatalf(
					"shouldRestoreBuiltinAvailability(%q) = %v, want %v",
					test.status,
					got,
					test.want,
				)
			}
		})
	}
}

func TestHistoricalEstimateCoverageRequiresMeasurableUsage(t *testing.T) {
	for name, rec := range map[string]*UsageRecord{
		"nil":           nil,
		"model only":    {Action: "translation", Model: "gpt-5"},
		"zero duration": {Action: "transcription", Model: "speechmatics"},
	} {
		if hasMeasurableProviderUsage(rec) {
			t.Fatalf("%s was incorrectly estimate-eligible", name)
		}
	}
	for name, rec := range map[string]*UsageRecord{
		"duration": {Action: "transcription", Quantity: 1},
		"input":    {Action: "translation", InputTokens: 1},
		"cached":   {Action: "translation", CachedInputTokens: 1},
		"write":    {Action: "translation", CacheWriteTokens: 1},
		"output":   {Action: "translation", OutputTokens: 1},
	} {
		if !hasMeasurableProviderUsage(rec) {
			t.Fatalf("%s was incorrectly estimate-ineligible", name)
		}
	}
}

func TestReliabilityMigrationNeverTreatsLegacyFeeOnlyRowsAsExact(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate catalog test source")
	}
	migrationPath := filepath.Join(
		filepath.Dir(filename),
		"..",
		"..",
		"migrations",
		"020_admin_billing_reliability.sql",
	)
	raw, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	sqlText := string(raw)
	if strings.Contains(sqlText, "service_fee_dp <> 0") {
		t.Fatal("fee-only legacy rows must not be classified as exact provider cost")
	}
	for _, forbiddenRewrite := range []string{
		"SET upstream_cost_usd",
		"SET service_fee_dp",
		"SET cost =",
	} {
		if strings.Contains(sqlText, forbiddenRewrite) {
			t.Fatalf("migration rewrites immutable ledger amount via %q", forbiddenRewrite)
		}
	}
	if !strings.Contains(sqlText, "pricing_snapshot->>'attribution' = 'provider_priced'") ||
		!strings.Contains(sqlText, "pricing_snapshot->'rates_usd' <> '{}'::jsonb") {
		t.Fatal("migration lacks explicit exact-cost evidence checks")
	}
	if !strings.Contains(sqlText, "ADD COLUMN IF NOT EXISTS provider VARCHAR(60)") ||
		!strings.Contains(sqlText, "idx_pricing_rules_active_provider_model") {
		t.Fatal("migration lacks provider-aware pricing rule identity")
	}
	if !strings.Contains(
		sqlText,
		"ADD COLUMN IF NOT EXISTS settled_at TIMESTAMP WITH TIME ZONE",
	) {
		t.Fatal("migration lacks one-shot usage settlement state")
	}
	if !strings.Contains(sqlText, "ADD COLUMN IF NOT EXISTS pending_config JSONB") {
		t.Fatal("migration lacks applied-versus-pending billing state")
	}
	if !strings.Contains(sqlText, "source IN ('provider', 'builtin+provider')") {
		t.Fatal("migration discards evidence of a successful legacy provider sync")
	}
}

func TestUserUsageItemDoesNotExposeUpstreamPricing(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(UserUsageItem{
		ID: "usage-1", CostDP: 2.5, UpstreamCostUSD: 1.25, ServiceFeeDP: 1.25,
		PricingSnapshot: map[string]any{"rates_usd": 1.25},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, sensitiveField := range []string{
		"upstream_cost_usd", "service_fee_dp", "pricing_snapshot", "rates_usd",
	} {
		if strings.Contains(body, sensitiveField) {
			t.Fatalf("user usage response leaked %q: %s", sensitiveField, body)
		}
	}
	if !strings.Contains(body, `"cost_dp":2.5`) {
		t.Fatalf("retail cost missing from user usage response: %s", body)
	}
}
