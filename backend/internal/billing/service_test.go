package billing

import (
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/dreamtrans/backend/internal/models"
	_ "modernc.org/sqlite"
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

func TestCalculateCostKeepsProviderSpecificPricesIsolated(t *testing.T) {
	model := "shared-model"
	providerA := "provider-a"
	providerB := "provider-b"
	service := &Service{rulesCache: []PricingRule{
		{
			RuleType: "chat", Provider: &providerA, Model: &model,
			UnitType: "input_token", PricePerUnit: 0.2, Priority: 1,
		},
		{
			RuleType: "chat", Provider: &providerB, Model: &model,
			UnitType: "input_token", PricePerUnit: 0.8, Priority: 100,
		},
		{
			RuleType: "chat", Model: &model,
			UnitType: "input_token", PricePerUnit: 0.1, Priority: 1000,
		},
	}}

	gotA, appliedA := service.calculateCostForProvider(
		"chat", providerA, model, 0, 10, 0, 0, 0,
	)
	if gotA != 2 || !appliedA["input_token"] {
		t.Fatalf("provider A cost = %v, applied=%v; want 2", gotA, appliedA)
	}
	gotB, _ := service.calculateCostForProvider(
		"chat", providerB, model, 0, 10, 0, 0, 0,
	)
	if gotB != 8 {
		t.Fatalf("provider B cost = %v, want 8", gotB)
	}
	gotLegacy, _ := service.calculateCostForProvider(
		"chat", "provider-c", model, 0, 10, 0, 0, 0,
	)
	if gotLegacy != 1 {
		t.Fatalf("legacy provider-neutral fallback cost = %v, want 1", gotLegacy)
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
	if _, err := service.calculateUsageCost(&UsageRecord{
		Action: "translation", Model: "speechmatics-translation", Quantity: 60,
	}); err == nil {
		t.Fatal("Speechmatics duration add-on without a duration retail rule was accepted")
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
	provider := "  OPENAI-Compatible "
	rule := PricingRule{
		RuleType: "embedding", UnitType: "input_token",
		PricePerUnit: 0.00000002, Model: &model, Provider: &provider,
	}
	if err := ValidatePricingRule(&rule); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if rule.Model != nil {
		t.Fatalf("blank model should normalize to nil")
	}
	if rule.Provider == nil || *rule.Provider != "openai-compatible" {
		t.Fatalf("provider = %v, want normalized openai-compatible", rule.Provider)
	}
}

func openPricingRuleMutationTestDB(t *testing.T, includeUpdatedAt bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`
		CREATE TABLE billing_config (
			singleton BOOLEAN PRIMARY KEY
		);
		INSERT INTO billing_config (singleton) VALUES (TRUE)
	`); err != nil {
		t.Fatal(err)
	}
	updatedAtColumn := ""
	if includeUpdatedAt {
		updatedAtColumn = ", updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP"
	}
	if _, err := db.Exec(`
		CREATE TABLE pricing_rules (
			id TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(16)))),
			rule_type TEXT NOT NULL,
			provider TEXT,
			model TEXT,
			price_per_unit REAL NOT NULL,
			unit_type TEXT NOT NULL,
			description TEXT,
			is_active BOOLEAN NOT NULL,
			priority INTEGER NOT NULL,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
			` + updatedAtColumn + `
		)
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE admin_audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_user_id TEXT,
			action TEXT NOT NULL,
			target_type TEXT NOT NULL,
			target_id TEXT,
			details BLOB NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestPricingRuleMutationsAuditAndReplaceCacheAtomically(t *testing.T) {
	db := openPricingRuleMutationTestDB(t, true)
	service := &Service{db: db}
	actorID := "00000000-0000-0000-0000-000000000001"
	provider := "openai-compatible"
	model := "gpt-test"
	rule := &PricingRule{
		RuleType:     "translation",
		Provider:     &provider,
		Model:        &model,
		PricePerUnit: 1,
		UnitType:     "input_token",
		Description:  "created",
		IsActive:     true,
		Priority:     10,
	}

	if err := service.CreatePricingRuleAs(t.Context(), actorID, rule); err != nil {
		t.Fatalf("create pricing rule: %v", err)
	}
	if rule.ID == "" {
		t.Fatal("created rule id was not returned")
	}
	if len(service.rulesCache) != 1 || service.rulesCache[0].PricePerUnit != 1 {
		t.Fatalf("cache after create = %#v", service.rulesCache)
	}

	rule.PricePerUnit = 2
	rule.Description = "updated"
	if err := service.UpdatePricingRuleAs(
		t.Context(),
		actorID,
		rule.ID,
		rule,
	); err != nil {
		t.Fatalf("update pricing rule: %v", err)
	}
	if len(service.rulesCache) != 1 ||
		service.rulesCache[0].PricePerUnit != 2 ||
		service.rulesCache[0].Description != "updated" {
		t.Fatalf("cache after update = %#v", service.rulesCache)
	}

	if err := service.DeletePricingRuleAs(
		t.Context(),
		actorID,
		rule.ID,
	); err != nil {
		t.Fatalf("delete pricing rule: %v", err)
	}
	if len(service.rulesCache) != 0 {
		t.Fatalf("cache after delete = %#v", service.rulesCache)
	}

	rows, err := db.Query(`
		SELECT actor_user_id, action, target_type, target_id, CAST(details AS TEXT)
		FROM admin_audit_logs
		ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	wantActions := []string{
		"billing.pricing_rule.create",
		"billing.pricing_rule.update",
		"billing.pricing_rule.delete",
	}
	var gotActions []string
	for rows.Next() {
		var gotActor, action, targetType, targetID, details string
		if err := rows.Scan(
			&gotActor,
			&action,
			&targetType,
			&targetID,
			&details,
		); err != nil {
			t.Fatal(err)
		}
		if gotActor != actorID ||
			targetType != "pricing_rule" ||
			targetID != rule.ID ||
			!strings.Contains(details, `"rule"`) {
			t.Fatalf(
				"invalid audit row: actor=%q action=%q type=%q target=%q details=%q",
				gotActor,
				action,
				targetType,
				targetID,
				details,
			)
		}
		gotActions = append(gotActions, action)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(gotActions) != len(wantActions) {
		t.Fatalf("audit actions = %#v, want %#v", gotActions, wantActions)
	}
	for i := range wantActions {
		if gotActions[i] != wantActions[i] {
			t.Fatalf("audit actions = %#v, want %#v", gotActions, wantActions)
		}
	}
}

func TestPricingRuleMutationRollsBackWhenActiveCacheCannotBePrepared(t *testing.T) {
	db := openPricingRuleMutationTestDB(t, false)
	service := &Service{db: db}
	rule := &PricingRule{
		RuleType: "chat", UnitType: "input_token",
		PricePerUnit: 1, IsActive: true,
	}

	err := service.CreatePricingRuleAs(
		t.Context(),
		"00000000-0000-0000-0000-000000000001",
		rule,
	)
	if err == nil || !strings.Contains(err.Error(), "updated_at") {
		t.Fatalf("cache preparation error = %v", err)
	}
	for _, table := range []string{"pricing_rules", "admin_audit_logs"} {
		var count int
		if countErr := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); countErr != nil {
			t.Fatal(countErr)
		}
		if count != 0 {
			t.Fatalf("%s count after rollback = %d, want 0", table, count)
		}
	}
	if len(service.rulesCache) != 0 {
		t.Fatalf("cache changed after rollback: %#v", service.rulesCache)
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

func TestNormalizeOperationFingerprintHandlesPostgresBPCharPadding(t *testing.T) {
	const fingerprint = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	for _, test := range []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "empty fingerprint padded by char column",
			value: strings.Repeat(" ", 64),
			want:  "",
		},
		{
			name:  "canonical fingerprint is unchanged",
			value: fingerprint,
			want:  fingerprint,
		},
		{
			name:  "input normalization remains case insensitive",
			value: "  " + strings.ToUpper(fingerprint) + "  ",
			want:  fingerprint,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := normalizeOperationFingerprint(test.value); got != test.want {
				t.Fatalf("normalized fingerprint = %q, want %q", got, test.want)
			}
		})
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
