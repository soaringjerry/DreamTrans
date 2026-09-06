package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

// 课表归类 — the HTTP surface.
//
//	GET    /api/ai/projects/{id}/timetable            a course's class times
//	POST   /api/ai/projects/{id}/timetable            add one {weekday,start,end,timezone,label}
//	DELETE /api/ai/projects/{id}/timetable/{slot_id}  remove one
//	GET    /api/ai/timetable                          every class time across the user's courses
//	POST   /api/ai/timetable/classify                 {apply:bool} file sessions by the timetable
//
// Sessions are also filed on their own when created (by start time) and when
// completed (by recorded span); see fileSessionByTimetable.

func (h *RAGHandler) handleProjectTimetable(
	w http.ResponseWriter, r *http.Request, project *models.AIProject, slotID string,
) {
	switch {
	case r.Method == http.MethodGet && slotID == "":
		slots, err := h.store.ListCourseSlots(r.Context(), project.UserID, project.ID)
		if err != nil {
			http.Error(w, "failed to list class times", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, map[string]any{"slots": slots})
	case r.Method == http.MethodPost && slotID == "":
		var slot models.CourseSlot
		if err := json.NewDecoder(r.Body).Decode(&slot); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		slot.ProjectID = project.ID
		if err := store.ValidateCourseSlot(&slot); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		err := h.store.CreateCourseSlot(r.Context(), project.UserID, &slot)
		if errors.Is(err, store.ErrTooManyCourseSlots) {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err != nil {
			http.Error(w, "failed to add class time", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		WriteJSON(w, map[string]any{"slot": slot})
	case r.Method == http.MethodDelete && slotID != "":
		err := h.store.DeleteCourseSlot(r.Context(), project.UserID, project.ID, slotID)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "class time not found", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, "failed to remove class time", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, map[string]bool{"success": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type timetableAssignmentView struct {
	SessionID       string    `json:"session_id"`
	Title           string    `json:"title"`
	StartedAt       time.Time `json:"started_at"`
	DurationSeconds int       `json:"duration_seconds"`
	// Course the session was filed under before this run, when any.
	FromProjectID  string `json:"from_project_id,omitempty"`
	ProjectID      string `json:"project_id"`
	SlotID         string `json:"slot_id"`
	OverlapMinutes int    `json:"overlap_minutes"`
	// "assign" for an unfiled session, "move" for one the timetable filed
	// elsewhere earlier.
	Change string `json:"change"`
}

type timetableClassifyResponse struct {
	Assignments []timetableAssignmentView `json:"assignments"`
	// Sessions already filed where the timetable would put them.
	Kept int `json:"kept"`
	// Sessions no class time claims.
	Unmatched int  `json:"unmatched"`
	Scanned   int  `json:"scanned"`
	Applied   int  `json:"applied"`
	Preview   bool `json:"preview"`
}

// HandleTimetable serves the user-wide routes.
func (h *RAGHandler) HandleTimetable(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "timetable requires PostgreSQL", http.StatusServiceUnavailable)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/ai/timetable"), "/")
	switch {
	case path == "" && r.Method == http.MethodGet:
		slots, err := h.store.ListCourseSlotsByUser(r.Context(), claims.UserID)
		if err != nil {
			http.Error(w, "failed to list class times", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, map[string]any{"slots": slots})
	case path == "classify" && r.Method == http.MethodPost:
		var req struct {
			Apply bool `json:"apply"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		response, err := classifySessionsByTimetable(r.Context(), h.store, claims.TenantID, claims.UserID, req.Apply)
		if err != nil {
			http.Error(w, "failed to file sessions", http.StatusInternalServerError)
			return
		}
		WriteJSON(w, response)
	case path == "" || path == "classify":
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

// loadTimetable prepares the user's slots for matching; nil when the user
// has no class times at all.
func loadTimetable(ctx context.Context, st *store.PostgresStore, userID string) ([]timetableSlot, error) {
	slots, err := st.ListCourseSlotsByUser(ctx, userID)
	if err != nil || len(slots) == 0 {
		return nil, err
	}
	weekStarts, err := st.ProjectWeekStarts(ctx, userID)
	if err != nil {
		return nil, err
	}
	return timetableSlotsFrom(slots, weekStarts), nil
}

func classifySessionsByTimetable(
	ctx context.Context, st *store.PostgresStore, tenantID, userID string, apply bool,
) (*timetableClassifyResponse, error) {
	response := &timetableClassifyResponse{
		Assignments: make([]timetableAssignmentView, 0), Preview: !apply,
	}
	slots, err := loadTimetable(ctx, st, userID)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return response, nil
	}
	sessions, err := st.ListClassifiableSessions(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	response.Scanned = len(sessions)
	assignments := make([]store.TimetableAssignment, 0)
	for _, session := range sessions {
		match := matchSessionToTimetable(
			session.StartedAt, time.Duration(session.DurationSeconds)*time.Second, slots,
		)
		switch {
		case match == nil:
			if session.ProjectID == "" {
				response.Unmatched++
			} else {
				// Filed by a class time that has since changed: leave it, a
				// person can move it.
				response.Kept++
			}
			continue
		case match.Slot.ProjectID == session.ProjectID:
			response.Kept++
			continue
		}
		change := "assign"
		if session.ProjectID != "" {
			change = "move"
		}
		response.Assignments = append(response.Assignments, timetableAssignmentView{
			SessionID: session.ID, Title: session.Title, StartedAt: session.StartedAt,
			DurationSeconds: session.DurationSeconds, FromProjectID: session.ProjectID,
			ProjectID: match.Slot.ProjectID, SlotID: match.Slot.ID,
			OverlapMinutes: int(match.Overlap / time.Minute), Change: change,
		})
		assignments = append(assignments, store.TimetableAssignment{
			SessionID: session.ID, ProjectID: match.Slot.ProjectID, SlotID: match.Slot.ID,
		})
	}
	if apply {
		applied, err := st.ApplyTimetableAssignments(ctx, tenantID, userID, assignments)
		if err != nil {
			return nil, err
		}
		response.Applied = applied
	}
	return response, nil
}

// fileSessionByTimetable files one session by the user's timetable: on
// creation from its start time, on completion from its recorded span. A
// manual link is never touched. Failure only logs — filing is a convenience
// and must not fail the session write. On success the session's ProjectID
// reflects the link.
func fileSessionByTimetable(ctx context.Context, st *store.PostgresStore, session *models.Session) {
	if st == nil || session == nil || session.ID == "" {
		return
	}
	slots, err := loadTimetable(ctx, st, session.UserID)
	if err != nil {
		log.Printf("timetable: load class times: %v", err)
		return
	}
	if len(slots) == 0 {
		return
	}
	match := matchSessionToTimetable(
		session.StartedAt, time.Duration(session.DurationSeconds)*time.Second, slots,
	)
	if match == nil {
		return
	}
	applied, err := st.ApplyTimetableAssignments(ctx, session.TenantID, session.UserID, []store.TimetableAssignment{{
		SessionID: session.ID, ProjectID: match.Slot.ProjectID, SlotID: match.Slot.ID,
	}})
	if err != nil {
		log.Printf("timetable: file session: %v", err)
		return
	}
	if applied > 0 {
		projectID := match.Slot.ProjectID
		session.ProjectID = &projectID
	}
}
