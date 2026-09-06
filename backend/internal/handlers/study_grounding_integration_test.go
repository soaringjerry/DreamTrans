package handlers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/google/uuid"
)

func TestStudyMaterialsReachTeachingAndInvalidateMap(t *testing.T) {
	authHandler, _, db := verificationIntegrationSetup(t)
	h := &RAGHandler{store: authHandler.store}
	userID, tenantID, projectID, sourceID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id,name,slug) VALUES($1,'study',$2)`, tenantID, "study-"+tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) })
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users(id,tenant_id,email,password_hash,name) VALUES($1,$2,$3,'unused','Study')`, userID, tenantID, userID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO ai_projects(id,tenant_id,user_id,name) VALUES($1,$2,$3,'Course')`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	project := &models.AIProject{ID: projectID, UserID: userID, TenantID: tenantID}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO knowledge_sources(id,project_id,tenant_id,user_id,source_type,name,status) VALUES($1,$2,$3,$4,'file','Textbook','ready')`, sourceID, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	fullText := strings.Repeat("This is actual course-specific source material. ", 20) + "EXACT_COURSE_DEFINITION"
	if _, err := db.ExecContext(t.Context(), `INSERT INTO knowledge_chunks(source_id,project_id,ordinal,content,vector) VALUES($1,$2,0,$3,'{}')`, sourceID, projectID, fullText); err != nil {
		t.Fatal(err)
	}
	fingerprint, _, err := h.store.StudyMaterialFingerprint(t.Context(), projectID, userID)
	if err != nil {
		t.Fatal(err)
	}
	skill := skillMapSkill{ID: "skill-1", Label: "Course definitions", Summary: "A short summary", Evidence: []skillMapEvidence{{SourceID: sourceID, Quote: "short quote"}}}
	doc := skillMapDocument{Version: 1, GeneratedAt: time.Now(), MaterialFingerprint: fingerprint, Skills: []skillMapSkill{skill}}
	content, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	artifact := &models.AIArtifact{TenantID: tenantID, UserID: userID, ProjectID: &projectID, ArtifactType: skillMapArtifactType, Title: "Map", Content: string(content)}
	if err := h.store.CreateAIArtifact(t.Context(), artifact); err != nil {
		t.Fatal(err)
	}
	ctx, err := h.studyVersionContext(t.Context(), project)
	if err != nil || store.StudyContentVersion(ctx) != artifact.ID {
		t.Fatalf("version context: %v %v", store.StudyContentVersion(ctx), err)
	}
	grounded, err := h.groundedStudyContext(ctx, project, &skill, studyLessonContext(&skill, nil))
	if err != nil || !strings.Contains(grounded, "EXACT_COURSE_DEFINITION") || !strings.Contains(grounded, "Textbook") {
		t.Fatalf("source missing: %q %v", grounded, err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE knowledge_chunks SET content='updated definition' WHERE source_id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.studyVersionContext(t.Context(), project); err == nil {
		t.Fatal("stale map accepted for new paid generation")
	}
	if _, err := h.groundedStudyContext(ctx, project, &skill, "old snapshot"); err == nil {
		t.Fatal("materials changed during retrieval but generation allowed")
	}
	w := httptest.NewRecorder()
	h.writeSkillMapPayload(t.Context(), w, project, nil, false)
	var payload struct {
		Stale bool `json:"stale"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil || !payload.Stale {
		t.Fatalf("missing stale marker: %s %v", w.Body.String(), err)
	}
}
