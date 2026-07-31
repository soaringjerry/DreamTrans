package billing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultCatalogVersion = "2026-07-31"
	DefaultDPPerUSD       = 1.0
	DefaultMarkupPercent  = 50.0
)

const (
	openAIPriceSource        = "https://developers.openai.com/api/docs/models"
	speechmaticsPriceSource  = "https://www.speechmatics.com/pricing"
	pricingStateLegacy       = "legacy_active"
	pricingStateManaged      = "managed_active"
	PricingStateLegacy       = "legacy_active"
	PricingStateCurrent      = "managed_current"
	PricingStateOutdated     = "managed_outdated"
	billingApplyConfirmation = "\u5e94\u7528\u6210\u672c\u66f4\u65b0"
	billingResetConfirmation = "\u91cd\u7f6e\u8ba1\u8d39"
	providerSyncUnverified   = "builtin_unverified"
	providerSyncConfirmed    = "provider_confirmed"
	providerSyncUnavailable  = "temporarily_unavailable"
)

var defaultCatalogEffectiveAt = time.Date(
	2026, time.July, 31, 0, 0, 0, 0, time.UTC,
)

type CostRate struct {
	ID                       string             `json:"id,omitempty"`
	Provider                 string             `json:"provider"`
	SKU                      string             `json:"sku"`
	Service                  string             `json:"service"`
	UnitType                 string             `json:"unit_type"`
	CostPerUnitUSD           float64            `json:"cost_per_unit_usd"`
	PublicCostPerUnitUSD     float64            `json:"public_cost_per_unit_usd"`
	EffectiveCostPerUnitUSD  float64            `json:"effective_cost_per_unit_usd"`
	CostSource               string             `json:"cost_source"`
	CostSourceLabel          string             `json:"cost_source_label"`
	CostOverrideID           string             `json:"cost_override_id,omitempty"`
	RetailDPPerUnit          float64            `json:"retail_dp_per_unit"`
	ProposedRetailDPPerUnit  float64            `json:"proposed_retail_dp_per_unit"`
	EffectiveRetailDPPerUnit *float64           `json:"effective_retail_dp_per_unit"`
	EffectiveRetailByAction  map[string]float64 `json:"effective_retail_by_action,omitempty"`
	MarkupPercent            float64            `json:"markup_percent"`
	GrossMargin              float64            `json:"gross_margin_percent"`
	CatalogVersion           string             `json:"catalog_version"`
	SourceURL                string             `json:"source_url"`
	EffectiveAt              string             `json:"effective_at,omitempty"`
	PublicEffectiveAt        string             `json:"public_effective_at,omitempty"`
	IsBuiltin                bool               `json:"is_builtin"`
	IsActive                 bool               `json:"is_active"`
	OverrideSource           string             `json:"override_source"`
}

type MarkupOverride struct {
	ScopeType     string  `json:"scope_type"`
	ScopeKey      string  `json:"scope_key"`
	MarkupPercent float64 `json:"markup_percent"`
}

type BillingConfig struct {
	DPPerUSD             float64          `json:"dp_per_usd"`
	DefaultMarkupPercent float64          `json:"default_markup_percent"`
	CatalogVersion       string           `json:"catalog_version"`
	PricingState         string           `json:"pricing_state"`
	UpdatedAt            string           `json:"updated_at,omitempty"`
	Overrides            []MarkupOverride `json:"overrides"`
}

type BillingCatalog struct {
	BuiltinVersion   string              `json:"builtin_version"`
	InstalledVersion string              `json:"installed_version"`
	Config           BillingConfig       `json:"config"`
	PendingConfig    *BillingConfigInput `json:"pending_config,omitempty"`
	Rates            []CostRate          `json:"rates"`
	HasUpdate        bool                `json:"has_update"`
	PricingState     string              `json:"pricing_state"`
}

type BillingConfigInput struct {
	DPPerUSD             float64          `json:"dp_per_usd"`
	DefaultMarkupPercent float64          `json:"default_markup_percent"`
	Overrides            []MarkupOverride `json:"overrides"`
}

type ManualModelCostInput struct {
	ModelID                  string  `json:"model_id"`
	Service                  string  `json:"service"`
	InputPerMillionUSD       float64 `json:"input_per_million_usd"`
	CachedInputPerMillionUSD float64 `json:"cached_input_per_million_usd"`
	CacheWritePerMillionUSD  float64 `json:"cache_write_per_million_usd"`
	OutputPerMillionUSD      float64 `json:"output_per_million_usd"`
}

type ProviderCostOverrideInput struct {
	Provider       string  `json:"provider"`
	SKU            string  `json:"sku"`
	Service        string  `json:"service"`
	UnitType       string  `json:"unit_type"`
	CostPerUnitUSD float64 `json:"cost_per_unit_usd"`
	SourceLabel    string  `json:"source_label"`
	EffectiveAt    string  `json:"effective_at,omitempty"`
}

type BillingPreview struct {
	Config          BillingConfig `json:"config"`
	Rates           []CostRate    `json:"rates"`
	Added           int           `json:"added"`
	Updated         int           `json:"updated"`
	Disabled        int           `json:"disabled"`
	Confirmation    string        `json:"confirmation"`
	CatalogVersion  string        `json:"catalog_version,omitempty"`
	TargetVersion   string        `json:"target_version,omitempty"`
	CurrentRevision string        `json:"current_revision,omitempty"`
}

type BillingAnalytics struct {
	UpstreamCostUSD                float64 `json:"upstream_cost_usd"`
	ServiceFeeDP                   float64 `json:"service_fee_dp"`
	RetailDP                       float64 `json:"retail_dp"`
	UsageCount                     int64   `json:"usage_count"`
	AttributedUsageCount           int64   `json:"attributed_usage_count"`
	LegacyUnknownCount             int64   `json:"legacy_unknown_count"`
	LegacyUnknownRetailDP          float64 `json:"legacy_unknown_retail_dp"`
	BYOKUsageCount                 int64   `json:"byok_usage_count"`
	BYOKServiceFeeDP               float64 `json:"byok_service_fee_dp"`
	NonProviderUsageCount          int64   `json:"non_provider_usage_count"`
	UnpricedUsageCount             int64   `json:"unpriced_usage_count"`
	EstimateEligibleCount          int64   `json:"estimate_eligible_count"`
	EstimatedLegacyUpstreamCostUSD float64 `json:"estimated_legacy_upstream_cost_usd"`
	EstimatedLegacyServiceFeeDP    float64 `json:"estimated_legacy_service_fee_dp"`
	EstimateCatalogVersion         string  `json:"estimate_catalog_version"`
	EstimateAvailable              bool    `json:"estimate_available"`
	EstimateError                  string  `json:"estimate_error,omitempty"`
}

type UserBillingSummary struct {
	Dreampoints            float64 `json:"dreampoints"`
	DreampointsUsed        float64 `json:"dreampoints_used"`
	EstimatedRealtimeHours float64 `json:"estimated_realtime_hours"`
	RealtimeRateDPPerHour  float64 `json:"realtime_rate_dp_per_hour"`
	EstimateProfile        string  `json:"estimate_profile"`
}

type UserUsageItem struct {
	ID                string         `json:"id"`
	SessionID         *string        `json:"session_id,omitempty"`
	Action            string         `json:"action"`
	Model             string         `json:"model"`
	Quantity          float64        `json:"quantity"`
	InputTokens       int            `json:"input_tokens"`
	CachedInputTokens int            `json:"cached_input_tokens"`
	CacheWriteTokens  int            `json:"cache_write_tokens"`
	OutputTokens      int            `json:"output_tokens"`
	UpstreamCostUSD   float64        `json:"-"`
	ServiceFeeDP      float64        `json:"-"`
	CostDP            float64        `json:"cost_dp"`
	PricingSnapshot   map[string]any `json:"-"`
	CreatedAt         string         `json:"created_at"`
}

var builtinCostRates = []CostRate{
	// OpenAI-compatible token rates are stored per token. Public pricing is per
	// million tokens, hence the 1e-6 conversion.
	{Provider: "openai-compatible", SKU: "gpt-5.6-sol", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 5.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-sol", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.50e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-sol", Service: "llm", UnitType: "cache_write_token", CostPerUnitUSD: 6.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-sol", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 30.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 2.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.20e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "cache_write_token", CostPerUnitUSD: 2.50e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 12.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 0.20e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.02e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "cache_write_token", CostPerUnitUSD: 0.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 1.20e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 1.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.125e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 10.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5-mini", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 0.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5-mini", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.025e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5-mini", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 2.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5-nano", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 0.05e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5-nano", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.005e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5-nano", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 0.40e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-4o", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 2.50e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-4o", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 1.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-4o", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 10.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-4o-mini", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 0.15e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-4o-mini", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.075e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-4o-mini", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 0.60e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "text-embedding-3-small", Service: "embedding", UnitType: "input_token", CostPerUnitUSD: 0.02e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "text-embedding-3-large", Service: "embedding", UnitType: "input_token", CostPerUnitUSD: 0.13e-6, SourceURL: openAIPriceSource},

	// Speechmatics public prices are hourly.
	{Provider: "speechmatics", SKU: "speechmatics-batch-melia-1", Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.129, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-batch-standard", Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.24, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-batch-enhanced", Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.40, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-realtime-standard", Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.24, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-realtime-enhanced", Service: "transcription", UnitType: "hour", CostPerUnitUSD: 0.43, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-translation", Service: "translation_addon", UnitType: "hour", CostPerUnitUSD: 0.65, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-summary", Service: "summary_addon", UnitType: "hour", CostPerUnitUSD: 0.12, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-chapters", Service: "chapters_addon", UnitType: "hour", CostPerUnitUSD: 0.40, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-sentiment", Service: "sentiment_addon", UnitType: "hour", CostPerUnitUSD: 0.12, SourceURL: speechmaticsPriceSource},
	{Provider: "speechmatics", SKU: "speechmatics-topics", Service: "topics_addon", UnitType: "hour", CostPerUnitUSD: 0.20, SourceURL: speechmaticsPriceSource},
}

func validateBillingConfig(input BillingConfigInput) error {
	if input.DPPerUSD <= 0 || input.DPPerUSD > 1_000_000 ||
		math.IsNaN(input.DPPerUSD) || math.IsInf(input.DPPerUSD, 0) {
		return fmt.Errorf("dp_per_usd must be a positive finite number")
	}
	if input.DefaultMarkupPercent < 0 || input.DefaultMarkupPercent > 100_000 ||
		math.IsNaN(input.DefaultMarkupPercent) || math.IsInf(input.DefaultMarkupPercent, 0) {
		return fmt.Errorf("default_markup_percent must be between 0 and 100000")
	}
	seen := make(map[string]bool)
	for i := range input.Overrides {
		override := &input.Overrides[i]
		override.ScopeType = strings.TrimSpace(override.ScopeType)
		override.ScopeKey = strings.TrimSpace(override.ScopeKey)
		if override.ScopeType != "provider" && override.ScopeType != "category" && override.ScopeType != "sku" {
			return fmt.Errorf("unsupported override scope_type")
		}
		if override.ScopeKey == "" || len(override.ScopeKey) > 260 {
			return fmt.Errorf("invalid override scope_key")
		}
		if override.MarkupPercent < 0 || override.MarkupPercent > 100_000 ||
			math.IsNaN(override.MarkupPercent) || math.IsInf(override.MarkupPercent, 0) {
			return fmt.Errorf("override markup_percent must be between 0 and 100000")
		}
		key := override.ScopeType + "\x00" + override.ScopeKey
		if seen[key] {
			return fmt.Errorf("duplicate override")
		}
		seen[key] = true
	}
	return nil
}

func (s *Service) EnsureBuiltinCatalog(ctx context.Context) error {
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return err
	}
	if err := upsertBuiltinCatalogTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertBuiltinCatalogTx(ctx context.Context, tx *sql.Tx) error {
	for i := range builtinCostRates {
		rate := &builtinCostRates[i]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_cost_rates
				(provider, sku, service, unit_type, cost_per_unit_usd,
				 catalog_version, source_url, effective_at, is_builtin, is_active)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, TRUE, TRUE)
			ON CONFLICT (provider, sku, service, unit_type) DO UPDATE SET
				cost_per_unit_usd = EXCLUDED.cost_per_unit_usd,
				catalog_version = EXCLUDED.catalog_version,
				source_url = EXCLUDED.source_url,
				effective_at = EXCLUDED.effective_at,
				is_active = TRUE,
				updated_at = NOW()
			WHERE provider_cost_rates.is_builtin = TRUE
		`, rate.Provider, rate.SKU, rate.Service, rate.UnitType,
			rate.CostPerUnitUSD, DefaultCatalogVersion, rate.SourceURL,
			defaultCatalogEffectiveAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetBillingCatalog(ctx context.Context) (*BillingCatalog, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	catalog, err := getBillingCatalogFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return catalog, nil
}

func catalogPricingState(cfg BillingConfig) string {
	if cfg.PricingState != pricingStateManaged {
		return PricingStateLegacy
	}
	if cfg.CatalogVersion != DefaultCatalogVersion {
		return PricingStateOutdated
	}
	return PricingStateCurrent
}

func (s *Service) getBillingConfig(ctx context.Context) (BillingConfig, error) {
	return getBillingConfigFrom(ctx, s.db)
}

type catalogQueryer interface {
	pricingRuleQueryer
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// lockBillingRevisionTx is the cross-process serialization point for every
// mutation that can change the effective or previewed billing state. A
// portable no-op UPDATE is used so the same invariant is testable with SQLite
// while PostgreSQL takes an exclusive row lock until transaction completion.
func lockBillingRevisionTx(ctx context.Context, tx *sql.Tx) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE billing_config
		SET singleton = singleton
		WHERE singleton = TRUE
	`)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("billing configuration singleton is missing")
	}
	return nil
}

