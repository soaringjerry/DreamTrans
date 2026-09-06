package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

// ErrVerificationTokenInvalid is returned when a token is unknown, expired or
// already used. Callers must not distinguish these cases to the client.
var ErrVerificationTokenInvalid = errors.New("verification token is invalid or expired")

// CreateEmailVerificationToken stores the hash of a freshly issued token and
// retires any earlier unused tokens for the same user so only the newest link
// works.
func (s *PostgresStore) CreateEmailVerificationToken(
	ctx context.Context,
	userID, tokenHash string,
	expiresAt time.Time,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		UPDATE email_verification_tokens SET used_at = NOW()
		WHERE user_id = $1 AND used_at IS NULL
	`, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, userID, tokenHash, expiresAt.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// LatestEmailVerificationIssuedAt reports when the newest token for the user
// was created, for resend throttling. Zero time when none exists.
func (s *PostgresStore) LatestEmailVerificationIssuedAt(ctx context.Context, userID string) (time.Time, error) {
	var issued sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT MAX(created_at) FROM email_verification_tokens WHERE user_id = $1
	`, userID).Scan(&issued)
	if err != nil {
		return time.Time{}, err
	}
	if !issued.Valid {
		return time.Time{}, nil
	}
	return issued.Time, nil
}

// ConsumeEmailVerificationToken marks the token used and the user verified in
// one transaction and returns the verified user. Returns
// ErrVerificationTokenInvalid for unknown, expired or spent tokens.
func (s *PostgresStore) ConsumeEmailVerificationToken(ctx context.Context, tokenHash string) (*models.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var userID string
	err = tx.QueryRowContext(ctx, `
		UPDATE email_verification_tokens
		SET used_at = NOW()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING user_id
	`, tokenHash).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrVerificationTokenInvalid
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE users SET email_verified = true, updated_at = NOW() WHERE id = $1
	`, userID); err != nil {
		return nil, err
	}
	user := &models.User{}
	err = tx.QueryRowContext(ctx, `
		SELECT id, tenant_id, email, password_hash, name, role, is_active, email_verified,
		       training_opt_in, last_login_at, created_at, updated_at
		FROM users WHERE id = $1
	`, userID).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.PasswordHash, &user.Name, &user.Role,
		&user.IsActive, &user.EmailVerified, &user.TrainingOptIn,
		&user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return user, nil
}

// SetUserEmailVerified is the administrative override for accounts whose
// mail never arrives.
func (s *PostgresStore) SetUserEmailVerified(ctx context.Context, userID string, verified bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE users SET email_verified = $2, updated_at = NOW() WHERE id = $1
	`, userID, verified)
	return err
}

// UserExistsByCanonicalEmail reports whether any account already maps to the
// canonical (alias-folded) form of an address.
func (s *PostgresStore) UserExistsByCanonicalEmail(ctx context.Context, canonical string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users WHERE email_canonical = $1)
	`, canonical).Scan(&exists)
	return exists, err
}

// DeleteExpiredEmailVerificationTokens is housekeeping for the token table.
func (s *PostgresStore) DeleteExpiredEmailVerificationTokens(ctx context.Context, olderThan time.Duration) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM email_verification_tokens
		WHERE expires_at < NOW() - $1::interval
	`, olderThan.String())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
