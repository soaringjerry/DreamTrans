package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

// TestTimetableFilesSessionsEndToEnd runs the matcher against Postgres: slot
// endpoints, filing on create and on completion, and the classify preview /
// apply pass that never touches a manual link.
func TestTimetableFilesSessionsEndToEnd(t *testing.T) {
	authHandler, _, db := verificationIntegrationSetup(t)
	h := &RAGHandler{store: authHandler.store}
	sessions := NewSessionHandler(authHandler.store)
	userID, tenantID := uuid.NewString(), uuid.NewString()
	courseA, courseB := uuid.NewString(), uuid.NewString()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO tenants(id,name,slug) VALUES($1,'Timetable',$2)`, tenantID, "timetable-"+tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID) })
	if _, err := db.ExecContext(t.Context(), `INSERT INTO users(id,tenant_id,email,password_hash,name) VALUES($1,$2,$3,'unused','Timetable')`, userID, tenantID, userID+"@example.test"); err != nil {
		t.Fatal(err)
	}
	for id, name := range map[string]string{courseA: "Course A", courseB: "Course B"} {
		if _, err := db.ExecContext(t.Context(), `INSERT INTO ai_projects(id,tenant_id,user_id,name) VALUES($1,$2,$3,$4)`, id, tenantID, userID, name); err != nil {
			t.Fatal(err)
		}
	}
	claims := &auth.UserClaims{UserID: userID, TenantID: tenantID}
	call := func(handler http.HandlerFunc, method, path string, body any) *httptest.ResponseRecorder {
		var reader *bytes.Reader
		if body != nil {
			encoded, _ := json.Marshal(body)
			reader = bytes.NewReader(encoded)
		} else {
			reader = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, path, reader)
		r = r.WithContext(context.WithValue(r.Context(), auth.UserClaimsKey, claims))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}

	// Slots via the project route: Course A Monday 10–12, Course B Monday 12–13.
	for _, slot := range []map[string]any{
		{"project": courseA, "weekday": 1, "start": "10:00", "end": "12:00", "timezone": "Australia/Melbourne", "label": "Lecture"},
		{"project": courseB, "weekday": 1, "start": "12:00", "end": "13:00", "timezone": "Australia/Melbourne"},
	} {
		w := call(h.HandleProjects, http.MethodPost, "/api/ai/projects/"+slot["project"].(string)+"/timetable", slot)
		if w.Code != http.StatusCreated {
			t.Fatalf("add slot: %d %s", w.Code, w.Body.String())
		}
	}
	if w := call(h.HandleProjects, http.MethodPost, "/api/ai/projects/"+courseA+"/timetable", map[string]any{"weekday": 9, "start": "10:00", "end": "12:00", "timezone": "UTC"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad weekday: %d", w.Code)
	}
	w := call(h.HandleTimetable, http.MethodGet, "/api/ai/timetable", nil)
	var listed struct {
		Slots []models.CourseSlot `json:"slots"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed.Slots) != 2 || listed.Slots[0].ProjectName != "Course A" {
		t.Fatalf("user timetable = %s (%v)", w.Body.String(), err)
	}
	slotA := listed.Slots[0].ID

	// A session created at Monday 10:03 Melbourne is filed under Course A at once.
	melbourne, _ := time.LoadLocation("Australia/Melbourne")
	mondayStart := time.Date(2026, 3, 2, 10, 3, 0, 0, melbourne)
	createAt := func(startedAt time.Time) string {
		var id string
		if err := db.QueryRowContext(t.Context(), `
			INSERT INTO sessions (user_id, tenant_id, title, status, started_at) VALUES ($1,$2,'Class','active',$3) RETURNING id
		`, userID, tenantID, startedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	created := &models.Session{ID: createAt(mondayStart), UserID: userID, TenantID: tenantID, StartedAt: mondayStart}
	fileSessionByTimetable(t.Context(), authHandler.store, created)
	if created.ProjectID == nil || *created.ProjectID != courseA {
		t.Fatalf("created session not filed under Course A: %v", created.ProjectID)
	}
	// Started at 11:45, fifteen minutes before Course B: by start time alone
	// the nearer class wins. It then ran only twenty minutes, mostly inside
	// Course A's lecture, so completion re-files it by overlap.
	lateStart := time.Date(2026, 3, 2, 11, 45, 0, 0, melbourne)
	late := &models.Session{ID: createAt(lateStart), UserID: userID, TenantID: tenantID, StartedAt: lateStart}
	fileSessionByTimetable(t.Context(), authHandler.store, late)
	if late.ProjectID == nil || *late.ProjectID != courseB {
		t.Fatalf("start-only filing should pick the nearer Course B, got %v", late.ProjectID)
	}
	w = call(sessions.HandleUpdateSession, http.MethodPatch, "/api/sessions/"+late.ID, map[string]any{"status": "completed", "duration_seconds": 20 * 60})
	if w.Code != http.StatusOK {
		t.Fatalf("complete: %d %s", w.Code, w.Body.String())
	}
	var linked string
	if err := db.QueryRowContext(t.Context(), `SELECT project_id FROM project_sessions WHERE session_id=$1`, late.ID).Scan(&linked); err != nil || linked != courseA {
		t.Fatalf("completion did not move to Course A: %s %v", linked, err)
	}

	// Classify: an old unfiled lecture is proposed; a manual link is never listed.
	old := createAt(time.Date(2026, 2, 23, 10, 0, 0, 0, melbourne))
	if _, err := db.ExecContext(t.Context(), `UPDATE sessions SET duration_seconds=6000, status='completed' WHERE id=$1`, old); err != nil {
		t.Fatal(err)
	}
	manual := createAt(time.Date(2026, 2, 16, 10, 0, 0, 0, melbourne))
	if err := authHandler.store.LinkProjectSession(t.Context(), courseB, manual, userID); err != nil {
		t.Fatal(err)
	}
	createAt(time.Date(2026, 3, 2, 19, 0, 0, 0, melbourne)) // an evening recording no class claims
	w = call(h.HandleTimetable, http.MethodPost, "/api/ai/timetable/classify", map[string]any{"apply": false})
	var preview timetableClassifyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if !preview.Preview || preview.Applied != 0 || len(preview.Assignments) != 1 || preview.Assignments[0].SessionID != old ||
		preview.Assignments[0].ProjectID != courseA || preview.Assignments[0].SlotID != slotA || preview.Assignments[0].Change != "assign" ||
		preview.Assignments[0].OverlapMinutes != 100 {
		t.Fatalf("preview = %+v", preview)
	}
	if preview.Kept != 2 || preview.Unmatched != 1 || preview.Scanned != 4 {
		t.Fatalf("counts = kept %d unmatched %d scanned %d", preview.Kept, preview.Unmatched, preview.Scanned)
	}
	var stillUnfiled int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM project_sessions WHERE session_id=$1`, old).Scan(&stillUnfiled); err != nil || stillUnfiled != 0 {
		t.Fatalf("preview wrote a link: %d %v", stillUnfiled, err)
	}
	w = call(h.HandleTimetable, http.MethodPost, "/api/ai/timetable/classify", map[string]any{"apply": true})
	var applied timetableClassifyResponse
	if err := json.Unmarshal(w.Body.Bytes(), &applied); err != nil || applied.Applied != 1 || applied.Preview {
		t.Fatalf("apply = %d %s", w.Code, w.Body.String())
	}
	var assignedBy string
	if err := db.QueryRowContext(t.Context(), `SELECT project_id, assigned_by FROM project_sessions WHERE session_id=$1`, old).Scan(&linked, &assignedBy); err != nil || linked != courseA || assignedBy != "timetable" {
		t.Fatalf("old lecture link = %s %s %v", linked, assignedBy, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT project_id, assigned_by FROM project_sessions WHERE session_id=$1`, manual).Scan(&linked, &assignedBy); err != nil || linked != courseB || assignedBy != "manual" {
		t.Fatalf("manual link touched: %s %s %v", linked, assignedBy, err)
	}
	// Removing the slot: 404 for a stranger's id, 200 for the owner.
	if w := call(h.HandleProjects, http.MethodDelete, "/api/ai/projects/"+courseB+"/timetable/"+slotA, nil); w.Code != http.StatusNotFound {
		t.Fatalf("delete through wrong course: %d", w.Code)
	}
	if w := call(h.HandleProjects, http.MethodDelete, "/api/ai/projects/"+courseA+"/timetable/"+slotA, nil); w.Code != http.StatusOK {
		t.Fatalf("delete slot: %d %s", w.Code, w.Body.String())
	}
	var remaining sql.NullString
	if err := db.QueryRowContext(t.Context(), `SELECT slot_id FROM project_sessions WHERE session_id=$1`, old).Scan(&remaining); err != nil || remaining.Valid {
		t.Fatalf("slot reference after delete: %v %v", remaining, err)
	}
}
