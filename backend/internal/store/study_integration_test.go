package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

// TestPostgresStudyPracticeOptIn verifies migration 027: rubric freezing,
// scenario rotation, attempt logging, and skill-state upserts, all scoped to
// the owning user.
func TestPostgresStudyPracticeOptIn(t *testing.T) {
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
		) VALUES ($1, 'Study integration', $2, 'pro', 1000, 1, 10)
	`, tenantID, "study-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO users (
			id, tenant_id, email, password_hash, name, role, is_active, email_verified
		) VALUES ($1,$2,$3,'integration-only','Study integration','user',true,true)
	`, userID, tenantID, "study-"+suffix+"@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `
		INSERT INTO ai_projects (
			id, tenant_id, user_id, name, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,'Study project','smart',64000)
	`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}

	skillKey := "识别相关关系"
	rubric := &models.StudyRubric{
		TenantID: tenantID, UserID: userID, ProjectID: projectID,
		SkillKey: skillKey, SkillLabel: "识别相关关系",
		Rubric: json.RawMessage(`{"levels":{"F":{"description":"核心判断错误"}}}`),
	}
	if err := postgresStore.CreateStudyRubric(t.Context(), rubric); err != nil {
		t.Fatalf("create rubric: %v", err)
	}
	// A second freeze attempt is a silent no-op; the stored copy wins.
	competing := *rubric
	competing.Rubric = json.RawMessage(`{"levels":{"F":{"description":"另一份"}}}`)
	if err := postgresStore.CreateStudyRubric(t.Context(), &competing); err != nil {
		t.Fatalf("competing rubric create: %v", err)
	}
	stored, err := postgresStore.GetStudyRubric(t.Context(), userID, projectID, skillKey)
	if err != nil || stored == nil {
		t.Fatalf("get rubric: %v / %v", stored, err)
	}
	if strings.Contains(string(stored.Rubric), "另一份") {
		t.Fatal("frozen rubric must not be overwritten")
	}
	if foreign, err := postgresStore.GetStudyRubric(
		t.Context(), uuid.NewString(), projectID, skillKey,
	); err != nil || foreign != nil {
		t.Fatalf("foreign-user rubric read = %v / %v, want nil", foreign, err)
	}

	scenarios := []*models.StudyScenario{}
	for difficulty := 1; difficulty <= 3; difficulty++ {
		scenarios = append(scenarios, &models.StudyScenario{
			TenantID: tenantID, UserID: userID, ProjectID: projectID,
			SkillKey: skillKey, SkillLabel: "识别相关关系",
			Difficulty: difficulty,
			Content:    json.RawMessage(`{"situation":"s","question":"q"}`),
		})
	}
	if err := postgresStore.CreateStudyScenarios(t.Context(), scenarios); err != nil {
		t.Fatalf("create scenarios: %v", err)
	}
	if scenarios[0].ID == "" {
		t.Fatal("scenario ids must be returned")
	}
	picked, err := postgresStore.PickStudyScenario(
		t.Context(), userID, projectID, skillKey, 2,
	)
	if err != nil || picked == nil || picked.Difficulty != 2 {
		t.Fatalf("pick difficulty 2: %+v / %v", picked, err)
	}
	if err := postgresStore.TouchStudyScenarioUse(t.Context(), picked.ID); err != nil {
		t.Fatalf("touch use: %v", err)
	}
	if loaded, err := postgresStore.GetStudyScenario(
		t.Context(), userID, projectID, picked.ID,
	); err != nil || loaded == nil || loaded.UsedCount != 1 {
		t.Fatalf("reload after touch: %+v / %v", loaded, err)
	}
	if none, err := postgresStore.PickStudyScenario(
		t.Context(), userID, projectID, "没有的能力", 1,
	); err != nil || none != nil {
		t.Fatalf("empty bank pick = %+v / %v, want nil", none, err)
	}

	scenarioID := picked.ID
	attempt := &models.StudyAttempt{
		TenantID: tenantID, UserID: userID, ProjectID: projectID,
		ScenarioID: &scenarioID, SkillKey: skillKey,
		Answer: "Correlation does not establish causation.",
		Grade:  "C", Feedback: "核心判断对了", NextStep: "补上混淆变量",
		Bonuses: json.RawMessage(`["no_hint"]`), XP: 130,
	}
	if err := postgresStore.CreateStudyAttempt(t.Context(), attempt); err != nil {
		t.Fatalf("create attempt: %v", err)
	}
	if attempt.ID == "" || attempt.CreatedAt.IsZero() {
		t.Fatalf("attempt id/created_at not returned: %+v", attempt)
	}

	state := &models.StudySkillState{
		UserID: userID, ProjectID: projectID, SkillKey: skillKey,
		TenantID: tenantID, SkillLabel: "识别相关关系",
		Level: "learner", XPTotal: 130, AttemptsCount: 1, CleanStreak: 1,
		LastGrade: "C",
	}
	if err := postgresStore.UpsertStudySkillState(t.Context(), state); err != nil {
		t.Fatalf("insert state: %v", err)
	}
	state.Level = "supervised"
	state.CleanStreak = 0
	state.XPTotal = 260
	state.AttemptsCount = 2
	if err := postgresStore.UpsertStudySkillState(t.Context(), state); err != nil {
		t.Fatalf("update state: %v", err)
	}
	states, err := postgresStore.ListStudySkillStates(t.Context(), userID, projectID)
	if err != nil || len(states) != 1 {
		t.Fatalf("list states: %+v / %v", states, err)
	}
	if states[0].Level != "supervised" || states[0].XPTotal != 260 ||
		states[0].AttemptsCount != 2 {
		t.Fatalf("state upsert lost fields: %+v", states[0])
	}
	if one, err := postgresStore.GetStudySkillState(
		t.Context(), userID, projectID, skillKey,
	); err != nil || one == nil || one.Level != "supervised" {
		t.Fatalf("get state: %+v / %v", one, err)
	}
}
