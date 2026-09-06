package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

// 课表归类: weekly class slots per course and the session links the
// timetable makes. A link written by a person ("manual") is never moved by
// the timetable; a link the timetable made may be revised by a later run.

const (
	SessionLinkManual    = "manual"
	SessionLinkTimetable = "timetable"

	// MaxCourseSlots bounds one course's timetable (lectures, tutorials,
	// labs, repeats).
	MaxCourseSlots = 20
	// TimetableClassifyLimit bounds one classification run over a user's
	// history; older sessions than that were long since filed by hand.
	TimetableClassifyLimit = 2000
)

var ErrTooManyCourseSlots = errors.New("course already has the maximum number of class times")

// FormatSlotMinute renders wall-clock minutes as "HH:MM"; 1440 is "24:00".
func FormatSlotMinute(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}

// ParseSlotMinute reads "HH:MM" (24-hour) into minutes since midnight;
// "24:00" is accepted as an end time.
func ParseSlotMinute(value string) (int, error) {
	value = strings.TrimSpace(value)
	var hour, minute int
	if _, err := fmt.Sscanf(value, "%d:%d", &hour, &minute); err != nil || len(value) != 5 || value[2] != ':' {
		return 0, fmt.Errorf("time must be HH:MM, got %q", value)
	}
	if hour < 0 || hour > 24 || minute < 0 || minute > 59 || (hour == 24 && minute != 0) {
		return 0, fmt.Errorf("time out of range: %q", value)
	}
	return hour*60 + minute, nil
}

// ValidateCourseSlot normalizes a slot: it fills the minute fields from the
// HH:MM strings, checks the weekday and the timezone name, and trims the
// label.
func ValidateCourseSlot(slot *models.CourseSlot) error {
	if slot.Weekday < 1 || slot.Weekday > 7 {
		return errors.New("weekday must be 1 (Monday) to 7 (Sunday)")
	}
	start, err := ParseSlotMinute(slot.Start)
	if err != nil {
		return err
	}
	end, err := ParseSlotMinute(slot.End)
	if err != nil {
		return err
	}
	if end <= start || start >= 1440 {
		return errors.New("end must be after start on the same day")
	}
	slot.StartMinute, slot.EndMinute = start, end
	slot.Start, slot.End = FormatSlotMinute(start), FormatSlotMinute(end)
	slot.Timezone = strings.TrimSpace(slot.Timezone)
	if slot.Timezone == "" || len(slot.Timezone) > 64 || strings.EqualFold(slot.Timezone, "Local") {
		return errors.New("timezone must be an IANA zone name")
	}
	if _, err := time.LoadLocation(slot.Timezone); err != nil {
		return fmt.Errorf("unknown timezone %q", slot.Timezone)
	}
	slot.Label = strings.TrimSpace(slot.Label)
	if len([]rune(slot.Label)) > 60 {
		return errors.New("label is too long")
	}
	return nil
}

func scanCourseSlot(scanner interface{ Scan(dest ...any) error }, withProjectName bool) (models.CourseSlot, error) {
	var slot models.CourseSlot
	dest := []any{
		&slot.ID, &slot.ProjectID, &slot.Weekday, &slot.StartMinute, &slot.EndMinute,
		&slot.Timezone, &slot.Label, &slot.CreatedAt,
	}
	if withProjectName {
		dest = append(dest, &slot.ProjectName)
	}
	if err := scanner.Scan(dest...); err != nil {
		return slot, err
	}
	slot.Start = FormatSlotMinute(slot.StartMinute)
	slot.End = FormatSlotMinute(slot.EndMinute)
	return slot, nil
}