func strictBillingConfigInput(raw []byte) (*BillingConfigInput, error) {
	if len(bytes.TrimSpace(raw)) == 0 ||
		bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input BillingConfigInput
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("invalid pending billing configuration: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid pending billing configuration")
	}
	if err := validateBillingConfig(input); err != nil {
		return nil, fmt.Errorf("invalid pending billing configuration: %w", err)
	}
	if input.Overrides == nil {
		input.Overrides = []MarkupOverride{}
	}
	return &input, nil
}

func getPendingBillingConfigFrom(
	ctx context.Context,
	queryer catalogQueryer,
) (*BillingConfigInput, error) {
	var raw []byte
	if err := queryer.QueryRowContext(ctx, `
		SELECT pending_config
		FROM billing_config
		WHERE singleton = TRUE
	`).Scan(&raw); err != nil {
		return nil, err
	}
	return strictBillingConfigInput(raw)
}

func getBillingCatalogFrom(
	ctx context.Context,
	queryer catalogQueryer,
) (*BillingCatalog, error) {
	cfg, err := getBillingConfigFrom(ctx, queryer)
	if err != nil {
		return nil, err
	}
	pending, err := getPendingBillingConfigFrom(ctx, queryer)
	if err != nil {
		return nil, err
	}
	rates, err := getCostRatesFrom(ctx, queryer)
	if err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, queryer)
	if err != nil {
		return nil, err
	}
	applyRetailPrices(rates, cfg)
	if pending != nil {
		pendingConfig := cfg
		pendingConfig.DPPerUSD = pending.DPPerUSD
		pendingConfig.DefaultMarkupPercent = pending.DefaultMarkupPercent
		pendingConfig.Overrides = pending.Overrides
		proposed := append([]CostRate(nil), rates...)
		applyRetailPrices(proposed, pendingConfig)
		proposedByKey := make(map[string]float64, len(proposed))
		for i := range proposed {
			proposedByKey[costRateKey(&proposed[i])] =
				proposed[i].ProposedRetailDPPerUnit
		}
		for i := range rates {
			rates[i].ProposedRetailDPPerUnit =
				proposedByKey[costRateKey(&rates[i])]
		}
	}
	applyActualRetailPricesFromRules(rates, rules)
	state := catalogPricingState(cfg)
	return &BillingCatalog{
		BuiltinVersion: DefaultCatalogVersion, InstalledVersion: cfg.CatalogVersion,
		Config: cfg, PendingConfig: pending, Rates: rates, PricingState: state,
		HasUpdate: state != PricingStateCurrent,
	}, nil
}

type billingRevisionQuery struct {
	name string
	sql  string
}

var billingRevisionQueries = []billingRevisionQuery{
	{
		name: "billing_config",
		sql: `SELECT singleton, dp_per_usd, default_markup_percent,
		             catalog_version, pricing_state, pending_config,
		             updated_at, updated_by
		      FROM billing_config
		      WHERE singleton = TRUE`,
	},
	{
		name: "billing_markup_overrides",
		sql: `SELECT scope_type, scope_key, markup_percent
		      FROM billing_markup_overrides
		      ORDER BY scope_type, scope_key`,
	},
	{
		name: "provider_cost_overrides",
		sql: `SELECT provider, sku, service, unit_type, cost_per_unit_usd,
		             source_label, effective_at
		      FROM provider_cost_overrides
		      ORDER BY provider, sku, service, unit_type`,
	},
	{
		name: "provider_cost_rates",
		sql: `SELECT provider, sku, service, unit_type, cost_per_unit_usd,
		             catalog_version, source_url, effective_at,
		             is_builtin, is_active
		      FROM provider_cost_rates
		      ORDER BY provider, sku, service, unit_type`,
	},
	{
		name: "pricing_rules",
		sql: `SELECT id, rule_type, provider, model, price_per_unit,
		             unit_type, description, priority, managed_key,
		             source, catalog_version
		      FROM pricing_rules
		      WHERE is_active = TRUE
		      ORDER BY id`,
	},
	{
		name: "provider_models",
		sql: `SELECT provider, model_id, source, provider_available
		      FROM provider_models
		      ORDER BY provider, model_id`,
	},
	{
		name: "model_policies",
		sql: `SELECT purpose, model_id, is_approved, is_default, cost_confirmed
		      FROM model_policies
		      ORDER BY purpose, model_id`,
	},
	{
		name: "provider_model_sync_status",
		sql: `SELECT provider, status
		      FROM provider_model_sync_status
		      ORDER BY provider`,
	},
}

func writeBillingRevisionValue(sum hash.Hash, value any) {
	var canonical string
	switch typed := value.(type) {
	case nil:
		canonical = "<null>"
	case []byte:
		canonical = string(typed)
	case string:
		canonical = typed
	case bool:
		canonical = strconv.FormatBool(typed)
	case int64:
		canonical = strconv.FormatInt(typed, 10)
	case float64:
		canonical = strconv.FormatFloat(typed, 'g', -1, 64)
	case time.Time:
		canonical = typed.UTC().Format(time.RFC3339Nano)
	default:
		canonical = fmt.Sprint(typed)
	}
	_, _ = sum.Write([]byte(strconv.Itoa(len(canonical))))
	_, _ = sum.Write([]byte{':'})
	_, _ = sum.Write([]byte(canonical))
	_, _ = sum.Write([]byte{'\n'})
}

