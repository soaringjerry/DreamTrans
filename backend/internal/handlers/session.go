package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

// SessionHandler handles session-related endpoints
type SessionHandler struct {
	store *store.PostgresStore
}

// NewSessionHandler creates a new session handler
func NewSessionHandler(store *store.PostgresStore) *SessionHandler {
	return &SessionHandler{store: store}
}

// CreateSessionRequest represents a session creation request
type CreateSessionRequest struct {
	Title          string `json:"title"`
	SourceLanguage string `json:"source_language"`
	TargetLanguage string `json:"target_language"`
}

// SessionListResponse represents a paginated session list
type SessionListResponse struct {
	Sessions []models.Session `json:"sessions"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// HandleListSessions lists all sessions for the current user
func (h *SessionHandler) HandleListSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Parse pagination params
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	sessions, err := h.store.GetSessionsByUser(r.Context(), claims.UserID, pageSize, offset)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch sessions"}`, http.StatusInternalServerError)
		return
	}

	if sessions == nil {
		sessions = []models.Session{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SessionListResponse{
		Sessions: sessions,
		Page:     page,
		PageSize: pageSize,
	})
}

// HandleCreateSession creates a new transcription session
func (h *SessionHandler) HandleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body with defaults
		req = CreateSessionRequest{}
	}

	// Set defaults
	if req.Title == "" {
		req.Title = "Session " + time.Now().Format("2006-01-02 15:04")
	}
	if req.SourceLanguage == "" {
		req.SourceLanguage = "en"
	}
	if req.TargetLanguage == "" {
		req.TargetLanguage = "zh"
	}

	session := &models.Session{
		UserID:         claims.UserID,
		TenantID:       claims.TenantID,
		Title:          req.Title,
		SourceLanguage: req.SourceLanguage,
		TargetLanguage: req.TargetLanguage,
		Status:         "active",
	}

	if err := h.store.CreateSession(r.Context(), session); err != nil {
		http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(session)
}

