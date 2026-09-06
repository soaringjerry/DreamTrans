package handlers

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/dreamtrans/backend/internal/risk"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/google/uuid"
)

func writePromotionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidPromotion):
		http.Error(w, `{"error":"邀请码无效、已暂停、已过期或名额已满"}`, http.StatusBadRequest)
	case errors.Is(err, store.ErrPromotionInput):
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, `{"error":"promotion not found"}`, http.StatusNotFound)
	default:
		log.Printf("promotion operation: %v", err)
		http.Error(w, `{"error":"promotion operation failed"}`, http.StatusInternalServerError)
	}
}

func promotionPagination(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if page > 1000000 {
		page = 1000000
	}
	return page, 20
}

func (h *AdminHandler) HandlePromotions(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireActor(w, r)
	if !ok {
		return
	}
	// Defense in depth: promotions spend platform funds.
	if actor.Role != "super_admin" {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	id := strings.TrimPrefix(r.URL.Path, "/api/admin/promotions")
	id = strings.TrimPrefix(id, "/")
	if id != "" {
		if _, err := uuid.Parse(id); err != nil {
			http.Error(w, `{"error":"invalid promotion id"}`, http.StatusBadRequest)
			return
		}
		switch r.Method {
		case http.MethodGet:
			page, size := promotionPagination(r)
			items, total, err := h.store.ListPromotionRegistrations(r.Context(), id, size, (page-1)*size)
			if err != nil {
				writePromotionError(w, err)
				return
			}
			WriteJSON(w, map[string]any{"registrations": items, "total": total, "page": page, "page_size": size})
		case http.MethodPatch:
			var input struct {
				Enabled *bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Enabled == nil {
				http.Error(w, `{"error":"enabled is required"}`, http.StatusBadRequest)
				return
			}
			if err := h.store.SetPromotionEnabled(r.Context(), id, *input.Enabled); err != nil {
				writePromotionError(w, err)
				return
			}
			WriteJSON(w, map[string]bool{"ok": true})
		default:
			http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		page, size := promotionPagination(r)
		items, total, err := h.store.ListPromotions(r.Context(), size, (page-1)*size, strings.TrimSpace(r.URL.Query().Get("search")))
		if err != nil {
			writePromotionError(w, err)
			return
		}
		WriteJSON(w, map[string]any{"invites": items, "total": total, "page": page, "page_size": size})
	case http.MethodPost:
		var input store.PromotionInvite
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if expected := os.Getenv("REGISTRATION_INVITE_CODE"); expected != "" && strings.EqualFold(strings.TrimSpace(input.Code), expected) {
			http.Error(w, `{"error":"code conflicts with the legacy registration invite"}`, http.StatusBadRequest)
			return
		}
		if err := h.store.CreatePromotion(r.Context(), &input, actor.UserID); err != nil {
			writePromotionError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		WriteJSON(w, input)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// HandlePromotionPreview exposes the offer, never channel tags or recipient data.
func (h *AuthHandler) HandlePromotionPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("REGISTRATION_ENABLED")), "true") {
		http.Error(w, `{"error":"registration is closed"}`, http.StatusForbidden)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	if code == "" || len(code) > 128 {
		writePromotionError(w, store.ErrInvalidPromotion)
		return
	}
	if expected := os.Getenv("REGISTRATION_INVITE_CODE"); expected != "" && code == expected {
		WriteJSON(w, map[string]any{"name": "邀请注册", "grant_usd": 0, "plan_code": ""})
		return
	}
	offer, err := h.store.PreviewPromotion(r.Context(), code)
	if err != nil {
		writePromotionError(w, err)
		return
	}
	if !h.EmailVerificationRequired() {
		http.Error(w, `{"error":"promotion registration requires email verification"}`, http.StatusServiceUnavailable)
		return
	}
	WriteJSON(w, map[string]any{"name": offer.Name, "grant_usd": offer.GrantUSD, "grant_days": offer.GrantDays, "plan_code": offer.PlanCode, "plan_days": offer.PlanDays, "expires_at": offer.ExpiresAt})
}

func (h *AuthHandler) fulfillPromotion(w http.ResponseWriter, r *http.Request, userID string) bool {
	if h.billing == nil {
		return true
	}
	decision, err := risk.NewService(h.store.DB()).UserDecision(r.Context(), userID)
	if err != nil {
		writeRiskError(w, err)
		return false
	}
	if decision != "legacy" {
		if err := h.billing.GrantTrialCredit(r.Context(), userID); err != nil {
			log.Printf("signup trial credit: %v", err)
			http.Error(w, `{"error":"signup rewards temporarily unavailable; please retry login"}`, http.StatusServiceUnavailable)
			return false
		}
	}
	if err := h.billing.GrantPromotionRewards(r.Context(), userID); err != nil {
		log.Printf("fulfill registration promotion: %v", err)
		http.Error(w, `{"error":"活动权益暂未到账，请重新登录重试；不会重复发放"}`, http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (h *AuthHandler) registrationPromotion(w http.ResponseWriter, r *http.Request, code string) (string, bool) {
	code = strings.TrimSpace(code)
	promotionCode := code
	expected := os.Getenv("REGISTRATION_INVITE_CODE")
	legacyInvite := expected != "" && len(code) == len(expected) && subtle.ConstantTimeCompare([]byte(code), []byte(expected)) == 1
	if legacyInvite {
		promotionCode = ""
	}
	if expected != "" && code == "" {
		http.Error(w, `{"error":"registration invite code is required"}`, http.StatusForbidden)
		return "", false
	}
	if promotionCode != "" {
		if _, err := h.store.PreviewPromotion(r.Context(), promotionCode); err != nil {
			writePromotionError(w, err)
			return "", false
		}
		if !h.EmailVerificationRequired() {
			http.Error(w, `{"error":"promotion registration requires email verification"}`, http.StatusServiceUnavailable)
			return "", false
		}
	}

	return promotionCode, true
}
