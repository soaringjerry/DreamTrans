package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/lib/pq"
)

var ErrInvalidPromotion = errors.New("promotion invite is invalid, paused, expired or full")
var ErrPromotionInput = errors.New("invalid promotion configuration")
var promotionCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{5,47}$`)

type PromotionInvite struct {
	ID               string    `json:"id"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	Channel          string    `json:"channel"`
	Tags             []string  `json:"tags"`
	Enabled          bool      `json:"enabled"`
	ExpiresAt        time.Time `json:"expires_at"`
	MaxRegistrations int       `json:"max_registrations"`
	GrantUSD         float64   `json:"grant_usd"`
	GrantDays        int       `json:"grant_days"`
	PlanCode         string    `json:"plan_code"`
	PlanDays         int       `json:"plan_days"`
	CreatedAt        time.Time `json:"created_at"`
	Registrations    int       `json:"registrations"`
	Verified         int       `json:"verified"`
	Rewarded         int       `json:"rewarded"`
}

const promotionColumns = `i.id, i.code, i.name, i.channel, i.tags, i.enabled, i.expires_at,
    i.max_registrations, i.grant_usd, i.grant_days, COALESCE(i.plan_code, ''), i.plan_days, i.created_at`

type promotionScanner interface{ Scan(...any) error }

func scanPromotion(row promotionScanner, stats bool) (*PromotionInvite, error) {
	p := &PromotionInvite{}
	var tags []byte
	dest := []any{&p.ID, &p.Code, &p.Name, &p.Channel, &tags, &p.Enabled, &p.ExpiresAt,
		&p.MaxRegistrations, &p.GrantUSD, &p.GrantDays, &p.PlanCode, &p.PlanDays, &p.CreatedAt}
	if stats {
		dest = append(dest, &p.Registrations, &p.Verified, &p.Rewarded)
	}
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(tags, &p.Tags); err != nil {
		return nil, err
	}
	return p, nil
}

func validatePromotion(p *PromotionInvite) error {
	p.Code = strings.ToUpper(strings.TrimSpace(p.Code))
	p.Name, p.Channel, p.PlanCode = strings.TrimSpace(p.Name), strings.TrimSpace(p.Channel), strings.TrimSpace(p.PlanCode)
	if p.Name == "" || p.Channel == "" || utf8.RuneCountInString(p.Name) > 100 || utf8.RuneCountInString(p.Channel) > 100 {
		return fmt.Errorf("%w: activity and channel must contain 1–100 characters", ErrPromotionInput)
	}
	if p.Code != "" && !promotionCodePattern.MatchString(p.Code) {
		return fmt.Errorf("%w: code must contain 6–48 letters, digits, underscores or hyphens", ErrPromotionInput)
	}
	if !p.ExpiresAt.After(time.Now()) || p.ExpiresAt.After(time.Now().AddDate(10, 0, 0)) ||
		p.MaxRegistrations < 1 || p.MaxRegistrations > 1000000 ||
		math.IsNaN(p.GrantUSD) || math.IsInf(p.GrantUSD, 0) || p.GrantUSD < 0 || p.GrantUSD > 10000 ||
		(p.GrantUSD > 0 && p.GrantUSD < 0.00000001) ||
		p.GrantDays < 1 || p.GrantDays > 3650 || p.PlanDays < 1 || p.PlanDays > 3650 || p.PlanCode == "free" {
		return fmt.Errorf("%w: check expiry, registration limit and rewards", ErrPromotionInput)
	}
	if len(p.Tags) > 20 {
		return fmt.Errorf("%w: at most 20 tags", ErrPromotionInput)
	}
	tags := make([]string, 0, len(p.Tags))
	seen := map[string]bool{}
	for _, tag := range p.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || utf8.RuneCountInString(tag) > 40 {
			return fmt.Errorf("%w: tags must contain 1–40 characters", ErrPromotionInput)
		}
		if !seen[tag] {
			tags = append(tags, tag)
			seen[tag] = true
		}
	}
	p.Tags = tags
	return nil
}

