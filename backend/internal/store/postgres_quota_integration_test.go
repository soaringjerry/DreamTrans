package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

// TestPostgresQuotaEnforcement is skipped during ordinary unit tests. CI or a
// local review can opt in with a disposable, fully migrated PostgreSQL URL.
func TestPostgresQuotaEnforcement(t *testing.T) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	postgresStore := &PostgresStore{db: db}
	if err := postgresStore.VerifySchema(t.Context()); err != nil {
		t.Fatalf("verify migrated schema: %v", err)
	}

	var tenantID, userID, sessionID string
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO tenants
			(name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
		VALUES ('Quota Integration', 'qi-' || gen_random_uuid(), 'free', 5, 0, 10)
		RETURNING id
	`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id = $1`, tenantID)
	})
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO users
			(tenant_id, email, password_hash, name, role, is_active, email_verified)
		VALUES ($1, gen_random_uuid() || '@example.invalid', 'integration-only', 'Quota User', 'user', true, true)
		RETURNING id
	`, tenantID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		INSERT INTO sessions (user_id, tenant_id, title, source_language, target_language, status)
		VALUES ($1, $2, 'Quota Session', 'en', 'zh', 'active')
		RETURNING id
	`, userID, tenantID).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}

	const attempts = 20
	var allowed atomic.Int64
	var rejected atomic.Int64
	var unexpected atomic.Int64
	var waitGroup sync.WaitGroup
	for range attempts {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			_, consumeErr := postgresStore.ConsumeAPIRequest(t.Context(), tenantID, userID)
			switch {
			case consumeErr == nil:
				allowed.Add(1)
			case errors.Is(consumeErr, ErrAPIQuota):
				rejected.Add(1)
			default:
				unexpected.Add(1)
			}
		}()
	}
	waitGroup.Wait()
	if allowed.Load() != 5 || rejected.Load() != attempts-5 || unexpected.Load() != 0 {
		t.Fatalf("API quota results: allowed=%d rejected=%d unexpected=%d",
			allowed.Load(), rejected.Load(), unexpected.Load())
	}

	translation := "ok"
	transcript := &models.Transcript{
		SessionID:       sessionID,
		ClientSegmentID: "11111111-1111-4111-8111-111111111111",
		Speaker:         "S",
		Text:            "你好",
		Translation:     &translation,
		StartTime:       0,
		Status:          "translated",
		IsPartial:       false,
	}
	if err := postgresStore.CreateTranscript(t.Context(), transcript); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("zero-byte storage quota error = %v, want ErrStorageQuota", err)
	}
	var transcriptCount int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM transcripts WHERE session_id = $1
	`, sessionID).Scan(&transcriptCount); err != nil {
		t.Fatal(err)
	}
	if transcriptCount != 0 {
		t.Fatalf("quota-rejected transcript was persisted: count=%d", transcriptCount)
	}

	if _, err := db.ExecContext(t.Context(), `
		UPDATE tenants SET storage_quota_gb = -1 WHERE id = $1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.CreateTranscript(t.Context(), transcript); err != nil {
		t.Fatal(err)
	}
	usedBytes, err := postgresStore.GetTenantTranscriptStorageBytes(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if usedBytes != 9 {
		t.Fatalf("stored payload bytes = %d, want 9", usedBytes)
	}
	summary, err := postgresStore.GetUsageSummary(
		t.Context(),
		tenantID,
		time.Now().UTC().Format("2006-01"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if summary.APIRequestCount != 5 {
		t.Fatalf("reported API requests = %d, want 5", summary.APIRequestCount)
	}
	if math.Abs(summary.StorageMB-9.0/(1024*1024)) > 1e-12 {
		t.Fatalf("reported storage = %.12f MiB, want 9 bytes", summary.StorageMB)
	}
	if _, err := db.ExecContext(t.Context(), `
		UPDATE tenants SET storage_quota_gb = 0 WHERE id = $1
	`, tenantID); err != nil {
		t.Fatal(err)
	}
	// Lowering a quota below existing usage must not break retry safety.
	if err := postgresStore.CreateTranscript(t.Context(), transcript); err != nil {
		t.Fatalf("idempotent upsert above a lowered quota failed: %v", err)
	}
	largerTranslation := "larger"
	larger := *transcript
	larger.Text = "this would grow the stored payload"
	larger.Translation = &largerTranslation
	if err := postgresStore.CreateTranscript(t.Context(), &larger); !errors.Is(err, ErrStorageQuota) {
		t.Fatalf("growing upsert above a lowered quota error = %v, want ErrStorageQuota", err)
	}
	smallerTranslation := "x"
	smaller := *transcript
	smaller.Text = "a"
	smaller.Translation = &smallerTranslation
	if err := postgresStore.CreateTranscript(t.Context(), &smaller); err != nil {
		t.Fatalf("shrinking upsert above a lowered quota failed: %v", err)
	}
	usedBytes, err = postgresStore.GetTenantTranscriptStorageBytes(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if usedBytes != 3 {
		t.Fatalf("storage counter after shrink = %d, want 3", usedBytes)
	}

	if _, err := db.ExecContext(t.Context(), `
		UPDATE users SET role = 'admin', is_active = false WHERE id = $1
	`, userID); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.UpdateUserName(t.Context(), userID, "Renamed Safely"); err != nil {
		t.Fatal(err)
	}
	var name, role string
	var active bool
	if err := db.QueryRowContext(t.Context(), `
		SELECT name, role, is_active FROM users WHERE id = $1
	`, userID).Scan(&name, &role, &active); err != nil {
		t.Fatal(err)
	}
	if name != "Renamed Safely" || role != "admin" || active {
		t.Fatalf("profile update changed admin-controlled fields: name=%q role=%q active=%v",
			name, role, active)
	}

	var superAdminID, regularAdminID, targetUserID string
	for _, fixture := range []struct {
		name string
		role string
		id   *string
	}{
		{name: "Atomic Super Admin", role: "super_admin", id: &superAdminID},
		{name: "Atomic Regular Admin", role: "admin", id: &regularAdminID},
		{name: "Atomic Target", role: "user", id: &targetUserID},
	} {
		if err := db.QueryRowContext(t.Context(), `
			INSERT INTO users
				(tenant_id, email, password_hash, name, role, is_active, email_verified)
			VALUES ($1, gen_random_uuid() || '@example.invalid', 'integration-only', $2, $3, true, true)
			RETURNING id
		`, tenantID, fixture.name, fixture.role).Scan(fixture.id); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.ExecContext(t.Context(), `
		UPDATE users SET role = 'admin' WHERE id = $1
	`, targetUserID); err != nil {
		t.Fatal(err)
	}
	changedName := "Must Not Apply"
	if err := postgresStore.UpdateUserAdminSafe(
		t.Context(),
		targetUserID,
		regularAdminID,
		&changedName,
		nil,
		nil,
	); !errors.Is(err, ErrAdminUserForbidden) {
		t.Fatalf("regular admin stale-target update error = %v, want ErrAdminUserForbidden", err)
	}
	if err := postgresStore.DeleteUserAdminSafe(
		t.Context(),
		targetUserID,
		regularAdminID,
	); !errors.Is(err, ErrAdminUserForbidden) {
		t.Fatalf("regular admin elevated-target delete error = %v, want ErrAdminUserForbidden", err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT name, role FROM users WHERE id = $1
	`, targetUserID).Scan(&name, &role); err != nil {
		t.Fatal(err)
	}
	if name != "Atomic Target" || role != "admin" {
		t.Fatalf("forbidden admin mutation changed target: name=%q role=%q", name, role)
	}

	demotedRole := "user"
	if err := postgresStore.UpdateUserAdminSafe(
		t.Context(),
		targetUserID,
		superAdminID,
		nil,
		&demotedRole,
		nil,
	); err != nil {
		t.Fatalf("super admin demote target: %v", err)
	}
	if err := postgresStore.DeleteUserAdminSafe(
		t.Context(),
		targetUserID,
		regularAdminID,
	); err != nil {
		t.Fatalf("regular admin delete demoted user: %v", err)
	}
	if err := postgresStore.DeleteUserAdminSafe(
		t.Context(),
		superAdminID,
		superAdminID,
	); !errors.Is(err, ErrAdminUserForbidden) {
		t.Fatalf("self-delete error = %v, want ErrAdminUserForbidden", err)
	}
	if err := postgresStore.DeleteSession(t.Context(), sessionID); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	usedBytes, err = postgresStore.GetTenantTranscriptStorageBytes(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if usedBytes != 0 {
		t.Fatalf("storage counter after session cascade = %d, want 0", usedBytes)
	}

	patchedName := "Quota Integration Patched"
	patchedMaxSessions := 42
	var patchWaitGroup sync.WaitGroup
	var patchErrors [2]error
	patchWaitGroup.Add(2)
	go func() {
		defer patchWaitGroup.Done()
		_, patchErrors[0] = postgresStore.UpdateTenantFields(
			t.Context(), tenantID, &patchedName, nil, nil, nil, nil,
		)
	}()
	go func() {
		defer patchWaitGroup.Done()
		_, patchErrors[1] = postgresStore.UpdateTenantFields(
			t.Context(), tenantID, nil, nil, nil, nil, &patchedMaxSessions,
		)
	}()
	patchWaitGroup.Wait()
	for _, patchErr := range patchErrors {
		if patchErr != nil {
			t.Fatalf("concurrent tenant patch: %v", patchErr)
		}
	}
	tenant, err := postgresStore.GetTenantByID(t.Context(), tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if tenant == nil || tenant.Name != patchedName || tenant.MaxSessions != patchedMaxSessions {
		t.Fatalf("concurrent tenant patches lost a field: %#v", tenant)
	}

	sessionForPatch := &models.Session{
		UserID:         regularAdminID,
		TenantID:       tenantID,
		Title:          "Before Concurrent Patch",
		SourceLanguage: "en",
		TargetLanguage: "zh",
		Status:         "active",
	}
	if err := postgresStore.CreateSessionWithQuota(t.Context(), sessionForPatch); err != nil {
		t.Fatal(err)
	}
	patchedTitle := "After Concurrent Patch"
	patchedDuration := 123
	var sessionPatchErrors [2]error
	patchWaitGroup.Add(2)
	go func() {
		defer patchWaitGroup.Done()
		_, sessionPatchErrors[0] = postgresStore.UpdateSessionFieldsWithQuota(
			t.Context(),
			sessionForPatch.ID,
			regularAdminID,
			&patchedTitle,
			nil,
			nil,
		)
	}()
	go func() {
		defer patchWaitGroup.Done()
		_, sessionPatchErrors[1] = postgresStore.UpdateSessionFieldsWithQuota(
			t.Context(),
			sessionForPatch.ID,
			regularAdminID,
			nil,
			nil,
			&patchedDuration,
		)
	}()
	patchWaitGroup.Wait()
	for _, patchErr := range sessionPatchErrors {
		if patchErr != nil {
			t.Fatalf("concurrent session patch: %v", patchErr)
		}
	}
	patchedSession, err := postgresStore.GetSessionByID(t.Context(), sessionForPatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if patchedSession == nil ||
		patchedSession.Title != patchedTitle ||
		patchedSession.DurationSeconds != patchedDuration {
		t.Fatalf("concurrent session patches lost a field: %#v", patchedSession)
	}
}
