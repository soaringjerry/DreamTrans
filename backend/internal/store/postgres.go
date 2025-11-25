package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	_ "github.com/lib/pq"
)

// PostgresStore handles all database operations
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore creates a new PostgreSQL store
func NewPostgresStore() (*PostgresStore, error) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable not set")
	}

	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &PostgresStore{db: db}, nil
}

// Close closes the database connection
func (s *PostgresStore) Close() error {
	return s.db.Close()
}

// DB returns the underlying database connection
func (s *PostgresStore) DB() *sql.DB {
	return s.db
}

// ========== User Operations ==========

// CreateUser creates a new user
func (s *PostgresStore) CreateUser(ctx context.Context, user *models.User) error {
	query := `
		INSERT INTO users (tenant_id, email, password_hash, name, role, is_active, email_verified)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`
	return s.db.QueryRowContext(ctx, query,
		user.TenantID, user.Email, user.PasswordHash, user.Name, user.Role, user.IsActive, user.EmailVerified,
	).Scan(&user.ID, &user.CreatedAt, &user.UpdatedAt)
}

// GetUserByID retrieves a user by ID
func (s *PostgresStore) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	user := &models.User{}
	query := `
		SELECT id, tenant_id, email, password_hash, name, role, is_active, email_verified, last_login_at, created_at, updated_at
		FROM users WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.PasswordHash, &user.Name, &user.Role,
		&user.IsActive, &user.EmailVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return user, err
}

// GetUserByEmail retrieves a user by email
func (s *PostgresStore) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	user := &models.User{}
	query := `
		SELECT id, tenant_id, email, password_hash, name, role, is_active, email_verified, last_login_at, created_at, updated_at
		FROM users WHERE email = $1`
	err := s.db.QueryRowContext(ctx, query, email).Scan(
		&user.ID, &user.TenantID, &user.Email, &user.PasswordHash, &user.Name, &user.Role,
		&user.IsActive, &user.EmailVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return user, err
}

// UpdateUserLastLogin updates the last login timestamp
func (s *PostgresStore) UpdateUserLastLogin(ctx context.Context, userID string) error {
	query := `UPDATE users SET last_login_at = NOW() WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, userID)
	return err
}

// UpdateUserPassword updates user password
func (s *PostgresStore) UpdateUserPassword(ctx context.Context, userID, passwordHash string) error {
	query := `UPDATE users SET password_hash = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, query, passwordHash, userID)
	return err
}

// ========== Tenant Operations ==========

// GetTenantByID retrieves a tenant by ID
func (s *PostgresStore) GetTenantByID(ctx context.Context, id string) (*models.Tenant, error) {
	tenant := &models.Tenant{}
	query := `
		SELECT id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions, created_at, updated_at
		FROM tenants WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&tenant.ID, &tenant.Name, &tenant.Slug, &tenant.Plan,
		&tenant.APIQuotaMonthly, &tenant.StorageQuotaGB, &tenant.MaxSessions,
		&tenant.CreatedAt, &tenant.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return tenant, err
}

// GetDefaultTenant retrieves the default tenant
func (s *PostgresStore) GetDefaultTenant(ctx context.Context) (*models.Tenant, error) {
	return s.GetTenantByID(ctx, "00000000-0000-0000-0000-000000000001")
}

// CreateTenant creates a new tenant
func (s *PostgresStore) CreateTenant(ctx context.Context, tenant *models.Tenant) error {
	query := `
		INSERT INTO tenants (name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`
	return s.db.QueryRowContext(ctx, query,
		tenant.Name, tenant.Slug, tenant.Plan, tenant.APIQuotaMonthly, tenant.StorageQuotaGB, tenant.MaxSessions,
	).Scan(&tenant.ID, &tenant.CreatedAt, &tenant.UpdatedAt)
}

// ========== Session Operations ==========

// CreateSession creates a new transcription session
func (s *PostgresStore) CreateSession(ctx context.Context, session *models.Session) error {
	query := `
		INSERT INTO sessions (user_id, tenant_id, title, source_language, target_language, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, started_at, created_at, updated_at`
	return s.db.QueryRowContext(ctx, query,
		session.UserID, session.TenantID, session.Title, session.SourceLanguage, session.TargetLanguage, session.Status,
	).Scan(&session.ID, &session.StartedAt, &session.CreatedAt, &session.UpdatedAt)
}

