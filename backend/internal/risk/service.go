package risk

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrInput = errors.New("invalid risk request")
var ErrDecision = errors.New("released signup rewards cannot be put on hold retroactively")

type Settings struct {
	Enabled                bool  `json:"enabled"`
	StrictMode             bool  `json:"strict_mode"`
	NetworkBurstLimit      int   `json:"network_burst_limit"`
	PrefixHourlyLimit      int   `json:"prefix_hourly_limit"`
	DailyRewardBudgetCents int64 `json:"daily_reward_budget_cents"`
	DeviceLimit            int   `json:"device_limit"`
	NetworkDailyLimit      int   `json:"network_daily_limit"`
	AutomaticDailyLimit    int   `json:"automatic_daily_limit"`
}
type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func ReadSettings(ctx context.Context, q queryRower) (Settings, error) {
	var s Settings
	err := q.QueryRowContext(ctx, `SELECT enabled,device_limit,network_daily_limit,automatic_daily_limit,strict_mode,network_burst_limit,prefix_hourly_limit,daily_reward_budget_cents FROM signup_risk_settings WHERE singleton`).Scan(&s.Enabled, &s.DeviceLimit, &s.NetworkDailyLimit, &s.AutomaticDailyLimit, &s.StrictMode, &s.NetworkBurstLimit, &s.PrefixHourlyLimit, &s.DailyRewardBudgetCents)
	return s, err
}

type Assessment struct {
	Decision     string   `json:"decision"`
	Reasons      []string `json:"reasons"`
	DeviceCount  int      `json:"device_count"`
	NetworkCount int      `json:"network_count"`
	DailyCount   int      `json:"daily_count"`
	Rules        Settings `json:"rules"`
	Score        int      `json:"score"`
	Evidence     Evidence `json:"evidence"`
}

// AssessTx serializes the short account-creation transactions across replicas.
// Counts include pending and deleted accounts, so parallel unverified signups
// cannot all receive the same first-device/first-network allowance.
func AssessTx(ctx context.Context, tx *sql.Tx, s *Signals) (*Assessment, error) {
	if s == nil {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(731820260907)`); err != nil {
		return nil, err
	}
	settings, err := ReadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	a := &Assessment{Decision: "allowed", Reasons: []string{}, Rules: settings}
	var emailExists bool
	err = tx.QueryRowContext(ctx, `SELECT
        EXISTS(SELECT 1 FROM signup_risk_profiles WHERE email_hash=$1),
        (SELECT COUNT(*) FROM signup_risk_profiles WHERE device_hash=$2 AND created_at>NOW()-INTERVAL '30 days'),
        (SELECT COUNT(*) FROM signup_risk_profiles WHERE network_hash=$3 AND created_at>NOW()-INTERVAL '24 hours'),
        (SELECT COUNT(*) FROM signup_risk_profiles WHERE decision IN ('allowed','approved') AND created_at>NOW()-INTERVAL '24 hours')`, s.EmailHash, s.DeviceHash, s.NetworkHash).Scan(&emailExists, &a.DeviceCount, &a.NetworkCount, &a.DailyCount)
	if err != nil {
		return nil, err
	}
	if emailExists {
		a.Reasons = append(a.Reasons, "previous_email")
	}
	if s.MissingDevice {
		a.Reasons = append(a.Reasons, "missing_device")
	}
	if s.NetworkHash == "" {
		a.Reasons = append(a.Reasons, "missing_network")
	}
	if a.DeviceCount >= settings.DeviceLimit {
		a.Reasons = append(a.Reasons, "device_accounts")
	}
	if a.NetworkCount >= settings.NetworkDailyLimit {
		a.Reasons = append(a.Reasons, "network_velocity")
	}
	if a.DailyCount >= settings.AutomaticDailyLimit {
		a.Reasons = append(a.Reasons, "daily_cap")
	}
	if err := enrichAssessment(ctx, tx, s, a); err != nil {
		return nil, err
	}
	if settings.StrictMode {
		a.Reasons = append(a.Reasons, "strict_mode")
	}
	if settings.StrictMode || (settings.Enabled && len(a.Reasons) > 0) {
		a.Decision = "review"
	}
	return a, nil
}
func RecordTx(ctx context.Context, tx *sql.Tx, userID string, s *Signals, a *Assessment) error {
	if a == nil {
		return nil
	}
	reasons, err := json.Marshal(a.Reasons)
	if err != nil {
		return err
	}
	rules, err := json.Marshal(a.Rules)
	if err != nil {
		return err
	}
	evidence, err := json.Marshal(a.Evidence)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO signup_risk_profiles(user_id,email_hash,device_hash,network_hash,decision,reasons,device_count,network_count,daily_count,rules,prefix_hash,score,evidence,fingerprint_hash)
        VALUES($1,$2,NULLIF($3,''),NULLIF($4,''),$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,NULLIF($14,''))`, userID, s.EmailHash, s.DeviceHash, s.NetworkHash, a.Decision, reasons, a.DeviceCount, a.NetworkCount, a.DailyCount, rules, s.PrefixHash, a.Score, evidence, s.FingerprintHash)
	return err
}