// HandleGetSession retrieves a single session with its transcripts
func (h *SessionHandler) HandleGetSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Extract session ID from URL path /api/sessions/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[3]

	session, err := h.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Check ownership
	if session.UserID != claims.UserID {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	// Get transcripts
	transcripts, err := h.store.GetTranscriptsBySession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch transcripts"}`, http.StatusInternalServerError)
		return
	}

	response := models.SessionWithTranscripts{
		Session:     *session,
		Transcripts: transcripts,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleUpdateSession updates a session
func (h *SessionHandler) HandleUpdateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Extract session ID
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[3]

	session, err := h.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Check ownership
	if session.UserID != claims.UserID {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	var req struct {
		Title           *string `json:"title"`
		Status          *string `json:"status"`
		DurationSeconds *int    `json:"duration_seconds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.Title != nil {
		session.Title = *req.Title
	}
	if req.Status != nil {
		session.Status = *req.Status
		if *req.Status == "completed" {
			now := time.Now()
			session.EndedAt = &now
		}
	}
	if req.DurationSeconds != nil {
		session.DurationSeconds = *req.DurationSeconds
	}

	if err := h.store.UpdateSession(r.Context(), session); err != nil {
		http.Error(w, `{"error":"failed to update session"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

// HandleDeleteSession deletes a session
func (h *SessionHandler) HandleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Extract session ID
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[3]

	session, err := h.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}

	// Check ownership
	if session.UserID != claims.UserID {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	if err := h.store.DeleteSession(r.Context(), sessionID); err != nil {
		http.Error(w, `{"error":"failed to delete session"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// TranscriptRequest represents a transcript save request
type TranscriptRequest struct {
	Speaker     string   `json:"speaker"`
	Text        string   `json:"text"`
	Translation *string  `json:"translation,omitempty"`
	StartTime   float64  `json:"start_time"`
	EndTime     *float64 `json:"end_time,omitempty"`
	Status      string   `json:"status"`
	IsPartial   bool     `json:"is_partial"`
}

// HandleSaveTranscript saves a transcript segment to a session
func (h *SessionHandler) HandleSaveTranscript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Extract session ID from /api/sessions/{id}/transcripts
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[3]

	// Verify session ownership
	session, err := h.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	if session.UserID != claims.UserID {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	var req TranscriptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Set defaults
	if req.Speaker == "" {
		req.Speaker = "Speaker"
	}
	if req.Status == "" {
		req.Status = "partial"
	}

	transcript := &models.Transcript{
		SessionID:   sessionID,
		Speaker:     req.Speaker,
		Text:        req.Text,
		Translation: req.Translation,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
		Status:      req.Status,
		IsPartial:   req.IsPartial,
	}

	if err := h.store.CreateTranscript(r.Context(), transcript); err != nil {
		http.Error(w, `{"error":"failed to save transcript"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(transcript)
}

// HandleBatchSaveTranscripts saves multiple transcript segments at once
func (h *SessionHandler) HandleBatchSaveTranscripts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Extract session ID
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[3]

	// Verify session ownership
	session, err := h.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	if session.UserID != claims.UserID {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	var reqs []TranscriptRequest
	if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	var saved []models.Transcript
	for _, req := range reqs {
		if req.Speaker == "" {
			req.Speaker = "Speaker"
		}
		if req.Status == "" {
			req.Status = "partial"
		}

		transcript := &models.Transcript{
			SessionID:   sessionID,
			Speaker:     req.Speaker,
			Text:        req.Text,
			Translation: req.Translation,
			StartTime:   req.StartTime,
			EndTime:     req.EndTime,
			Status:      req.Status,
			IsPartial:   req.IsPartial,
		}

		if err := h.store.CreateTranscript(r.Context(), transcript); err != nil {
			// Continue on error but log it
			continue
		}
		saved = append(saved, *transcript)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"saved": saved,
		"count": len(saved),
	})
}

// HandleExportSession exports a session in various formats
func (h *SessionHandler) HandleExportSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Extract session ID from /api/sessions/{id}/export
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"session id required"}`, http.StatusBadRequest)
		return
	}
	sessionID := parts[3]

	session, err := h.store.GetSessionByID(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if session == nil {
		http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
		return
	}
	if session.UserID != claims.UserID {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	transcripts, err := h.store.GetTranscriptsBySession(r.Context(), sessionID)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch transcripts"}`, http.StatusInternalServerError)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="`+session.Title+`.json"`)
		json.NewEncoder(w).Encode(models.SessionWithTranscripts{
			Session:     *session,
			Transcripts: transcripts,
		})

	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+session.Title+`.txt"`)
		for _, t := range transcripts {
			line := t.Speaker + ": " + t.Text
			if t.Translation != nil && *t.Translation != "" {
				line += " | " + *t.Translation
			}
			w.Write([]byte(line + "\n"))
		}

	case "srt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+session.Title+`.srt"`)
		for i, t := range transcripts {
			// SRT format
			start := formatSRTTime(t.StartTime)
			end := start
			if t.EndTime != nil {
				end = formatSRTTime(*t.EndTime)
			}
			w.Write([]byte(strconv.Itoa(i+1) + "\n"))
			w.Write([]byte(start + " --> " + end + "\n"))
			text := t.Text
			if t.Translation != nil && *t.Translation != "" {
				text += "\n" + *t.Translation
			}
			w.Write([]byte(text + "\n\n"))
		}

	default:
		http.Error(w, `{"error":"unsupported format"}`, http.StatusBadRequest)
	}
}

func formatSRTTime(seconds float64) string {
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int((seconds - float64(int(seconds))) * 1000)
	return strings.Join([]string{
		padZero(h, 2),
		padZero(m, 2),
		padZero(s, 2),
	}, ":") + "," + padZero(ms, 3)
}

func padZero(n, width int) string {
	s := strconv.Itoa(n)
	for len(s) < width {
		s = "0" + s
	}
	return s
}