func billingRevisionFrom(
	ctx context.Context,
	queryer catalogQueryer,
) (string, error) {
	sum := sha256.New()
	for _, query := range billingRevisionQueries {
		writeBillingRevisionValue(sum, query.name)
		rows, err := queryer.QueryContext(ctx, query.sql)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return "", err
		}
		for rows.Next() {
			values := make([]any, len(columns))
			destinations := make([]any, len(columns))
			for i := range values {
				destinations[i] = &values[i]
			}
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				return "", err
			}
			writeBillingRevisionValue(sum, "row")
			for _, value := range values {
				writeBillingRevisionValue(sum, value)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return "", err
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", sum.Sum(nil)), nil
}

func requireBillingRevision(expected, actual string) error {
	expected = strings.TrimSpace(expected)
	actual = strings.TrimSpace(actual)
	if expected == "" || actual == "" || expected != actual {
		return fmt.Errorf(
			"%w; reload the preview before confirming",
			ErrBillingPreviewStale,
		)
	}
	return nil
}

func getBillingConfigFrom(
	ctx context.Context,
	queryer catalogQueryer,
) (BillingConfig, error) {
	var cfg BillingConfig
	if err := queryer.QueryRowContext(ctx, `
		SELECT dp_per_usd, default_markup_percent, catalog_version,
		       pricing_state, updated_at
		FROM billing_config WHERE singleton = TRUE
	`).Scan(
		&cfg.DPPerUSD,
		&cfg.DefaultMarkupPercent,
		&cfg.CatalogVersion,
		&cfg.PricingState,
		&cfg.UpdatedAt,
	); err != nil {
		return cfg, err
	}
	rows, err := queryer.QueryContext(ctx, `
		SELECT scope_type, scope_key, markup_percent
		FROM billing_markup_overrides
		ORDER BY CASE scope_type WHEN 'provider' THEN 1 WHEN 'category' THEN 2 ELSE 3 END,
		         scope_key
	`)
	if err != nil {
		return cfg, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var override MarkupOverride
		if err := rows.Scan(&override.ScopeType, &override.ScopeKey, &override.MarkupPercent); err != nil {
			return cfg, err
		}
		cfg.Overrides = append(cfg.Overrides, override)
	}
	return cfg, rows.Err()
}

func (s *Service) getCostRates(ctx context.Context) ([]CostRate, error) {
	return getCostRatesFrom(ctx, s.db)
}

func getCostRatesFrom(
	ctx context.Context,
	queryer catalogQueryer,
) ([]CostRate, error) {
	rows, err := queryer.QueryContext(ctx, `
		SELECT r.id, r.provider, r.sku, r.service, r.unit_type,
		       r.cost_per_unit_usd,
		       COALESCE(o.cost_per_unit_usd, r.cost_per_unit_usd),
		       CASE
		         WHEN o.id IS NOT NULL THEN 'contract_override'
		         WHEN r.is_builtin THEN 'public_catalog'
		         ELSE 'manual'
		       END,
		       COALESCE(
		         o.source_label,
		         CASE WHEN r.is_builtin THEN 'official_public_price' ELSE 'manual' END
		       ),
		       COALESCE(CAST(o.id AS TEXT), ''),
		       r.catalog_version, r.source_url,
		       COALESCE(CAST(o.effective_at AS TEXT), CAST(r.effective_at AS TEXT), ''),
		       COALESCE(CAST(r.effective_at AS TEXT), ''),
		       r.is_builtin, r.is_active
		FROM provider_cost_rates r
		LEFT JOIN provider_cost_overrides o
		  ON o.provider = r.provider
		 AND o.sku = r.sku
		 AND o.service = r.service
		 AND o.unit_type = r.unit_type
		ORDER BY r.provider, r.service, r.sku, r.unit_type
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rates []CostRate
	for rows.Next() {
		var rate CostRate
		if err := rows.Scan(&rate.ID, &rate.Provider, &rate.SKU, &rate.Service,
			&rate.UnitType, &rate.PublicCostPerUnitUSD,
			&rate.EffectiveCostPerUnitUSD, &rate.CostSource,
			&rate.CostSourceLabel, &rate.CostOverrideID, &rate.CatalogVersion,
			&rate.SourceURL, &rate.EffectiveAt, &rate.PublicEffectiveAt,
			&rate.IsBuiltin, &rate.IsActive); err != nil {
			return nil, err
		}
		// cost_per_unit_usd remains the effective value for API compatibility.
		rate.CostPerUnitUSD = rate.EffectiveCostPerUnitUSD
		rates = append(rates, rate)
	}
	return rates, rows.Err()
}

func effectiveMarkup(rate *CostRate, cfg BillingConfig) (float64, string) {
	markup := cfg.DefaultMarkupPercent
	source := "global"
	bestRank := 0
	for _, override := range cfg.Overrides {
		match := false
		rank := 0
		switch override.ScopeType {
		case "provider":
			match = override.ScopeKey == rate.Provider
			rank = 1
		case "category":
			match = override.ScopeKey == rate.Service
			rank = 2
		case "sku":
			match = override.ScopeKey == rate.Provider+":"+rate.SKU ||
				override.ScopeKey == rate.SKU
			rank = 3
		}
		if match && rank > bestRank {
			markup = override.MarkupPercent
			source = override.ScopeType
			bestRank = rank
		}
	}
	return markup, source
}

func applyRetailPrices(rates []CostRate, cfg BillingConfig) {
	for i := range rates {
		if rates[i].EffectiveCostPerUnitUSD == 0 && rates[i].CostPerUnitUSD != 0 {
			rates[i].EffectiveCostPerUnitUSD = rates[i].CostPerUnitUSD
		}
		if rates[i].PublicCostPerUnitUSD == 0 && rates[i].CostPerUnitUSD != 0 {
			rates[i].PublicCostPerUnitUSD = rates[i].CostPerUnitUSD
		}
		rates[i].CostPerUnitUSD = rates[i].EffectiveCostPerUnitUSD
		markup, source := effectiveMarkup(&rates[i], cfg)
		rates[i].MarkupPercent = markup
		rates[i].OverrideSource = source
		rates[i].RetailDPPerUnit = rates[i].CostPerUnitUSD * cfg.DPPerUSD * (1 + markup/100)
		rates[i].ProposedRetailDPPerUnit = rates[i].RetailDPPerUnit
		if markup > 0 {
			rates[i].GrossMargin = markup / (100 + markup) * 100
		}
	}
}

func (s *Service) applyActualRetailPrices(rates []CostRate) {
	s.rulesCacheMu.RLock()
	rules := append([]PricingRule(nil), s.rulesCache...)
	s.rulesCacheMu.RUnlock()
	applyActualRetailPricesFromRules(rates, rules)
}

func applyActualRetailPricesFromRules(rates []CostRate, rules []PricingRule) {
	for i := range rates {
		rate := &rates[i]
		rate.EffectiveRetailByAction = make(map[string]float64)
		category, ok := pricingRuleCategory(rate.UnitType)
		if !ok {
			continue
		}
		for _, action := range managedActions(rate.Service) {
			selected := selectPricingRules(
				rules,
				action,
				rate.Provider,
				rate.SKU,
			)
			if choice, exists := selected[category]; exists {
				rate.EffectiveRetailByAction[action] = choice.rule.PricePerUnit
			} else if category == "cached_input_token" ||
				category == "cache_write_token" {
				if fallback, exists := selected["input_token"]; exists {
					rate.EffectiveRetailByAction[action] = fallback.rule.PricePerUnit
				}
			}
		}
		var common float64
		first := true
		same := true
		for _, value := range rate.EffectiveRetailByAction {
			if first {
				common = value
				first = false
				continue
			}
			if math.Abs(common-value) > 1e-12 {
				same = false
				break
			}
		}
		if !first && same {
			rate.EffectiveRetailDPPerUnit = &common
		}
	}
}

func (s *Service) PreviewBillingConfig(ctx context.Context, input BillingConfigInput) (*BillingPreview, error) {
	if err := validateBillingConfig(input); err != nil {
		return nil, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rates, err := getCostRatesFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	cfg, err := getBillingConfigFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	cfg.DPPerUSD = input.DPPerUSD
	cfg.DefaultMarkupPercent = input.DefaultMarkupPercent
	cfg.Overrides = input.Overrides
	applyRetailPrices(rates, cfg)
	applyActualRetailPricesFromRules(rates, rules)
	preview := &BillingPreview{
		Config: cfg, Rates: rates, Confirmation: "应用计费设置",
		CatalogVersion: cfg.CatalogVersion,
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}

func (s *Service) UpdateBillingConfig(ctx context.Context, input BillingConfigInput, actorID string) error {
	if err := validateBillingConfig(input); err != nil {
		return err
	}
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return err
	}
	var installedVersion, pricingState string
	if err := tx.QueryRowContext(ctx, `
		SELECT catalog_version, pricing_state
		FROM billing_config
		WHERE singleton = TRUE
	`).Scan(&installedVersion, &pricingState); err != nil {
		return err
	}
	mode := "pending"
	if pricingState == pricingStateManaged &&
		installedVersion == DefaultCatalogVersion {
		mode = "applied"
		if _, err := tx.ExecContext(ctx, `
			UPDATE billing_config
			SET dp_per_usd = $1, default_markup_percent = $2,
			    pending_config = NULL,
			    updated_at = NOW(), updated_by = $3
			WHERE singleton = TRUE
		`, input.DPPerUSD, input.DefaultMarkupPercent, nullUUID(actorID)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM billing_markup_overrides`); err != nil {
			return err
		}
		for _, override := range input.Overrides {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO billing_markup_overrides
					(scope_type, scope_key, markup_percent, updated_by)
				VALUES ($1, $2, $3, $4)
			`, override.ScopeType, override.ScopeKey, override.MarkupPercent,
				nullUUID(actorID)); err != nil {
				return err
			}
		}
		if err := regenerateManagedPricingRulesTx(ctx, tx); err != nil {
			return err
		}
	} else {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE billing_config
			SET pending_config = $1::jsonb,
			    updated_at = NOW(), updated_by = $2
			WHERE singleton = TRUE
		`, string(payload), nullUUID(actorID)); err != nil {
			return err
		}
	}
	if err := insertAuditTx(
		ctx, tx, actorID, "billing.config.update",
		"billing_config", installedVersion, map[string]any{
			"mode":  mode,
			"input": input,
		},
	); err != nil {
		return err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.replaceRulesCache(rules)
	return nil
}

// PreviewBuiltinCatalogApply compares the currently installed public list with
// the version shipped in this binary. Effective contract overrides are retained.
func (s *Service) PreviewBuiltinCatalogApply(ctx context.Context) (*BillingPreview, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := getBillingCatalogFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	revision, err := billingRevisionFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	proposed := make([]CostRate, len(builtinCostRates))
	copy(proposed, builtinCostRates)
	for i := range proposed {
		proposed[i].CatalogVersion = DefaultCatalogVersion
		proposed[i].IsBuiltin = true
		proposed[i].IsActive = true
		proposed[i].PublicCostPerUnitUSD = proposed[i].CostPerUnitUSD
		proposed[i].EffectiveCostPerUnitUSD = proposed[i].CostPerUnitUSD
		proposed[i].CostSource = "public_catalog"
		proposed[i].CostSourceLabel = "official_public_price"
		proposed[i].EffectiveAt = defaultCatalogEffectiveAt.Format(time.RFC3339)
		proposed[i].PublicEffectiveAt = proposed[i].EffectiveAt
	}
	// Retain effective contract overrides in the preview.
	overrides := make(map[string]CostRate)
	for i := range current.Rates {
		rate := current.Rates[i]
		overrides[costRateKey(&rate)] = rate
	}
	for i := range proposed {
		old, exists := overrides[costRateKey(&proposed[i])]
		if exists && old.CostOverrideID != "" {
			proposed[i].EffectiveCostPerUnitUSD = old.EffectiveCostPerUnitUSD
			proposed[i].CostPerUnitUSD = old.EffectiveCostPerUnitUSD
			proposed[i].CostSource = old.CostSource
			proposed[i].CostSourceLabel = old.CostSourceLabel
			proposed[i].CostOverrideID = old.CostOverrideID
			proposed[i].EffectiveAt = old.EffectiveAt
		}
	}
	for _, currentRate := range current.Rates {
		if !currentRate.IsBuiltin {
			proposed = append(proposed, currentRate)
		}
	}
	cfg := current.Config
	if current.PendingConfig != nil {
		cfg.DPPerUSD = current.PendingConfig.DPPerUSD
		cfg.DefaultMarkupPercent =
			current.PendingConfig.DefaultMarkupPercent
		cfg.Overrides = current.PendingConfig.Overrides
	}
	cfg.CatalogVersion = DefaultCatalogVersion
	cfg.PricingState = pricingStateManaged
	applyRetailPrices(proposed, cfg)
	applyActualRetailPricesFromRules(proposed, rules)
	preview := &BillingPreview{
		Config: cfg, Rates: proposed, Confirmation: billingApplyConfirmation,
		CatalogVersion:  current.InstalledVersion,
		TargetVersion:   DefaultCatalogVersion,
		CurrentRevision: revision,
	}
	existing := make(map[string]CostRate, len(current.Rates))
	for _, rate := range current.Rates {
		existing[costRateKey(&rate)] = rate
	}
	for _, rate := range proposed {
		old, ok := existing[costRateKey(&rate)]
		if !ok {
			preview.Added++
		} else if math.Abs(old.PublicCostPerUnitUSD-rate.PublicCostPerUnitUSD) > 1e-12 ||
			old.EffectiveRetailDPPerUnit == nil ||
			math.Abs(*old.EffectiveRetailDPPerUnit-rate.ProposedRetailDPPerUnit) > 1e-12 {
			preview.Updated++
		}
		delete(existing, costRateKey(&rate))
	}
	for _, old := range existing {
		if old.IsBuiltin {
			preview.Disabled++
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}

// ApplyBuiltinCatalog updates official cost rows and recomputes managed retail
// prices while preserving the administrator's currency conversion and markup
// overrides. ResetBillingDefaults is the stronger operation that clears them.
func (s *Service) ApplyBuiltinCatalog(
	ctx context.Context,
	actorID, catalogVersion, confirmation, currentRevision string,
) (*BillingCatalog, error) {
	if strings.TrimSpace(catalogVersion) != DefaultCatalogVersion {
		return nil, fmt.Errorf(
			"%w: catalog version changed; reload before applying",
			ErrBillingPreviewStale,
		)
	}
	if strings.TrimSpace(confirmation) != billingApplyConfirmation {
		return nil, fmt.Errorf("confirmation text does not match")
	}
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return nil, err
	}
	revision, err := billingRevisionFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := requireBillingRevision(currentRevision, revision); err != nil {
		return nil, err
	}
	cfg, err := getBillingConfigFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	pending, err := getPendingBillingConfigFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	target := BillingConfigInput{
		DPPerUSD: cfg.DPPerUSD, DefaultMarkupPercent: cfg.DefaultMarkupPercent,
		Overrides: cfg.Overrides,
	}
	if pending != nil {
		target = *pending
	}
	if err := upsertBuiltinCatalogTx(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE provider_cost_rates
		SET is_active = FALSE, updated_at = NOW()
		WHERE is_builtin = TRUE AND catalog_version <> $1
	`, DefaultCatalogVersion); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE billing_config
		SET dp_per_usd = $1, default_markup_percent = $2,
		    catalog_version = $3, pricing_state = $4,
		    pending_config = NULL,
		    updated_at = NOW(), updated_by = $5
		WHERE singleton = TRUE
	`, target.DPPerUSD, target.DefaultMarkupPercent,
		DefaultCatalogVersion, pricingStateManaged, nullUUID(actorID)); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM billing_markup_overrides`); err != nil {
		return nil, err
	}
	for _, override := range target.Overrides {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing_markup_overrides
				(scope_type, scope_key, markup_percent, updated_by)
			VALUES ($1, $2, $3, $4)
		`, override.ScopeType, override.ScopeKey, override.MarkupPercent,
			nullUUID(actorID)); err != nil {
			return nil, err
		}
	}
	if err := regenerateManagedPricingRulesIfActiveTx(ctx, tx); err != nil {
		return nil, err
	}
	if err := insertAuditTx(ctx, tx, actorID, "billing.catalog.apply", "billing_config", DefaultCatalogVersion, map[string]any{
		"catalog_version": DefaultCatalogVersion,
		"applied_config":  target,
	}); err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.replaceRulesCache(rules)
	return s.GetBillingCatalog(ctx)
}

