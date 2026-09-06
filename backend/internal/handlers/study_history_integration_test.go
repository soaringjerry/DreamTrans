package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/google/uuid"
)

func TestStudyHistoryReadOnlyPaginationAndOwnership(t *testing.T) {
	authHandler, _, db := verificationIntegrationSetup(t)
	// No AI service: review must work with no generation, including old maps.
	h := &RAGHandler{store: authHandler.store}
	userID, tenantID, projectID, scenarioID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id,name,slug) VALUES($1,'History',$2)`, tenantID, "history-"+tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) })
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users(id,tenant_id,email,password_hash,name) VALUES($1,$2,$3,'unused','History')`, userID, tenantID, userID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO ai_projects(id,tenant_id,user_id,name) VALUES($1,$2,$3,'Course')`, projectID, tenantID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO study_scenarios(id,tenant_id,user_id,project_id,skill_key,skill_label,content,status,content_version)
		VALUES($1,$2,$3,$4,'skill','Old skill','{"question":"Old question","format":"single","options":["A","B"],"answer_indexes":[1],"c_anchor":"Old answer","explanation":"Old explanation"}','retired','old-map')`, scenarioID, tenantID, userID, projectID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO study_attempts(tenant_id,user_id,project_id,scenario_id,skill_key,answer,grade,feedback,xp)
		SELECT $1,$2,$3,$4,'skill','My saved answer','C','Saved feedback',100 FROM generate_series(1,52)`, tenantID, userID, projectID, scenarioID); err != nil {
		t.Fatal(err)
	}
	request := func(owner, cursor string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/ai/projects/"+projectID+"/study/attempts?before="+cursor, nil)
		if owner != "" {
			r = r.WithContext(context.WithValue(r.Context(), auth.UserClaimsKey, &auth.UserClaims{UserID: owner, TenantID: tenantID}))
		}
		w := httptest.NewRecorder()
		h.HandleProjects(w, r)
		return w
	}
	type page struct {
		Items      []studyHistoryItem `json:"items"`
		NextCursor string             `json:"next_cursor"`
	}
	read := func(cursor string) page {
		w := request(userID, cursor)
		var result page
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("history response: %d %s", w.Code, w.Body.String())
		}
		return result
	}
	first := read("")
	if len(first.Items) != 50 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	seen := make(map[string]bool)
	for _, item := range first.Items {
		seen[item.ID] = true
		if item.Answer != "My saved answer" || item.Feedback != "Saved feedback" || item.Reveal == nil || item.Reveal.ModelAnswer != "Old answer" || len(item.Reveal.AnswerIndexes) != 1 {
			t.Fatalf("missing original answer or explanation: %+v", item)
		}
	}
	second := read(first.NextCursor)
	if len(second.Items) != 2 || second.NextCursor != "" {
		t.Fatalf("second page: %+v", second)
	}
	for _, item := range second.Items {
		if seen[item.ID] {
			t.Fatal("pagination repeated an attempt with the same timestamp")
		}
	}
	if request("", "").Code != http.StatusUnauthorized || request(uuid.NewString(), "").Code != http.StatusNotFound {
		t.Fatal("history was accessible outside the owning account")
	}
	if request(userID, "not-a-uuid").Code != http.StatusBadRequest {
		t.Fatal("invalid cursor accepted")
	}
	if len(read(uuid.NewString()).Items) != 0 {
		t.Fatal("unknown cursor returned unrelated records")
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM study_scenarios WHERE id=$1`, scenarioID); err != nil {
		t.Fatal(err)
	}
	deleted := read("")
	if len(deleted.Items) != 50 || string(deleted.Items[0].Scenario) != "null" || deleted.Items[0].Answer != "My saved answer" {
		t.Fatal("deleting a scenario hid the saved answer")
	}
	var totalXP, attempts int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*), SUM(xp) FROM study_attempts WHERE project_id=$1`, projectID).Scan(&attempts, &totalXP); err != nil || attempts != 52 || totalXP != 5200 {
		t.Fatalf("review changed attempts or XP: %d %d %v", attempts, totalXP, err)
	}
}