// RewardsAllowedTx must be called after locking the billing account, as must
// administrator review. Missing profiles are pre-feature or admin-created users.
func RewardsAllowedTx(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var decision string
	var verified, active bool
	err := tx.QueryRowContext(ctx, `SELECT r.decision,u.email_verified,u.is_active FROM signup_risk_profiles r JOIN users u ON u.id=r.user_id WHERE r.user_id=$1 FOR UPDATE OF r`, userID).Scan(&decision, &verified, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return (decision == "allowed" || decision == "approved") && verified && active, nil
}

type Profile struct {
	Assessment
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Email         string     `json:"email"`
	Name          string     `json:"name"`
	Verified      bool       `json:"verified"`
	CreatedAt     time.Time  `json:"created_at"`
	ReviewedAt    *time.Time `json:"reviewed_at"`
	ReviewNote    string     `json:"review_note"`
	BudgetBlocked bool       `json:"budget_blocked"`
	Promotion     string     `json:"promotion"`
	Channel       string     `json:"channel"`
}
type Service struct{ db *sql.DB }

func NewService(db *sql.DB) *Service                              { return &Service{db: db} }
func (s *Service) Settings(ctx context.Context) (Settings, error) { return ReadSettings(ctx, s.db) }
func (s *Service) UpdateSettings(ctx context.Context, input Settings, actor string) error {
	if input.NetworkBurstLimit < 1 || input.NetworkBurstLimit > 10000 || input.PrefixHourlyLimit < 1 || input.PrefixHourlyLimit > 100000 || input.DailyRewardBudgetCents < 0 || input.DailyRewardBudgetCents > 100000000 {
		return ErrInput
	}
	if input.DeviceLimit < 1 || input.DeviceLimit > 100 || input.NetworkDailyLimit < 1 || input.NetworkDailyLimit > 10000 || input.AutomaticDailyLimit < 1 || input.AutomaticDailyLimit > 100000 {
		return ErrInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(731820260907)`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(731820260908)`); err != nil {
		return err
	}
	before, err := ReadSettings(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE signup_risk_settings SET enabled=$1,device_limit=$2,network_daily_limit=$3,automatic_daily_limit=$4,updated_at=NOW(),updated_by=$5,strict_mode=$6,network_burst_limit=$7,prefix_hourly_limit=$8,daily_reward_budget_cents=$9 WHERE singleton`, input.Enabled, input.DeviceLimit, input.NetworkDailyLimit, input.AutomaticDailyLimit, actor, input.StrictMode, input.NetworkBurstLimit, input.PrefixHourlyLimit, input.DailyRewardBudgetCents); err != nil {
		return err
	}
	details, err := json.Marshal(map[string]any{"before": before, "after": input})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO signup_risk_audit(actor_id,action,details) VALUES($1,'settings',$2)`, actor, details); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Service) List(ctx context.Context, decision, search string, limit, offset int) ([]Profile, int, error) {
	if !validDecisionFilter(decision) || limit < 1 || limit > 100 || offset < 0 {
		return nil, 0, ErrInput
	}
	filter := ` FROM signup_risk_profiles r LEFT JOIN users u ON u.id=r.user_id
        LEFT JOIN promotion_registrations pr ON pr.user_id=u.id LEFT JOIN promotion_invites pi ON pi.id=pr.invite_id
        WHERE ($1='' OR r.decision=$1 OR ($1='budget_hold' AND r.budget_blocked)) AND ($2='' OR u.email ILIKE $3 OR u.name ILIKE $3 OR pi.channel ILIKE $3)`
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*)`+filter, decision, search, "%"+search+"%").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,COALESCE(r.user_id::text,''),COALESCE(u.email,''),COALESCE(u.name,''),COALESCE(u.email_verified,false),r.decision,r.reasons,r.device_count,r.network_count,r.daily_count,r.rules,r.created_at,r.reviewed_at,r.review_note,COALESCE(pi.name,''),COALESCE(pi.channel,''),r.score,r.evidence,r.budget_blocked`+filter+` ORDER BY CASE WHEN r.decision='review' THEN r.score ELSE 0 END DESC,r.created_at DESC,r.id LIMIT $4 OFFSET $5`, decision, search, "%"+search+"%", limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	profiles := make([]Profile, 0)
	for rows.Next() {
		var p Profile
		var reasons, rules, evidence []byte
		if err := rows.Scan(&p.ID, &p.UserID, &p.Email, &p.Name, &p.Verified, &p.Decision, &reasons, &p.DeviceCount, &p.NetworkCount, &p.DailyCount, &rules, &p.CreatedAt, &p.ReviewedAt, &p.ReviewNote, &p.Promotion, &p.Channel, &p.Score, &evidence, &p.BudgetBlocked); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(reasons, &p.Reasons); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(rules, &p.Rules); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal(evidence, &p.Evidence); err != nil {
			return nil, 0, err
		}
		profiles = append(profiles, p)
	}
	return profiles, total, rows.Err()
}
func validDecisionFilter(value string) bool {
	switch value {
	case "", "allowed", "review", "approved", "denied", "budget_hold":
		return true
	}
	return false
}

