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

// TestPostgresConceptMapArtifactsOptIn verifies migration 024 admits the
// concept_map artifact type and exercises the project-scoped queries the
// concept map endpoints depend on.
func TestPostgresConceptMapArtifactsOptIn(t *testing.T) {
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
	otherUserID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO tenants (
			id, name, slug, plan, api_quota_monthly, storage_quota_gb,
			max_sessions
		) VALUES ($1, 'Concept map integration', $2, 'pro', 1000, 1, 10)
	`, tenantID, "concept-map-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `
			DELETE FROM tenants WHERE id=$1
		`, tenantID)
	})
	for index, id := range []string{userID, otherUserID} {
		if _, err := db.ExecContext(t.Context(), `
			INSERT INTO users (
				id, tenant_id, email, password_hash, name, role, is_active,
				email_verified
			) VALUES ($1, $2, $3, 'x', 'Concept Map', 'user', true, true)
		`, id, tenantID, "concept-map-"+suffix+"-"+string(rune('a'+index))+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}

	createSession := func(title string, startedAt time.Time) string {
		var id string
		if err := db.QueryRowContext(t.Context(), `
			INSERT INTO sessions (user_id, tenant_id, title, source_language,
			                      target_language, status, started_at)
			VALUES ($1, $2, $3, 'en', 'zh', 'completed', $4)
			RETURNING id
		`, userID, tenantID, title, startedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	base := time.Now().UTC().Add(-48 * time.Hour)
	secondSessionID := createSession("第二课", base.Add(24*time.Hour))
	firstSessionID := createSession("第一课", base)
	createSession("未关联的课", base.Add(2*time.Hour))

	projectID := uuid.NewString()
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO ai_projects (
			id, tenant_id, user_id, name, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,'Concept map project','smart',64000)
	`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	for _, sessionID := range []string{secondSessionID, firstSessionID} {
		if _, err := db.ExecContext(t.Context(), `
			INSERT INTO project_sessions (project_id, session_id)
			VALUES ($1, $2)
		`, projectID, sessionID); err != nil {
			t.Fatal(err)
		}
	}

	refs, err := postgresStore.ListProjectSessionRefs(
		t.Context(), tenantID, userID, projectID, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 {
		t.Fatalf("linked sessions = %d, want 2", len(refs))
	}
	if refs[0].ID != firstSessionID || refs[1].ID != secondSessionID {
		t.Fatalf("sessions not in chronological order: %+v", refs)
	}
	if refs[0].Title != "第一课" {
		t.Fatalf("title = %q, want 第一课", refs[0].Title)
	}
	foreignRefs, err := postgresStore.ListProjectSessionRefs(
		t.Context(), tenantID, otherUserID, projectID, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(foreignRefs) != 0 {
		t.Fatalf("another user must not see project sessions, got %d", len(foreignRefs))
	}

	makeArtifact := func(clientRequestID string) *models.AIArtifact {
		id := projectID
		return &models.AIArtifact{
			ID: uuid.NewString(), TenantID: tenantID, UserID: userID,
			ProjectID: &id, ArtifactType: "concept_map", Title: "知识地图",
			Content:         `{"version":1,"topics":[{"id":"t1","label":"主题","children":[]}],"links":[]}`,
			ContextPolicy:   map[string]any{"mode": "project_transcripts"},
			ClientRequestID: clientRequestID,
			RequestHash:     strings.Repeat("a", 64),
		}
	}
	older := makeArtifact("concept-map-" + suffix + "-1")
	if err := postgresStore.CreateAIArtifact(t.Context(), older); err != nil {
		t.Fatalf("migration 024 must admit concept_map artifacts: %v", err)
	}
	// created_at is server-assigned; force distinct ordering.
	if _, err := db.ExecContext(t.Context(), `
		UPDATE ai_artifacts SET created_at = created_at - INTERVAL '1 hour'
		WHERE id=$1
	`, older.ID); err != nil {
		t.Fatal(err)
	}
	newer := makeArtifact("concept-map-" + suffix + "-2")
	if err := postgresStore.CreateAIArtifact(t.Context(), newer); err != nil {
		t.Fatal(err)
	}

	latest, err := postgresStore.GetLatestAIArtifactByProject(
		t.Context(), userID, projectID, "concept_map",
	)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != newer.ID {
		t.Fatalf("latest = %+v, want id %s", latest, newer.ID)
	}
	if latest.ContextPolicy["mode"] != "project_transcripts" {
		t.Fatalf("context policy not round-tripped: %+v", latest.ContextPolicy)
	}

	removed, err := postgresStore.DeleteAIArtifactsByProjectAndTypeExcept(
		t.Context(), userID, projectID, "concept_map", newer.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	latest, err = postgresStore.GetLatestAIArtifactByProject(
		t.Context(), userID, projectID, "concept_map",
	)
	if err != nil {
		t.Fatal(err)
	}
	if latest == nil || latest.ID != newer.ID {
		t.Fatal("kept artifact must survive superseded cleanup")
	}
}
