package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

const maxSessionDurationSeconds = 2_147_483_647

// NewSessionHandler creates a new session handler
func NewSessionHandler(postgresStore *store.PostgresStore) *SessionHandler {
	return &SessionHandler{store: postgresStore}
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
	total, err := h.store.CountSessionsByUser(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, `{"error":"failed to count sessions"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, SessionListResponse{
		Sessions: sessions,
		Total:    total,
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	// Set defaults
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "Session " + time.Now().Format("2006-01-02 15:04")
	}
	if len([]rune(req.Title)) > 255 {
		http.Error(w, `{"error":"title is too long"}`, http.StatusBadRequest)
		return
	}
	if req.SourceLanguage == "" {
		req.SourceLanguage = "en"
	}
	if req.TargetLanguage == "" {
		req.TargetLanguage = "zh"
	}
	req.SourceLanguage = strings.TrimSpace(req.SourceLanguage)
	req.TargetLanguage = strings.TrimSpace(req.TargetLanguage)
	if req.SourceLanguage == "" || req.TargetLanguage == "" ||
		len(req.SourceLanguage) > 10 || len(req.TargetLanguage) > 10 {
		http.Error(w, `{"error":"invalid language code"}`, http.StatusBadRequest)
		return
	}

	session := &models.Session{
		UserID:         claims.UserID,
		TenantID:       claims.TenantID,
		Title:          req.Title,
		SourceLanguage: req.SourceLanguage,
		TargetLanguage: req.TargetLanguage,
		Status:         "active",
	}

	if err := h.store.CreateSessionWithQuota(r.Context(), session); err != nil {
		if errors.Is(err, store.ErrSessionLimit) {
			http.Error(w, `{"error":"active session limit reached"}`, http.StatusPaymentRequired)
			return
		}
		http.Error(w, `{"error":"failed to create session"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	encodeJSONResponse(w, session)
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
	encodeJSONResponse(w, response)
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
		title := strings.TrimSpace(*req.Title)
		if title == "" || len([]rune(title)) > 255 {
			http.Error(w, `{"error":"invalid title"}`, http.StatusBadRequest)
			return
		}
		req.Title = &title
	}
	if req.Status != nil {
		if !validSessionStatus(*req.Status) {
			http.Error(w, `{"error":"invalid session status"}`, http.StatusBadRequest)
			return
		}
	}
	if req.DurationSeconds != nil {
		if *req.DurationSeconds < 0 || *req.DurationSeconds > maxSessionDurationSeconds {
			http.Error(w, `{"error":"duration_seconds is out of range"}`, http.StatusBadRequest)
			return
		}
	}

	session, err := h.store.UpdateSessionFieldsWithQuota(
		r.Context(),
		sessionID,
		claims.UserID,
		req.Title,
		req.Status,
		req.DurationSeconds,
	)
	if err != nil {
		if errors.Is(err, store.ErrSessionLimit) {
			http.Error(w, `{"error":"active session limit reached"}`, http.StatusPaymentRequired)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error":"session not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"failed to update session"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, session)
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
	writeHTTPResponse(w, []byte(`{"success":true}`))
}

// TranscriptRequest represents a transcript save request
type TranscriptRequest struct {
	ClientSegmentID string   `json:"client_segment_id,omitempty"`
	Speaker         string   `json:"speaker"`
	Text            string   `json:"text"`
	Translation     *string  `json:"translation,omitempty"`
	StartTime       float64  `json:"start_time"`
	EndTime         *float64 `json:"end_time,omitempty"`
	Status          string   `json:"status"`
	IsPartial       bool     `json:"is_partial"`
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
	if err := validateTranscriptRequest(&req); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	clientSegmentID, err := normalizeClientSegmentID(req.ClientSegmentID)
	if err != nil {
		http.Error(w, `{"error":"invalid client_segment_id"}`, http.StatusBadRequest)
		return
	}

	transcript := &models.Transcript{
		SessionID:       sessionID,
		ClientSegmentID: clientSegmentID,
		Speaker:         req.Speaker,
		Text:            req.Text,
		Translation:     req.Translation,
		StartTime:       req.StartTime,
		EndTime:         req.EndTime,
		Status:          req.Status,
		IsPartial:       req.IsPartial,
	}

	if err := h.store.CreateTranscript(r.Context(), transcript); err != nil {
		if errors.Is(err, store.ErrStorageQuota) {
			http.Error(w, `{"error":"tenant storage quota exceeded"}`, http.StatusPaymentRequired)
			return
		}
		http.Error(w, `{"error":"failed to save transcript"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	encodeJSONResponse(w, transcript)
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
	if len(reqs) == 0 || len(reqs) > 500 {
		http.Error(w, `{"error":"batch must contain between 1 and 500 transcripts"}`, http.StatusBadRequest)
		return
	}

	transcripts := make([]*models.Transcript, 0, len(reqs))
	for _, req := range reqs {
		if req.Speaker == "" {
			req.Speaker = "Speaker"
		}
		if req.Status == "" {
			req.Status = "partial"
		}
		if err := validateTranscriptRequest(&req); err != nil {
			http.Error(w, `{"error":"invalid transcript batch"}`, http.StatusBadRequest)
			return
		}
		clientSegmentID, err := normalizeClientSegmentID(req.ClientSegmentID)
		if err != nil {
			http.Error(w, `{"error":"invalid client_segment_id"}`, http.StatusBadRequest)
			return
		}

		transcript := &models.Transcript{
			SessionID:       sessionID,
			ClientSegmentID: clientSegmentID,
			Speaker:         req.Speaker,
			Text:            req.Text,
			Translation:     req.Translation,
			StartTime:       req.StartTime,
			EndTime:         req.EndTime,
			Status:          req.Status,
			IsPartial:       req.IsPartial,
		}
		transcripts = append(transcripts, transcript)
	}
	if err := h.store.BatchCreateTranscripts(r.Context(), transcripts); err != nil {
		if errors.Is(err, store.ErrStorageQuota) {
			http.Error(w, `{"error":"tenant storage quota exceeded"}`, http.StatusPaymentRequired)
			return
		}
		http.Error(w, `{"error":"failed to save transcript batch"}`, http.StatusInternalServerError)
		return
	}
	saved := make([]models.Transcript, 0, len(transcripts))
	for _, transcript := range transcripts {
		saved = append(saved, *transcript)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	encodeJSONResponse(w, map[string]interface{}{
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
		setDownloadFilename(w, session.Title, "json")
		encodeJSONResponse(w, models.SessionWithTranscripts{
			Session:     *session,
			Transcripts: transcripts,
		})

	case "txt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		setDownloadFilename(w, session.Title, "txt")
		for index := range transcripts {
			t := &transcripts[index]
			line := t.Speaker + ": " + t.Text
			if t.Translation != nil && *t.Translation != "" {
				line += " | " + *t.Translation
			}
			if !writeHTTPResponse(w, []byte(line+"\n")) {
				return
			}
		}

	case "srt":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		setDownloadFilename(w, session.Title, "srt")
		for i := range transcripts {
			t := &transcripts[i]
			// SRT format
			start := formatSRTTime(t.StartTime)
			end := start
			if t.EndTime != nil {
				end = formatSRTTime(*t.EndTime)
			}
			if !writeHTTPResponse(w, []byte(strconv.Itoa(i+1)+"\n")) {
				return
			}
			if !writeHTTPResponse(w, []byte(start+" --> "+end+"\n")) {
				return
			}
			text := t.Text
			if t.Translation != nil && *t.Translation != "" {
				text += "\n" + *t.Translation
			}
			if !writeHTTPResponse(w, []byte(text+"\n\n")) {
				return
			}
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

func validSessionStatus(status string) bool {
	switch status {
	case "active", "paused", "completed", "archived":
		return true
	default:
		return false
	}
}

func validateTranscriptRequest(req *TranscriptRequest) error {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return fmt.Errorf("text is required")
	}
	if len([]rune(text)) > 20_000 {
		return fmt.Errorf("text is too long")
	}
	if len([]rune(req.Speaker)) > 50 {
		return fmt.Errorf("speaker is too long")
	}
	if req.Translation != nil && len([]rune(*req.Translation)) > 20_000 {
		return fmt.Errorf("translation is too long")
	}
	switch req.Status {
	case "partial", "confirmed", "translated":
	default:
		return fmt.Errorf("invalid transcript status")
	}
	if req.StartTime < 0 || (req.EndTime != nil && *req.EndTime < req.StartTime) {
		return fmt.Errorf("invalid transcript timing")
	}
	return nil
}

func setDownloadFilename(w http.ResponseWriter, title, extension string) {
	safeTitle := strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == '"' || r == '\\' || r == '/' {
			return -1
		}
		return r
	}, strings.TrimSpace(title))
	if safeTitle == "" {
		safeTitle = "session"
	}
	filename := safeTitle + "." + extension
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.QueryEscape(filename))
}
