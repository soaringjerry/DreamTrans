package billing

import (
	"context"
	"encoding/json"
	"time"
)

// maxStatementRows caps each list on one statement. A long-lived account would
// otherwise pull an unbounded result set into memory; callers narrow the window
// instead, and Truncated says when that is necessary.
const maxStatementRows = 5000

// StatementTotals summarizes a statement window: what the account spent, split
// the same way the workspace splits a session, and what it paid in.
type StatementTotals struct {
	TranscriptionUSD     float64 `json:"transcription_usd"`
	TranscriptionSeconds float64 `json:"transcription_seconds"`
	TranslationUSD       float64 `json:"translation_usd"`
	AIUSD                float64 `json:"ai_usd"`
	ChargedUSD           float64 `json:"charged_usd"`
	RefundedUSD          float64 `json:"refunded_usd"`
	FromGrantUSD         float64 `json:"from_grant_usd"`
	FromWalletUSD        float64 `json:"from_wallet_usd"`
	TopupUSD             float64 `json:"topup_usd"`
	MembershipUSD        float64 `json:"membership_usd"`
}

// UserStatement is everything a user needs to reconcile one period: the
// itemized charges, the balance movements behind them, and the payments in.
type UserStatement struct {
	From      string               `json:"from"`
	To        string               `json:"to"`
	Usage     []UserUsageItem      `json:"usage"`
	Ledger    []BalanceTransaction `json:"ledger"`
	Payments  []PaymentRow         `json:"payments"`
	Totals    StatementTotals      `json:"totals"`
	Truncated bool                 `json:"truncated"`
}

// UserStatement collects one account's billing records over the half-open
// window [from, to). Both bounds are required and interpreted as UTC.
func (s *Service) UserStatement(ctx context.Context, userID string, from, to time.Time) (*UserStatement, error) {
	if !to.After(from) {
		return nil, invalidBillingInputf("to must be after from")
	}
	acct, err := s.accountForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	statement := &UserStatement{
		From:     from.UTC().Format(time.RFC3339),
		To:       to.UTC().Format(time.RFC3339),
		Usage:    make([]UserUsageItem, 0),
		Ledger:   make([]BalanceTransaction, 0),
		Payments: make([]PaymentRow, 0),
	}

	usageRows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, action, COALESCE(model, ''), quantity,
		       COALESCE(input_tokens, 0), cached_input_tokens, cache_write_tokens,
		       COALESCE(output_tokens, 0), charge_usd, grant_usd, wallet_usd, cost_attribution,
		       COALESCE(feature, ''), project_id,
		       settled_at IS NOT NULL, refunded_at IS NOT NULL,
		       pricing_snapshot, CAST(created_at AS TEXT)
		FROM usage_logs
		WHERE user_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, userID, from, to, maxStatementRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = usageRows.Close() }()
	for usageRows.Next() {
		var item UserUsageItem
		var raw []byte
		if err := usageRows.Scan(&item.ID, &item.SessionID, &item.Action, &item.Model, &item.Quantity,
			&item.InputTokens, &item.CachedInputTokens, &item.CacheWriteTokens, &item.OutputTokens,
			&item.CostUSD, &item.GrantUSD, &item.WalletUSD, &item.Attribution,
			&item.Feature, &item.ProjectID, &item.Settled, &item.Refunded,
			&raw, &item.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &item.PricingSnapshot)
		statement.Usage = append(statement.Usage, item)
	}
	if err := usageRows.Err(); err != nil {
		return nil, err
	}
	if len(statement.Usage) > maxStatementRows {
		statement.Usage = statement.Usage[:maxStatementRows]
		statement.Truncated = true
	}

	ledgerRows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, bucket, grant_id, amount, balance_after, transaction_type,
		       reference_type, reference_id, COALESCE(description, ''), created_by, CAST(created_at AS TEXT)
		FROM balance_transactions
		WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, acct.ID, from, to, maxStatementRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ledgerRows.Close() }()
	for ledgerRows.Next() {
		var item BalanceTransaction
		if err := ledgerRows.Scan(&item.ID, &item.UserID, &item.Bucket, &item.GrantID, &item.AmountUSD,
			&item.BalanceAfterUSD, &item.TransactionType, &item.ReferenceType, &item.ReferenceID,
			&item.Description, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		statement.Ledger = append(statement.Ledger, item)
	}
	if err := ledgerRows.Err(); err != nil {
		return nil, err
	}
	if len(statement.Ledger) > maxStatementRows {
		statement.Ledger = statement.Ledger[:maxStatementRows]
		statement.Truncated = true
	}

	paymentRows, err := s.db.QueryContext(ctx, `
		SELECT id, kind, amount_usd, bonus_usd, COALESCE(stripe_object_id, ''), status, description, CAST(created_at AS TEXT)
		FROM payments
		WHERE account_id = $1 AND created_at >= $2 AND created_at < $3
		ORDER BY created_at DESC, id DESC
		LIMIT $4
	`, acct.ID, from, to, maxStatementRows+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = paymentRows.Close() }()
	for paymentRows.Next() {
		var item PaymentRow
		if err := paymentRows.Scan(&item.ID, &item.Kind, &item.AmountUSD, &item.BonusUSD, &item.StripeObjectID,
			&item.Status, &item.Description, &item.CreatedAt); err != nil {
			return nil, err
		}
		statement.Payments = append(statement.Payments, item)
	}
	if err := paymentRows.Err(); err != nil {
		return nil, err
	}
	if len(statement.Payments) > maxStatementRows {
		statement.Payments = statement.Payments[:maxStatementRows]
		statement.Truncated = true
	}

	statement.Totals = statementTotals(statement.Usage, statement.Payments)
	return statement, nil
}

// statementTotals splits charges the same way GetSessionCostSummaries does, so
// a month total and the per-session line the workspace shows agree. A refunded
// row is reported under RefundedUSD and left out of the spend buckets.
func statementTotals(usage []UserUsageItem, payments []PaymentRow) StatementTotals {
	var totals StatementTotals
	for i := range usage {
		item := &usage[i]
		if item.Refunded {
			totals.RefundedUSD += item.CostUSD
			continue
		}
		switch item.Action {
		case "transcription":
			totals.TranscriptionUSD += item.CostUSD
			totals.TranscriptionSeconds += item.Quantity * 60
		case "translation":
			totals.TranslationUSD += item.CostUSD
		default:
			totals.AIUSD += item.CostUSD
		}
		totals.ChargedUSD += item.CostUSD
		totals.FromGrantUSD += item.GrantUSD
		totals.FromWalletUSD += item.WalletUSD
	}
	for i := range payments {
		payment := &payments[i]
		if payment.Status != "succeeded" {
			continue
		}
		switch payment.Kind {
		case "topup":
			totals.TopupUSD += payment.AmountUSD
		case "membership":
			totals.MembershipUSD += payment.AmountUSD
		}
	}
	return totals
}
