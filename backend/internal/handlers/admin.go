package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

// AdminHandler handles admin-only endpoints
type AdminHandler struct {
	store   *store.PostgresStore
	billing *billing.Service
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(store *store.PostgresStore, billingSvc *billing.Service) *AdminHandler {
	return &AdminHandler{store: store, billing: billingSvc}
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

// CreateUserRequest represents an admin user creation request
type CreateUserRequest struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Name        string  `json:"name"`
	Role        string  `json:"role"`
	Dreampoints float64 `json:"dreampoints"`
}

// HandleCreateUser creates a new user (admin only)
func (h *AdminHandler) HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req CreateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.Name = strings.TrimSpace(req.Name)

	if req.Email == "" || !strings.Contains(req.Email, "@") {
		http.Error(w, `{"error":"invalid email"}`, http.StatusBadRequest)
		return
	}
	if len(req.Password) < 6 {
		http.Error(w, `{"error":"password must be at least 6 characters"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = strings.Split(req.Email, "@")[0]
	}
	if req.Role == "" {
		req.Role = "user"
	}

	ctx := r.Context()
	claims := auth.GetUserClaims(ctx)

	// Only super_admin can create admin/super_admin users
	if req.Role == "super_admin" && claims.Role != "super_admin" {
		http.Error(w, `{"error":"cannot create super admin"}`, http.StatusForbidden)
		return
	}
	if req.Role == "admin" && claims.Role != "super_admin" {
		http.Error(w, `{"error":"cannot create admin user"}`, http.StatusForbidden)
		return
	}

	// Check if user exists
	existing, _ := h.store.GetUserByEmail(ctx, req.Email)
	if existing != nil {
		http.Error(w, `{"error":"email already registered"}`, http.StatusConflict)
		return
	}

	// Hash password
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		http.Error(w, `{"error":"failed to hash password"}`, http.StatusInternalServerError)
		return
	}

	// Create user with default tenant
	user := &models.User{
		TenantID:      "00000000-0000-0000-0000-000000000001",
		Email:         req.Email,
		PasswordHash:  passwordHash,
		Name:          req.Name,
		Role:          req.Role,
		IsActive:      true,
		EmailVerified: true,
	}

	if err := h.store.CreateUser(ctx, user); err != nil {
		http.Error(w, `{"error":"failed to create user"}`, http.StatusInternalServerError)
		return
	}

	// Set initial Dreampoints
	if req.Dreampoints > 0 && h.billing != nil {
		h.billing.AddBalance(ctx, user.ID, req.Dreampoints, "admin_adjustment", "Initial balance from admin", &claims.UserID)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// HandleGetPricingRules returns all pricing rules
func (h *AdminHandler) HandleGetPricingRules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	rules, err := h.billing.GetAllPricingRules(r.Context())
	if err != nil {
		http.Error(w, `{"error":"failed to get pricing rules"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"rules": rules})
}

// HandleCreatePricingRule creates a new pricing rule
func (h *AdminHandler) HandleCreatePricingRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	var rule billing.PricingRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if rule.RuleType == "" || rule.UnitType == "" {
		http.Error(w, `{"error":"rule_type and unit_type required"}`, http.StatusBadRequest)
		return
	}

	if err := h.billing.CreatePricingRule(r.Context(), &rule); err != nil {
		http.Error(w, `{"error":"failed to create rule"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// HandleUpdatePricingRule updates a pricing rule
func (h *AdminHandler) HandleUpdatePricingRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"rule id required"}`, http.StatusBadRequest)
		return
	}
	ruleID := parts[4]

	var rule billing.PricingRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if err := h.billing.UpdatePricingRule(r.Context(), ruleID, &rule); err != nil {
		http.Error(w, `{"error":"failed to update rule"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// HandleDeletePricingRule deletes a pricing rule
func (h *AdminHandler) HandleDeletePricingRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"rule id required"}`, http.StatusBadRequest)
		return
	}
	ruleID := parts[4]

	if err := h.billing.DeletePricingRule(r.Context(), ruleID); err != nil {
		http.Error(w, `{"error":"failed to delete rule"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}

// AdjustBalanceRequest for admin balance adjustments
type AdjustBalanceRequest struct {
	UserID      string  `json:"user_id"`
	Amount      float64 `json:"amount"`
	Description string  `json:"description"`
}

// HandleAdjustBalance adds or removes Dreampoints for a user
func (h *AdminHandler) HandleAdjustBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	var req AdjustBalanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	if req.UserID == "" {
		http.Error(w, `{"error":"user_id required"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	claims := auth.GetUserClaims(ctx)

	if req.Amount > 0 {
		err := h.billing.AddBalance(ctx, req.UserID, req.Amount, "admin_adjustment", req.Description, &claims.UserID)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	} else if req.Amount < 0 {
		err := h.billing.DeductBalance(ctx, req.UserID, -req.Amount, "admin_adjustment", req.Description)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
			return
		}
	}

	// Return updated balance
	balance, _ := h.billing.GetUserBalance(ctx, req.UserID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(balance)
}

// HandleGetUserBalance gets a user's Dreampoint balance
func (h *AdminHandler) HandleGetUserBalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 5 {
		http.Error(w, `{"error":"user id required"}`, http.StatusBadRequest)
		return
	}
	userID := parts[4]

	balance, err := h.billing.GetUserBalance(r.Context(), userID)
	if err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	// Also get recent transactions
	txns, _ := h.billing.GetBalanceHistory(r.Context(), userID, 50)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"balance":      balance,
		"transactions": txns,
	})
}

