// Package billing implements pricing, atomic usage charging, and refunds.
package billing

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

const maxManualBalanceAdjustment = 1_000_000_000
const maxUsageQuantity = 1_000_000_000
const maxDatabaseTokenCount = 2_147_483_647
const maxStoredUsageCost = 100_000_000
const usageFingerprintBytes = 32

var ErrPricingRuleNotFound = errors.New("pricing rule not found")
var ErrPlanQuotaExceeded = errors.New("tenant plan quota exceeded")

func normalizeOperationFingerprint(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

type PricingRule struct {
	ID           string  `json:"id"`
	RuleType     string  `json:"rule_type"`
	Model        *string `json:"model"`
	PricePerUnit float64 `json:"price_per_unit"`
	UnitType     string  `json:"unit_type"`
	Description  string  `json:"description"`
	IsActive     bool    `json:"is_active"`
	Priority     int     `json:"priority"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
}

type UsageRecord struct {
	UserID      string
	TenantID    string
	SessionID   *string
	Action      string // transcription, translation, chat, summarize
	Model       string
	Quantity    float64 // minutes for transcription
	InputTokens int
	// CachedInputTokens and CacheWriteTokens are subsets of InputTokens when
	// the provider reports prompt-cache details.
	CachedInputTokens int
	CacheWriteTokens  int
	OutputTokens      int
	// IdempotencyKey prevents retries of externally identified work (for
	// example a Speechmatics batch job) from charging twice.
	IdempotencyKey string
	// IdempotencyDuplicate is output-only: RecordUsage marks it when the key
	// already existed and no new debit was written. Callers that cannot replay
	// the original result must not execute the upstream operation again.
	IdempotencyDuplicate bool
	// ReuseRefundedReservation permits a durable provider workflow to start a
	// new attempt with the same logical key after a known provider failure was
	// fully refunded. It does not re-debit settled or ambiguous reservations.
	ReuseRefundedReservation bool
	// OperationFingerprint binds an idempotency key to immutable logical
	// provider input. It prevents a changed retry path from reusing and
	// overwriting an unrelated settled operation.
	OperationFingerprint string
}

type BalanceTransaction struct {
	ID              string  `json:"id"`
	UserID          string  `json:"user_id"`
	Amount          float64 `json:"amount"`
	BalanceAfter    float64 `json:"balance_after"`
	TransactionType string  `json:"transaction_type"`
	ReferenceType   *string `json:"reference_type"`
	ReferenceID     *string `json:"reference_id"`
	Description     string  `json:"description"`
	CreatedBy       *string `json:"created_by"`
	CreatedAt       string  `json:"created_at"`
}

type UserBalance struct {
	UserID          string  `json:"user_id"`
	Email           string  `json:"email"`
	Name            string  `json:"name"`
	Dreampoints     float64 `json:"dreampoints"`
	DreampointsUsed float64 `json:"dreampoints_used"`
}

type SystemStats struct {
	TotalUsers       int                `json:"total_users"`
	ActiveUsers      int                `json:"active_users"`
	TotalSessions    int                `json:"total_sessions"`
	TotalTranscripts int                `json:"total_transcripts"`
	TotalDreampoints float64            `json:"total_dreampoints"`
	TotalUsed        float64            `json:"total_used"`
	UsageByAction    map[string]float64 `json:"usage_by_action"`
	UsageByModel     map[string]float64 `json:"usage_by_model"`
}

type Service struct {
	db           *sql.DB
	rulesCache   []PricingRule
	rulesCacheMu sync.RWMutex
	lastRefresh  time.Time
}

func NewService(db *sql.DB) *Service {
	s := &Service{db: db}
	if err := s.refreshRulesCache(); err != nil {
		log.Printf("Failed to initialize pricing rules cache: %v", err)
	}
	return s
}

func (s *Service) refreshRulesCache() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, rule_type, model, price_per_unit, unit_type,
		       COALESCE(description, ''), is_active, priority
		FROM pricing_rules
		WHERE is_active = true
		ORDER BY priority DESC, updated_at DESC, id DESC
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var rules []PricingRule
	for rows.Next() {
		var r PricingRule
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Model, &r.PricePerUnit,
			&r.UnitType, &r.Description, &r.IsActive, &r.Priority); err != nil {
			return err
		}
		rules = append(rules, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	s.rulesCacheMu.Lock()
	s.rulesCache = rules
	s.lastRefresh = time.Now()
	s.rulesCacheMu.Unlock()
	return nil
}

func (s *Service) GetPricingRules(ctx context.Context) ([]PricingRule, error) {
	// Refresh cache if older than 5 minutes
	s.rulesCacheMu.RLock()
	lastRefresh := s.lastRefresh
	s.rulesCacheMu.RUnlock()
	if time.Since(lastRefresh) > 5*time.Minute {
		if err := s.refreshRulesCache(); err != nil {
			return nil, err
		}
	}

	s.rulesCacheMu.RLock()
	defer s.rulesCacheMu.RUnlock()
	return append([]PricingRule(nil), s.rulesCache...), nil
}

func (s *Service) GetAllPricingRules(ctx context.Context) ([]PricingRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, rule_type, model, price_per_unit, unit_type,
		       COALESCE(description, ''), is_active, priority,
		       created_at, updated_at
		FROM pricing_rules
		ORDER BY rule_type, priority DESC, updated_at DESC, id DESC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var rules []PricingRule
	for rows.Next() {
		var r PricingRule
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Model, &r.PricePerUnit,
			&r.UnitType, &r.Description, &r.IsActive, &r.Priority,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (s *Service) CreatePricingRule(ctx context.Context, r *PricingRule) error {
	if err := ValidatePricingRule(r); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, is_active, priority)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.RuleType, r.Model, r.PricePerUnit, r.UnitType, r.Description, r.IsActive, r.Priority)
	if err == nil {
		if refreshErr := s.refreshRulesCache(); refreshErr != nil {
			log.Printf("Failed to refresh pricing rules after create: %v", refreshErr)
		}
	}
	return err
}

func (s *Service) UpdatePricingRule(ctx context.Context, id string, r *PricingRule) error {
	if err := ValidatePricingRule(r); err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE pricing_rules
		SET rule_type = $1, model = $2, price_per_unit = $3, unit_type = $4,
		    description = $5, is_active = $6, priority = $7
		WHERE id = $8
	`, r.RuleType, r.Model, r.PricePerUnit, r.UnitType, r.Description, r.IsActive, r.Priority, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrPricingRuleNotFound
	}
	if refreshErr := s.refreshRulesCache(); refreshErr != nil {
		log.Printf("Failed to refresh pricing rules after update: %v", refreshErr)
	}
	return nil
}