func (s *Service) UpsertManualModelCost(ctx context.Context, input ManualModelCostInput, actorID string) error {
	input.ModelID = strings.TrimSpace(input.ModelID)
	input.Service = strings.TrimSpace(input.Service)
	if input.ModelID == "" || len(input.ModelID) > 200 ||
		(input.Service != "llm" && input.Service != "embedding") {
		return fmt.Errorf("invalid model cost")
	}
	values := []struct {
		unit       string
		perMillion float64
	}{
		{"input_token", input.InputPerMillionUSD},
		{"cached_input_token", input.CachedInputPerMillionUSD},
		{"cache_write_token", input.CacheWritePerMillionUSD},
		{"output_token", input.OutputPerMillionUSD},
	}
	if input.Service == "embedding" {
		values = values[:1]
	}
	for _, value := range values {
		if value.perMillion < 0 || value.perMillion > 1_000_000 ||
			math.IsNaN(value.perMillion) || math.IsInf(value.perMillion, 0) {
			return fmt.Errorf("model costs must be finite non-negative USD amounts")
		}
	}
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return err
	}
	for _, value := range values {
		var isBuiltin bool
		baseErr := tx.QueryRowContext(ctx, `
			SELECT is_builtin
			FROM provider_cost_rates
			WHERE provider = 'openai-compatible' AND sku = $1
			  AND service = $2 AND unit_type = $3
		`, input.ModelID, input.Service, value.unit).Scan(&isBuiltin)
		if baseErr != nil && baseErr != sql.ErrNoRows {
			return baseErr
		}
		if value.perMillion == 0 {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM provider_cost_overrides
				WHERE provider = 'openai-compatible' AND sku = $1
				  AND service = $2 AND unit_type = $3
			`, input.ModelID, input.Service, value.unit); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM provider_cost_rates
				WHERE provider = 'openai-compatible' AND sku = $1
				  AND service = $2 AND unit_type = $3 AND is_builtin = FALSE
			`, input.ModelID, input.Service, value.unit); err != nil {
				return err
			}
			continue
		}
		if isBuiltin {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO provider_cost_overrides
					(provider, sku, service, unit_type, cost_per_unit_usd,
					 source_label, effective_at, updated_by)
				VALUES (
					'openai-compatible', $1, $2, $3, $4,
					'model_admin_override', NOW(), $5
				)
				ON CONFLICT (provider, sku, service, unit_type) DO UPDATE SET
					cost_per_unit_usd = EXCLUDED.cost_per_unit_usd,
					source_label = EXCLUDED.source_label,
					effective_at = EXCLUDED.effective_at,
					updated_by = EXCLUDED.updated_by,
					updated_at = NOW()
			`, input.ModelID, input.Service, value.unit,
				value.perMillion/1_000_000, nullUUID(actorID)); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_cost_rates
				(provider, sku, service, unit_type, cost_per_unit_usd,
				 catalog_version, source_url, is_builtin, is_active)
			VALUES ('openai-compatible', $1, $2, $3, $4, 'manual', '', FALSE, TRUE)
			ON CONFLICT (provider, sku, service, unit_type) DO UPDATE SET
				cost_per_unit_usd = EXCLUDED.cost_per_unit_usd,
				catalog_version = 'manual',
				source_url = '',
				is_active = TRUE,
				updated_at = NOW()
			WHERE provider_cost_rates.is_builtin = FALSE
		`, input.ModelID, input.Service, value.unit, value.perMillion/1_000_000); err != nil {
			return err
		}
	}
	if err := regenerateManagedPricingRulesIfActiveTx(ctx, tx); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, actorID, "billing.model_cost.update", "model", input.ModelID, input); err != nil {
		return err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.replaceRulesCache(rules)
	return nil
}

func validateProviderCostOverride(input *ProviderCostOverrideInput) (time.Time, error) {
	input.Provider = strings.ToLower(strings.TrimSpace(input.Provider))
	input.SKU = strings.TrimSpace(input.SKU)
	input.Service = strings.TrimSpace(input.Service)
	input.UnitType = strings.TrimSpace(input.UnitType)
	input.SourceLabel = strings.TrimSpace(input.SourceLabel)
	input.Provider, input.SKU = CanonicalSKU(input.Provider, input.SKU, input.Service)
	if input.Provider == "" || len(input.Provider) > 60 ||
		input.SKU == "" || len(input.SKU) > 200 ||
		input.Service == "" || len(input.Service) > 50 {
		return time.Time{}, fmt.Errorf("invalid provider cost identity")
	}
	switch input.UnitType {
	case "hour", "minute", "input_token", "cached_input_token",
		"cache_write_token", "output_token":
	default:
		return time.Time{}, fmt.Errorf("unsupported provider cost unit_type")
	}
	if input.CostPerUnitUSD < 0 || input.CostPerUnitUSD >= maxStoredUsageCost ||
		math.IsNaN(input.CostPerUnitUSD) || math.IsInf(input.CostPerUnitUSD, 0) {
		return time.Time{}, fmt.Errorf("cost_per_unit_usd must be a finite non-negative number")
	}
	if input.SourceLabel == "" {
		input.SourceLabel = "contract"
	}
	if len([]rune(input.SourceLabel)) > 120 {
		return time.Time{}, fmt.Errorf("source_label is too long")
	}
	effectiveAt := time.Now().UTC()
	if strings.TrimSpace(input.EffectiveAt) != "" {
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(input.EffectiveAt))
		if err != nil {
			return time.Time{}, fmt.Errorf("effective_at must use RFC3339")
		}
		effectiveAt = parsed.UTC()
		if effectiveAt.After(time.Now().UTC()) {
			return time.Time{}, fmt.Errorf(
				"future effective_at is not supported; save the override when it becomes effective",
			)
		}
	}
	return effectiveAt, nil
}

// UpsertProviderCostOverride overlays a contract price on the public catalog.
// A removable manual base row supports providers/SKUs that have no public row.
func (s *Service) UpsertProviderCostOverride(
	ctx context.Context,
	input ProviderCostOverrideInput,
	actorID string,
) (*BillingCatalog, error) {
	effectiveAt, err := validateProviderCostOverride(&input)
	if err != nil {
		return nil, err
	}
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO provider_cost_rates
			(provider, sku, service, unit_type, cost_per_unit_usd,
			 catalog_version, source_url, effective_at, is_builtin, is_active)
		VALUES ($1, $2, $3, $4, $5, 'override-base', '', $6, FALSE, TRUE)
		ON CONFLICT (provider, sku, service, unit_type) DO NOTHING
	`, input.Provider, input.SKU, input.Service, input.UnitType,
		input.CostPerUnitUSD, effectiveAt); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO provider_cost_overrides
			(provider, sku, service, unit_type, cost_per_unit_usd,
			 source_label, effective_at, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (provider, sku, service, unit_type) DO UPDATE SET
			cost_per_unit_usd = EXCLUDED.cost_per_unit_usd,
			source_label = EXCLUDED.source_label,
			effective_at = EXCLUDED.effective_at,
			updated_by = EXCLUDED.updated_by,
			updated_at = NOW()
	`, input.Provider, input.SKU, input.Service, input.UnitType,
		input.CostPerUnitUSD, input.SourceLabel, effectiveAt,
		nullUUID(actorID)); err != nil {
		return nil, err
	}
	if err := regenerateManagedPricingRulesIfActiveTx(ctx, tx); err != nil {
		return nil, err
	}
	targetID := strings.Join([]string{
		input.Provider, input.SKU, input.Service, input.UnitType,
	}, ":")
	if err := insertAuditTx(
		ctx, tx, actorID, "billing.cost_override.upsert",
		"provider_cost", targetID, input,
	); err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.replaceRulesCache(rules)
	return s.GetBillingCatalog(ctx)
}

