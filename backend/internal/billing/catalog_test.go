package billing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
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
	applyRetailPrices(rates, cfg)
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
	if _, err := service.ResetBillingDefaults(t.Context(), "", "RESET"); err == nil {
		t.Fatal("expected confirmation mismatch before database work")
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