// ValidatePricingRule rejects rules which could credit users through negative
// prices or silently bypass billing through unsupported action/unit names.
func ValidatePricingRule(r *PricingRule) error {
	if r == nil {
		return fmt.Errorf("pricing rule is required")
	}
	allowedUnits := map[string]bool{
		"minute":             true,
		"hour":               true,
		"input_token":        true,
		"cached_input_token": true,
		"cache_write_token":  true,
		"output_token":       true,
	}
	r.RuleType = strings.TrimSpace(r.RuleType)
	r.UnitType = strings.TrimSpace(r.UnitType)
	r.Description = strings.TrimSpace(r.Description)
	if !validUsageAction(r.RuleType) {
		return fmt.Errorf("unsupported rule_type")
	}
	if !allowedUnits[r.UnitType] {
		return fmt.Errorf("unsupported unit_type")
	}
	if r.PricePerUnit < 0 || r.PricePerUnit >= 100_000_000 ||
		math.IsNaN(r.PricePerUnit) || math.IsInf(r.PricePerUnit, 0) {
		return fmt.Errorf("price_per_unit must be a non-negative finite number below 100000000")
	}
	if r.Model != nil {
		model := strings.TrimSpace(*r.Model)
		if len(model) > 200 {
			return fmt.Errorf("model is too long")
		}
		if model == "" {
			r.Model = nil
		} else {
			r.Model = &model
		}
	}
	if len([]rune(r.Description)) > 500 {
		return fmt.Errorf("description is too long")
	}
	if r.Priority < -1000 || r.Priority > 1000 {
		return fmt.Errorf("priority must be between -1000 and 1000")
	}
	return nil
}

func validUsageAction(action string) bool {
	switch action {
	case "transcription", "translation", "chat", "summarize", "embedding", "rag_query":
		return true
	default:
		return false
	}
}

func (s *Service) DeletePricingRule(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM pricing_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrPricingRuleNotFound
	}
	if refreshErr := s.refreshRulesCache(); refreshErr != nil {
		log.Printf("Failed to refresh pricing rules after delete: %v", refreshErr)
	}
	return nil
}

func (s *Service) CalculateCost(action string, model string, quantity float64, inputTokens, outputTokens int) float64 {
	totalCost, _ := s.calculateCost(
		action,
		model,
		quantity,
		inputTokens,
		0,
		0,
		outputTokens,
	)
	return totalCost
}

type selectedPricingRule struct {
	rule  PricingRule
	exact bool
}

func pricingRuleCategory(unitType string) (string, bool) {
	switch unitType {
	case "minute", "hour":
		return "duration", true
	case "input_token", "cached_input_token", "cache_write_token", "output_token":
		return unitType, true
	default:
		return "", false
	}
}

func selectPricingRules(rules []PricingRule, action, model string) map[string]selectedPricingRule {
	selected := make(map[string]selectedPricingRule)
	for index := range rules {
		rule := &rules[index]
		if rule.RuleType != action {
			continue
		}
		exact := rule.Model != nil && model != "" && *rule.Model == model
		if rule.Model != nil && !exact {
			continue
		}
		category, ok := pricingRuleCategory(rule.UnitType)
		if !ok {
			continue
		}
		current, set := selected[category]
		if !set || (exact && !current.exact) ||
			(exact == current.exact && rule.Priority > current.rule.Priority) {
			selected[category] = selectedPricingRule{rule: *rule, exact: exact}
		}
	}
	return selected
}

func (s *Service) calculateCost(
	action string,
	model string,
	quantity float64,
	inputTokens, cachedInputTokens, cacheWriteTokens, outputTokens int,
) (float64, map[string]bool) {
	s.rulesCacheMu.RLock()
	defer s.rulesCacheMu.RUnlock()

	selected := selectPricingRules(s.rulesCache, action, model)

	ordinaryInputTokens := inputTokens - cachedInputTokens - cacheWriteTokens
	if ordinaryInputTokens < 0 {
		ordinaryInputTokens = inputTokens
		cachedInputTokens = 0
		cacheWriteTokens = 0
	}
	var totalCost float64
	applied := make(map[string]bool, len(selected))
	for category := range selected {
		choice := selected[category]
		rule := choice.rule
		applied[category] = true
		switch category {
		case "duration":
			if rule.UnitType == "hour" {
				totalCost += rule.PricePerUnit * (quantity / 60)
			} else {
				totalCost += rule.PricePerUnit * quantity
			}
		case "input_token":
			totalCost += rule.PricePerUnit * float64(ordinaryInputTokens)
		case "cached_input_token":
			totalCost += rule.PricePerUnit * float64(cachedInputTokens)
		case "cache_write_token":
			totalCost += rule.PricePerUnit * float64(cacheWriteTokens)
		case "output_token":
			totalCost += rule.PricePerUnit * float64(outputTokens)
		}
	}
	// Treat provider cache usage as ordinary input whenever the active price
	// set has no cache-specific rate. This intentionally fails expensive, not
	// cheap, for OpenAI-compatible providers with incomplete usage metadata.
	if inputChoice, ok := selected["input_token"]; ok {
		if cachedInputTokens > 0 && !applied["cached_input_token"] {
			totalCost += inputChoice.rule.PricePerUnit * float64(cachedInputTokens)
		}
		if cacheWriteTokens > 0 && !applied["cache_write_token"] {
			totalCost += inputChoice.rule.PricePerUnit * float64(cacheWriteTokens)
		}
	}
	return totalCost, applied
}