func (s *Service) DeleteProviderCostOverride(
	ctx context.Context,
	provider, sku, service, unitType, actorID string,
) (*BillingCatalog, error) {
	input := ProviderCostOverrideInput{
		Provider: provider, SKU: sku, Service: service, UnitType: unitType,
	}
	if _, err := validateProviderCostOverride(&input); err != nil {
		return nil, err
	}
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return nil, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM provider_cost_overrides
		WHERE provider = $1 AND sku = $2 AND service = $3 AND unit_type = $4
	`, input.Provider, input.SKU, input.Service, input.UnitType)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected == 0 {
		return nil, sql.ErrNoRows
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM provider_cost_rates
		WHERE provider = $1 AND sku = $2 AND service = $3 AND unit_type = $4
		  AND catalog_version = 'override-base' AND is_builtin = FALSE
	`, input.Provider, input.SKU, input.Service, input.UnitType); err != nil {
		return nil, err
	}
	if err := regenerateManagedPricingRulesIfActiveTx(ctx, tx); err != nil {
		return nil, err
	}
	targetID := strings.Join([]string{
		input.Provider, input.SKU, input.Service, input.UnitType,
	}, ":")
	if err := insertAuditTx(
		ctx, tx, actorID, "billing.cost_override.delete",
		"provider_cost", targetID, map[string]string{
			"provider": input.Provider, "sku": input.SKU,
			"service": input.Service, "unit_type": input.UnitType,
		},
	); err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.replaceRulesCache(rules)
	return s.GetBillingCatalog(ctx)
}

func (s *Service) PreviewBillingReset(ctx context.Context) (*BillingPreview, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := getBillingCatalogFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	revision, err := billingRevisionFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	defaultRates := append([]CostRate(nil), builtinCostRates...)
	for i := range defaultRates {
		defaultRates[i].CatalogVersion = DefaultCatalogVersion
		defaultRates[i].IsBuiltin = true
		defaultRates[i].IsActive = true
		defaultRates[i].PublicCostPerUnitUSD = defaultRates[i].CostPerUnitUSD
		defaultRates[i].EffectiveCostPerUnitUSD = defaultRates[i].CostPerUnitUSD
		defaultRates[i].CostSource = "public_catalog"
		defaultRates[i].CostSourceLabel = "official_public_price"
		defaultRates[i].EffectiveAt = defaultCatalogEffectiveAt.Format(time.RFC3339)
		defaultRates[i].PublicEffectiveAt = defaultRates[i].EffectiveAt
	}
	cfg := BillingConfig{
		DPPerUSD: DefaultDPPerUSD, DefaultMarkupPercent: DefaultMarkupPercent,
		CatalogVersion: DefaultCatalogVersion, PricingState: pricingStateManaged,
		Overrides: []MarkupOverride{},
	}
	applyRetailPrices(defaultRates, cfg)
	existing := make(map[string]*CostRate, len(current.Rates))
	for i := range current.Rates {
		rate := &current.Rates[i]
		existing[costRateKey(rate)] = rate
	}
	applyActualRetailPricesFromRules(defaultRates, rules)
	preview := &BillingPreview{
		Config: cfg, Rates: defaultRates, Confirmation: billingResetConfirmation,
		CatalogVersion:  current.InstalledVersion,
		TargetVersion:   DefaultCatalogVersion,
		CurrentRevision: revision,
	}
	for i := range defaultRates {
		rate := &defaultRates[i]
		old, ok := existing[costRateKey(rate)]
		if !ok {
			preview.Added++
		} else if math.Abs(old.CostPerUnitUSD-rate.CostPerUnitUSD) > 1e-12 ||
			math.Abs(old.RetailDPPerUnit-rate.RetailDPPerUnit) > 1e-12 {
			preview.Updated++
		}
		delete(existing, costRateKey(rate))
	}
	preview.Disabled = len(existing)
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return preview, nil
}

func costRateKey(rate *CostRate) string {
	return rate.Provider + "\x00" + rate.SKU + "\x00" + rate.Service + "\x00" + rate.UnitType
}

func (s *Service) ResetBillingDefaults(
	ctx context.Context,
	actorID, confirmation, currentRevision string,
) (*BillingCatalog, error) {
	if confirmation != billingResetConfirmation {
		return nil, fmt.Errorf("confirmation text does not match")
	}
	s.pricingViewMu.Lock()
	defer s.pricingViewMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockBillingRevisionTx(ctx, tx); err != nil {
		return nil, err
	}
	revision, err := billingRevisionFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := requireBillingRevision(currentRevision, revision); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM billing_markup_overrides`); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_cost_overrides`); err != nil {
		return nil, err
	}
	// Manual cost entries are deliberately removed by a full reset. Built-in
	// rows are then upserted to the version shipped with this binary.
	if _, err := tx.ExecContext(ctx, `DELETE FROM provider_cost_rates WHERE is_builtin = FALSE`); err != nil {
		return nil, err
	}
	if err := upsertBuiltinCatalogTx(ctx, tx); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE provider_cost_rates
		SET is_active = FALSE, updated_at = NOW()
		WHERE is_builtin = TRUE AND catalog_version <> $1
	`, DefaultCatalogVersion); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE billing_config
		SET dp_per_usd = $1, default_markup_percent = $2,
		    catalog_version = $3, pricing_state = $4,
		    pending_config = NULL,
		    updated_at = NOW(), updated_by = $5
		WHERE singleton = TRUE
	`, DefaultDPPerUSD, DefaultMarkupPercent, DefaultCatalogVersion,
		pricingStateManaged, nullUUID(actorID)); err != nil {
		return nil, err
	}
	// A reset is the explicit boundary at which legacy/manual retail rules are
	// replaced. The ledger is untouched.
	if _, err := tx.ExecContext(ctx, `UPDATE pricing_rules SET is_active = FALSE`); err != nil {
		return nil, err
	}
	if err := regenerateManagedPricingRulesTx(ctx, tx); err != nil {
		return nil, err
	}
	if err := reconcileModelPoliciesAfterResetTx(ctx, tx, actorID); err != nil {
		return nil, err
	}
	details := map[string]any{
		"catalog_version":        DefaultCatalogVersion,
		"dp_per_usd":             DefaultDPPerUSD,
		"default_markup_percent": DefaultMarkupPercent,
	}
	if err := insertAuditTx(ctx, tx, actorID, "billing.defaults.reset", "billing_config", DefaultCatalogVersion, details); err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.replaceRulesCache(rules)
	return s.GetBillingCatalog(ctx)
}

func regenerateManagedPricingRulesIfActiveTx(ctx context.Context, tx *sql.Tx) error {
	var state, version string
	if err := tx.QueryRowContext(ctx, `
		SELECT pricing_state, catalog_version
		FROM billing_config
		WHERE singleton = TRUE
	`).Scan(&state, &version); err != nil {
		return err
	}
	if state != pricingStateManaged || version != DefaultCatalogVersion {
		return nil
	}
	return regenerateManagedPricingRulesTx(ctx, tx)
}

func regenerateManagedPricingRulesTx(ctx context.Context, tx *sql.Tx) error {
	var dpPerUSD, defaultMarkup float64
	if err := tx.QueryRowContext(ctx, `
		SELECT dp_per_usd, default_markup_percent
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&dpPerUSD, &defaultMarkup); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT r.provider, r.sku, r.service, r.unit_type,
		       COALESCE(o.cost_per_unit_usd, r.cost_per_unit_usd)
		FROM provider_cost_rates r
		LEFT JOIN provider_cost_overrides o
		  ON o.provider = r.provider
		 AND o.sku = r.sku
		 AND o.service = r.service
		 AND o.unit_type = r.unit_type
		WHERE r.is_active = TRUE
		ORDER BY r.provider, r.sku, r.service, r.unit_type
	`)
	if err != nil {
		return err
	}
	var rates []CostRate
	for rows.Next() {
		var rate CostRate
		if err := rows.Scan(&rate.Provider, &rate.SKU, &rate.Service,
			&rate.UnitType, &rate.CostPerUnitUSD); err != nil {
			_ = rows.Close()
			return err
		}
		rates = append(rates, rate)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	overrideRows, err := tx.QueryContext(ctx, `
		SELECT scope_type, scope_key, markup_percent FROM billing_markup_overrides
	`)
	if err != nil {
		return err
	}
	cfg := BillingConfig{DPPerUSD: dpPerUSD, DefaultMarkupPercent: defaultMarkup}
	for overrideRows.Next() {
		var override MarkupOverride
		if err := overrideRows.Scan(&override.ScopeType, &override.ScopeKey, &override.MarkupPercent); err != nil {
			_ = overrideRows.Close()
			return err
		}
		cfg.Overrides = append(cfg.Overrides, override)
	}
	if err := overrideRows.Close(); err != nil {
		return err
	}
	applyRetailPrices(rates, cfg)
	if _, err := tx.ExecContext(ctx, `
		UPDATE pricing_rules SET is_active = FALSE
		WHERE source = 'managed'
	`); err != nil {
		return err
	}
	for i := range rates {
		rate := &rates[i]
		actions := managedActions(rate.Service)
		for _, action := range actions {
			managedKey := strings.Join([]string{
				rate.Provider, rate.SKU, action, rate.UnitType,
			}, ":")
			description := fmt.Sprintf(
				"%s %s; upstream %.12f USD; markup %.4f%%",
				rate.Provider, rate.SKU, rate.CostPerUnitUSD, rate.MarkupPercent,
			)
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO pricing_rules
					(rule_type, provider, model, price_per_unit, unit_type, description,
					 is_active, priority, managed_key, source, catalog_version)
				VALUES ($1, $2, $3, $4, $5, $6, TRUE, 100, $7, 'managed', $8)
				ON CONFLICT (managed_key) WHERE managed_key IS NOT NULL DO UPDATE SET
					rule_type = EXCLUDED.rule_type,
					provider = EXCLUDED.provider,
					model = EXCLUDED.model,
					price_per_unit = EXCLUDED.price_per_unit,
					unit_type = EXCLUDED.unit_type,
					description = EXCLUDED.description,
					is_active = TRUE,
					priority = EXCLUDED.priority,
					source = EXCLUDED.source,
					catalog_version = EXCLUDED.catalog_version,
					updated_at = NOW()
			`, action, rate.Provider, rate.SKU, rate.RetailDPPerUnit,
				rate.UnitType, description, managedKey,
				DefaultCatalogVersion); err != nil {
				return err
			}
		}
	}
	// Backward-compatible model aliases used by older frontends and pending
	// reservations. They resolve to the current enhanced defaults.
	aliases := []struct {
		action, model, target string
	}{
		{"transcription", "speechmatics", "speechmatics-realtime-enhanced"},
		{"transcription", "speechmatics-realtime", "speechmatics-realtime-enhanced"},
		{"transcription", "speechmatics-classic-token", "speechmatics-realtime-enhanced"},
		{"transcription", "speechmatics-batch", "speechmatics-batch-enhanced"},
	}
	for _, alias := range aliases {
		for i := range rates {
			rate := &rates[i]
			if rate.SKU != alias.target || rate.UnitType != "hour" {
				continue
			}
			key := "alias:" + alias.action + ":" + alias.model + ":hour"
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO pricing_rules
					(rule_type, provider, model, price_per_unit, unit_type, description,
					 is_active, priority, managed_key, source, catalog_version)
				VALUES ($1, $2, $3, $4, 'hour', $5, TRUE, 100, $6, 'managed', $7)
				ON CONFLICT (managed_key) WHERE managed_key IS NOT NULL DO UPDATE SET
					rule_type = EXCLUDED.rule_type,
					provider = EXCLUDED.provider,
					model = EXCLUDED.model,
					price_per_unit = EXCLUDED.price_per_unit,
					unit_type = EXCLUDED.unit_type,
					description = EXCLUDED.description,
					is_active = TRUE,
					priority = EXCLUDED.priority,
					source = EXCLUDED.source,
					catalog_version = EXCLUDED.catalog_version,
					updated_at = NOW()
			`, alias.action, rate.Provider, alias.model, rate.RetailDPPerUnit,
				"Compatibility alias for "+alias.target, key, DefaultCatalogVersion); err != nil {
				return err
			}
		}
	}
	return nil
}