func (s *PostgresStore) CreatePromotion(ctx context.Context, p *PromotionInvite, actor string) error {
	if err := validatePromotion(p); err != nil {
		return err
	}
	if p.Code == "" {
		data := make([]byte, 10)
		if _, err := rand.Read(data); err != nil {
			return err
		}
		p.Code = "DT-" + strings.ToUpper(hex.EncodeToString(data))
	}
	tags, err := json.Marshal(p.Tags)
	if err != nil {
		return err
	}
	// Plan definitions can change later just like manually assigned memberships;
	// the selected plan and duration on an invitation are immutable.
	err = s.db.QueryRowContext(ctx, `INSERT INTO promotion_invites
        (code,name,channel,tags,expires_at,max_registrations,grant_usd,grant_days,plan_code,plan_days,created_by)
        SELECT $1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11
        WHERE $9 = '' OR EXISTS (SELECT 1 FROM plans WHERE code=$9 AND active=true AND code<>'free')
        RETURNING id,enabled,created_at`, p.Code, p.Name, p.Channel, tags, p.ExpiresAt, p.MaxRegistrations,
		p.GrantUSD, p.GrantDays, p.PlanCode, p.PlanDays, actor).Scan(&p.ID, &p.Enabled, &p.CreatedAt)
	var pgErr *pq.Error
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: select an active membership plan", ErrPromotionInput)
	}
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: invite code already exists", ErrPromotionInput)
	}
	return err
}

func (s *PostgresStore) ListPromotions(ctx context.Context, limit, offset int, search string) ([]PromotionInvite, int, error) {
	filter := ` WHERE ($1='' OR i.name ILIKE $1 OR i.channel ILIKE $1 OR i.code ILIKE $1 OR i.tags::text ILIKE $1)`
	if search != "" {
		search = "%" + search + "%"
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM promotion_invites i`+filter, search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+promotionColumns+`,
        (SELECT COUNT(*) FROM promotion_registrations r WHERE r.invite_id=i.id),
        (SELECT COUNT(*) FROM promotion_registrations r JOIN users u ON u.id=r.user_id WHERE r.invite_id=i.id AND u.email_verified),
        (SELECT COUNT(*) FROM promotion_registrations r WHERE r.invite_id=i.id AND r.rewarded_at IS NOT NULL)
        FROM promotion_invites i`+filter+` ORDER BY i.created_at DESC,i.id LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]PromotionInvite, 0)
	for rows.Next() {
		p, err := scanPromotion(rows, true)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *p)
	}
	return items, total, rows.Err()
}

func (s *PostgresStore) SetPromotionEnabled(ctx context.Context, id string, enabled bool) error {
	result, err := s.db.ExecContext(ctx, `UPDATE promotion_invites SET enabled=$2 WHERE id=$1`, id, enabled)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func reservePromotionTx(ctx context.Context, tx *sql.Tx, code string) (*PromotionInvite, error) {
	if code == "" {
		return nil, nil
	}
	p, err := scanPromotion(tx.QueryRowContext(ctx, `SELECT `+promotionColumns+` FROM promotion_invites i WHERE i.code=$1 FOR UPDATE`, strings.ToUpper(strings.TrimSpace(code))), false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvalidPromotion
	}
	if err != nil {
		return nil, err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM promotion_registrations WHERE invite_id=$1`, p.ID).Scan(&count); err != nil {
		return nil, err
	}
	if !p.Enabled || !p.ExpiresAt.After(time.Now()) || count >= p.MaxRegistrations {
		return nil, ErrInvalidPromotion
	}
	return p, nil
}

func recordPromotionTx(ctx context.Context, tx *sql.Tx, inviteID string, user *models.User) error {
	hash := sha256.Sum256([]byte(auth.CanonicalEmail(user.Email)))
	_, err := tx.ExecContext(ctx, `INSERT INTO promotion_registrations(invite_id,user_id,canonical_email_hash) VALUES ($1,$2,$3)`, inviteID, user.ID, hex.EncodeToString(hash[:]))
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrInvalidPromotion
	}
	return err
}

func (s *PostgresStore) PreviewPromotion(ctx context.Context, code string) (*PromotionInvite, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return reservePromotionTx(ctx, tx, code)
}

type PromotionRegistration struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Verified     bool       `json:"verified"`
	RegisteredAt time.Time  `json:"registered_at"`
	RewardedAt   *time.Time `json:"rewarded_at"`
	PlanUntil    *time.Time `json:"plan_until"`
}

func (s *PostgresStore) ListPromotionRegistrations(ctx context.Context, id string, limit, offset int) ([]PromotionRegistration, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM promotion_registrations WHERE invite_id=$1`, id).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,COALESCE(r.user_id::text,''),COALESCE(u.email,''),COALESCE(u.name,''),COALESCE(u.email_verified,false),r.registered_at,r.rewarded_at,r.plan_until
        FROM promotion_registrations r LEFT JOIN users u ON u.id=r.user_id WHERE r.invite_id=$1 ORDER BY r.registered_at DESC,r.id LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]PromotionRegistration, 0)
	for rows.Next() {
		var r PromotionRegistration
		if err := rows.Scan(&r.ID, &r.UserID, &r.Email, &r.Name, &r.Verified, &r.RegisteredAt, &r.RewardedAt, &r.PlanUntil); err != nil {
			return nil, 0, err
		}
		items = append(items, r)
	}
	return items, total, rows.Err()
}