func (s *Service) calculateUsageCost(record *UsageRecord) (float64, error) {
	if record == nil || !validUsageAction(record.Action) {
		return 0, fmt.Errorf("unsupported usage action")
	}
	cost, applied := s.calculateCost(
		record.Action,
		record.Model,
		record.Quantity,
		record.InputTokens,
		record.CachedInputTokens,
		record.CacheWriteTokens,
		record.OutputTokens,
	)
	if record.Action == "transcription" && record.Quantity > 0 && !applied["duration"] {
		return 0, fmt.Errorf("no active duration pricing rule for %s", record.Action)
	}
	// rag_query is a quota ledger entry and intentionally has no default
	// monetary rule. Every provider-token action fails closed if an operator
	// accidentally removes its active rule.
	if record.Action != "rag_query" {
		if record.InputTokens > 0 && !applied["input_token"] {
			return 0, fmt.Errorf("no active input-token pricing rule for %s", record.Action)
		}
		if record.OutputTokens > 0 && !applied["output_token"] {
			return 0, fmt.Errorf("no active output-token pricing rule for %s", record.Action)
		}
	}
	return cost, nil
}

func (s *Service) RecordUsage(ctx context.Context, rec *UsageRecord) (float64, error) {
	costs, err := s.RecordUsageBatch(ctx, []*UsageRecord{rec})
	if err != nil {
		return 0, err
	}
	return costs[0], nil
}

