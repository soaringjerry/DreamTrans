package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

// TestPostgresAIGenerationIdempotencyOptIn exercises the durable provider-call
// fence against the same migrated PostgreSQL instance used by Linux CI.
func TestPostgresAIGenerationIdempotencyOptIn(t *testing.T) {
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

	tenantID := uuid.NewString()
	userID := uuid.NewString()
	sessionID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO tenants (
			id, name, slug, plan, api_quota_monthly, storage_quota_gb,
			max_sessions
		) VALUES ($1, 'Generation integration', $2, 'pro', 1000, 1, 10)
	`, tenantID, "generation-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `
			DELETE FROM tenants WHERE id=$1
		`, tenantID)
	})
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO users (
			id, tenant_id, email, password_hash, name, role, is_active,
			email_verified
		) VALUES (
			$1,$2,$3,'integration-only','Generation integration',
			'user',true,true
		)
	`, userID, tenantID, "generation-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO sessions (
			id, user_id, tenant_id, title, source_language,
			target_language, status
		) VALUES ($1,$2,$3,'Generation session','en','zh','active')
	`, sessionID, userID, tenantID); err != nil {
		t.Fatal(err)
	}

	requestID := uuid.NewString()
	requestHash := strings.Repeat("a", 64)
	leaseOwner := "generation-" + uuid.NewString()
	request := models.AIGenerationRequest{
		TenantID:        tenantID,
		UserID:          userID,
		SessionID:       &sessionID,
		ClientRequestID: requestID,
		RequestKind:     "chat",
		RequestHash:     requestHash,
		LeaseOwner:      leaseOwner,
	}
	begin, err := postgresStore.BeginAIGenerationRequest(
		t.Context(), &request, time.Minute,
	)
	if err != nil {
		t.Fatalf("begin generation: %v", err)
	}
	if begin.Outcome != models.AIGenerationOutcomeAcquired || !begin.Created {
		t.Fatalf("first begin result = %#v", begin)
	}

	inProgressRequest := models.AIGenerationRequest{
		TenantID:        tenantID,
		UserID:          userID,
		SessionID:       &sessionID,
		ClientRequestID: requestID,
		RequestKind:     "chat",
		RequestHash:     requestHash,
		LeaseOwner:      "generation-" + uuid.NewString(),
	}
	inProgress, err := postgresStore.BeginAIGenerationRequest(
		t.Context(), &inProgressRequest, time.Minute,
	)
	if err != nil {
		t.Fatalf("begin duplicate in-flight generation: %v", err)
	}
	if inProgress.Outcome != models.AIGenerationOutcomeInProgress ||
		inProgress.Created {
		t.Fatalf("in-flight begin result = %#v", inProgress)
	}

	if err := postgresStore.CompleteAIGenerationRequest(
		t.Context(), request.ID, tenantID, userID, "wrong-owner",
		json.RawMessage(`{"answer":"must not win"}`),
	); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("wrong-owner completion error = %v, want ErrLeaseLost", err)
	}
	if err := postgresStore.CompleteAIGenerationRequest(
		t.Context(), request.ID, tenantID, userID, leaseOwner,
		json.RawMessage(`{"answer":"ok"}`),
	); err != nil {
		t.Fatalf("complete generation: %v", err)
	}

	replayRequest := models.AIGenerationRequest{
		TenantID:        tenantID,
		UserID:          userID,
		SessionID:       &sessionID,
		ClientRequestID: requestID,
		RequestKind:     "chat",
		RequestHash:     requestHash,
		LeaseOwner:      "generation-" + uuid.NewString(),
	}
	replay, err := postgresStore.BeginAIGenerationRequest(
		t.Context(), &replayRequest, time.Minute,
	)
	if err != nil {
		t.Fatalf("begin completed generation replay: %v", err)
	}
	if replay.Outcome != models.AIGenerationOutcomeReplay ||
		!json.Valid(replay.Request.ResponseJSON) {
		t.Fatalf("ready replay result = %#v", replay)
	}

	conflictRequest := models.AIGenerationRequest{
		TenantID:        tenantID,
		UserID:          userID,
		SessionID:       &sessionID,
		ClientRequestID: requestID,
		RequestKind:     "chat",
		RequestHash:     strings.Repeat("b", 64),
		LeaseOwner:      "generation-" + uuid.NewString(),
	}
	conflict, err := postgresStore.BeginAIGenerationRequest(
		t.Context(), &conflictRequest, time.Minute,
	)
	if err != nil {
		t.Fatalf("begin conflicting generation: %v", err)
	}
	if conflict.Outcome != models.AIGenerationOutcomeHashConflict {
		t.Fatalf("conflicting begin result = %#v", conflict)
	}

	if _, err := db.ExecContext(t.Context(), `
		UPDATE ai_generation_requests
		SET expires_at=NOW() - INTERVAL '1 second'
		WHERE id=$1
	`, request.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgresStore.PruneExpiredAIGenerationRequests(
		t.Context(),
	); err != nil {
		t.Fatalf("prune expired generation requests: %v", err)
	}
	var remaining int
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM ai_generation_requests WHERE id=$1
	`, request.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("expired generation request remained: count=%d", remaining)
	}

	cascadeRequest := models.AIGenerationRequest{
		TenantID:        tenantID,
		UserID:          userID,
		SessionID:       &sessionID,
		ClientRequestID: uuid.NewString(),
		RequestKind:     "chat",
		RequestHash:     strings.Repeat("c", 64),
		LeaseOwner:      "generation-" + uuid.NewString(),
	}
	cascadeBegin, err := postgresStore.BeginAIGenerationRequest(
		t.Context(), &cascadeRequest, time.Minute,
	)
	if err != nil ||
		cascadeBegin.Outcome != models.AIGenerationOutcomeAcquired {
		t.Fatalf("begin session-scoped request: result=%#v err=%v", cascadeBegin, err)
	}
	if _, err := db.ExecContext(t.Context(), `
		DELETE FROM sessions WHERE id=$1
	`, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM ai_generation_requests WHERE id=$1
	`, cascadeRequest.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("session-scoped request survived session deletion: count=%d", remaining)
	}

	artifactRequest := models.AIGenerationRequest{
		TenantID:        tenantID,
		UserID:          userID,
		ClientRequestID: uuid.NewString(),
		RequestKind:     "artifact",
		RequestHash:     strings.Repeat("d", 64),
		LeaseOwner:      "generation-" + uuid.NewString(),
	}
	artifactBegin, err := postgresStore.BeginAIGenerationRequest(
		t.Context(), &artifactRequest, time.Minute,
	)
	if err != nil ||
		artifactBegin.Outcome != models.AIGenerationOutcomeAcquired {
		t.Fatalf("begin artifact reservation: result=%#v err=%v", artifactBegin, err)
	}
	if err := postgresStore.ReleaseAIGenerationRequest(
		t.Context(), artifactRequest.ID, tenantID, userID,
		artifactRequest.LeaseOwner,
	); err != nil {
		t.Fatalf("release artifact reservation: %v", err)
	}
	if err := db.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM ai_generation_requests WHERE id=$1
	`, artifactRequest.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("released artifact reservation remained: count=%d", remaining)
	}
}