func reconcileModelPoliciesAfterResetTx(
	ctx context.Context,
	tx *sql.Tx,
	actorID string,
) error {
	syncStatus := providerSyncUnverified
	if err := tx.QueryRowContext(ctx, `
		SELECT status
		FROM provider_model_sync_status
		WHERE provider = 'openai-compatible'
		FOR UPDATE
	`).Scan(&syncStatus); err != nil && err != sql.ErrNoRows {
		return err
	}
	// Before the first successful provider sync, shipped models are the only
	// safe availability source. Once a provider response has been confirmed,
	// or a later refresh is temporarily unavailable, preserve its last-known
	// true/false availability exactly.
	if shouldRestoreBuiltinAvailability(syncStatus) {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO provider_models
				(provider, model_id, source, provider_available)
			VALUES
			  ('openai-compatible', 'gpt-5.6-luna', 'builtin', TRUE),
			  ('openai-compatible', 'gpt-5.6-sol', 'builtin', TRUE),
			  ('openai-compatible', 'text-embedding-3-small', 'builtin', TRUE)
			ON CONFLICT (provider, model_id) DO UPDATE SET
				source = CASE
					WHEN provider_models.source IN ('provider', 'builtin+provider')
						THEN 'builtin+provider'
					ELSE 'builtin'
				END,
				provider_available = TRUE
		`); err != nil {
			return err
		}
	}
	// A full reset removes manual costs. Policies that no longer have an
	// effective active cost, or whose provider model is unavailable, must not
	// remain approved/default.
	if _, err := tx.ExecContext(ctx, `
		WITH completeness AS (
		  SELECT p.purpose, p.model_id,
		         EXISTS (
		           SELECT 1 FROM provider_models pm
		           WHERE pm.provider = 'openai-compatible'
		             AND pm.model_id = p.model_id
		             AND pm.provider_available = TRUE
		         ) AS provider_available,
		         CASE
		           WHEN p.purpose = 'embedding' THEN EXISTS (
		             SELECT 1 FROM provider_cost_rates r
		             WHERE r.provider = 'openai-compatible'
		               AND r.sku = p.model_id
		               AND r.service = 'embedding'
		               AND r.unit_type = 'input_token'
		               AND r.is_active = TRUE
		           )
		           ELSE EXISTS (
		             SELECT 1 FROM provider_cost_rates r
		             WHERE r.provider = 'openai-compatible'
		               AND r.sku = p.model_id
		               AND r.service = 'llm'
		               AND r.unit_type = 'input_token'
		               AND r.is_active = TRUE
		           ) AND EXISTS (
		             SELECT 1 FROM provider_cost_rates r
		             WHERE r.provider = 'openai-compatible'
		               AND r.sku = p.model_id
		               AND r.service = 'llm'
		               AND r.unit_type = 'output_token'
		               AND r.is_active = TRUE
		           )
		         END AS cost_complete
		  FROM model_policies p
		)
		UPDATE model_policies p
		SET cost_confirmed = c.cost_complete,
		    is_approved = p.is_approved
		      AND c.cost_complete
		      AND c.provider_available,
		    is_default = p.is_default
		      AND c.cost_complete
		      AND c.provider_available,
		    updated_at = NOW(),
		    updated_by = $1
		FROM completeness c
		WHERE c.purpose = p.purpose AND c.model_id = p.model_id
	`, nullUUID(actorID)); err != nil {
		return err
	}
	// Keep an existing valid default. Otherwise prefer the shipped model, then
	// deterministically select another available model with complete built-in
	// costs. If none exists, returning an error rolls back the entire reset.
	fallbacks := []struct {
		purpose        string
		preferredModel string
		service        string
	}{
		{"translation", "gpt-5.6-luna", "llm"},
		{"summary", "gpt-5.6-sol", "llm"},
		{"chat", "gpt-5.6-sol", "llm"},
		{"embedding", "text-embedding-3-small", "embedding"},
	}
	for _, fallback := range fallbacks {
		var hasDefault bool
		if err := tx.QueryRowContext(ctx, `
			SELECT EXISTS (
			  SELECT 1
			  FROM model_policies p
			  JOIN provider_models pm
			    ON pm.provider = 'openai-compatible'
			   AND pm.model_id = p.model_id
			   AND pm.provider_available = TRUE
			  WHERE p.purpose = $1
			    AND p.is_approved = TRUE
			    AND p.is_default = TRUE
			    AND EXISTS (
			      SELECT 1 FROM provider_cost_rates r
			      WHERE r.provider = 'openai-compatible'
			        AND r.sku = p.model_id
			        AND r.service = $2
			        AND r.unit_type = 'input_token'
			        AND r.is_builtin = TRUE
			        AND r.is_active = TRUE
			    )
			    AND (
			      $1 = 'embedding' OR EXISTS (
			        SELECT 1 FROM provider_cost_rates r
			        WHERE r.provider = 'openai-compatible'
			          AND r.sku = p.model_id
			          AND r.service = $2
			          AND r.unit_type = 'output_token'
			          AND r.is_builtin = TRUE
			          AND r.is_active = TRUE
			      )
			    )
			)
		`, fallback.purpose, fallback.service).Scan(&hasDefault); err != nil {
			return err
		}
		if hasDefault {
			continue
		}
		var selectedModel string
		if err := tx.QueryRowContext(ctx, `
			SELECT pm.model_id
			FROM provider_models pm
			WHERE pm.provider = 'openai-compatible'
			  AND pm.provider_available = TRUE
			  AND EXISTS (
			    SELECT 1 FROM provider_cost_rates r
			    WHERE r.provider = pm.provider
			      AND r.sku = pm.model_id
			      AND r.service = $1
			      AND r.unit_type = 'input_token'
			      AND r.is_builtin = TRUE
			      AND r.is_active = TRUE
			  )
			  AND (
			    $2 = 'embedding' OR EXISTS (
			      SELECT 1 FROM provider_cost_rates r
			      WHERE r.provider = pm.provider
			        AND r.sku = pm.model_id
			        AND r.service = $1
			        AND r.unit_type = 'output_token'
			        AND r.is_builtin = TRUE
			        AND r.is_active = TRUE
			    )
			  )
			ORDER BY
			  CASE WHEN pm.model_id = $3 THEN 0 ELSE 1 END,
			  pm.model_id
			LIMIT 1
		`, fallback.service, fallback.purpose, fallback.preferredModel).Scan(
			&selectedModel,
		); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf(
					"billing reset cannot choose an available built-in %s model (provider sync status %s)",
					fallback.purpose,
					syncStatus,
				)
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE model_policies
			SET is_default = FALSE, updated_at = NOW(), updated_by = $1
			WHERE purpose = $2
		`, nullUUID(actorID), fallback.purpose); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO model_policies
				(purpose, model_id, is_approved, is_default, cost_confirmed,
				 updated_at, updated_by)
			VALUES ($1, $2, TRUE, TRUE, TRUE, NOW(), $3)
			ON CONFLICT (purpose, model_id) DO UPDATE SET
				is_approved = TRUE,
				is_default = TRUE,
				cost_confirmed = TRUE,
				updated_at = NOW(),
				updated_by = EXCLUDED.updated_by
		`, fallback.purpose, selectedModel, nullUUID(actorID)); err != nil {
			return err
		}
	}
	return nil
}

func shouldRestoreBuiltinAvailability(syncStatus string) bool {
	return strings.TrimSpace(syncStatus) == providerSyncUnverified
}

func managedActions(service string) []string {
	switch service {
	case "llm":
		return []string{"translation", "chat", "summarize"}
	case "embedding":
		return []string{"embedding"}
	case "transcription":
		return []string{"transcription"}
	case "translation_addon":
		return []string{"translation"}
	case "summary_addon":
		return []string{"summarize"}
	default:
		return nil
	}
}

// CanonicalSKU normalizes compatibility identifiers before cost lookup. The
// actionOrService hint allows old records without an explicit provider to be
// resolved deterministically.
func CanonicalSKU(provider, sku, actionOrService string) (string, string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	sku = strings.TrimSpace(sku)
	hint := strings.ToLower(strings.TrimSpace(actionOrService))
	lowerSKU := strings.ToLower(sku)
	if provider == "openai" {
		provider = "openai-compatible"
	}
	if provider == "" {
		if strings.HasPrefix(lowerSKU, "speechmatics") ||
			hint == "transcription" || hint == "transcription_addon" {
			provider = "speechmatics"
		} else if sku != "" && hint != "rag_query" {
			provider = "openai-compatible"
		}
	}
	if provider == "speechmatics" {
		switch lowerSKU {
		case "speechmatics", "speechmatics-realtime",
			"speechmatics-classic-token":
			sku = "speechmatics-realtime-enhanced"
		case "speechmatics-batch":
			sku = "speechmatics-batch-enhanced"
		}
	}
	return provider, sku
}

func nullUUID(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func insertAuditTx(ctx context.Context, tx *sql.Tx, actorID, action, targetType, targetID string, details any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO admin_audit_logs
			(actor_user_id, action, target_type, target_id, details)
		VALUES ($1, $2, $3, $4, $5)
	`, nullUUID(actorID), action, targetType, targetID, payload)
	return err
}

