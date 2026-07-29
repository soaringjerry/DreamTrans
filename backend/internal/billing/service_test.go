package billing

import (
	"errors"
	"math"
	"testing"

	"github.com/dreamtrans/backend/internal/models"
)

func stringPointer(value string) *string { return &value }

func TestCalculateCostPrefersModelSpecificRulesPerUnit(t *testing.T) {
	service := &Service{rulesCache: []PricingRule{
		{RuleType: "translation", Model: stringPointer("special"), UnitType: "input_token", PricePerUnit: 0.2, Priority: 10},
		{RuleType: "translation", Model: stringPointer("special"), UnitType: "output_token", PricePerUnit: 0.4, Priority: 10},
		{RuleType: "translation", Model: nil, UnitType: "input_token", PricePerUnit: 0.1},
		{RuleType: "translation", Model: nil, UnitType: "output_token", PricePerUnit: 0.3},
	}}

	got := service.CalculateCost("translation", "special", 0, 2, 3)
	want := 2*0.2 + 3*0.4
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCalculateCostFallsBackIndependentlyByUnit(t *testing.T) {
	service := &Service{rulesCache: []PricingRule{
		{RuleType: "chat", Model: stringPointer("special"), UnitType: "input_token", PricePerUnit: 0.2, Priority: 10},
		{RuleType: "chat", Model: nil, UnitType: "input_token", PricePerUnit: 0.1},
		{RuleType: "chat", Model: nil, UnitType: "output_token", PricePerUnit: 0.3},
	}}

	got := service.CalculateCost("chat", "special", 0, 2, 3)
	want := 2*0.2 + 3*0.3
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestCalculateUsageCostFailsClosedWhenActiveRuleIsMissing(t *testing.T) {
	service := &Service{rulesCache: []PricingRule{
		{RuleType: "chat", UnitType: "input_token", PricePerUnit: 0},
	}}
	if _, err := service.calculateUsageCost(&UsageRecord{
		Action: "chat", InputTokens: 10, OutputTokens: 5,
	}); err == nil {
		t.Fatal("missing output-token rule was accepted")
	}
	cost, err := service.calculateUsageCost(&UsageRecord{
		Action: "chat", InputTokens: 10,
	})
	if err != nil {
		t.Fatalf("explicit zero-price input rule was rejected: %v", err)
	}
	if cost != 0 {
		t.Fatalf("zero-price rule cost = %v, want 0", cost)
	}
	if _, err := service.calculateUsageCost(&UsageRecord{
		Action: "typo", InputTokens: 1,
	}); err == nil {
		t.Fatal("unsupported action was accepted")
	}
}

func TestParseJSONBoolHandlesJSONScalarsAndStrings(t *testing.T) {
	for _, value := range []string{"true", `"true"`} {
		if !parseJSONBool(value, false) {
			t.Fatalf("expected %q to parse true", value)
		}
	}
	for _, value := range []string{"false", `"false"`} {
		if parseJSONBool(value, true) {
			t.Fatalf("expected %q to parse false", value)
		}
	}
}

func TestValidatePricingRuleRejectsUnsafeValues(t *testing.T) {
	tests := []PricingRule{
		{RuleType: "translation", UnitType: "input_token", PricePerUnit: -1},
		{RuleType: "unknown", UnitType: "minute", PricePerUnit: 1},
		{RuleType: "chat", UnitType: "request", PricePerUnit: 1},
		{RuleType: "chat", UnitType: "output_token", PricePerUnit: math.NaN()},
		{RuleType: "chat", UnitType: "output_token", PricePerUnit: 100_000_000},
		{RuleType: "chat", UnitType: "output_token", PricePerUnit: 1, Priority: 1001},
	}
	for i := range tests {
		if err := ValidatePricingRule(&tests[i]); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

func TestValidatePricingRuleNormalizesOptionalModel(t *testing.T) {
	model := "  "
	rule := PricingRule{
		RuleType: "embedding", UnitType: "input_token",
		PricePerUnit: 0.00000002, Model: &model,
	}
	if err := ValidatePricingRule(&rule); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if rule.Model != nil {
		t.Fatalf("blank model should normalize to nil")
	}
}

func TestRecordUsageBatchRejectsMixedTenantsBeforeDatabaseWork(t *testing.T) {
	service := &Service{}
	_, err := service.RecordUsageBatch(t.Context(), []*UsageRecord{
		{UserID: "user-1", TenantID: "tenant-1", Action: "chat"},
		{UserID: "user-1", TenantID: "tenant-2", Action: "rag_query"},
	})
	if err == nil {
		t.Fatal("mixed-tenant usage batch must be rejected")
	}
}

func TestValidateSettlementUsageAllowsZeroQuantityAndCalculatesActualCost(t *testing.T) {
	service := &Service{rulesCache: []PricingRule{
		{RuleType: "translation", UnitType: "input_token", PricePerUnit: 0.1},
		{RuleType: "translation", UnitType: "output_token", PricePerUnit: 0.3},
	}}
	actual := &UsageRecord{
		UserID: " user-1 ", TenantID: " tenant-1 ", Action: " translation ",
		Model: " model ", Quantity: 0, InputTokens: 2, OutputTokens: 3,
	}
	cost, err := service.validateSettlementUsage(actual)
	if err != nil {
		t.Fatalf("validate actual usage: %v", err)
	}
	if want := 2*0.1 + 3*0.3; math.Abs(cost-want) > 1e-12 {
		t.Fatalf("settlement cost = %v, want %v", cost, want)
	}
	if actual.UserID != "user-1" || actual.TenantID != "tenant-1" ||
		actual.Action != "translation" || actual.Model != "model" {
		t.Fatalf("settlement fields were not normalized: %#v", actual)
	}
}

func TestValidateSettlementUsageRejectsUnsafeActualUsage(t *testing.T) {
	service := &Service{}
	tests := []*UsageRecord{
		nil,
		{UserID: "user", TenantID: "tenant", Action: "translation", Quantity: -1},
		{UserID: "user", TenantID: "tenant", Action: "translation", Quantity: math.NaN()},
		{UserID: "user", TenantID: "tenant", Action: "translation", InputTokens: -1},
		{UserID: "user", TenantID: "tenant", Action: "translation", OutputTokens: -1},
		{UserID: "", TenantID: "tenant", Action: "translation"},
	}
	for index, actual := range tests {
		if _, err := service.validateSettlementUsage(actual); err == nil {
			t.Fatalf("case %d: unsafe settlement usage was accepted", index)
		}
	}
}

func TestValidateTenantPlanUsageEnforcesHardCeilings(t *testing.T) {
	limits := models.PlanLimits{TranscriptionMinutes: 60, RAGQueries: 100}
	for _, test := range []struct {
		name          string
		transcription float64
		ragQueries    int64
		wantQuotaErr  bool
	}{
		{name: "at both limits", transcription: 60, ragQueries: 100},
		{name: "transcription over", transcription: 60.01, ragQueries: 100, wantQuotaErr: true},
		{name: "RAG over", transcription: 60, ragQueries: 101, wantQuotaErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateTenantPlanUsage(limits, test.transcription, test.ragQueries)
			if got := errors.Is(err, ErrPlanQuotaExceeded); got != test.wantQuotaErr {
				t.Fatalf("quota error = %v, want %v (err=%v)", got, test.wantQuotaErr, err)
			}
		})
	}
}

func TestValidateTenantPlanUsageAllowsUnlimitedPlan(t *testing.T) {
	limits := models.PlanLimits{TranscriptionMinutes: -1, RAGQueries: -1}
	if err := validateTenantPlanUsage(limits, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("unlimited plan was rejected: %v", err)
	}
}
