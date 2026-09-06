package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
)

// TestPostgresCourseTimetable covers migration 042: slot CRUD scoped to the
// owner, and the rule that the timetable may file or move only its own
// links while a manual link stands.
func TestPostgresCourseTimetable(t *testing.T) {
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
	ctx := t.Context()

	tenantID := uuid.NewString()
	userID := uuid.NewString()
	otherUserID := uuid.NewString()
	suffix := strings.ReplaceAll(tenantID, "-", "")
	if _, err := db.ExecContext(ctx, `
		INSERT INTO tenants (id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
		VALUES ($1, 'Timetable integration', $2, 'pro', 1000, 1, 10)
	`, tenantID, "timetable-"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db.ExecContext(context.Background(), `DELETE FROM tenants WHERE id=$1`, tenantID)
	})
	for index, id := range []string{userID, otherUserID} {
		if _, err := db.ExecContext(ctx, `
			INSERT INTO users (id, tenant_id, email, password_hash, name, role, is_active, email_verified)
			VALUES ($1, $2, $3, 'x', 'Timetable', 'user', true, true)
		`, id, tenantID, "timetable-"+suffix+"-"+string(rune('a'+index))+"@example.com"); err != nil {
			t.Fatal(err)
		}
	}
	createProject := func(owner, name string) string {
		id := uuid.NewString()
		if _, err := db.ExecContext(ctx, `
			INSERT INTO ai_projects (id, tenant_id, user_id, name, context_mode, max_context_tokens)
			VALUES ($1,$2,$3,$4,'smart',64000)
		`, id, tenantID, owner, name); err != nil {
			t.Fatal(err)
		}
		return id
	}
	courseA := createProject(userID, "Course A")
	courseB := createProject(userID, "Course B")
	foreign := createProject(otherUserID, "Someone else's course")
	createSession := func(owner string, startedAt time.Time) string {
		var id string
		if err := db.QueryRowContext(ctx, `
			INSERT INTO sessions (user_id, tenant_id, title, status, started_at, duration_seconds)
			VALUES ($1, $2, 'Class', 'completed', $3, 3600) RETURNING id
		`, owner, tenantID, startedAt).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now().UTC()
	unfiled := createSession(userID, now.Add(-72*time.Hour))
	byHand := createSession(userID, now.Add(-48*time.Hour))
	byTimetable := createSession(userID, now.Add(-24*time.Hour))
	foreignSession := createSession(otherUserID, now)

	// Slot CRUD, owner-scoped.
	slot := models.CourseSlot{ProjectID: courseA, Weekday: 1, Start: "10:00", End: "12:00", Timezone: "Australia/Melbourne", Label: "Lecture"}
	if err := ValidateCourseSlot(&slot); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.CreateCourseSlot(ctx, userID, &slot); err != nil {
		t.Fatal(err)
	}
	if slot.ID == "" || slot.CreatedAt.IsZero() {
		t.Fatalf("slot not returned: %#v", slot)
	}
	slotB := models.CourseSlot{ProjectID: courseB, Weekday: 1, Start: "12:00", End: "13:00", Timezone: "Australia/Melbourne"}
	if err := ValidateCourseSlot(&slotB); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.CreateCourseSlot(ctx, userID, &slotB); err != nil {
		t.Fatal(err)
	}
	stolen := models.CourseSlot{ProjectID: foreign, Weekday: 1, Start: "10:00", End: "12:00", Timezone: "UTC"}
	if err := ValidateCourseSlot(&stolen); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.CreateCourseSlot(ctx, userID, &stolen); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("slot on another user's course: err = %v", err)
	}
	slots, err := postgresStore.ListCourseSlots(ctx, userID, courseA)
	if err != nil || len(slots) != 1 || slots[0].Start != "10:00" || slots[0].End != "12:00" || slots[0].Label != "Lecture" {
		t.Fatalf("ListCourseSlots = %#v, %v", slots, err)
	}
	if slots, err := postgresStore.ListCourseSlots(ctx, otherUserID, courseA); err != nil || len(slots) != 0 {
		t.Fatalf("other user sees slots: %#v, %v", slots, err)
	}
	all, err := postgresStore.ListCourseSlotsByUser(ctx, userID)
	if err != nil || len(all) != 2 || all[0].ProjectName != "Course A" || all[1].ProjectName != "Course B" {
		t.Fatalf("ListCourseSlotsByUser = %#v, %v", all, err)
	}
	starts, err := postgresStore.ProjectWeekStarts(ctx, userID)
	if err != nil || len(starts) != 2 || !starts[courseA].IsZero() {
		t.Fatalf("ProjectWeekStarts = %#v, %v", starts, err)
	}

	// Filing: manual link stands, timetable link may be made and moved.
	if err := postgresStore.LinkProjectSession(ctx, courseA, byHand, userID); err != nil {
		t.Fatal(err)
	}
	applied, err := postgresStore.ApplyTimetableAssignments(ctx, tenantID, userID, []TimetableAssignment{
		{SessionID: unfiled, ProjectID: courseA, SlotID: slot.ID},
		{SessionID: byHand, ProjectID: courseB, SlotID: slotB.ID},
		{SessionID: byTimetable, ProjectID: courseA, SlotID: slot.ID},
		{SessionID: foreignSession, ProjectID: courseA, SlotID: slot.ID},
		{SessionID: unfiled, ProjectID: foreign, SlotID: slot.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied != 2 {
		t.Fatalf("applied = %d, want 2 (unfiled + byTimetable)", applied)
	}
	linkOf := func(sessionID string) (project, assignedBy, slotID string) {
		var slotRef sql.NullString
		if err := db.QueryRowContext(ctx, `
			SELECT project_id, assigned_by, slot_id FROM project_sessions WHERE session_id=$1
		`, sessionID).Scan(&project, &assignedBy, &slotRef); err != nil {
			t.Fatal(err)
		}
		return project, assignedBy, slotRef.String
	}
	if project, by, slotID := linkOf(byHand); project != courseA || by != SessionLinkManual || slotID != "" {
		t.Fatalf("manual link changed: %s %s %s", project, by, slotID)
	}
	if project, by, slotID := linkOf(unfiled); project != courseA || by != SessionLinkTimetable || slotID != slot.ID {
		t.Fatalf("unfiled not filed: %s %s %s", project, by, slotID)
	}
	var foreignLinks int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM project_sessions WHERE session_id=$1`, foreignSession).Scan(&foreignLinks); err != nil {
		t.Fatal(err)
	}
	if foreignLinks != 0 {
		t.Fatal("another user's session was filed")
	}
	// Same assignment again is a no-op; a different course moves it.
	applied, err = postgresStore.ApplyTimetableAssignments(ctx, tenantID, userID, []TimetableAssignment{
		{SessionID: byTimetable, ProjectID: courseA, SlotID: slot.ID},
	})
	if err != nil || applied != 0 {
		t.Fatalf("repeat apply = %d, %v", applied, err)
	}
	applied, err = postgresStore.ApplyTimetableAssignments(ctx, tenantID, userID, []TimetableAssignment{
		{SessionID: byTimetable, ProjectID: courseB, SlotID: slotB.ID},
	})
	if err != nil || applied != 1 {
		t.Fatalf("move = %d, %v", applied, err)
	}
	if project, by, _ := linkOf(byTimetable); project != courseB || by != SessionLinkTimetable {
		t.Fatalf("timetable link not moved: %s %s", project, by)
	}
	// A person re-filing a timetable link makes it manual.
	if err := postgresStore.LinkProjectSession(ctx, courseA, byTimetable, userID); err != nil {
		t.Fatal(err)
	}
	if project, by, slotID := linkOf(byTimetable); project != courseA || by != SessionLinkManual || slotID != "" {
		t.Fatalf("manual relink: %s %s %s", project, by, slotID)
	}
	sessions, err := postgresStore.ListProjectSessions(ctx, tenantID, userID, courseA)
	if err != nil || len(sessions) != 3 {
		t.Fatalf("ListProjectSessions = %d, %v", len(sessions), err)
	}
	for _, session := range sessions {
		want := SessionLinkManual
		if session.ID == unfiled {
			want = SessionLinkTimetable
		}
		if session.AssignedBy != want {
			t.Fatalf("session %s assigned_by = %q, want %q", session.ID, session.AssignedBy, want)
		}
	}

	// Classifiable: unlinked or timetable-linked only.
	classifiable, err := postgresStore.ListClassifiableSessions(ctx, tenantID, userID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]ClassifiableSession)
	for _, session := range classifiable {
		ids[session.ID] = session
	}
	if _, ok := ids[byHand]; ok {
		t.Fatal("manual link listed as classifiable")
	}
	if got, ok := ids[unfiled]; !ok || got.ProjectID != courseA || got.SlotID != slot.ID {
		t.Fatalf("timetable link missing from classifiable: %#v", got)
	}

	// Deleting a slot keeps the session in the course.
	if err := postgresStore.DeleteCourseSlot(ctx, otherUserID, courseA, slot.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("other user deleted slot: %v", err)
	}
	if err := postgresStore.DeleteCourseSlot(ctx, userID, courseA, slot.ID); err != nil {
		t.Fatal(err)
	}
	if project, by, slotID := linkOf(unfiled); project != courseA || by != SessionLinkTimetable || slotID != "" {
		t.Fatalf("after slot delete: %s %s %s", project, by, slotID)
	}

	// The per-course cap.
	for i := 0; i < MaxCourseSlots-1; i++ {
		extra := models.CourseSlot{ProjectID: courseB, Weekday: 2, Start: "08:00", End: "09:00", Timezone: "UTC"}
		if err := ValidateCourseSlot(&extra); err != nil {
			t.Fatal(err)
		}
		if err := postgresStore.CreateCourseSlot(ctx, userID, &extra); err != nil {
			t.Fatalf("slot %d: %v", i, err)
		}
	}
	extra := models.CourseSlot{ProjectID: courseB, Weekday: 2, Start: "08:00", End: "09:00", Timezone: "UTC"}
	if err := ValidateCourseSlot(&extra); err != nil {
		t.Fatal(err)
	}
	if err := postgresStore.CreateCourseSlot(ctx, userID, &extra); !errors.Is(err, ErrTooManyCourseSlots) {
		t.Fatalf("cap not enforced: %v", err)
	}
}
