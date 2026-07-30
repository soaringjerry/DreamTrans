package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	DefaultCatalogVersion = "2026-07-30"
	DefaultDPPerUSD       = 1.0
	DefaultMarkupPercent  = 100.0
)

const (
	openAIPriceSource       = "https://developers.openai.com/api/docs/models"
	speechmaticsPriceSource = "https://www.speechmatics.com/pricing"
)

type CostRate struct {
	ID              string  `json:"id,omitempty"`
	Provider        string  `json:"provider"`
	SKU             string  `json:"sku"`
	Service         string  `json:"service"`
	UnitType        string  `json:"unit_type"`
	CostPerUnitUSD  float64 `json:"cost_per_unit_usd"`
	RetailDPPerUnit float64 `json:"retail_dp_per_unit"`
	MarkupPercent   float64 `json:"markup_percent"`
	GrossMargin     float64 `json:"gross_margin_percent"`
	CatalogVersion  string  `json:"catalog_version"`
	SourceURL       string  `json:"source_url"`
	EffectiveAt     string  `json:"effective_at,omitempty"`
	IsBuiltin       bool    `json:"is_builtin"`
	IsActive        bool    `json:"is_active"`
	OverrideSource  string  `json:"override_source"`
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
	UpdatedAt            string           `json:"updated_at,omitempty"`
	Overrides            []MarkupOverride `json:"overrides"`
}