func (s *Service) GetBillingAnalytics(ctx context.Context) (*BillingAnalytics, error) {
	var result BillingAnalytics
	if err := s.db.QueryRowContext(ctx, `
		SELECT
		  COALESCE(SUM(upstream_cost_usd)
		    FILTER (WHERE cost_attribution = 'provider_priced'), 0),
		  COALESCE(SUM(service_fee_dp)
		    FILTER (WHERE cost_attribution IN ('provider_priced', 'byok')), 0),
		  COALESCE(SUM(cost), 0),
		  COUNT(*),
		  COUNT(*) FILTER (
		    WHERE cost_attribution IN ('provider_priced', 'byok', 'non_provider')
		  ),
		  COUNT(*) FILTER (WHERE cost_attribution = 'legacy_unknown'),
		  COALESCE(SUM(cost)
		    FILTER (WHERE cost_attribution = 'legacy_unknown'), 0),
		  COUNT(*) FILTER (WHERE cost_attribution = 'byok'),
		  COALESCE(SUM(service_fee_dp)
		    FILTER (WHERE cost_attribution = 'byok'), 0),
		  COUNT(*) FILTER (WHERE cost_attribution = 'non_provider'),
		  COUNT(*) FILTER (WHERE cost_attribution = 'unpriced')
		FROM usage_logs
		WHERE refunded_at IS NULL
	`).Scan(
		&result.UpstreamCostUSD,
		&result.ServiceFeeDP,
		&result.RetailDP,
		&result.UsageCount,
		&result.AttributedUsageCount,
		&result.LegacyUnknownCount,
		&result.LegacyUnknownRetailDP,
		&result.BYOKUsageCount,
		&result.BYOKServiceFeeDP,
		&result.NonProviderUsageCount,
		&result.UnpricedUsageCount,
	); err != nil {
		return nil, err
	}
	cfg, cfgErr := s.getBillingConfig(ctx)
	rates, ratesErr := s.getCostRates(ctx)
	if cfgErr != nil || ratesErr != nil {
		// Exact aggregates remain useful even if the optional current-catalog
		// estimate cannot be produced.
		result.EstimateError = "cost_catalog_unavailable"
		return &result, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT action, COALESCE(model, ''),
		       COALESCE(SUM(quantity), 0),
		       COALESCE(SUM(input_tokens), 0),
		       COALESCE(SUM(cached_input_tokens), 0),
		       COALESCE(SUM(cache_write_tokens), 0),
		       COALESCE(SUM(output_tokens), 0),
		       COALESCE(SUM(cost), 0),
		       COUNT(*)
		FROM usage_logs
		WHERE refunded_at IS NULL
		  AND cost_attribution = 'legacy_unknown'
		GROUP BY action, COALESCE(model, '')
	`)
	if err != nil {
		result.EstimateError = "legacy_estimate_query_failed"
		return &result, nil
	}
	defer func() { _ = rows.Close() }()
	result.EstimateCatalogVersion = DefaultCatalogVersion
	for i := range rates {
		if rates[i].CostOverrideID != "" {
			result.EstimateCatalogVersion += "+effective-overrides"
			break
		}
	}
	for rows.Next() {
		var rec UsageRecord
		var historicalRetail float64
		var groupCount int64
		if err := rows.Scan(
			&rec.Action,
			&rec.Model,
			&rec.Quantity,
			&rec.InputTokens,
			&rec.CachedInputTokens,
			&rec.CacheWriteTokens,
			&rec.OutputTokens,
			&historicalRetail,
			&groupCount,
		); err != nil {
			result.EstimateError = "legacy_estimate_scan_failed"
			return &result, nil
		}
		if !hasMeasurableProviderUsage(&rec) {
			continue
		}
		upstream, _, _, estimateErr := calculateProviderUpstream(&rec, rates, cfg)
		if estimateErr != nil {
			continue
		}
		result.EstimateEligibleCount += groupCount
		result.EstimatedLegacyUpstreamCostUSD += upstream
		result.EstimatedLegacyServiceFeeDP +=
			historicalRetail - upstream*cfg.DPPerUSD
	}
	if err := rows.Err(); err != nil {
		result.EstimateError = "legacy_estimate_query_failed"
		return &result, nil
	}
	result.EstimateAvailable = true
	return &result, nil
}

func (s *Service) GetUserBillingSummary(ctx context.Context, userID string) (*UserBillingSummary, error) {
	balance, err := s.GetUserBalance(ctx, userID)
	if err != nil {
		return nil, err
	}
	rate := s.CalculateCost("transcription", "speechmatics-realtime-enhanced", 60, 0, 0)
	if rate <= 0 {
		rate = s.CalculateCost("transcription", "speechmatics", 60, 0, 0)
	}
	summary := &UserBillingSummary{
		Dreampoints: balance.Dreampoints, DreampointsUsed: balance.DreampointsUsed,
		RealtimeRateDPPerHour: rate, EstimateProfile: "speechmatics-realtime-enhanced",
	}
	if rate > 0 {
		summary.EstimatedRealtimeHours = math.Max(0, balance.Dreampoints/rate)
	}
	return summary, nil
}

func (s *Service) GetUserUsage(ctx context.Context, userID, sessionID string, limit int) ([]UserUsageItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	sessionID = strings.TrimSpace(sessionID)
	var rows *sql.Rows
	var err error
	if sessionID == "" {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, session_id, action, COALESCE(model, ''), quantity,
			       COALESCE(input_tokens, 0), cached_input_tokens, cache_write_tokens,
			       COALESCE(output_tokens, 0), upstream_cost_usd, service_fee_dp,
			       cost, pricing_snapshot, created_at
			FROM usage_logs
			WHERE user_id = $1
			ORDER BY created_at DESC
			LIMIT $2
		`, userID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, session_id, action, COALESCE(model, ''), quantity,
			       COALESCE(input_tokens, 0), cached_input_tokens, cache_write_tokens,
			       COALESCE(output_tokens, 0), upstream_cost_usd, service_fee_dp,
			       cost, pricing_snapshot, created_at
			FROM usage_logs
			WHERE user_id = $1 AND session_id = $2
			ORDER BY created_at DESC
			LIMIT $3
		`, userID, sessionID, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]UserUsageItem, 0)
	for rows.Next() {
		var item UserUsageItem
		var raw []byte
		if err := rows.Scan(&item.ID, &item.SessionID, &item.Action, &item.Model,
			&item.Quantity, &item.InputTokens, &item.CachedInputTokens,
			&item.CacheWriteTokens, &item.OutputTokens, &item.UpstreamCostUSD,
			&item.ServiceFeeDP, &item.CostDP, &raw, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.PricingSnapshot)
		items = append(items, item)
	}
	return items, rows.Err()
}

type usageCostBreakdown struct {
	ChargeDP        float64
	UpstreamCostUSD float64
	ServiceFeeDP    float64
	PricingSnapshot []byte
	Attribution     string
}

func validateUsageCostBreakdown(value usageCostBreakdown) error {
	if value.ChargeDP < 0 || value.ChargeDP >= maxStoredUsageCost ||
		math.IsNaN(value.ChargeDP) || math.IsInf(value.ChargeDP, 0) {
		return fmt.Errorf("calculated charge is outside the supported range")
	}
	if value.UpstreamCostUSD < 0 ||
		value.UpstreamCostUSD >= maxStoredUsageCost ||
		math.IsNaN(value.UpstreamCostUSD) ||
		math.IsInf(value.UpstreamCostUSD, 0) {
		return fmt.Errorf("calculated upstream cost is outside the supported range")
	}
	if math.Abs(value.ServiceFeeDP) >= maxStoredUsageCost ||
		math.IsNaN(value.ServiceFeeDP) ||
		math.IsInf(value.ServiceFeeDP, 0) {
		return fmt.Errorf("calculated service fee is outside the supported range")
	}
	if len(value.PricingSnapshot) == 0 {
		return fmt.Errorf("pricing snapshot is required")
	}
	return nil
}

type usagePricingView struct {
	Config BillingConfig
	Rates  []CostRate
	Rules  []PricingRule
}

func nonProviderUsage(rec *UsageRecord) bool {
	return rec != nil && rec.Action == "rag_query"
}

func (s *Service) loadUsagePricingView(
	ctx context.Context,
) (*usagePricingView, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := getBillingConfigFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	rates, err := getCostRatesFrom(ctx, tx)
	if err != nil {
		return nil, err
	}
	rules, err := loadActivePricingRules(ctx, tx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &usagePricingView{Config: cfg, Rates: rates, Rules: rules}, nil
}

func hasMeasurableProviderUsage(rec *UsageRecord) bool {
	if rec == nil {
		return false
	}
	if rec.Action == "transcription" && rec.Quantity > 0 {
		return true
	}
	provider, _ := CanonicalSKU(rec.Provider, rec.Model, rec.Action)
	if provider == "speechmatics" && rec.Quantity > 0 {
		return true
	}
	return rec.InputTokens > 0 ||
		rec.CachedInputTokens > 0 ||
		rec.CacheWriteTokens > 0 ||
		rec.OutputTokens > 0
}

func providerCostServiceForUsage(rec *UsageRecord, provider, sku string) string {
	switch rec.Action {
	case "transcription":
		return "transcription"
	case "embedding":
		return "embedding"
	case "translation":
		if provider == "speechmatics" && sku == "speechmatics-translation" {
			return "translation_addon"
		}
		return "llm"
	case "summarize":
		if provider == "speechmatics" && sku == "speechmatics-summary" {
			return "summary_addon"
		}
		return "llm"
	case "chat":
		return "llm"
	default:
		return ""
	}
}

func durationPricedService(service string) bool {
	switch service {
	case "transcription", "translation_addon", "summary_addon",
		"chapters_addon", "sentiment_addon", "topics_addon":
		return true
	default:
		return false
	}
}

func calculateUsageFromUnitRates(
	rec *UsageRecord,
	rates map[string]float64,
	requireDuration bool,
) (float64, error) {
	if rec == nil {
		return 0, fmt.Errorf("usage is required")
	}
	var total float64
	hourRate, hasHour := rates["hour"]
	minuteRate, hasMinute := rates["minute"]
	if requireDuration {
		switch {
		case hasHour:
			total += hourRate * rec.Quantity / 60
		case hasMinute:
			total += minuteRate * rec.Quantity
		default:
			return 0, fmt.Errorf("%w: duration rate", ErrProviderCostNotFound)
		}
	}
	ordinaryInput := rec.InputTokens - rec.CachedInputTokens - rec.CacheWriteTokens
	if ordinaryInput < 0 {
		ordinaryInput = rec.InputTokens
	}
	inputRate, hasInput := rates["input_token"]
	if ordinaryInput > 0 {
		if !hasInput {
			return 0, fmt.Errorf("%w: input rate", ErrProviderCostNotFound)
		}
		total += inputRate * float64(ordinaryInput)
	}
	for unit, quantity := range map[string]int{
		"cached_input_token": rec.CachedInputTokens,
		"cache_write_token":  rec.CacheWriteTokens,
	} {
		if quantity <= 0 {
			continue
		}
		rate, exists := rates[unit]
		if !exists {
			rate, exists = inputRate, hasInput
		}
		if !exists {
			return 0, fmt.Errorf("%w: %s rate", ErrProviderCostNotFound, unit)
		}
		total += rate * float64(quantity)
	}
	if rec.OutputTokens > 0 {
		outputRate, exists := rates["output_token"]
		if !exists {
			return 0, fmt.Errorf("%w: output rate", ErrProviderCostNotFound)
		}
		total += outputRate * float64(rec.OutputTokens)
	}
	return total, nil
}

func calculateProviderUpstream(
	rec *UsageRecord,
	rates []CostRate,
	cfg BillingConfig,
) (float64, map[string]float64, float64, error) {
	if rec == nil {
		return 0, nil, 0, fmt.Errorf("usage is required")
	}
	provider, sku := CanonicalSKU(rec.Provider, rec.Model, rec.Action)
	if provider == "" || sku == "" {
		return 0, nil, 0, fmt.Errorf(
			"%w: provider/model identity is missing",
			ErrProviderCostNotFound,
		)
	}
	candidates := make(map[string]*CostRate)
	var representative *CostRate
	service := providerCostServiceForUsage(rec, provider, sku)
	for i := range rates {
		rate := &rates[i]
		if !rate.IsActive || rate.Provider != provider || rate.SKU != sku ||
			(service != "" && rate.Service != service) {
			continue
		}
		candidates[rate.UnitType] = rate
		if representative == nil {
			representative = rate
		}
	}
	if representative == nil {
		return 0, nil, 0, fmt.Errorf(
			"%w: %s/%s",
			ErrProviderCostNotFound,
			provider,
			sku,
		)
	}
	applied := make(map[string]float64, len(candidates))
	for unit, rate := range candidates {
		applied[unit] = rate.CostPerUnitUSD
	}
	requireDuration := durationPricedService(service)
	upstream, err := calculateUsageFromUnitRates(rec, applied, requireDuration)
	if err != nil {
		return 0, nil, 0, fmt.Errorf(
			"%w: %s/%s: %v",
			ErrProviderCostNotFound,
			provider,
			sku,
			err,
		)
	}
	markup, _ := effectiveMarkup(representative, cfg)
	return upstream, applied, markup, nil
}

func customerFundedServiceFee(
	upstreamCostUSD float64,
	cfg BillingConfig,
	markupPercent float64,
) float64 {
	return upstreamCostUSD * cfg.DPPerUSD * markupPercent / 100
}

func (s *Service) currentRetailUnitRates(rec *UsageRecord) map[string]float64 {
	s.rulesCacheMu.RLock()
	defer s.rulesCacheMu.RUnlock()
	return currentRetailUnitRatesFromRules(rec, s.rulesCache)
}

func currentRetailUnitRatesFromRules(
	rec *UsageRecord,
	rules []PricingRule,
) map[string]float64 {
	provider, _ := CanonicalSKU(rec.Provider, rec.Model, rec.Action)
	selected := selectPricingRules(
		rules,
		rec.Action,
		provider,
		rec.Model,
	)
	rates := make(map[string]float64, len(selected))
	for _, choice := range selected {
		rates[choice.rule.UnitType] = choice.rule.PricePerUnit
	}
	return rates
}

type usagePricingSnapshot struct {
	SnapshotVersion int                `json:"snapshot_version"`
	CatalogVersion  string             `json:"catalog_version"`
	DPPerUSD        float64            `json:"dp_per_usd"`
	MarkupPercent   float64            `json:"markup_percent"`
	Provider        string             `json:"provider"`
	CanonicalSKU    string             `json:"canonical_sku"`
	Action          string             `json:"action"`
	Attribution     string             `json:"attribution"`
	RatesUSD        map[string]float64 `json:"rates_usd"`
	RetailRatesDP   map[string]float64 `json:"retail_rates_dp"`
}

func resolveUsageCostFromSnapshot(
	raw []byte,
	rec *UsageRecord,
	reservedAttribution string,
) (usageCostBreakdown, error) {
	var snapshot usagePricingSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil ||
		snapshot.SnapshotVersion < 2 {
		return usageCostBreakdown{}, ErrPricingSnapshotIncomplete
	}
	if reservedAttribution == AttributionNonProvider {
		return usageCostBreakdown{
			ChargeDP: 0, PricingSnapshot: raw,
			Attribution: AttributionNonProvider,
		}, nil
	}
	if snapshot.DPPerUSD <= 0 ||
		snapshot.Provider == "" ||
		snapshot.CanonicalSKU == "" ||
		len(snapshot.RatesUSD) == 0 ||
		(snapshot.Attribution != "" &&
			snapshot.Attribution != reservedAttribution) {
		return usageCostBreakdown{}, ErrPricingSnapshotIncomplete
	}
	billingRecord := *rec
	billingRecord.Provider = snapshot.Provider
	billingRecord.Model = snapshot.CanonicalSKU
	service := providerCostServiceForUsage(
		&billingRecord,
		snapshot.Provider,
		snapshot.CanonicalSKU,
	)
	requireDuration := durationPricedService(service)
	upstream, err := calculateUsageFromUnitRates(
		&billingRecord,
		snapshot.RatesUSD,
		requireDuration,
	)
	if err != nil {
		return usageCostBreakdown{}, fmt.Errorf(
			"%w: upstream units: %v",
			ErrPricingSnapshotIncomplete,
			err,
		)
	}
	attribution := reservedAttribution
	chargeDP := 0.0
	platformUpstream := upstream
	serviceFee := 0.0
	switch attribution {
	case AttributionBYOK:
		chargeDP = upstream * snapshot.DPPerUSD * snapshot.MarkupPercent / 100
		platformUpstream = 0
		serviceFee = chargeDP
	case AttributionProviderPriced:
		if len(snapshot.RetailRatesDP) == 0 {
			return usageCostBreakdown{}, ErrPricingSnapshotIncomplete
		}
		chargeDP, err = calculateUsageFromUnitRates(
			&billingRecord,
			snapshot.RetailRatesDP,
			requireDuration,
		)
		if err != nil {
			return usageCostBreakdown{}, fmt.Errorf(
				"%w: retail units: %v",
				ErrPricingSnapshotIncomplete,
				err,
			)
		}
		serviceFee = chargeDP - upstream*snapshot.DPPerUSD
	default:
		return usageCostBreakdown{}, ErrPricingSnapshotIncomplete
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil || values == nil {
		return usageCostBreakdown{}, ErrPricingSnapshotIncomplete
	}
	values["provider_reference_cost_usd"] = upstream
	values["retail_dp"] = chargeDP
	values["settled_from_reservation_snapshot"] = true
	updated, err := json.Marshal(values)
	if err != nil {
		return usageCostBreakdown{}, err
	}
	return usageCostBreakdown{
		ChargeDP: chargeDP, UpstreamCostUSD: platformUpstream,
		ServiceFeeDP: serviceFee, PricingSnapshot: updated,
		Attribution: attribution,
	}, nil
}

func annotatePricingSnapshot(
	snapshot []byte,
	chargedDP float64,
	requestPolicies ...bool,
) []byte {
	var values map[string]any
	if err := json.Unmarshal(snapshot, &values); err != nil || values == nil {
		values = make(map[string]any)
	}
	values["charged_dp"] = chargedDP
	if len(requestPolicies) > 0 {
		values["billing_enabled"] = requestPolicies[0]
	}
	if len(requestPolicies) > 1 {
		values["allow_negative_balance"] = requestPolicies[1]
	}
	updated, err := json.Marshal(values)
	if err != nil {
		return snapshot
	}
	return updated
}

func billingPolicyFromPricingSnapshot(snapshot []byte) (bool, bool) {
	var values struct {
		BillingEnabled *bool `json:"billing_enabled"`
	}
	if err := json.Unmarshal(snapshot, &values); err != nil ||
		values.BillingEnabled == nil {
		return false, false
	}
	return *values.BillingEnabled, true
}

func negativeBalancePolicyFromPricingSnapshot(snapshot []byte) (bool, bool) {
	var values struct {
		AllowNegativeBalance *bool `json:"allow_negative_balance"`
	}
	if err := json.Unmarshal(snapshot, &values); err != nil ||
		values.AllowNegativeBalance == nil {
		return false, false
	}
	return *values.AllowNegativeBalance, true
}

func (s *Service) resolveUsageCost(
	ctx context.Context,
	rec *UsageRecord,
	retailDP float64,
) (usageCostBreakdown, error) {
	if nonProviderUsage(rec) {
		return resolveUsageCostWithView(rec, retailDP, nil)
	}
	view, err := s.loadUsagePricingView(ctx)
	if err != nil {
		return usageCostBreakdown{}, fmt.Errorf("load usage pricing view: %w", err)
	}
	return resolveUsageCostWithView(rec, retailDP, view)
}

func priceUsageWithView(
	rec *UsageRecord,
	view *usagePricingView,
) (usageCostBreakdown, error) {
	if nonProviderUsage(rec) {
		return resolveUsageCostWithView(rec, 0, nil)
	}
	if view == nil {
		return usageCostBreakdown{}, fmt.Errorf("usage pricing view is required")
	}
	if rec.CustomerFunded &&
		catalogPricingState(view.Config) != PricingStateCurrent {
		return usageCostBreakdown{}, fmt.Errorf(
			"%w: apply the latest catalog before using a customer provider key",
			ErrPricingCatalogNotApplied,
		)
	}
	retailDP := 0.0
	if !rec.CustomerFunded {
		var err error
		retailDP, err = calculateUsageCostFromRules(rec, view.Rules)
		if err != nil {
			return usageCostBreakdown{}, err
		}
	}
	return resolveUsageCostWithView(rec, retailDP, view)
}

func resolveUsageCostWithView(
	rec *UsageRecord,
	retailDP float64,
	view *usagePricingView,
) (usageCostBreakdown, error) {
	if nonProviderUsage(rec) {
		snapshot, _ := json.Marshal(map[string]any{
			"snapshot_version": 2,
			"attribution":      AttributionNonProvider,
			"mode":             "quota_only",
			"action":           rec.Action,
			"retail_dp":        retailDP,
		})
		return usageCostBreakdown{
			ChargeDP: 0, PricingSnapshot: snapshot,
			Attribution: AttributionNonProvider,
		}, nil
	}
	if view == nil {
		return usageCostBreakdown{}, fmt.Errorf("usage pricing view is required")
	}
	cfg := view.Config
	upstream, applied, markup, err := calculateProviderUpstream(
		rec,
		view.Rates,
		cfg,
	)
	if err != nil {
		log.Printf(
			"billing blocked unpriced provider usage action=%q provider=%q model=%q: %v",
			rec.Action,
			rec.Provider,
			rec.Model,
			err,
		)
		return usageCostBreakdown{}, err
	}
	provider, sku := CanonicalSKU(rec.Provider, rec.Model, rec.Action)
	retailRates := currentRetailUnitRatesFromRules(rec, view.Rules)
	attribution := AttributionProviderPriced
	chargeDP := retailDP
	platformUpstream := upstream
	serviceFee := retailDP - upstream*cfg.DPPerUSD
	if rec.CustomerFunded {
		attribution = AttributionBYOK
		chargeDP = customerFundedServiceFee(upstream, cfg, markup)
		platformUpstream = 0
		serviceFee = chargeDP
	}
	snapshot, _ := json.Marshal(map[string]any{
		"snapshot_version":            2,
		"catalog_version":             cfg.CatalogVersion,
		"estimate_catalog_version":    DefaultCatalogVersion,
		"pricing_state":               catalogPricingState(cfg),
		"dp_per_usd":                  cfg.DPPerUSD,
		"default_markup_percent":      cfg.DefaultMarkupPercent,
		"markup_percent":              markup,
		"model":                       rec.Model,
		"canonical_sku":               sku,
		"provider":                    provider,
		"action":                      rec.Action,
		"rates_usd":                   applied,
		"retail_rates_dp":             retailRates,
		"attribution":                 attribution,
		"provider_reference_cost_usd": upstream,
		"retail_dp":                   chargeDP,
	})
	return usageCostBreakdown{
		ChargeDP: chargeDP, UpstreamCostUSD: platformUpstream,
		ServiceFeeDP: serviceFee, PricingSnapshot: snapshot,
		Attribution: attribution,
	}, nil
}
