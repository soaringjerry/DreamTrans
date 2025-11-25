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

// AdminHandler handles admin-only endpoints
type AdminHandler struct {
	store *store.PostgresStore
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(store *store.PostgresStore) *AdminHandler {
	return &AdminHandler{store: store}
}

// UserListResponse represents a paginated user list
type UserListResponse struct {
	Users    []models.User `json:"users"`
	Total    int           `json:"total"`
	Page     int           `json:"page"`
	PageSize int           `json:"page_size"`
}

// TenantListResponse represents a paginated tenant list
type TenantListResponse struct {
	Tenants  []models.Tenant `json:"tenants"`
	Total    int             `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// HandleListUsers lists all users (admin only)
func (h *AdminHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse pagination
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	users, total, err := h.store.ListUsers(r.Context(), pageSize, offset)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch users"}`, http.StatusInternalServerError)
		return
	}

	if users == nil {
		users = []models.User{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(UserListResponse{
		Users:    users,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// HandleGetUser gets a specific user
func (h *AdminHandler) HandleGetUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract user ID from /api/admin/users/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"user id required"}`, http.StatusBadRequest)
		return
	}
	userID := parts[4]

	user, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	// Get tenant info
	tenant, _ := h.store.GetTenantByID(r.Context(), user.TenantID)

	response := struct {
		User   *models.User   `json:"user"`
		Tenant *models.Tenant `json:"tenant,omitempty"`
	}{
		User:   user,
		Tenant: tenant,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UpdateUserRequest represents an admin user update request
type UpdateUserRequest struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

// HandleUpdateUser updates a user (admin only)
func (h *AdminHandler) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract user ID
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"user id required"}`, http.StatusBadRequest)
		return
	}
	userID := parts[4]

	var req UpdateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	user, err := h.store.GetUserByID(ctx, userID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	// Prevent modifying super_admin unless you're a super_admin
	currentClaims := auth.GetUserClaims(ctx)
	if user.Role == "super_admin" && currentClaims.Role != "super_admin" {
		http.Error(w, `{"error":"cannot modify super admin"}`, http.StatusForbidden)
		return
	}

	if req.Name != nil {
		user.Name = *req.Name
	}
	if req.Role != nil {
		// Only super_admin can assign super_admin role
		if *req.Role == "super_admin" && currentClaims.Role != "super_admin" {
			http.Error(w, `{"error":"cannot assign super admin role"}`, http.StatusForbidden)
			return
		}
		user.Role = *req.Role
	}
	if req.IsActive != nil {
		user.IsActive = *req.IsActive
	}

	if err := h.store.UpdateUser(ctx, user); err != nil {
		http.Error(w, `{"error":"failed to update user"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// HandleDeleteUser deletes a user (admin only)
func (h *AdminHandler) HandleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract user ID
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"user id required"}`, http.StatusBadRequest)
		return
	}
	userID := parts[4]

	ctx := r.Context()
	user, err := h.store.GetUserByID(ctx, userID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if user == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	// Prevent deleting super_admin
	if user.Role == "super_admin" {
		http.Error(w, `{"error":"cannot delete super admin"}`, http.StatusForbidden)
		return
	}

	// Prevent self-deletion
	currentClaims := auth.GetUserClaims(ctx)
	if user.ID == currentClaims.UserID {
		http.Error(w, `{"error":"cannot delete yourself"}`, http.StatusForbidden)
		return
	}

	if err := h.store.DeleteUser(ctx, userID); err != nil {
		http.Error(w, `{"error":"failed to delete user"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// HandleListTenants lists all tenants (admin only)
func (h *AdminHandler) HandleListTenants(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	offset := (page - 1) * pageSize

	tenants, total, err := h.store.ListTenants(r.Context(), pageSize, offset)
	if err != nil {
		http.Error(w, `{"error":"failed to fetch tenants"}`, http.StatusInternalServerError)
		return
	}

	if tenants == nil {
		tenants = []models.Tenant{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(TenantListResponse{
		Tenants:  tenants,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	})
}

// UpdateTenantRequest represents a tenant update request
type UpdateTenantRequest struct {
	Name            *string `json:"name"`
	Plan            *string `json:"plan"`
	APIQuotaMonthly *int    `json:"api_quota_monthly"`
	StorageQuotaGB  *int    `json:"storage_quota_gb"`
	MaxSessions     *int    `json:"max_sessions"`
}

// HandleUpdateTenant updates a tenant (admin only)
func (h *AdminHandler) HandleUpdateTenant(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Extract tenant ID from /api/admin/tenants/{id}
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"tenant id required"}`, http.StatusBadRequest)
		return
	}
	tenantID := parts[4]

	var req UpdateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	tenant, err := h.store.GetTenantByID(ctx, tenantID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if tenant == nil {
		http.Error(w, `{"error":"tenant not found"}`, http.StatusNotFound)
		return
	}

	if req.Name != nil {
		tenant.Name = *req.Name
	}
	if req.Plan != nil {
		tenant.Plan = *req.Plan
	}
	if req.APIQuotaMonthly != nil {
		tenant.APIQuotaMonthly = *req.APIQuotaMonthly
	}
	if req.StorageQuotaGB != nil {
		tenant.StorageQuotaGB = *req.StorageQuotaGB
	}
	if req.MaxSessions != nil {
		tenant.MaxSessions = *req.MaxSessions
	}

	if err := h.store.UpdateTenant(ctx, tenant); err != nil {
		http.Error(w, `{"error":"failed to update tenant"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tenant)
}

// HandleGetStats returns global statistics
func (h *AdminHandler) HandleGetStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	stats, err := h.store.GetGlobalStats(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to get stats"}`, http.StatusInternalServerError)
		return
	}

	// Add current month key
	stats["current_month"] = time.Now().Format("2006-01")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleGetUsage returns usage summary for a tenant
func (h *AdminHandler) HandleGetUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID == "" {
		// Use current user's tenant
		claims := auth.GetUserClaims(r.Context())
		if claims != nil {
			tenantID = claims.TenantID
		}
	}

	if tenantID == "" {
		http.Error(w, `{"error":"tenant_id required"}`, http.StatusBadRequest)
		return
	}

	monthKey := r.URL.Query().Get("month")
	if monthKey == "" {
		monthKey = time.Now().Format("2006-01")
	}

	summary, err := h.store.GetUsageSummary(r.Context(), tenantID, monthKey)
	if err != nil {
		http.Error(w, `{"error":"failed to get usage"}`, http.StatusInternalServerError)
		return
	}

	// Get tenant for limits
	tenant, _ := h.store.GetTenantByID(r.Context(), tenantID)
	limits := models.PlanLimitsMap["free"]
	if tenant != nil {
		limits = models.PlanLimitsMap[tenant.Plan]
	}

	response := struct {
		*models.UsageSummary
		Limits models.PlanLimits `json:"limits"`
		Plan   string            `json:"plan"`
	}{
		UsageSummary: summary,
		Limits:       limits,
		Plan:         tenant.Plan,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
