package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
)

func billingAdminErrorStatus(err error) int {
	switch {
	case errors.Is(err, billing.ErrBillingPreviewStale):
		return http.StatusConflict
	case errors.Is(err, billing.ErrInvalidBillingInput):
		return http.StatusBadRequest
	case errors.Is(err, sql.ErrNoRows):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeBillingAdminError(w http.ResponseWriter, operation string, err error) {
	status := billingAdminErrorStatus(err)
	switch status {
	case http.StatusBadRequest, http.StatusConflict:
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, status)
	case http.StatusNotFound:
		http.Error(w, `{"error":"billing resource not found"}`, status)
	default:
		log.Printf("%s: %v", operation, err)
		http.Error(w, `{"error":"billing operation failed"}`, status)
	}
}

func (h *AdminHandler) HandleBillingCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	catalog, err := h.billing.GetBillingCatalog(r.Context())
	if err != nil {
		writeBillingAdminError(w, "load billing catalog", err)
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
	catalog, err := h.billing.UpdateBillingConfig(r.Context(), input, claims.UserID)
	if err != nil {
		writeBillingAdminError(w, "update billing configuration", err)
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
		writeBillingAdminError(w, "preview billing configuration", err)
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
		writeBillingAdminError(w, "apply billing catalog", err)
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
		writeBillingAdminError(w, "preview billing catalog update", err)
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
		writeBillingAdminError(w, "update model cost", err)
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
		writeBillingAdminError(w, "upsert provider cost override", err)
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
		writeBillingAdminError(w, "delete provider cost override", err)
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
		writeBillingAdminError(w, "preview billing reset", err)
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
		writeBillingAdminError(w, "reset billing defaults", err)
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
		writeBillingAdminError(w, "load billing analytics", err)
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
