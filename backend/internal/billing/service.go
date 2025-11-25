package billing

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"
)

type PricingRule struct {
	ID           string   `json:"id"`
	RuleType     string   `json:"rule_type"`
	Model        *string  `json:"model"`
	PricePerUnit float64  `json:"price_per_unit"`
	UnitType     string   `json:"unit_type"`
	Description  string   `json:"description"`
	IsActive     bool     `json:"is_active"`
	Priority     int      `json:"priority"`
	CreatedAt    string   `json:"created_at"`
	UpdatedAt    string   `json:"updated_at"`
}

type UsageRecord struct {
	UserID       string
	TenantID     string
	SessionID    *string
	Action       string  // transcription, translation, chat, summarize
	Model        string
	Quantity     float64 // minutes for transcription
	InputTokens  int
	OutputTokens int
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
	TotalUsers       int     `json:"total_users"`
	ActiveUsers      int     `json:"active_users"`
	TotalSessions    int     `json:"total_sessions"`
	TotalTranscripts int     `json:"total_transcripts"`
	TotalDreampoints float64 `json:"total_dreampoints"`
	TotalUsed        float64 `json:"total_used"`
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
	s.refreshRulesCache()
	return s
}

func (s *Service) refreshRulesCache() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, rule_type, model, price_per_unit, unit_type,
		       COALESCE(description, ''), is_active, priority
		FROM pricing_rules
		WHERE is_active = true
		ORDER BY priority DESC
	`)
	if err != nil {
		log.Printf("Failed to refresh pricing rules cache: %v", err)
		return
	}
	defer rows.Close()

	var rules []PricingRule
	for rows.Next() {
		var r PricingRule
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Model, &r.PricePerUnit,
			&r.UnitType, &r.Description, &r.IsActive, &r.Priority); err != nil {
			continue
		}
		rules = append(rules, r)
	}

	s.rulesCacheMu.Lock()
	s.rulesCache = rules
	s.lastRefresh = time.Now()
	s.rulesCacheMu.Unlock()
}

func (s *Service) GetPricingRules(ctx context.Context) ([]PricingRule, error) {
	// Refresh cache if older than 5 minutes
	if time.Since(s.lastRefresh) > 5*time.Minute {
		s.refreshRulesCache()
	}

	s.rulesCacheMu.RLock()
	defer s.rulesCacheMu.RUnlock()
	return s.rulesCache, nil
}

func (s *Service) GetAllPricingRules(ctx context.Context) ([]PricingRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, rule_type, model, price_per_unit, unit_type,
		       COALESCE(description, ''), is_active, priority,
		       created_at, updated_at
		FROM pricing_rules
		ORDER BY rule_type, priority DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rules []PricingRule
	for rows.Next() {
		var r PricingRule
		if err := rows.Scan(&r.ID, &r.RuleType, &r.Model, &r.PricePerUnit,
			&r.UnitType, &r.Description, &r.IsActive, &r.Priority,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			continue
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func (s *Service) CreatePricingRule(ctx context.Context, r *PricingRule) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, is_active, priority)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, r.RuleType, r.Model, r.PricePerUnit, r.UnitType, r.Description, r.IsActive, r.Priority)
	if err == nil {
		s.refreshRulesCache()
	}
	return err
}

func (s *Service) UpdatePricingRule(ctx context.Context, id string, r *PricingRule) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE pricing_rules
		SET rule_type = $1, model = $2, price_per_unit = $3, unit_type = $4,
		    description = $5, is_active = $6, priority = $7
		WHERE id = $8
	`, r.RuleType, r.Model, r.PricePerUnit, r.UnitType, r.Description, r.IsActive, r.Priority, id)
	if err == nil {
		s.refreshRulesCache()
	}
	return err
}

