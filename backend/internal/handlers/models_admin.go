package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/modelcatalog"
)

type ModelCatalogHandler struct {
	service *modelcatalog.Service
}

func NewModelCatalogHandler(service *modelcatalog.Service) *ModelCatalogHandler {
	return &ModelCatalogHandler{service: service}
}

func (h *ModelCatalogHandler) HandleAvailable(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	models, err := h.service.Available(r.Context(), r.URL.Query().Get("purpose"))
	if err != nil {
		http.Error(w, `{"error":"failed to load available models"}`, http.StatusBadRequest)
		return
	}
	WriteJSON(w, map[string]any{"models": models})
}

func (h *ModelCatalogHandler) HandlePreferences(w http.ResponseWriter, r *http.Request) {
	claims := auth.GetUserClaims(r.Context())
	switch r.Method {
	case http.MethodGet:
		prefs, err := h.service.EffectivePreferences(r.Context(), claims.UserID)
		if err != nil {
			http.Error(w, `{"error":"failed to load model preferences"}`, http.StatusServiceUnavailable)
			return
		}
		WriteJSON(w, prefs)
	case http.MethodPut:
		var prefs modelcatalog.Preferences
		if err := json.NewDecoder(r.Body).Decode(&prefs); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		saved, err := h.service.SavePreferences(r.Context(), claims.UserID, prefs)
		if err != nil {
			http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
			return
		}
		WriteJSON(w, saved)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *ModelCatalogHandler) HandleAdminCatalog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	catalog, err := h.service.AdminCatalog(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to load model catalog"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, catalog)
}

func (h *ModelCatalogHandler) HandleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if err := h.service.RefreshByActor(r.Context(), claims.UserID); err != nil {
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadGateway)
		return
	}
	catalog, err := h.service.AdminCatalog(r.Context())
	if err != nil {
		http.Error(w, `{"error":"models refreshed but reload failed"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, catalog)
}

func (h *ModelCatalogHandler) HandlePolicies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var update modelcatalog.PolicyUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if err := h.service.UpdatePolicy(r.Context(), update, claims.UserID); err != nil {
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, http.StatusBadRequest)
		return
	}
	catalog, err := h.service.AdminCatalog(r.Context())
	if err != nil {
		http.Error(w, `{"error":"policy saved but reload failed"}`, http.StatusInternalServerError)
		return
	}
	WriteJSON(w, catalog)
}
