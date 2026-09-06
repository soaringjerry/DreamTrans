package billing

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dreamtrans/backend/internal/risk"
)

// GrantPromotionRewards fulfills the immutable registration offer. Account
// locking and the receipt update share the grant/ledger transaction, so retries
// after a failed verification response cannot issue duplicate credit.
func (s *Service) GrantPromotionRewards(ctx context.Context, userID string) error {
	var pending bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM promotion_registrations WHERE user_id=$1 AND rewarded_at IS NULL)`, userID).Scan(&pending); err != nil {
		return err
	}
	if !pending {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	acct, err := lockAccountForUserTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	allowed, err := risk.RewardsAllowedTx(ctx, tx, userID)
	if err != nil {
		return err
	}
	if !allowed {
		return nil
	}
	var id, name, planCode string
	var amount float64
	var grantDays, planDays int
	err = tx.QueryRowContext(ctx, `SELECT r.id,i.name,i.grant_usd,i.grant_days,COALESCE(i.plan_code,''),i.plan_days
        FROM promotion_registrations r JOIN promotion_invites i ON i.id=r.invite_id JOIN users u ON u.id=r.user_id
        WHERE r.user_id=$1 AND r.rewarded_at IS NULL AND u.email_verified AND u.is_active
        FOR UPDATE OF r`, userID).Scan(&id, &name, &amount, &grantDays, &planCode, &planDays)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	reserved, err := risk.ReserveRewardTx(ctx, tx, userID, "promotion", amount)
	if err != nil {
		return err
	}
	if !reserved {
		return tx.Commit()
	}
	now := time.Now().UTC()
	var grantID *string
	if amount > 0 {
		expires := now.Add(time.Duration(grantDays) * 24 * time.Hour)
		item, err := addGrantTx(ctx, tx, acct, &GrantInput{UserID: userID, Kind: GrantPromo, AmountUSD: amount, ExpiresAt: &expires, Note: "推广活动：" + name})
		if err != nil {
			return err
		}
		grantID = &item.ID
	}
	var until *time.Time
	if planCode != "" {
		value := now.Add(time.Duration(planDays) * 24 * time.Hour)
		until = &value
	}
	// Gift memberships remain separate from Stripe/manual assignments. A paid
	// membership takes precedence; the gift applies while unexpired otherwise.
	if _, err := tx.ExecContext(ctx, `UPDATE promotion_registrations SET rewarded_at=$2,grant_id=$3,plan_until=$4 WHERE id=$1`, id, now, grantID, until); err != nil {
		return err
	}
	return tx.Commit()
}

func loadPromotionPlan(ctx context.Context, q queryRower, acct *accountRow) error {
	var code string
	var until time.Time
	err := q.QueryRowContext(ctx, `SELECT i.plan_code,r.plan_until FROM promotion_registrations r
        JOIN promotion_invites i ON i.id=r.invite_id WHERE r.user_id=$1 AND r.rewarded_at IS NOT NULL AND r.plan_until>NOW() AND i.plan_code IS NOT NULL`, acct.UserID).Scan(&code, &until)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	acct.promotionPlan, err = getPlanTx(ctx, q, code)
	acct.promotionUntil = until
	return err
}