// Review returns the user ID to fulfill after approval. Only held/denied cases
// can change; this does not claw back previously granted funds or disable login.
func (s *Service) Review(ctx context.Context, id, decision, note, actor string) (string, error) {
	note = strings.TrimSpace(note)
	if (decision != "approved" && decision != "denied") || note == "" || utf8.RuneCountInString(note) > 500 {
		return "", ErrInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	var userID string
	if err := tx.QueryRowContext(ctx, `SELECT user_id FROM signup_risk_profiles WHERE id=$1 AND user_id IS NOT NULL`, id).Scan(&userID); err != nil {
		return "", err
	}
	// Same lock order as all ledger mutations.
	var accountID string
	if err := tx.QueryRowContext(ctx, `SELECT a.id FROM billing_accounts a JOIN users u ON u.billing_account_id=a.id WHERE u.id=$1 FOR UPDATE OF a`, userID).Scan(&accountID); err != nil {
		return "", err
	}
	var previous string
	var budgetBlocked bool
	if err := tx.QueryRowContext(ctx, `SELECT decision,budget_blocked FROM signup_risk_profiles WHERE id=$1 FOR UPDATE`, id).Scan(&previous, &budgetBlocked); err != nil {
		return "", err
	}
	if previous == decision {
		return userID, nil
	}
	if previous == "approved" || (previous == "allowed" && (!budgetBlocked || decision != "approved")) {
		return "", ErrDecision
	}
	if _, err := tx.ExecContext(ctx, `UPDATE signup_risk_profiles SET decision=$2,reviewed_at=NOW(),reviewed_by=$3,review_note=$4 WHERE id=$1`, id, decision, actor, note); err != nil {
		return "", err
	}
	details, err := json.Marshal(map[string]string{"before": previous, "after": decision, "note": note})
	if err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO signup_risk_audit(profile_id,actor_id,action,details) VALUES($1,$2,'review',$3)`, id, actor, details); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return userID, nil
}
func (s *Service) UserDecision(ctx context.Context, userID string) (string, error) {
	var decision string
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN budget_blocked AND decision IN ('allowed','approved') THEN 'budget_hold' ELSE decision END FROM signup_risk_profiles WHERE user_id=$1`, userID).Scan(&decision)
	if errors.Is(err, sql.ErrNoRows) {
		return "legacy", nil
	}
	return decision, err
}

// PruneSignals drops correlation identifiers after 30 days; decision evidence
// remains for review. Email HMACs remain to prevent delete/re-register rewards.
func (s *Service) PruneSignals(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE signup_risk_profiles SET device_hash=NULL,network_hash=NULL,prefix_hash=NULL,fingerprint_hash=NULL WHERE created_at<NOW()-INTERVAL '30 days' AND (device_hash IS NOT NULL OR network_hash IS NOT NULL OR prefix_hash IS NOT NULL OR fingerprint_hash IS NOT NULL)`)
	if err != nil {
		return fmt.Errorf("prune signup risk signals: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM signup_risk_reward_spend WHERE created_at<NOW()-INTERVAL '30 days'`); err != nil {
		return fmt.Errorf("prune reward budget receipts: %w", err)
	}
	return nil
}

// AuditEntry is one immutable administrator review action.
type AuditEntry struct {
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Details   json.RawMessage `json:"details"`
	CreatedAt time.Time       `json:"created_at"`
}

func (s *Service) Audit(ctx context.Context, id string) ([]AuditEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(u.email, 'deleted administrator'), a.action, a.details, a.created_at FROM signup_risk_audit a LEFT JOIN users u ON u.id=a.actor_id WHERE a.profile_id=$1 ORDER BY a.created_at DESC, a.id LIMIT 50`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]AuditEntry, 0)
	for rows.Next() {
		var entry AuditEntry
		if err := rows.Scan(&entry.Actor, &entry.Action, &entry.Details, &entry.CreatedAt); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