// SettleUsageReservation atomically replaces an idempotent up-front usage
// reservation with the provider's actual usage. A lower actual cost refunds the
// difference, while a higher cost is debited only when the remaining balance
// can cover it. Repeating the same settlement is idempotent because the stored
// usage already has the requested cost after the first commit.
//
// The reservation is deliberately kept charged when settlement cannot collect
// an additional amount. Callers must not deliver the provider result in that
// case, and should stop further paid work on the connection.
func (s *Service) SettleUsageReservation(
	ctx context.Context,
	idempotencyKey string,
	actual *UsageRecord,
) (float64, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return 0, fmt.Errorf("idempotency key is required for settlement")
	}
	actualCost, err := s.validateSettlementUsage(actual)
	if err != nil {
		return 0, err
	}
	upstreamCost, serviceFee, pricingSnapshot := s.upstreamBreakdown(ctx, actual, actualCost)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var (
		currentBalance float64
		userTenantID   string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT dreampoints, tenant_id
		FROM users
		WHERE id = $1
		FOR UPDATE
	`, actual.UserID).Scan(&currentBalance, &userTenantID); err != nil {
		return 0, err
	}
	if userTenantID != actual.TenantID {
		return 0, fmt.Errorf("usage tenant does not match user tenant")
	}
	planLimits, err := lockTenantPlanLimitsTx(ctx, tx, actual.TenantID)
	if err != nil {
		return 0, err
	}
	var (
		usageID                          string
		reservedUserID, reservedTenantID string
		reservedAction                   string
		reservedOperationFingerprint     string
		reservedMonthKey                 string
		reservedCost, reservedQuantity   float64
		refundedAt                       sql.NullTime
		settledAt                        sql.NullTime
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT id, user_id, tenant_id, action,
		       provider_operation_fingerprint, month_key, cost, quantity,
		       refunded_at, settled_at
		FROM usage_logs
		WHERE idempotency_key = $1
		FOR UPDATE
	`, idempotencyKey).Scan(
		&usageID,
		&reservedUserID,
		&reservedTenantID,
		&reservedAction,
		&reservedOperationFingerprint,
		&reservedMonthKey,
		&reservedCost,
		&reservedQuantity,
		&refundedAt,
		&settledAt,
	); err != nil {
		return 0, err
	}
	if refundedAt.Valid {
		return 0, fmt.Errorf("usage reservation was already refunded")
	}
	if reservedUserID != actual.UserID ||
		reservedTenantID != actual.TenantID ||
		reservedAction != actual.Action {
		return 0, fmt.Errorf("settlement does not match usage reservation")
	}
	if normalizeOperationFingerprint(reservedOperationFingerprint) !=
		actual.OperationFingerprint {
		return 0, fmt.Errorf(
			"settlement does not match provider operation fingerprint",
		)
	}
	if settledAt.Valid {
		// A previous attempt committed this logical provider operation. Its
		// exact response may have been lost, but replay must never reprice or
		// debit the same stable reservation again.
		if err := tx.Commit(); err != nil {
			return 0, err
		}
		return reservedCost, nil
	}

	enabled, err := boolSettingTx(ctx, tx, "billing_enabled", true)
	if err != nil {
		return 0, err
	}
	if !enabled {
		// Keep the stored margin identity intact even when billing is disabled:
		// retail DP (zero) = upstream cost in DP + service fee.
		serviceFee -= actualCost
		actualCost = 0
	}
	delta := actualCost - reservedCost

	if delta > 0 {
		allowNegative, settingErr := boolSettingTx(ctx, tx, "allow_negative_balance", false)
		if settingErr != nil {
			return 0, settingErr
		}
		if !allowNegative && currentBalance < delta {
			return 0, fmt.Errorf(
				"insufficient balance to settle usage: %.4f < %.4f",
				currentBalance,
				delta,
			)
		}
	}

	newBalance := currentBalance - delta
	if delta != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE users
			SET dreampoints = $1,
			    dreampoints_used = GREATEST(0, dreampoints_used + $2)
			WHERE id = $3
		`, newBalance, delta, actual.UserID); err != nil {
			return 0, err
		}
		transactionType := "debit"
		description := actual.Action + " usage settlement"
		amount := -delta
		if delta < 0 {
			transactionType = "refund"
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO balance_transactions
				(user_id, amount, balance_after, transaction_type, reference_type, reference_id, description)
			VALUES ($1, $2, $3, $4, 'usage', $5, $6)
		`, actual.UserID, amount, newBalance, transactionType, usageID, description); err != nil {
			return 0, err
		}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE usage_logs
		SET quantity = $1,
		    session_id = $2,
		    model = $3,
		    input_tokens = $4,
		    cached_input_tokens = $5,
		    cache_write_tokens = $6,
		    output_tokens = $7,
		    cost = $8,
		    upstream_cost_usd = $9,
		    service_fee_dp = $10,
		    pricing_snapshot = $11,
		    settled_at = NOW()
		WHERE id = $12
	`, actual.Quantity, actual.SessionID, actual.Model, actual.InputTokens,
		actual.CachedInputTokens, actual.CacheWriteTokens, actual.OutputTokens,
		actualCost, upstreamCost, serviceFee, pricingSnapshot, usageID); err != nil {
		return 0, err
	}
	if actual.Action == "transcription" && actual.Quantity > reservedQuantity {
		if err := enforceTenantPlanQuotaTx(
			ctx,
			tx,
			actual.TenantID,
			reservedMonthKey,
			planLimits,
		); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return actualCost, nil
}

func (s *Service) validateSettlementUsage(actual *UsageRecord) (float64, error) {
	if actual == nil {
		return 0, fmt.Errorf("actual usage is required for settlement")
	}
	actual.UserID = strings.TrimSpace(actual.UserID)
	actual.TenantID = strings.TrimSpace(actual.TenantID)
	actual.Action = strings.TrimSpace(actual.Action)
	actual.Model = strings.TrimSpace(actual.Model)
	actual.OperationFingerprint = normalizeOperationFingerprint(
		actual.OperationFingerprint,
	)
	if actual.UserID == "" || actual.TenantID == "" || actual.Action == "" {
		return 0, fmt.Errorf("actual usage requires user, tenant, and action")
	}
	if !validUsageAction(actual.Action) {
		return 0, fmt.Errorf("unsupported usage action")
	}
	if actual.Quantity < 0 || actual.Quantity > maxUsageQuantity ||
		math.IsNaN(actual.Quantity) || math.IsInf(actual.Quantity, 0) ||
		actual.InputTokens < 0 || actual.InputTokens > maxDatabaseTokenCount ||
		actual.CachedInputTokens < 0 || actual.CachedInputTokens > actual.InputTokens ||
		actual.CacheWriteTokens < 0 || actual.CacheWriteTokens > actual.InputTokens ||
		actual.CachedInputTokens > actual.InputTokens-actual.CacheWriteTokens ||
		actual.OutputTokens < 0 || actual.OutputTokens > maxDatabaseTokenCount {
		return 0, fmt.Errorf("actual usage contains invalid quantities")
	}
	if len(actual.Action) > 50 || len(actual.Model) > 200 {
		return 0, fmt.Errorf("actual usage exceeds field limits")
	}
	if actual.OperationFingerprint != "" {
		fingerprint, fingerprintErr := hex.DecodeString(
			actual.OperationFingerprint,
		)
		if fingerprintErr != nil || len(fingerprint) != usageFingerprintBytes ||
			hex.EncodeToString(fingerprint) != actual.OperationFingerprint {
			return 0, fmt.Errorf("actual usage has an invalid operation fingerprint")
		}
	}
	cost, err := s.calculateUsageCost(actual)
	if err != nil {
		return 0, err
	}
	if cost < 0 || cost >= maxStoredUsageCost ||
		math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, fmt.Errorf("actual usage calculated an invalid cost")
	}
	return cost, nil
}

func lockTenantPlanLimitsTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID string,
) (models.PlanLimits, error) {
	var plan string
	if err := tx.QueryRowContext(ctx, `
		SELECT plan
		FROM tenants
		WHERE id = $1
		FOR UPDATE
	`, tenantID).Scan(&plan); err != nil {
		return models.PlanLimits{}, err
	}
	limits, ok := models.PlanLimitsMap[plan]
	if !ok {
		return models.PlanLimits{}, fmt.Errorf("unsupported tenant plan %q", plan)
	}
	return limits, nil
}

func enforceTenantPlanQuotaTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, monthKey string,
	limits models.PlanLimits,
) error {
	if limits.TranscriptionMinutes < 0 && limits.RAGQueries < 0 {
		return nil
	}
	var transcriptionMinutes float64
	var ragQueryCount int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN action = 'transcription' THEN quantity ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN action = 'rag_query' THEN 1 ELSE 0 END), 0)
		FROM usage_logs
		WHERE tenant_id = $1 AND month_key = $2
	`, tenantID, monthKey).Scan(&transcriptionMinutes, &ragQueryCount); err != nil {
		return err
	}
	return validateTenantPlanUsage(limits, transcriptionMinutes, ragQueryCount)
}

func validateTenantPlanUsage(
	limits models.PlanLimits,
	transcriptionMinutes float64,
	ragQueryCount int64,
) error {
	if math.IsNaN(transcriptionMinutes) || math.IsInf(transcriptionMinutes, 0) ||
		transcriptionMinutes < 0 || ragQueryCount < 0 {
		return fmt.Errorf("invalid tenant usage totals")
	}
	if limits.TranscriptionMinutes >= 0 &&
		transcriptionMinutes > float64(limits.TranscriptionMinutes)+1e-9 {
		return fmt.Errorf(
			"%w: transcription minutes %.6f exceed %d",
			ErrPlanQuotaExceeded,
			transcriptionMinutes,
			limits.TranscriptionMinutes,
		)
	}
	if limits.RAGQueries >= 0 && ragQueryCount > int64(limits.RAGQueries) {
		return fmt.Errorf(
			"%w: RAG queries %d exceed %d",
			ErrPlanQuotaExceeded,
			ragQueryCount,
			limits.RAGQueries,
		)
	}
	return nil
}