// GetSessionByID retrieves a session by ID
func (s *PostgresStore) GetSessionByID(ctx context.Context, id string) (*models.Session, error) {
	session := &models.Session{}
	query := `
		SELECT id, user_id, tenant_id, title, source_language, target_language, duration_seconds, status, started_at, ended_at, created_at, updated_at
		FROM sessions WHERE id = $1`
	err := s.db.QueryRowContext(ctx, query, id).Scan(
		&session.ID, &session.UserID, &session.TenantID, &session.Title,
		&session.SourceLanguage, &session.TargetLanguage, &session.DurationSeconds,
		&session.Status, &session.StartedAt, &session.EndedAt, &session.CreatedAt, &session.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return session, err
}

// GetSessionsByUser retrieves all sessions for a user
func (s *PostgresStore) GetSessionsByUser(ctx context.Context, userID string, limit, offset int) ([]models.Session, error) {
	query := `
		SELECT id, user_id, tenant_id, title, source_language, target_language, duration_seconds, status, started_at, ended_at, created_at, updated_at
		FROM sessions WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	rows, err := s.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []models.Session
	for rows.Next() {
		var session models.Session
		if err := rows.Scan(
			&session.ID, &session.UserID, &session.TenantID, &session.Title,
			&session.SourceLanguage, &session.TargetLanguage, &session.DurationSeconds,
			&session.Status, &session.StartedAt, &session.EndedAt, &session.CreatedAt, &session.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// UpdateSession updates a session
func (s *PostgresStore) UpdateSession(ctx context.Context, session *models.Session) error {
	query := `
		UPDATE sessions SET title = $1, duration_seconds = $2, status = $3, ended_at = $4
		WHERE id = $5`
	_, err := s.db.ExecContext(ctx, query, session.Title, session.DurationSeconds, session.Status, session.EndedAt, session.ID)
	return err
}

// DeleteSession deletes a session
func (s *PostgresStore) DeleteSession(ctx context.Context, id string) error {
	query := `DELETE FROM sessions WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// ========== Transcript Operations ==========

// CreateTranscript creates a new transcript segment
func (s *PostgresStore) CreateTranscript(ctx context.Context, transcript *models.Transcript) error {
	query := `
		INSERT INTO transcripts (session_id, speaker, text, translation, start_time, end_time, status, is_partial)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at, updated_at`
	return s.db.QueryRowContext(ctx, query,
		transcript.SessionID, transcript.Speaker, transcript.Text, transcript.Translation,
		transcript.StartTime, transcript.EndTime, transcript.Status, transcript.IsPartial,
	).Scan(&transcript.ID, &transcript.CreatedAt, &transcript.UpdatedAt)
}

// GetTranscriptsBySession retrieves all transcripts for a session
func (s *PostgresStore) GetTranscriptsBySession(ctx context.Context, sessionID string) ([]models.Transcript, error) {
	query := `
		SELECT id, session_id, speaker, text, translation, start_time, end_time, status, is_partial, created_at, updated_at
		FROM transcripts WHERE session_id = $1
		ORDER BY start_time ASC`
	rows, err := s.db.QueryContext(ctx, query, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var transcripts []models.Transcript
	for rows.Next() {
		var t models.Transcript
		if err := rows.Scan(
			&t.ID, &t.SessionID, &t.Speaker, &t.Text, &t.Translation,
			&t.StartTime, &t.EndTime, &t.Status, &t.IsPartial, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		transcripts = append(transcripts, t)
	}
	return transcripts, rows.Err()
}

// UpdateTranscript updates a transcript
func (s *PostgresStore) UpdateTranscript(ctx context.Context, transcript *models.Transcript) error {
	query := `
		UPDATE transcripts SET text = $1, translation = $2, end_time = $3, status = $4, is_partial = $5
		WHERE id = $6`
	_, err := s.db.ExecContext(ctx, query,
		transcript.Text, transcript.Translation, transcript.EndTime, transcript.Status, transcript.IsPartial, transcript.ID,
	)
	return err
}

// ========== Refresh Token Operations ==========

// CreateRefreshToken stores a refresh token
func (s *PostgresStore) CreateRefreshToken(ctx context.Context, token *models.RefreshToken) error {
	query := `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, created_at`
	return s.db.QueryRowContext(ctx, query, token.UserID, token.TokenHash, token.ExpiresAt).Scan(&token.ID, &token.CreatedAt)
}

// GetRefreshTokenByHash retrieves a refresh token by hash
func (s *PostgresStore) GetRefreshTokenByHash(ctx context.Context, tokenHash string) (*models.RefreshToken, error) {
	token := &models.RefreshToken{}
	query := `
		SELECT id, user_id, token_hash, expires_at, created_at, revoked_at
		FROM refresh_tokens WHERE token_hash = $1 AND revoked_at IS NULL`
	err := s.db.QueryRowContext(ctx, query, tokenHash).Scan(
		&token.ID, &token.UserID, &token.TokenHash, &token.ExpiresAt, &token.CreatedAt, &token.RevokedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return token, err
}

// RevokeRefreshToken revokes a refresh token
func (s *PostgresStore) RevokeRefreshToken(ctx context.Context, tokenHash string) error {
	query := `UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1`
	_, err := s.db.ExecContext(ctx, query, tokenHash)
	return err
}

// RevokeAllUserRefreshTokens revokes all refresh tokens for a user
func (s *PostgresStore) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	query := `UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := s.db.ExecContext(ctx, query, userID)
	return err
}

// ========== Usage Log Operations ==========

// CreateUsageLog creates a usage log entry
func (s *PostgresStore) CreateUsageLog(ctx context.Context, log *models.UsageLog) error {
	metadataJSON, _ := json.Marshal(log.Metadata)
	query := `
		INSERT INTO usage_logs (tenant_id, user_id, action, quantity, session_id, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, month_key`
	return s.db.QueryRowContext(ctx, query,
		log.TenantID, log.UserID, log.Action, log.Quantity, log.SessionID, metadataJSON,
	).Scan(&log.ID, &log.CreatedAt, &log.MonthKey)
}

// GetUsageSummary retrieves usage summary for a tenant in a given month
func (s *PostgresStore) GetUsageSummary(ctx context.Context, tenantID, monthKey string) (*models.UsageSummary, error) {
	summary := &models.UsageSummary{TenantID: tenantID, MonthKey: monthKey}
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN action = 'transcription' THEN quantity ELSE 0 END), 0) as transcription_minutes,
			COALESCE(SUM(CASE WHEN action = 'translation' THEN 1 ELSE 0 END), 0) as translation_count,
			COALESCE(SUM(CASE WHEN action = 'rag_query' THEN 1 ELSE 0 END), 0) as rag_query_count,
			COALESCE(SUM(CASE WHEN action = 'storage' THEN quantity ELSE 0 END), 0) as storage_mb
		FROM usage_logs WHERE tenant_id = $1 AND month_key = $2`
	err := s.db.QueryRowContext(ctx, query, tenantID, monthKey).Scan(
		&summary.TranscriptionMinutes, &summary.TranslationCount, &summary.RAGQueryCount, &summary.StorageMB,
	)
	return summary, err
}

// ========== Admin Operations ==========

// ListUsers retrieves all users with pagination
func (s *PostgresStore) ListUsers(ctx context.Context, limit, offset int) ([]models.User, int, error) {
	// Get total count
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, tenant_id, email, password_hash, name, role, is_active, email_verified, last_login_at, created_at, updated_at
		FROM users ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var users []models.User
	for rows.Next() {
		var user models.User
		if err := rows.Scan(
			&user.ID, &user.TenantID, &user.Email, &user.PasswordHash, &user.Name, &user.Role,
			&user.IsActive, &user.EmailVerified, &user.LastLoginAt, &user.CreatedAt, &user.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		users = append(users, user)
	}
	return users, total, rows.Err()
}

// ListTenants retrieves all tenants with pagination
func (s *PostgresStore) ListTenants(ctx context.Context, limit, offset int) ([]models.Tenant, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants").Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `
		SELECT id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions, created_at, updated_at
		FROM tenants ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := s.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var tenants []models.Tenant
	for rows.Next() {
		var tenant models.Tenant
		if err := rows.Scan(
			&tenant.ID, &tenant.Name, &tenant.Slug, &tenant.Plan,
			&tenant.APIQuotaMonthly, &tenant.StorageQuotaGB, &tenant.MaxSessions,
			&tenant.CreatedAt, &tenant.UpdatedAt,
		); err != nil {
			return nil, 0, err
		}
		tenants = append(tenants, tenant)
	}
	return tenants, total, rows.Err()
}

// UpdateUser updates a user
func (s *PostgresStore) UpdateUser(ctx context.Context, user *models.User) error {
	query := `UPDATE users SET name = $1, role = $2, is_active = $3 WHERE id = $4`
	_, err := s.db.ExecContext(ctx, query, user.Name, user.Role, user.IsActive, user.ID)
	return err
}

// DeleteUser deletes a user
func (s *PostgresStore) DeleteUser(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = $1`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// UpdateTenant updates a tenant
func (s *PostgresStore) UpdateTenant(ctx context.Context, tenant *models.Tenant) error {
	query := `UPDATE tenants SET name = $1, plan = $2, api_quota_monthly = $3, storage_quota_gb = $4, max_sessions = $5 WHERE id = $6`
	_, err := s.db.ExecContext(ctx, query, tenant.Name, tenant.Plan, tenant.APIQuotaMonthly, tenant.StorageQuotaGB, tenant.MaxSessions, tenant.ID)
	return err
}

// GetGlobalStats retrieves global statistics
func (s *PostgresStore) GetGlobalStats(ctx context.Context) (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	var userCount, tenantCount, sessionCount, transcriptCount int
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&userCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants").Scan(&tenantCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions").Scan(&sessionCount)
	s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM transcripts").Scan(&transcriptCount)

	stats["user_count"] = userCount
	stats["tenant_count"] = tenantCount
	stats["session_count"] = sessionCount
	stats["transcript_count"] = transcriptCount

	return stats, nil
}
