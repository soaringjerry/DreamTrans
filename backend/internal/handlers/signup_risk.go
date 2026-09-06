package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/risk"
	"github.com/google/uuid"
)

func (h *AuthHandler) SetSignupRisk(detector *risk.Detector, service *risk.Service) {
	h.signupDetector = detector
	h.signupRisk = service
}
func (h *AuthHandler) signupSignals(r *http.Request, email string, browser *risk.BrowserSignals) *risk.Signals {
	if h.signupDetector == nil {
		return nil
	}
	signals := h.signupDetector.Signals(r, auth.CanonicalEmail(email))
	h.signupDetector.AddBrowserSignals(signals, r, browser)
	return signals
}
func (h *AuthHandler) signupNeedsReview(ctx context.Context, userID string) bool {
	if h.signupRisk == nil {
		return false
	}
	decision, err := h.signupRisk.UserDecision(ctx, userID)
	return err != nil || decision == "review" || decision == "denied"
}
func (h *AuthHandler) HandleSignupContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if h.signupDetector == nil {
		http.Error(w, `{"error":"signup protection unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	// Trust configured application HTTPS, never arbitrary forwarded headers.
	request := r.Clone(r.Context())
	if strings.HasPrefix(strings.ToLower(os.Getenv("APP_BASE_URL")), "https://") {
		request.URL.Scheme = "https"
	}
	if err := h.signupDetector.Prepare(w, request); err != nil {
		http.Error(w, `{"error":"signup protection unavailable"}`, http.StatusServiceUnavailable)
		return
	}
	WriteJSON(w, map[string]bool{"ok": true})
}

func writeRiskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, risk.ErrInput):
		http.Error(w, `{"error":"请检查风控阈值或填写审核备注"}`, http.StatusBadRequest)
	case errors.Is(err, risk.ErrDecision):
		http.Error(w, `{"error":"已放行的权益不能通过审核操作撤回"}`, http.StatusConflict)
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, `{"error":"记录或账号不存在"}`, http.StatusNotFound)
	default:
		log.Printf("signup risk operation: %v", err)
		http.Error(w, `{"error":"风控操作暂时不可用"}`, http.StatusInternalServerError)
	}
}

func (h *AdminHandler) HandleSignupRisk(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	if actor.Role != "super_admin" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	service := risk.NewService(h.store.DB())
	rest := strings.TrimPrefix(r.URL.Path, "/api/admin/signup-risk")
	if rest == "/budget" && r.Method == http.MethodGet {
		budget, err := service.Budget(r.Context())
		if err != nil {
			writeRiskError(w, err)
			return
		}
		WriteJSON(w, budget)
		return
	}
	if rest == "/settings" {
		h.handleRiskSettings(w, r, service, actor.UserID)
		return
	}
	if rest == "" || rest == "/" {
		if r.Method != http.MethodGet {
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		page, size := promotionPagination(r)
		profiles, total, err := service.List(r.Context(), r.URL.Query().Get("decision"), strings.TrimSpace(r.URL.Query().Get("search")), size, (page-1)*size)
		if err != nil {
			writeRiskError(w, err)
			return
		}
		WriteJSON(w, map[string]any{"profiles": profiles, "total": total})
		return
	}
	id := strings.TrimPrefix(rest, "/")
	if _, err := uuid.Parse(id); err != nil {
		http.Error(w, `{"error":"invalid risk id"}`, http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodGet {
		entries, err := service.Audit(r.Context(), id)
		if err != nil {
			writeRiskError(w, err)
			return
		}
		WriteJSON(w, map[string]any{"audit": entries})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeRiskError(w, risk.ErrInput)
		return
	}
	userID, err := service.Review(r.Context(), id, input.Decision, input.Note, actor.UserID)
	if err != nil {
		writeRiskError(w, err)
		return
	}
	// Approval is durable even if reward issuance fails; replaying approval or
	// the user's next login retries the same idempotent ledger operations.
	if input.Decision == "approved" && h.billing != nil {
		if err := h.billing.GrantTrialCredit(r.Context(), userID); err != nil {
			writeRiskError(w, err)
			return
		}
		if err := h.billing.GrantPromotionRewards(r.Context(), userID); err != nil {
			writeRiskError(w, err)
			return
		}
	}
	status, err := service.UserDecision(r.Context(), userID)
	if err != nil {
		writeRiskError(w, err)
		return
	}
	WriteJSON(w, map[string]any{"ok": true, "reward_status": status})
}
func (h *AdminHandler) handleRiskSettings(w http.ResponseWriter, r *http.Request, service *risk.Service, actor string) {
	switch r.Method {
	case http.MethodGet:
		settings, err := service.Settings(r.Context())
		if err != nil {
			writeRiskError(w, err)
			return
		}
		WriteJSON(w, settings)
	case http.MethodPut:
		var input risk.Settings
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeRiskError(w, risk.ErrInput)
			return
		}
		if err := service.UpdateSettings(r.Context(), input, actor); err != nil {
			writeRiskError(w, err)
			return
		}
		WriteJSON(w, input)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