type BillingCatalog struct {
	BuiltinVersion   string        `json:"builtin_version"`
	InstalledVersion string        `json:"installed_version"`
	Config           BillingConfig `json:"config"`
	Rates            []CostRate    `json:"rates"`
	HasUpdate        bool          `json:"has_update"`
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

type BillingPreview struct {
	Config       BillingConfig `json:"config"`
	Rates        []CostRate    `json:"rates"`
	Added        int           `json:"added"`
	Updated      int           `json:"updated"`
	Disabled     int           `json:"disabled"`
	Confirmation string        `json:"confirmation"`
}

type BillingAnalytics struct {
	UpstreamCostUSD float64 `json:"upstream_cost_usd"`
	ServiceFeeDP    float64 `json:"service_fee_dp"`
	RetailDP        float64 `json:"retail_dp"`
	UsageCount      int64   `json:"usage_count"`
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
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 2.50e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "cache_write_token", CostPerUnitUSD: 3.125e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-terra", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 15.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "input_token", CostPerUnitUSD: 1.00e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "cached_input_token", CostPerUnitUSD: 0.10e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "cache_write_token", CostPerUnitUSD: 1.25e-6, SourceURL: openAIPriceSource},
	{Provider: "openai-compatible", SKU: "gpt-5.6-luna", Service: "llm", UnitType: "output_token", CostPerUnitUSD: 6.00e-6, SourceURL: openAIPriceSource},
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertBuiltinCatalogTx(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertBuiltinCatalogTx(ctx context.Context, tx *sql.Tx) error {
	for _, rate := range builtinCostRates {
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
			time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GetBillingCatalog(ctx context.Context) (*BillingCatalog, error) {
	cfg, err := s.getBillingConfig(ctx)
	if err != nil {
		return nil, err
	}
	rates, err := s.getCostRates(ctx)
	if err != nil {
		return nil, err
	}
	applyRetailPrices(rates, cfg)
	return &BillingCatalog{
		BuiltinVersion: DefaultCatalogVersion, InstalledVersion: cfg.CatalogVersion,
		Config: cfg, Rates: rates, HasUpdate: cfg.CatalogVersion != DefaultCatalogVersion,
	}, nil
}

func (s *Service) getBillingConfig(ctx context.Context) (BillingConfig, error) {
	var cfg BillingConfig
	if err := s.db.QueryRowContext(ctx, `
		SELECT dp_per_usd, default_markup_percent, catalog_version, updated_at
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&cfg.DPPerUSD, &cfg.DefaultMarkupPercent, &cfg.CatalogVersion, &cfg.UpdatedAt); err != nil {
		return cfg, err
	}
	rows, err := s.db.QueryContext(ctx, `
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
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, provider, sku, service, unit_type, cost_per_unit_usd,
		       catalog_version, source_url, COALESCE(effective_at::text, ''),
		       is_builtin, is_active
		FROM provider_cost_rates
		ORDER BY provider, service, sku, unit_type
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var rates []CostRate
	for rows.Next() {
		var rate CostRate
		if err := rows.Scan(&rate.ID, &rate.Provider, &rate.SKU, &rate.Service,
			&rate.UnitType, &rate.CostPerUnitUSD, &rate.CatalogVersion,
			&rate.SourceURL, &rate.EffectiveAt, &rate.IsBuiltin, &rate.IsActive); err != nil {
			return nil, err
		}
		rates = append(rates, rate)
	}
	return rates, rows.Err()
}

func effectiveMarkup(rate CostRate, cfg BillingConfig) (float64, string) {
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
		markup, source := effectiveMarkup(rates[i], cfg)
		rates[i].MarkupPercent = markup
		rates[i].OverrideSource = source
		rates[i].RetailDPPerUnit = rates[i].CostPerUnitUSD * cfg.DPPerUSD * (1 + markup/100)
		if markup > 0 {
			rates[i].GrossMargin = markup / (100 + markup) * 100
		}
	}
}

func (s *Service) PreviewBillingConfig(ctx context.Context, input BillingConfigInput) (*BillingPreview, error) {
	if err := validateBillingConfig(input); err != nil {
		return nil, err
	}
	rates, err := s.getCostRates(ctx)
	if err != nil {
		return nil, err
	}
	cfg := BillingConfig{
		DPPerUSD: input.DPPerUSD, DefaultMarkupPercent: input.DefaultMarkupPercent,
		CatalogVersion: DefaultCatalogVersion, Overrides: input.Overrides,
	}
	applyRetailPrices(rates, cfg)
	return &BillingPreview{Config: cfg, Rates: rates}, nil
}

func (s *Service) UpdateBillingConfig(ctx context.Context, input BillingConfigInput, actorID string) error {
	if err := validateBillingConfig(input); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE billing_config
		SET dp_per_usd = $1, default_markup_percent = $2,
		    catalog_version = $3, updated_at = NOW(), updated_by = $4
		WHERE singleton = TRUE
	`, input.DPPerUSD, input.DefaultMarkupPercent, DefaultCatalogVersion, nullUUID(actorID)); err != nil {
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
		`, override.ScopeType, override.ScopeKey, override.MarkupPercent, nullUUID(actorID)); err != nil {
			return err
		}
	}
	if err := regenerateManagedPricingRulesTx(ctx, tx, DefaultCatalogVersion); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, actorID, "billing.config.update", "billing_config", DefaultCatalogVersion, input); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.refreshRulesCache()
}

// ApplyBuiltinCatalog updates official cost rows and recomputes managed retail
// prices while preserving the administrator's currency conversion and markup
// overrides. ResetBillingDefaults is the stronger operation that clears them.
func (s *Service) ApplyBuiltinCatalog(ctx context.Context, actorID string) (*BillingCatalog, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
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
		SET catalog_version = $1, updated_at = NOW(), updated_by = $2
		WHERE singleton = TRUE
	`, DefaultCatalogVersion, nullUUID(actorID)); err != nil {
		return nil, err
	}
	if err := regenerateManagedPricingRulesTx(ctx, tx, DefaultCatalogVersion); err != nil {
		return nil, err
	}
	if err := insertAuditTx(ctx, tx, actorID, "billing.catalog.apply", "billing_config", DefaultCatalogVersion, map[string]any{
		"catalog_version": DefaultCatalogVersion,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.refreshRulesCache(); err != nil {
		return nil, err
	}
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
	if input.InputPerMillionUSD == 0 && input.OutputPerMillionUSD == 0 {
		return fmt.Errorf("at least one model cost must be greater than zero")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, value := range values {
		if value.perMillion == 0 {
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM provider_cost_rates
				WHERE provider = 'openai-compatible' AND sku = $1
				  AND service = $2 AND unit_type = $3 AND is_builtin = FALSE
			`, input.ModelID, input.Service, value.unit); err != nil {
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
				is_builtin = FALSE,
				is_active = TRUE,
				updated_at = NOW()
		`, input.ModelID, input.Service, value.unit, value.perMillion/1_000_000); err != nil {
			return err
		}
	}
	if err := regenerateManagedPricingRulesTx(ctx, tx, DefaultCatalogVersion); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, actorID, "billing.model_cost.update", "model", input.ModelID, input); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.refreshRulesCache()
}

func (s *Service) PreviewBillingReset(ctx context.Context) (*BillingPreview, error) {
	current, err := s.GetBillingCatalog(ctx)
	if err != nil {
		return nil, err
	}
	defaultRates := append([]CostRate(nil), builtinCostRates...)
	cfg := BillingConfig{
		DPPerUSD: DefaultDPPerUSD, DefaultMarkupPercent: DefaultMarkupPercent,
		CatalogVersion: DefaultCatalogVersion, Overrides: []MarkupOverride{},
	}
	applyRetailPrices(defaultRates, cfg)
	existing := make(map[string]CostRate, len(current.Rates))
	for _, rate := range current.Rates {
		existing[costRateKey(rate)] = rate
	}
	preview := &BillingPreview{Config: cfg, Rates: defaultRates, Confirmation: "重置计费"}
	for _, rate := range defaultRates {
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
	return preview, nil
}

func costRateKey(rate CostRate) string {
	return rate.Provider + "\x00" + rate.SKU + "\x00" + rate.Service + "\x00" + rate.UnitType
}

func (s *Service) ResetBillingDefaults(ctx context.Context, actorID, confirmation string) (*BillingCatalog, error) {
	if confirmation != "重置计费" {
		return nil, fmt.Errorf("confirmation text does not match")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM billing_markup_overrides`); err != nil {
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
		    catalog_version = $3, updated_at = NOW(), updated_by = $4
		WHERE singleton = TRUE
	`, DefaultDPPerUSD, DefaultMarkupPercent, DefaultCatalogVersion, nullUUID(actorID)); err != nil {
		return nil, err
	}
	// A reset is the explicit boundary at which legacy/manual retail rules are
	// replaced. The ledger is untouched.
	if _, err := tx.ExecContext(ctx, `UPDATE pricing_rules SET is_active = FALSE`); err != nil {
		return nil, err
	}
	if err := regenerateManagedPricingRulesTx(ctx, tx, DefaultCatalogVersion); err != nil {
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
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if err := s.refreshRulesCache(); err != nil {
		return nil, err
	}
	return s.GetBillingCatalog(ctx)
}

func regenerateManagedPricingRulesTx(ctx context.Context, tx *sql.Tx, version string) error {
	var dpPerUSD, defaultMarkup float64
	if err := tx.QueryRowContext(ctx, `
		SELECT dp_per_usd, default_markup_percent
		FROM billing_config WHERE singleton = TRUE
	`).Scan(&dpPerUSD, &defaultMarkup); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT provider, sku, service, unit_type, cost_per_unit_usd
		FROM provider_cost_rates WHERE is_active = TRUE
		ORDER BY provider, sku, service, unit_type
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
	for _, rate := range rates {
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
					(rule_type, model, price_per_unit, unit_type, description,
					 is_active, priority, managed_key, source, catalog_version)
				VALUES ($1, $2, $3, $4, $5, TRUE, 100, $6, 'managed', $7)
				ON CONFLICT (managed_key) WHERE managed_key IS NOT NULL DO UPDATE SET
					price_per_unit = EXCLUDED.price_per_unit,
					description = EXCLUDED.description,
					is_active = TRUE,
					catalog_version = EXCLUDED.catalog_version,
					updated_at = NOW()
			`, action, rate.SKU, rate.RetailDPPerUnit, rate.UnitType,
				description, managedKey, version); err != nil {
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
		{"transcription", "speechmatics-batch", "speechmatics-batch-enhanced"},
	}
	for _, alias := range aliases {
		for _, rate := range rates {
			if rate.SKU != alias.target || rate.UnitType != "hour" {
				continue
			}
			key := "alias:" + alias.action + ":" + alias.model + ":hour"
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO pricing_rules
					(rule_type, model, price_per_unit, unit_type, description,
					 is_active, priority, managed_key, source, catalog_version)
				VALUES ($1, $2, $3, 'hour', $4, TRUE, 100, $5, 'managed', $6)
				ON CONFLICT (managed_key) WHERE managed_key IS NOT NULL DO UPDATE SET
					price_per_unit = EXCLUDED.price_per_unit,
					description = EXCLUDED.description,
					is_active = TRUE,
					catalog_version = EXCLUDED.catalog_version,
					updated_at = NOW()
			`, alias.action, alias.model, rate.RetailDPPerUnit,
				"Compatibility alias for "+alias.target, key, version); err != nil {
				return err
			}
		}
	}
	return nil
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
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(upstream_cost_usd), 0),
		       COALESCE(SUM(service_fee_dp), 0),
		       COALESCE(SUM(cost), 0),
		       COUNT(*)
		FROM usage_logs
		WHERE refunded_at IS NULL
	`).Scan(&result.UpstreamCostUSD, &result.ServiceFeeDP, &result.RetailDP, &result.UsageCount)
	return &result, err
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
	args := []any{userID}
	filter := ""
	if strings.TrimSpace(sessionID) != "" {
		args = append(args, strings.TrimSpace(sessionID))
		filter = " AND session_id = $2"
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, action, COALESCE(model, ''), quantity,
		       COALESCE(input_tokens, 0), cached_input_tokens, cache_write_tokens,
		       COALESCE(output_tokens, 0), upstream_cost_usd, service_fee_dp,
		       cost, pricing_snapshot, created_at
		FROM usage_logs
		WHERE user_id = $1`+filter+`
		ORDER BY created_at DESC
		LIMIT $`+fmt.Sprint(len(args)), args...)
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

func (s *Service) upstreamBreakdown(ctx context.Context, rec *UsageRecord, retailDP float64) (float64, float64, []byte) {
	cfg, err := s.getBillingConfig(ctx)
	if err != nil {
		return 0, retailDP, []byte(`{"mode":"legacy"}`)
	}
	rates, err := s.getCostRates(ctx)
	if err != nil {
		return 0, retailDP, []byte(`{"mode":"legacy"}`)
	}
	var candidates []CostRate
	for _, rate := range rates {
		if rate.IsActive && rate.SKU == rec.Model {
			candidates = append(candidates, rate)
		}
	}
	ordinaryInput := rec.InputTokens - rec.CachedInputTokens - rec.CacheWriteTokens
	if ordinaryInput < 0 {
		ordinaryInput = rec.InputTokens
	}
	var upstream float64
	applied := make(map[string]float64)
	hasCached := false
	hasWrite := false
	for _, rate := range candidates {
		var quantity float64
		switch rate.UnitType {
		case "hour":
			quantity = rec.Quantity / 60
		case "minute":
			quantity = rec.Quantity
		case "input_token":
			quantity = float64(ordinaryInput)
		case "cached_input_token":
			hasCached = true
			quantity = float64(rec.CachedInputTokens)
		case "cache_write_token":
			hasWrite = true
			quantity = float64(rec.CacheWriteTokens)
		case "output_token":
			quantity = float64(rec.OutputTokens)
		}
		if quantity > 0 {
			upstream += rate.CostPerUnitUSD * quantity
			applied[rate.UnitType] = rate.CostPerUnitUSD
		}
	}
	// Missing cache-specific rates are conservatively charged at normal input
	// cost, matching the retail pricing fallback.
	for _, rate := range candidates {
		if rate.UnitType != "input_token" {
			continue
		}
		if !hasCached {
			upstream += rate.CostPerUnitUSD * float64(rec.CachedInputTokens)
		}
		if !hasWrite {
			upstream += rate.CostPerUnitUSD * float64(rec.CacheWriteTokens)
		}
	}
	costDP := upstream * cfg.DPPerUSD
	serviceFee := retailDP - costDP
	snapshot, _ := json.Marshal(map[string]any{
		"catalog_version":        cfg.CatalogVersion,
		"dp_per_usd":             cfg.DPPerUSD,
		"default_markup_percent": cfg.DefaultMarkupPercent,
		"model":                  rec.Model,
		"action":                 rec.Action,
		"rates_usd":              applied,
		"retail_dp":              retailDP,
	})
	return upstream, serviceFee, snapshot
}

func sortedBuiltinRates() []CostRate {
	rates := append([]CostRate(nil), builtinCostRates...)
	sort.Slice(rates, func(i, j int) bool { return costRateKey(rates[i]) < costRateKey(rates[j]) })
	return rates
}