// HandleGetSystemStats returns comprehensive system statistics
func (h *AdminHandler) HandleGetSystemStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()

	// Basic stats from store
	basicStats, _ := h.store.GetGlobalStats(ctx)

	// Billing stats - provide defaults if billing not enabled or errors
	var billingStats interface{}
	if h.billing != nil {
		stats, err := h.billing.GetSystemStats(ctx)
		if err == nil && stats != nil {
			billingStats = stats
		} else {
			billingStats = map[string]interface{}{
				"total_dreampoints": 0,
				"total_used":        0,
				"total_users":       0,
				"active_users":      0,
				"usage_by_action":   map[string]float64{},
				"usage_by_model":    map[string]float64{},
			}
		}
	} else {
		billingStats = map[string]interface{}{
			"total_dreampoints": 0,
			"total_used":        0,
			"total_users":       0,
			"active_users":      0,
			"usage_by_action":   map[string]float64{},
			"usage_by_model":    map[string]float64{},
		}
	}

	response := map[string]interface{}{
		"basic":   basicStats,
		"billing": billingStats,
		"time":    time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleGetSystemSettings returns system settings
func (h *AdminHandler) HandleGetSystemSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Return defaults if billing not enabled
	if h.billing == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"billing_enabled":        "false",
			"free_tier_dreampoints":  "100",
			"allow_negative_balance": "false",
			"allow_user_api_key":     "false",
		})
		return
	}

	ctx := r.Context()

	// Start with defaults
	settings := map[string]string{
		"billing_enabled":        "true",
		"free_tier_dreampoints":  "100",
		"allow_negative_balance": "false",
		"allow_user_api_key":     "false",
	}

	keys := []string{"billing_enabled", "free_tier_dreampoints", "allow_negative_balance", "allow_user_api_key"}

	for _, key := range keys {
		val, err := h.billing.GetSystemSetting(ctx, key)
		if err == nil && val != "" {
			// Remove quotes from JSON string
			settings[key] = strings.Trim(val, `"`)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// HandleUpdateSystemSettings updates system settings
func (h *AdminHandler) HandleUpdateSystemSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if h.billing == nil {
		http.Error(w, `{"error":"billing not enabled"}`, http.StatusServiceUnavailable)
		return
	}

	var settings map[string]string
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	claims := auth.GetUserClaims(ctx)

	for key, value := range settings {
		// Store as JSON string
		jsonValue := `"` + value + `"`
		if err := h.billing.SetSystemSetting(ctx, key, jsonValue, &claims.UserID); err != nil {
			http.Error(w, `{"error":"failed to update setting"}`, http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"success":true}`))
}