// CreateCourseSlot adds a class time to a course the user owns. The slot
// must already be validated.
func (s *PostgresStore) CreateCourseSlot(
	ctx context.Context, userID string, slot *models.CourseSlot,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Lock the course so two concurrent adds cannot both pass the cap.
	var lockedProjectID string
	err = tx.QueryRowContext(ctx, `
		SELECT id FROM ai_projects WHERE id = $1 AND user_id = $2 FOR UPDATE
	`, slot.ProjectID, userID).Scan(&lockedProjectID)
	if err != nil {
		return err
	}
	var count int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FROM course_slots WHERE project_id = $1
	`, slot.ProjectID).Scan(&count); err != nil {
		return err
	}
	if count >= MaxCourseSlots {
		return ErrTooManyCourseSlots
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO course_slots (project_id, weekday, start_minute, end_minute, timezone, label)
		SELECT p.id, $3, $4, $5, $6, $7 FROM ai_projects p WHERE p.id = $1 AND p.user_id = $2
		RETURNING id, created_at
	`, slot.ProjectID, userID, slot.Weekday, slot.StartMinute, slot.EndMinute,
		slot.Timezone, slot.Label).Scan(&slot.ID, &slot.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// ListCourseSlots returns a course's class times in week order.
func (s *PostgresStore) ListCourseSlots(
	ctx context.Context, userID, projectID string,
) ([]models.CourseSlot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cs.id, cs.project_id, cs.weekday, cs.start_minute, cs.end_minute,
		       cs.timezone, cs.label, cs.created_at
		FROM course_slots cs
		JOIN ai_projects p ON p.id = cs.project_id
		WHERE cs.project_id = $1 AND p.user_id = $2
		ORDER BY cs.weekday, cs.start_minute, cs.created_at
	`, projectID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	slots := make([]models.CourseSlot, 0)
	for rows.Next() {
		slot, err := scanCourseSlot(rows, false)
		if err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	return slots, rows.Err()
}

// ListCourseSlotsByUser returns every class time across the user's courses,
// each carrying its course name, in week order.
func (s *PostgresStore) ListCourseSlotsByUser(
	ctx context.Context, userID string,
) ([]models.CourseSlot, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT cs.id, cs.project_id, cs.weekday, cs.start_minute, cs.end_minute,
		       cs.timezone, cs.label, cs.created_at, p.name
		FROM course_slots cs
		JOIN ai_projects p ON p.id = cs.project_id
		WHERE p.user_id = $1
		ORDER BY cs.weekday, cs.start_minute, p.name, cs.created_at
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	slots := make([]models.CourseSlot, 0)
	for rows.Next() {
		slot, err := scanCourseSlot(rows, true)
		if err != nil {
			return nil, err
		}
		slots = append(slots, slot)
	}
	return slots, rows.Err()
}

// DeleteCourseSlot removes one class time. Sessions it filed stay in the
// course (the link only loses its slot reference).
func (s *PostgresStore) DeleteCourseSlot(
	ctx context.Context, userID, projectID, slotID string,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM course_slots cs
		USING ai_projects p
		WHERE cs.project_id = p.id AND cs.id = $1 AND cs.project_id = $2 AND p.user_id = $3
	`, slotID, projectID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

// ProjectWeekStarts maps each of the user's courses to its week_start (zero
// when unset), so the matcher can ignore sessions from before a course began.
func (s *PostgresStore) ProjectWeekStarts(
	ctx context.Context, userID string,
) (map[string]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, week_start FROM ai_projects WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	starts := make(map[string]time.Time)
	for rows.Next() {
		var id string
		var weekStart sql.NullTime
		if err := rows.Scan(&id, &weekStart); err != nil {
			return nil, err
		}
		if weekStart.Valid {
			starts[id] = weekStart.Time
		} else {
			starts[id] = time.Time{}
		}
	}
	return starts, rows.Err()
}

// ClassifiableSession is a session the timetable may file: unlinked, or
// linked by an earlier timetable run.
type ClassifiableSession struct {
	ID              string
	Title           string
	StartedAt       time.Time
	DurationSeconds int
	ProjectID       string // "" when unlinked
	SlotID          string
}

// ListClassifiableSessions returns the user's sessions that are unlinked or
// timetable-linked, newest first, bounded by TimetableClassifyLimit.
func (s *PostgresStore) ListClassifiableSessions(
	ctx context.Context, tenantID, userID string,
) ([]ClassifiableSession, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT se.id, se.title, se.started_at, se.duration_seconds,
		       COALESCE(ps.project_id::text, ''), COALESCE(ps.slot_id::text, '')
		FROM sessions se
		LEFT JOIN project_sessions ps ON ps.session_id = se.id
		WHERE se.user_id = $1 AND se.tenant_id = $2
		  AND (ps.session_id IS NULL OR ps.assigned_by = $3)
		ORDER BY se.started_at DESC, se.id
		LIMIT $4
	`, userID, tenantID, SessionLinkTimetable, TimetableClassifyLimit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	sessions := make([]ClassifiableSession, 0)
	for rows.Next() {
		var session ClassifiableSession
		if err := rows.Scan(
			&session.ID, &session.Title, &session.StartedAt, &session.DurationSeconds,
			&session.ProjectID, &session.SlotID,
		); err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

// TimetableAssignment files one session into one course by a slot.
type TimetableAssignment struct {
	SessionID string
	ProjectID string
	SlotID    string
}

// ApplyTimetableAssignments links sessions to courses on the timetable's
// behalf. A manual link is left alone (the row is skipped, not an error); a
// missing or timetable-made link is written. Everything is checked against
// the owner. Returns how many rows changed.
func (s *PostgresStore) ApplyTimetableAssignments(
	ctx context.Context, tenantID, userID string, assignments []TimetableAssignment,
) (int, error) {
	if len(assignments) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	applied := 0
	for _, assignment := range assignments {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO project_sessions (project_id, session_id, assigned_by, slot_id)
			SELECT p.id, se.id, $5, cs.id
			FROM ai_projects p
			JOIN sessions se ON se.user_id = p.user_id AND se.tenant_id = p.tenant_id
			JOIN course_slots cs ON cs.project_id = p.id
			WHERE p.id = $1 AND se.id = $2 AND cs.id = $3
			  AND p.user_id = $4 AND p.tenant_id = $6
			ON CONFLICT (session_id) DO UPDATE
			SET project_id = excluded.project_id, slot_id = excluded.slot_id,
			    assigned_by = excluded.assigned_by
			WHERE project_sessions.assigned_by = $5
			  AND (project_sessions.project_id <> excluded.project_id
			       OR project_sessions.slot_id IS DISTINCT FROM excluded.slot_id)
		`, assignment.ProjectID, assignment.SessionID, assignment.SlotID,
			userID, SessionLinkTimetable, tenantID)
		if err != nil {
			return 0, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, err
		}
		applied += int(affected)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return applied, nil
}