func (s *Service) DeletePricingRule(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM pricing_rules WHERE id = $1`, id)
	if err == nil {
		s.refreshRulesCache()
	}
	return err
}

func (s *Service) CalculateCost(action string, model string, quantity float64, inputTokens, outputTokens int) float64 {
	s.rulesCacheMu.RLock()
	defer s.rulesCacheMu.RUnlock()

	var totalCost float64

	// Find matching rules (highest priority first due to cache ordering)
	for _, rule := range s.rulesCache {
		if rule.RuleType != action {
			continue
		}

		// Check model match (nil means default/any)
		modelMatch := rule.Model == nil || (model != "" && *rule.Model == model)
		if !modelMatch {
			continue
		}

		switch rule.UnitType {
		case "minute":
			totalCost += rule.PricePerUnit * quantity
			return totalCost // Only one minute-based rule applies
		case "hour":
			totalCost += rule.PricePerUnit * (quantity / 60.0)
			return totalCost
		case "input_token":
			totalCost += rule.PricePerUnit * float64(inputTokens)
		case "output_token":
			totalCost += rule.PricePerUnit * float64(outputTokens)
		}
	}

	return totalCost
}

func (s *Service) RecordUsage(ctx context.Context, rec *UsageRecord) (float64, error) {
	cost := s.CalculateCost(rec.Action, rec.Model, rec.Quantity, rec.InputTokens, rec.OutputTokens)

	monthKey := time.Now().Format("2006-01")

	// Insert usage log
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO usage_logs (tenant_id, user_id, action, quantity, session_id, model, input_tokens, output_tokens, cost, month_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, rec.TenantID, rec.UserID, rec.Action, rec.Quantity, rec.SessionID, rec.Model, rec.InputTokens, rec.OutputTokens, cost, monthKey)
	if err != nil {
		return 0, err
	}

	// Deduct from user balance
	if cost > 0 {
		err = s.DeductBalance(ctx, rec.UserID, cost, "usage", rec.Action)
		if err != nil {
			log.Printf("Failed to deduct balance for user %s: %v", rec.UserID, err)
			// Don't fail the request, just log
		}
	}

	return cost, nil
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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

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
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Check if negative balance allowed
	var allowNegative bool
	var settingVal string
	err = tx.QueryRowContext(ctx, `SELECT value FROM system_settings WHERE key = 'allow_negative_balance'`).Scan(&settingVal)
	if err == nil && settingVal == `"true"` {
		allowNegative = true
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
	defer rows.Close()

	var txns []BalanceTransaction
	for rows.Next() {
		var t BalanceTransaction
		if err := rows.Scan(&t.ID, &t.UserID, &t.Amount, &t.BalanceAfter, &t.TransactionType,
			&t.ReferenceType, &t.ReferenceID, &t.Description, &t.CreatedBy, &t.CreatedAt); err != nil {
			continue
		}
		txns = append(txns, t)
	}
	return txns, nil
}

func (s *Service) GetSystemStats(ctx context.Context) (*SystemStats, error) {
	stats := &SystemStats{
		UsageByAction: make(map[string]float64),
		UsageByModel:  make(map[string]float64),
	}

	// Basic counts
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&stats.TotalUsers)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_active = true`).Scan(&stats.ActiveUsers)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions`).Scan(&stats.TotalSessions)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM transcripts`).Scan(&stats.TotalTranscripts)

	// Dreampoints totals
	s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(dreampoints), 0), COALESCE(SUM(dreampoints_used), 0) FROM users`).Scan(&stats.TotalDreampoints, &stats.TotalUsed)

	// Usage by action
	rows, err := s.db.QueryContext(ctx, `
		SELECT action, COALESCE(SUM(cost), 0) FROM usage_logs GROUP BY action
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var action string
			var cost float64
			if rows.Scan(&action, &cost) == nil {
				stats.UsageByAction[action] = cost
			}
		}
	}

	// Usage by model
	rows, err = s.db.QueryContext(ctx, `
		SELECT COALESCE(model, 'default'), COALESCE(SUM(cost), 0) FROM usage_logs WHERE model IS NOT NULL GROUP BY model
	`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var model string
			var cost float64
			if rows.Scan(&model, &cost) == nil {
				stats.UsageByModel[model] = cost
			}
		}
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