// RecordUsageBatch records related usage items and debits their combined cost
// atomically. This is used for provider operations (such as a RAG request)
// which incur several independently priced units but must never be partially
// charged.
//
//nolint:gocyclo // The validation and one-transaction ledger flow is intentionally linear.
func (s *Service) RecordUsageBatch(ctx context.Context, records []*UsageRecord) ([]float64, error) {
	if len(records) == 0 {
		return []float64{}, nil
	}
	userID := ""
	tenantID := ""
	costs := make([]float64, len(records))
	upstreamCosts := make([]float64, len(records))
	serviceFees := make([]float64, len(records))
	pricingSnapshots := make([][]byte, len(records))
	for i, rec := range records {
		if rec == nil {
			return nil, fmt.Errorf("usage record %d is nil", i)
		}
		rec.IdempotencyDuplicate = false
		rec.UserID = strings.TrimSpace(rec.UserID)
		rec.TenantID = strings.TrimSpace(rec.TenantID)
		rec.Action = strings.TrimSpace(rec.Action)
		rec.Model = strings.TrimSpace(rec.Model)
		rec.IdempotencyKey = strings.TrimSpace(rec.IdempotencyKey)
		rec.OperationFingerprint = normalizeOperationFingerprint(
			rec.OperationFingerprint,
		)
		if rec.UserID == "" || rec.TenantID == "" || rec.Action == "" {
			return nil, fmt.Errorf("usage record %d requires user, tenant, and action", i)
		}
		if !validUsageAction(rec.Action) {
			return nil, fmt.Errorf("usage record %d has unsupported action", i)
		}
		if userID == "" {
			userID = rec.UserID
		} else if rec.UserID != userID {
			return nil, fmt.Errorf("usage batch must belong to one user")
		}
		if tenantID == "" {
			tenantID = rec.TenantID
		} else if rec.TenantID != tenantID {
			return nil, fmt.Errorf("usage batch must belong to one tenant")
		}
		if rec.Quantity < 0 || rec.Quantity > maxUsageQuantity ||
			math.IsNaN(rec.Quantity) || math.IsInf(rec.Quantity, 0) ||
			rec.InputTokens < 0 || rec.InputTokens > maxDatabaseTokenCount ||
			rec.CachedInputTokens < 0 || rec.CachedInputTokens > rec.InputTokens ||
			rec.CacheWriteTokens < 0 || rec.CacheWriteTokens > rec.InputTokens ||
			rec.CachedInputTokens > rec.InputTokens-rec.CacheWriteTokens ||
			rec.OutputTokens < 0 || rec.OutputTokens > maxDatabaseTokenCount {
			return nil, fmt.Errorf("usage record %d contains invalid quantities", i)
		}
		if len(rec.Action) > 50 || len(rec.Model) > 200 || len(rec.IdempotencyKey) > 255 {
			return nil, fmt.Errorf("usage record %d exceeds field limits", i)
		}
		if rec.OperationFingerprint != "" {
			fingerprint, fingerprintErr := hex.DecodeString(
				rec.OperationFingerprint,
			)
			if fingerprintErr != nil || len(fingerprint) != usageFingerprintBytes ||
				hex.EncodeToString(fingerprint) != rec.OperationFingerprint {
				return nil, fmt.Errorf(
					"usage record %d has an invalid operation fingerprint",
					i,
				)
			}
		}
		calculatedCost, costErr := s.calculateUsageCost(rec)
		if costErr != nil {
			return nil, fmt.Errorf("usage record %d pricing: %w", i, costErr)
		}
		costs[i] = calculatedCost
		if costs[i] < 0 || costs[i] >= maxStoredUsageCost ||
			math.IsNaN(costs[i]) || math.IsInf(costs[i], 0) {
			return nil, fmt.Errorf("usage record %d calculated an invalid cost", i)
		}
	}
	for i, rec := range records {
		upstreamCosts[i], serviceFees[i], pricingSnapshots[i] =
			s.upstreamBreakdown(ctx, rec, costs[i])
	}
	monthKey := time.Now().UTC().Format("2006-01")

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentBalance float64
	var userTenantID string
	if err := tx.QueryRowContext(ctx, `
		SELECT dreampoints, tenant_id FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&currentBalance, &userTenantID); err != nil {
		return nil, err
	}
	if userTenantID != tenantID {
		return nil, fmt.Errorf("usage tenant does not match user tenant")
	}

	planLimits, err := lockTenantPlanLimitsTx(ctx, tx, tenantID)
	if err != nil {
		return nil, err
	}
	enabled, err := boolSettingTx(ctx, tx, "billing_enabled", true)
	if err != nil {
		return nil, err
	}
	if !enabled {
		for i := range costs {
			// upstreamBreakdown calculated the fee from the pre-disable retail
			// price. Removing that price leaves the upstream spend as a
			// negative margin instead of reporting revenue that was not billed.
			serviceFees[i] -= costs[i]
			costs[i] = 0
		}
	}

	type insertedUsage struct {
		id     string
		action string
		cost   float64
	}
	inserted := make([]insertedUsage, 0, len(records))
	totalCost := 0.0
	quotaRelevantInsert := false
	for i, rec := range records {
		var idempotencyKey any
		if rec.IdempotencyKey != "" {
			idempotencyKey = rec.IdempotencyKey
		}
		var usageID string
		insertErr := tx.QueryRowContext(ctx, `
				INSERT INTO usage_logs
					(tenant_id, user_id, action, quantity, session_id, model,
					 input_tokens, cached_input_tokens, cache_write_tokens,
					 output_tokens, cost, upstream_cost_usd, service_fee_dp,
					 pricing_snapshot, month_key, idempotency_key,
					 provider_operation_fingerprint)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
				ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
				RETURNING id
			`, rec.TenantID, rec.UserID, rec.Action, rec.Quantity, rec.SessionID, rec.Model,
			rec.InputTokens, rec.CachedInputTokens, rec.CacheWriteTokens,
			rec.OutputTokens, costs[i], upstreamCosts[i], serviceFees[i],
			pricingSnapshots[i], monthKey, idempotencyKey,
			rec.OperationFingerprint).Scan(&usageID)
		if insertErr == sql.ErrNoRows && idempotencyKey != nil {
			var (
				existingUsageID                  string
				existingTenantID, existingUserID string
				existingAction                   string
				existingFingerprint              string
				existingCost                     float64
				existingRefundedAt               sql.NullTime
			)
			if err := tx.QueryRowContext(ctx, `
					SELECT id, tenant_id, user_id, action,
					       provider_operation_fingerprint, cost, refunded_at
					FROM usage_logs
					WHERE idempotency_key = $1
					FOR UPDATE
				`, idempotencyKey).Scan(
				&existingUsageID,
				&existingTenantID,
				&existingUserID,
				&existingAction,
				&existingFingerprint,
				&existingCost,
				&existingRefundedAt,
			); err != nil {
				return nil, err
			}
			if existingTenantID != rec.TenantID || existingUserID != rec.UserID || existingAction != rec.Action {
				return nil, fmt.Errorf("idempotency key belongs to different usage")
			}
			if normalizeOperationFingerprint(existingFingerprint) !=
				rec.OperationFingerprint {
				return nil, fmt.Errorf(
					"idempotency key belongs to a different provider operation",
				)
			}
			if existingRefundedAt.Valid && rec.ReuseRefundedReservation {
				if _, err := tx.ExecContext(ctx, `
					UPDATE usage_logs
					SET quantity=$1,
					    session_id=$2,
					    model=$3,
					    input_tokens=$4,
					    cached_input_tokens=$5,
					    cache_write_tokens=$6,
					    output_tokens=$7,
					    cost=$8,
					    upstream_cost_usd=$9,
					    service_fee_dp=$10,
					    pricing_snapshot=$11,
					    month_key=$12,
					    refunded_at=NULL,
					    settled_at=NULL
					WHERE id=$13 AND refunded_at IS NOT NULL
				`, rec.Quantity, rec.SessionID, rec.Model, rec.InputTokens,
					rec.CachedInputTokens, rec.CacheWriteTokens,
					rec.OutputTokens, costs[i], upstreamCosts[i],
					serviceFees[i], pricingSnapshots[i], monthKey,
					existingUsageID,
				); err != nil {
					return nil, err
				}
				inserted = append(inserted, insertedUsage{
					id: existingUsageID, action: rec.Action, cost: costs[i],
				})
				totalCost += costs[i]
				if rec.Action == "transcription" || rec.Action == "rag_query" {
					quotaRelevantInsert = true
				}
				continue
			}
			costs[i] = existingCost
			rec.IdempotencyDuplicate = true
			continue
		}
		if insertErr != nil {
			return nil, insertErr
		}
		inserted = append(inserted, insertedUsage{id: usageID, action: rec.Action, cost: costs[i]})
		totalCost += costs[i]
		if rec.Action == "transcription" || rec.Action == "rag_query" {
			quotaRelevantInsert = true
		}
	}
	if quotaRelevantInsert {
		if err := enforceTenantPlanQuotaTx(
			ctx,
			tx,
			tenantID,
			monthKey,
			planLimits,
		); err != nil {
			return nil, err
		}
	}

	if totalCost > 0 {
		allowNegative, settingErr := boolSettingTx(ctx, tx, "allow_negative_balance", false)
		if settingErr != nil {
			return nil, settingErr
		}
		if !allowNegative && currentBalance < totalCost {
			return nil, fmt.Errorf("insufficient balance: %.4f < %.4f", currentBalance, totalCost)
		}
		if _, err := tx.ExecContext(ctx, `
				UPDATE users
				SET dreampoints = $1, dreampoints_used = dreampoints_used + $2
				WHERE id = $3
			`, currentBalance-totalCost, totalCost, userID); err != nil {
			return nil, err
		}
		runningBalance := currentBalance
		for _, usage := range inserted {
			if usage.cost <= 0 {
				continue
			}
			runningBalance -= usage.cost
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO balance_transactions
					(user_id, amount, balance_after, transaction_type, reference_type, reference_id, description)
				VALUES ($1, $2, $3, 'debit', 'usage', $4, $5)
			`, userID, -usage.cost, runningBalance, usage.id, usage.action); err != nil {
				return nil, err
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return costs, nil
}

// RefundUsage reverses a previously idempotent reservation exactly once.
func (s *Service) RefundUsage(ctx context.Context, idempotencyKey, description string) error {
	if strings.TrimSpace(idempotencyKey) == "" {
		return fmt.Errorf("idempotency key is required for a refund")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var userID string
	if err := tx.QueryRowContext(ctx, `
		SELECT user_id
		FROM usage_logs
		WHERE idempotency_key = $1
	`, idempotencyKey).Scan(&userID); err != nil {
		return err
	}
	var lockedUserID string
	if err := tx.QueryRowContext(ctx, `
		SELECT id FROM users WHERE id = $1 FOR UPDATE
	`, userID).Scan(&lockedUserID); err != nil {
		return err
	}

	var usageID, lockedUsageUserID string
	var cost float64
	var refundedAt sql.NullTime
	var settledAt sql.NullTime
	if err := tx.QueryRowContext(ctx, `
		SELECT id, user_id, cost, refunded_at, settled_at
		FROM usage_logs
		WHERE idempotency_key = $1
		FOR UPDATE
	`, idempotencyKey).Scan(
		&usageID,
		&lockedUsageUserID,
		&cost,
		&refundedAt,
		&settledAt,
	); err != nil {
		return err
	}
	if lockedUsageUserID != userID {
		return fmt.Errorf("usage reservation owner changed")
	}
	if refundedAt.Valid {
		return tx.Commit()
	}
	if settledAt.Valid {
		return fmt.Errorf("usage reservation was already settled")
	}

	if cost > 0 {
		var newBalance float64
		if err := tx.QueryRowContext(ctx, `
			UPDATE users
			SET dreampoints = dreampoints + $1,
			    dreampoints_used = GREATEST(0, dreampoints_used - $1)
			WHERE id = $2
			RETURNING dreampoints
		`, cost, userID).Scan(&newBalance); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO balance_transactions
				(user_id, amount, balance_after, transaction_type, reference_type, reference_id, description)
			VALUES ($1, $2, $3, 'refund', 'usage', $4, $5)
		`, userID, cost, newBalance, usageID, description); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE usage_logs
		SET cost = 0, quantity = 0, refunded_at = NOW()
		WHERE id = $1
	`, usageID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) GetUserBalance(ctx context.Context, userID string) (*UserBalance, error) {
	var ub UserBalance
	err := s.db.QueryRowContext(ctx, `
		SELECT id, email, COALESCE(name, ''), dreampoints, dreampoints_used
		FROM users WHERE id = $1
	`, userID).Scan(&ub.UserID, &ub.Email, &ub.Name, &ub.Dreampoints, &ub.DreampointsUsed)
	if err != nil {
		return nil, err
	}
	return &ub, nil
}

func (s *Service) AddBalance(ctx context.Context, userID string, amount float64, refType, description string, createdBy *string) error {
	if amount <= 0 || amount > maxManualBalanceAdjustment || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return fmt.Errorf("credit amount must be a positive finite number")
	}
	userID = strings.TrimSpace(userID)
	refType = strings.TrimSpace(refType)
	description = strings.TrimSpace(description)
	if userID == "" || len(refType) > 30 || len([]rune(description)) > 500 {
		return fmt.Errorf("invalid balance adjustment metadata")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Update balance
	var newBalance float64
	err = tx.QueryRowContext(ctx, `
		UPDATE users SET dreampoints = dreampoints + $1
		WHERE id = $2
		RETURNING dreampoints
	`, amount, userID).Scan(&newBalance)
	if err != nil {
		return err
	}

	// Log transaction
	_, err = tx.ExecContext(ctx, `
		INSERT INTO balance_transactions (user_id, amount, balance_after, transaction_type, reference_type, description, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, userID, amount, newBalance, "credit", refType, description, createdBy)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Service) DeductBalance(ctx context.Context, userID string, amount float64, refType, description string) error {
	if amount <= 0 || amount > maxManualBalanceAdjustment || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return fmt.Errorf("debit amount must be a positive finite number")
	}
	userID = strings.TrimSpace(userID)
	refType = strings.TrimSpace(refType)
	description = strings.TrimSpace(description)
	if userID == "" || len(refType) > 30 || len([]rune(description)) > 500 {
		return fmt.Errorf("invalid balance adjustment metadata")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Check if negative balance allowed
	allowNegative, err := boolSettingTx(ctx, tx, "allow_negative_balance", false)
	if err != nil {
		return err
	}

	// Check balance
	var currentBalance float64
	err = tx.QueryRowContext(ctx, `SELECT dreampoints FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&currentBalance)
	if err != nil {
		return err
	}

	if !allowNegative && currentBalance < amount {
		return fmt.Errorf("insufficient balance: %.4f < %.4f", currentBalance, amount)
	}

	// Update balance
	newBalance := currentBalance - amount
	_, err = tx.ExecContext(ctx, `
		UPDATE users SET dreampoints = $1, dreampoints_used = dreampoints_used + $2 WHERE id = $3
	`, newBalance, amount, userID)
	if err != nil {
		return err
	}

	// Log transaction
	_, err = tx.ExecContext(ctx, `
		INSERT INTO balance_transactions (user_id, amount, balance_after, transaction_type, reference_type, description)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, userID, -amount, newBalance, "debit", refType, description)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (s *Service) GetBalanceHistory(ctx context.Context, userID string, limit int) ([]BalanceTransaction, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, amount, balance_after, transaction_type, reference_type, reference_id,
		       COALESCE(description, ''), created_by, created_at
		FROM balance_transactions
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var txns []BalanceTransaction
	for rows.Next() {
		var t BalanceTransaction
		if err := rows.Scan(&t.ID, &t.UserID, &t.Amount, &t.BalanceAfter, &t.TransactionType,
			&t.ReferenceType, &t.ReferenceID, &t.Description, &t.CreatedBy, &t.CreatedAt); err != nil {
			return nil, err
		}
		txns = append(txns, t)
	}
	return txns, rows.Err()
}

func (s *Service) GetSystemStats(ctx context.Context) (*SystemStats, error) {
	stats := &SystemStats{
		UsageByAction: make(map[string]float64),
		UsageByModel:  make(map[string]float64),
	}

	// Basic counts
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&stats.TotalUsers); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_active = true`).Scan(&stats.ActiveUsers); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&stats.TotalSessions); err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcripts`).Scan(&stats.TotalTranscripts); err != nil {
		return nil, err
	}

	// Dreampoints totals
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(dreampoints), 0), COALESCE(SUM(dreampoints_used), 0) FROM users`).Scan(&stats.TotalDreampoints, &stats.TotalUsed); err != nil {
		return nil, err
	}

	// Usage by action
	rows, err := s.db.QueryContext(ctx, `
		SELECT action, COALESCE(SUM(cost), 0) FROM usage_logs GROUP BY action
	`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var action string
		var cost float64
		if err := rows.Scan(&action, &cost); err != nil {
			_ = rows.Close()
			return nil, err
		}
		stats.UsageByAction[action] = cost
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Usage by model
	rows, err = s.db.QueryContext(ctx, `
		SELECT COALESCE(model, 'default'), COALESCE(SUM(cost), 0) FROM usage_logs WHERE model IS NOT NULL GROUP BY model
	`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var model string
		var cost float64
		if err := rows.Scan(&model, &cost); err != nil {
			_ = rows.Close()
			return nil, err
		}
		stats.UsageByModel[model] = cost
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	return stats, nil
}

func (s *Service) GetSystemSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = $1`, key).Scan(&value)
	return value, err
}

func (s *Service) SetSystemSetting(ctx context.Context, key string, value string, updatedBy *string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO system_settings (key, value, updated_by, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3, updated_at = NOW()
	`, key, value, updatedBy)
	return err
}

// SetSystemSettings updates a validated group atomically.
func (s *Service) SetSystemSettings(ctx context.Context, settings map[string]string, updatedBy *string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for key, value := range settings {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO system_settings (key, value, updated_by, updated_at)
			VALUES ($1, $2, $3, NOW())
			ON CONFLICT (key) DO UPDATE SET value = $2, updated_by = $3, updated_at = NOW()
		`, key, value, updatedBy); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetFreeTierCredit returns the configured signup credit.
func (s *Service) GetFreeTierCredit(ctx context.Context) (float64, error) {
	value, err := s.GetSystemSetting(ctx, "free_tier_dreampoints")
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	credit, err := strconv.ParseFloat(strings.Trim(strings.TrimSpace(value), `"`), 64)
	if err != nil || credit < 0 || math.IsNaN(credit) || math.IsInf(credit, 0) {
		return 0, fmt.Errorf("invalid free_tier_dreampoints setting")
	}
	return credit, nil
}

// BillingEnabled reports whether usage should be charged.
func (s *Service) BillingEnabled(ctx context.Context) (bool, error) {
	value, err := s.GetSystemSetting(ctx, "billing_enabled")
	if err != nil {
		if err == sql.ErrNoRows {
			return true, nil
		}
		return false, err
	}
	return parseJSONBool(value, true), nil
}

// CanUsePaidFeatures performs a cheap preflight check. The authoritative
// balance check still happens inside RecordUsage's transaction.
func (s *Service) CanUsePaidFeatures(ctx context.Context, userID string) (bool, error) {
	enabled, err := s.BillingEnabled(ctx)
	if err != nil {
		return false, err
	}
	if !enabled {
		return true, nil
	}
	value, err := s.GetSystemSetting(ctx, "allow_negative_balance")
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil && parseJSONBool(value, false) {
		return true, nil
	}
	var balance float64
	if err := s.db.QueryRowContext(ctx, `SELECT dreampoints FROM users WHERE id = $1`, userID).Scan(&balance); err != nil {
		return false, err
	}
	return balance > 0, nil
}

// CanAffordUsage estimates a known usage record before an upstream request is
// started. RecordUsage remains the authoritative, transactional check.
func (s *Service) CanAffordUsage(ctx context.Context, userID string, rec *UsageRecord) (bool, error) {
	return s.CanAffordUsageBatch(ctx, userID, []*UsageRecord{rec})
}

// CanAffordUsageBatch estimates the combined cost of one provider operation.
// RecordUsageBatch remains the authoritative transactional check.
func (s *Service) CanAffordUsageBatch(ctx context.Context, userID string, records []*UsageRecord) (bool, error) {
	enabled, err := s.BillingEnabled(ctx)
	if err != nil {
		return false, err
	}
	if !enabled {
		return true, nil
	}
	value, err := s.GetSystemSetting(ctx, "allow_negative_balance")
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil && parseJSONBool(value, false) {
		return true, nil
	}
	cost := 0.0
	for i, rec := range records {
		if rec == nil || rec.Quantity < 0 || math.IsNaN(rec.Quantity) || math.IsInf(rec.Quantity, 0) ||
			rec.Quantity > maxUsageQuantity ||
			rec.InputTokens < 0 || rec.InputTokens > maxDatabaseTokenCount ||
			rec.CachedInputTokens < 0 || rec.CachedInputTokens > rec.InputTokens ||
			rec.CacheWriteTokens < 0 || rec.CacheWriteTokens > rec.InputTokens ||
			rec.CachedInputTokens > rec.InputTokens-rec.CacheWriteTokens ||
			rec.OutputTokens < 0 || rec.OutputTokens > maxDatabaseTokenCount {
			return false, fmt.Errorf("usage record %d contains invalid quantities", i)
		}
		itemCost, costErr := s.calculateUsageCost(rec)
		if costErr != nil {
			return false, fmt.Errorf("usage record %d pricing: %w", i, costErr)
		}
		if itemCost < 0 || math.IsNaN(itemCost) || math.IsInf(itemCost, 0) {
			return false, fmt.Errorf("usage record %d calculated an invalid cost", i)
		}
		cost += itemCost
	}
	if cost <= 0 {
		return true, nil
	}
	var balance float64
	if err := s.db.QueryRowContext(ctx, `SELECT dreampoints FROM users WHERE id = $1`, userID).Scan(&balance); err != nil {
		return false, err
	}
	return balance >= cost, nil
}

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func boolSettingTx(ctx context.Context, queryer queryRower, key string, fallback bool) (bool, error) {
	var value string
	err := queryer.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = $1`, key).Scan(&value)
	if err != nil {
		if err == sql.ErrNoRows {
			return fallback, nil
		}
		return false, err
	}
	return parseJSONBool(value, fallback), nil
}

func parseJSONBool(value string, fallback bool) bool {
	parsed, err := strconv.ParseBool(strings.Trim(strings.TrimSpace(value), `"`))
	if err != nil {
		return fallback
	}
	return parsed
}
