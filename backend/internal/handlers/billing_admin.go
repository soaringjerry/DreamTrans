package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
)

func (h *AdminHandler) HandleBillingCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	catalog, err := h.billing.GetBillingCatalog(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to load billing catalog"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input billing.BillingConfigInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if err := h.billing.UpdateBillingConfig(r.Context(), input, claims.UserID); err != nil {
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
		return
	}
	catalog, err := h.billing.GetBillingCatalog(r.Context())
	if err != nil {
		http.Error(w, `{"error":"configuration saved but reload failed"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input billing.BillingConfigInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	preview, err := h.billing.PreviewBillingConfig(r.Context(), input)
	if err != nil {
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
		return
	}
	WriteJSON(w, preview)
}

func (h *AdminHandler) HandleBillingCatalogApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		CatalogVersion  string `json:"catalog_version"`
		Confirmation    string `json:"confirmation"`
		CurrentRevision string `json:"current_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	catalog, err := h.billing.ApplyBuiltinCatalog(
		r.Context(),
		claims.UserID,
		strings.TrimSpace(request.CatalogVersion),
		strings.TrimSpace(request.Confirmation),
		strings.TrimSpace(request.CurrentRevision),
	)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, billing.ErrBillingPreviewStale) {
			status = http.StatusConflict
		}
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, status)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingCatalogApplyPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	preview, err := h.billing.PreviewBuiltinCatalogApply(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to preview billing catalog update"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, preview)
}

func (h *AdminHandler) HandleBillingModelCost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input billing.ManualModelCostInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if err := h.billing.UpsertManualModelCost(r.Context(), input, claims.UserID); err != nil {
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
		return
	}
	WriteJSON(w, map[string]bool{"success": true})
}

func (h *AdminHandler) HandleProviderCostOverride(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input billing.ProviderCostOverrideInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	catalog, err := h.billing.UpsertProviderCostOverride(
		r.Context(),
		input,
		claims.UserID,
	)
	if err != nil {
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleProviderCostOverrideDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	catalog, err := h.billing.DeleteProviderCostOverride(
		r.Context(),
		r.URL.Query().Get("provider"),
		r.URL.Query().Get("sku"),
		r.URL.Query().Get("service"),
		r.URL.Query().Get("unit_type"),
		claims.UserID,
	)
	if err != nil {
		status := http.StatusBadRequest
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, status)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingResetPreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	preview, err := h.billing.PreviewBillingReset(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to preview billing reset"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, preview)
}

func (h *AdminHandler) HandleBillingReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Confirmation    string `json:"confirmation"`
		CurrentRevision string `json:"current_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	catalog, err := h.billing.ResetBillingDefaults(
		r.Context(), claims.UserID, strings.TrimSpace(request.Confirmation),
		strings.TrimSpace(request.CurrentRevision),
	)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, billing.ErrBillingPreviewStale) {
			status = http.StatusConflict
		}
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, status)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	analytics, err := h.billing.GetBillingAnalytics(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to load billing analytics"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, analytics)
}

type BillingHandler struct {
	billing *billing.Service
}

func NewBillingHandler(service *billing.Service) *BillingHandler {
	return &BillingHandler{billing: service}
}

func (h *BillingHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	summary, err := h.billing.GetUserBillingSummary(r.Context(), claims.UserID)
	if err != nil {
		http.Error(w, `{"error":"failed to load billing summary"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, summary)
}

func (h *BillingHandler) HandleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	items, err := h.billing.GetUserUsage(
		r.Context(), claims.UserID, r.URL.Query().Get("session_id"), 100,
	)
	if err != nil {
		http.Error(w, `{"error":"failed to load usage"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, map[string]any{"usage": items})
}

func safeJSONError(err error) string {
	if err == nil {
		return ""
	}
	message, _ := json.Marshal(err.Error())
	return strings.Trim(string(message), `"`)
}
