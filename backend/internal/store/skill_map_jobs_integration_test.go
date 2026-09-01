package store

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

// TestPostgresSkillMapJobLifecycle verifies migration 028: enqueue, claim,
// lease renewal, and learner cancellation of an in-flight generation.
func TestPostgresSkillMapJobLifecycle(t *testing.T) {
	databaseURL := os.Getenv("DREAMTRANS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("DREAMTRANS_TEST_DATABASE_URL is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.PingContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	postgresStore := &PostgresStore{db: db}

	tenantID := uuid.NewString()
	userID := uuid.NewString()
	projectID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO tenants (
			id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions
		) VALUES ($1, 'Skill map jobs', $2, 'pro', 1000, 1, 10)
	`, tenantID, "smj-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO users (
			id, tenant_id, email, password_hash, name, role, is_active, email_verified
		) VALUES ($1,$2,$3,'integration-only','Skill map jobs','user',true,true)
	`, userID, tenantID, "smj-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO ai_projects (
			id, tenant_id, user_id, name, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,'Skill map project','smart',64000)
	`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}

	hash := strings.Repeat("ab", 32)
	job := &models.SkillMapJob{
		TenantID: tenantID, UserID: userID, ProjectID: projectID,
		RequestHash: hash, ClientRequestID: "req-" + suffix,
	}
	created, err := postgresStore.CreateSkillMapJob(t.Context(), job)
	if err != nil || !created {
		t.Fatalf("create job: created=%v err=%v", created, err)
	}
	// Same client request replays instead of enqueueing twice.
	replay := &models.SkillMapJob{
		TenantID: tenantID, UserID: userID, ProjectID: projectID,
		RequestHash: hash, ClientRequestID: "req-" + suffix,
	}
	created, err = postgresStore.CreateSkillMapJob(t.Context(), replay)
	if err != nil || created || replay.ID != job.ID {
		t.Fatalf("replay: created=%v id=%s err=%v", created, replay.ID, err)
	}

	claimed, err := postgresStore.ClaimSkillMapJobs(t.Context(), "worker-a", 1, time.Minute)
	if err != nil || len(claimed) != 1 || claimed[0].ID != job.ID {
		t.Fatalf("claim: %+v err=%v", claimed, err)
	}
	owner := claimed[0].LeaseOwner
	if ok, err := postgresStore.RenewSkillMapJobLease(t.Context(), job.ID, owner, time.Minute); err != nil || !ok {
		t.Fatalf("renew while processing: ok=%v err=%v", ok, err)
	}
	active, err := postgresStore.GetActiveSkillMapJob(t.Context(), userID, projectID)
	if err != nil || active == nil || active.Status != "processing" {
		t.Fatalf("active: %+v err=%v", active, err)
	}

	// The learner cancels: the job leaves the active set, the worker's next
	// renewal fails so it aborts, and its completion is rejected as lease lost.
	cancelled, err := postgresStore.CancelSkillMapJobs(t.Context(), userID, projectID)
	if err != nil || cancelled != 1 {
		t.Fatalf("cancel: n=%d err=%v", cancelled, err)
	}
	if ok, err := postgresStore.RenewSkillMapJobLease(t.Context(), job.ID, owner, time.Minute); err != nil || ok {
		t.Fatalf("renew after cancel should fail: ok=%v err=%v", ok, err)
	}
	if err := postgresStore.CompleteSkillMapJob(t.Context(), job.ID, owner); err != ErrLeaseLost {
		t.Fatalf("complete after cancel: err=%v, want ErrLeaseLost", err)
	}
	if active, err := postgresStore.GetActiveSkillMapJob(t.Context(), userID, projectID); err != nil || active != nil {
		t.Fatalf("active after cancel: %+v err=%v", active, err)
	}
	latest, err := postgresStore.GetLatestSkillMapJob(t.Context(), userID, projectID)
	if err != nil || latest == nil || latest.Status != "cancelled" {
		t.Fatalf("latest: %+v err=%v", latest, err)
	}
	if again, err := postgresStore.ClaimSkillMapJobs(t.Context(), "worker-b", 1, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("cancelled job must not be claimable: %+v err=%v", again, err)
	}

	// Canceling nothing is a no-op, and a fresh request can be queued again.
	if n, err := postgresStore.CancelSkillMapJobs(t.Context(), userID, projectID); err != nil || n != 0 {
		t.Fatalf("cancel noop: n=%d err=%v", n, err)
	}
	next := &models.SkillMapJob{
		TenantID: tenantID, UserID: userID, ProjectID: projectID,
		RequestHash: hash, ClientRequestID: "req2-" + suffix,
	}
	if created, err := postgresStore.CreateSkillMapJob(t.Context(), next); err != nil || !created {
		t.Fatalf("re-enqueue: created=%v err=%v", created, err)
	}
}
