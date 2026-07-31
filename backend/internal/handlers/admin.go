// Package handlers implements DreamTrans HTTP and WebSocket endpoints.
package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"math"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

// AdminHandler handles admin-only endpoints
type AdminHandler struct {
	store      *store.PostgresStore
	billing    *billing.Service
	ragCleanup func(tenantID, userID, sessionID string) error
}

// NewAdminHandler creates a new admin handler
func NewAdminHandler(postgresStore *store.PostgresStore, billingSvc *billing.Service) *AdminHandler {
	return &AdminHandler{store: postgresStore, billing: billingSvc}
}

// SetRAGCleanup installs the legacy SQLite RAG cleanup used after PostgreSQL
// has committed an administrative user deletion.
func (h *AdminHandler) SetRAGCleanup(cleanup func(tenantID, userID, sessionID string) error) {
	h.ragCleanup = cleanup
}

type legacyRAGCleanupResult struct {
	Status    string `json:"status"`
	Attempted int    `json:"attempted"`
	Failed    int    `json:"failed"`
}

type deleteAdminUserResponse struct {
	Success          bool                    `json:"success"`
	LegacyRAGCleanup *legacyRAGCleanupResult `json:"legacy_rag_cleanup,omitempty"`
}

type legacyRAGCleanupFailure struct {
	sessionID string
	err       error
}

func writeDeleteAdminUserSuccess(w http.ResponseWriter, cleanup *legacyRAGCleanupResult) {
	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, deleteAdminUserResponse{
		Success:          true,
		LegacyRAGCleanup: cleanup,
	})
}

func deleteAdminUserAndCleanup(
	ctx context.Context,
	tenantID, targetUserID, actorUserID string,
	listSessionIDs func(context.Context, string, string) ([]string, error),
	deleteUser func(context.Context, string, string) error,
	cleanup func(tenantID, userID, sessionID string) error,
) (*legacyRAGCleanupResult, []legacyRAGCleanupFailure, error) {
	var sessionIDs []string
	if cleanup != nil {
		var err error
		sessionIDs, err = listSessionIDs(ctx, tenantID, targetUserID)
		if err != nil {
			return nil, nil, err
		}
	}

	if err := deleteUser(ctx, targetUserID, actorUserID); err != nil {
		return nil, nil, err
	}
	if cleanup == nil {
		return nil, nil, nil
	}

	result := &legacyRAGCleanupResult{
		Status:    "completed",
		Attempted: len(sessionIDs),
	}
	failures := make([]legacyRAGCleanupFailure, 0)
	for _, sessionID := range sessionIDs {
		if err := cleanup(tenantID, targetUserID, sessionID); err != nil {
			result.Failed++
			failures = append(failures, legacyRAGCleanupFailure{
				sessionID: sessionID,
				err:       err,
			})
		}
	}
	if result.Failed > 0 {
		result.Status = "partial_failure"
	}
	return result, failures, nil
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

	claims := auth.GetUserClaims(r.Context())
	var users []models.User
	var total int
	var err error
	if claims.Role == "super_admin" {
		users, total, err = h.store.ListUsers(r.Context(), pageSize, offset)
	} else {
		users, total, err = h.store.ListUsersByTenant(r.Context(), claims.TenantID, pageSize, offset)
	}
	if err != nil {
		http.Error(w, `{"error":"failed to fetch users"}`, http.StatusInternalServerError)
		return
	}

	if users == nil {
		users = []models.User{}
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, UserListResponse{
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
	claims := auth.GetUserClaims(r.Context())
	if claims.Role != "super_admin" && user.TenantID != claims.TenantID {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	// Get tenant info
	tenant, err := h.store.GetTenantByID(r.Context(), user.TenantID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	response := struct {
		User   *models.User   `json:"user"`
		Tenant *models.Tenant `json:"tenant,omitempty"`
	}{
		User:   user,
		Tenant: tenant,
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, response)
}

// UpdateUserRequest represents an admin user update request
type UpdateUserRequest struct {
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	IsActive *bool   `json:"is_active"`
}

func validateAdminUserUpdate(
	req UpdateUserRequest,
	user *models.User,
	claims *auth.UserClaims,
) (*string, int, string) {
	if claims.Role != "super_admin" && user.TenantID != claims.TenantID {
		return nil, http.StatusNotFound, `{"error":"user not found"}`
	}
	if (user.Role == "admin" || user.Role == "super_admin") && claims.Role != "super_admin" {
		return nil, http.StatusForbidden, `{"error":"cannot modify an administrator"}`
	}

	var namePatch *string
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 100 {
			return nil, http.StatusBadRequest, `{"error":"invalid name"}`
		}
		namePatch = &name
	}
	if user.ID == claims.UserID &&
		((req.Role != nil && *req.Role != user.Role) || (req.IsActive != nil && !*req.IsActive)) {
		return nil, http.StatusForbidden, `{"error":"cannot demote or disable yourself"}`
	}
	if req.Role != nil {
		if !validUserRole(*req.Role) {
			return nil, http.StatusBadRequest, `{"error":"invalid role"}`
		}
		if (*req.Role == "admin" || *req.Role == "super_admin") && claims.Role != "super_admin" {
			return nil, http.StatusForbidden, `{"error":"cannot assign administrator role"}`
		}
	}
	return namePatch, 0, ""
}

func writeAdminUserUpdateError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrLastSuperAdmin):
		http.Error(w, `{"error":"at least one active super administrator is required"}`, http.StatusConflict)
	case errors.Is(err, store.ErrAdminUserForbidden):
		http.Error(w, `{"error":"cannot modify this administrator"}`, http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
	default:
		http.Error(w, `{"error":"failed to update user"}`, http.StatusInternalServerError)
	}
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

	// Only a super administrator may modify any elevated account.
	currentClaims := auth.GetUserClaims(ctx)
	if currentClaims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	namePatch, status, message := validateAdminUserUpdate(req, user, currentClaims)
	if status != 0 {
		http.Error(w, message, status)
		return
	}

	if err := h.store.UpdateUserAdminSafe(
		ctx,
		userID,
		currentClaims.UserID,
		namePatch,
		req.Role,
		req.IsActive,
	); err != nil {
		writeAdminUserUpdateError(w, err)
		return
	}
	user, err = h.store.GetUserByID(ctx, userID)
	if err != nil || user == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, user)
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

	currentClaims := auth.GetUserClaims(ctx)
	if currentClaims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if currentClaims.Role != "super_admin" && user.TenantID != currentClaims.TenantID {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	// Super-admin accounts are never deletable through the API, and regular
	// admins cannot delete peer administrators.
	if user.Role == "super_admin" ||
		(user.Role == "admin" && currentClaims.Role != "super_admin") {
		http.Error(w, `{"error":"cannot delete this administrator"}`, http.StatusForbidden)
		return
	}

	// Prevent self-deletion
	if user.ID == currentClaims.UserID {
		http.Error(w, `{"error":"cannot delete yourself"}`, http.StatusForbidden)
		return
	}

	var cancelledJobIDs []string
	deleteUser := func(
		deleteContext context.Context,
		targetUserID, actorUserID string,
	) error {
		var deleteErr error
		cancelledJobIDs, deleteErr =
			h.store.DeleteUserAdminSafeAndCancelIndexJobs(
				deleteContext,
				targetUserID,
				actorUserID,
			)
		return deleteErr
	}
	cleanupResult, cleanupFailures, err := deleteAdminUserAndCleanup(
		ctx,
		user.TenantID,
		userID,
		currentClaims.UserID,
		h.store.ListSessionIDsByOwner,
		deleteUser,
		h.ragCleanup,
	)
	if err != nil {
		if errors.Is(err, store.ErrAdminUserForbidden) {
			http.Error(w, `{"error":"cannot delete this administrator"}`, http.StatusForbidden)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"failed to delete user"}`, http.StatusInternalServerError)
		return
	}
	cancelActiveAIIndexJobs(cancelledJobIDs)

	for _, failure := range cleanupFailures {
		log.Printf(
			"delete legacy RAG data after administrative user deletion: tenant_id=%s user_id=%s session_id=%s: %v",
			strconv.Quote(user.TenantID),
			strconv.Quote(userID),
			strconv.Quote(failure.sessionID),
			strconv.Quote(failure.err.Error()),
		)
	}

	writeDeleteAdminUserSuccess(w, cleanupResult)
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
	encodeJSONResponse(w, TenantListResponse{
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

	var namePatch *string
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 100 {
			http.Error(w, `{"error":"invalid tenant name"}`, http.StatusBadRequest)
			return
		}
		namePatch = &name
	}
	if req.Plan != nil {
		if _, ok := models.PlanLimitsMap[*req.Plan]; !ok {
			http.Error(w, `{"error":"invalid plan"}`, http.StatusBadRequest)
			return
		}
	}
	if req.APIQuotaMonthly != nil {
		if *req.APIQuotaMonthly < -1 || *req.APIQuotaMonthly > 1_000_000_000 {
			http.Error(w, `{"error":"invalid API quota"}`, http.StatusBadRequest)
			return
		}
	}
	if req.StorageQuotaGB != nil {
		if *req.StorageQuotaGB < -1 || *req.StorageQuotaGB > 1_000_000 {
			http.Error(w, `{"error":"invalid storage quota"}`, http.StatusBadRequest)
			return
		}
	}
	if req.MaxSessions != nil {
		if *req.MaxSessions < -1 || *req.MaxSessions > 1_000_000 {
			http.Error(w, `{"error":"invalid session quota"}`, http.StatusBadRequest)
			return
		}
	}

	tenant, err := h.store.UpdateTenantFields(
		ctx,
		tenantID,
		namePatch,
		req.Plan,
		req.APIQuotaMonthly,
		req.StorageQuotaGB,
		req.MaxSessions,
	)
	if err != nil {
		http.Error(w, `{"error":"failed to update tenant"}`, http.StatusInternalServerError)
		return
	}
	if tenant == nil {
		http.Error(w, `{"error":"tenant not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, tenant)
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
	stats["current_month"] = time.Now().UTC().Format("2006-01")

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, stats)
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
		monthKey = time.Now().UTC().Format("2006-01")
	}
	if parsed, err := time.Parse("2006-01", monthKey); err != nil || parsed.Format("2006-01") != monthKey {
		http.Error(w, `{"error":"month must use YYYY-MM format"}`, http.StatusBadRequest)
		return
	}

	tenant, err := h.store.GetTenantByID(r.Context(), tenantID)
	if err != nil {
		http.Error(w, `{"error":"failed to get tenant"}`, http.StatusInternalServerError)
		return
	}
	if tenant == nil {
		http.Error(w, `{"error":"tenant not found"}`, http.StatusNotFound)
		return
	}
	summary, err := h.store.GetUsageSummary(r.Context(), tenantID, monthKey)
	if err != nil {
		http.Error(w, `{"error":"failed to get usage"}`, http.StatusInternalServerError)
		return
	}

	// Get tenant for limits
	limits, ok := models.PlanLimitsMap[tenant.Plan]
	if !ok {
		limits = models.PlanLimitsMap["free"]
	}
	// Tenant-level overrides are the enforced values for these two limits.
	limits.StorageGB = tenant.StorageQuotaGB
	limits.MaxSessions = tenant.MaxSessions

	response := struct {
		*models.UsageSummary
		Limits          models.PlanLimits `json:"limits"`
		Plan            string            `json:"plan"`
		APIQuotaMonthly int               `json:"api_quota_monthly"`
	}{
		UsageSummary:    summary,
		Limits:          limits,
		Plan:            tenant.Plan,
		APIQuotaMonthly: tenant.APIQuotaMonthly,
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, response)
}

// CreateUserRequest represents an admin user creation request
type CreateUserRequest struct {
	Email       string  `json:"email"`
	Password    string  `json:"password"`
	Name        string  `json:"name"`
	Role        string  `json:"role"`
	TenantID    string  `json:"tenant_id,omitempty"`
	Dreampoints float64 `json:"dreampoints"`
}

// HandleCreateUser creates a new user (admin only)
//
//nolint:gocyclo // Validation is intentionally explicit to keep every authorization branch visible.
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

	address, emailErr := mail.ParseAddress(req.Email)
	if req.Email == "" || emailErr != nil || !strings.EqualFold(address.Address, req.Email) {
		http.Error(w, `{"error":"invalid email"}`, http.StatusBadRequest)
		return
	}
	if utf8.RuneCountInString(req.Password) < 10 || len(req.Password) > 72 {
		http.Error(w, `{"error":"password must be 10-72 characters and at most 72 bytes"}`, http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = strings.Split(req.Email, "@")[0]
	}
	if len([]rune(req.Name)) > 100 {
		http.Error(w, `{"error":"name is too long"}`, http.StatusBadRequest)
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	if !validUserRole(req.Role) {
		http.Error(w, `{"error":"invalid role"}`, http.StatusBadRequest)
		return
	}
	if req.Dreampoints < 0 || req.Dreampoints > 1_000_000_000 ||
		math.IsNaN(req.Dreampoints) || math.IsInf(req.Dreampoints, 0) {
		http.Error(w, `{"error":"invalid initial balance"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	claims := auth.GetUserClaims(ctx)
	if claims.Role != "super_admin" {
		req.Dreampoints = 0
		if h.billing != nil {
			initialCredit, creditErr := h.billing.GetFreeTierCredit(ctx)
			if creditErr != nil {
				http.Error(w, `{"error":"failed to load account defaults"}`, http.StatusInternalServerError)
				return
			}
			req.Dreampoints = initialCredit
		}
	}
	tenantID := claims.TenantID
	if claims.Role == "super_admin" && strings.TrimSpace(req.TenantID) != "" {
		tenantID = strings.TrimSpace(req.TenantID)
	}
	tenant, tenantErr := h.store.GetTenantByID(ctx, tenantID)
	if tenantErr != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if tenant == nil {
		http.Error(w, `{"error":"tenant not found"}`, http.StatusBadRequest)
		return
	}

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
	existing, lookupErr := h.store.GetUserByEmail(ctx, req.Email)
	if lookupErr != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
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
		TenantID:      tenantID,
		Email:         req.Email,
		PasswordHash:  passwordHash,
		Name:          req.Name,
		Role:          req.Role,
		IsActive:      true,
		EmailVerified: true,
		Dreampoints:   req.Dreampoints,
	}

	if err := h.store.CreateUser(ctx, user); err != nil {
		http.Error(w, `{"error":"failed to create user"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, user)
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
	encodeJSONResponse(w, map[string]interface{}{"rules": rules})
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

	if err := billing.ValidatePricingRule(&rule); err != nil {
		http.Error(w, `{"error":"invalid pricing rule"}`, http.StatusBadRequest)
		return
	}

	if err := h.billing.CreatePricingRule(r.Context(), &rule); err != nil {
		http.Error(w, `{"error":"failed to create rule"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeHTTPResponse(w, []byte(`{"success":true}`))
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
	if err := billing.ValidatePricingRule(&rule); err != nil {
		http.Error(w, `{"error":"invalid pricing rule"}`, http.StatusBadRequest)
		return
	}

	if err := h.billing.UpdatePricingRule(r.Context(), ruleID, &rule); err != nil {
		if errors.Is(err, billing.ErrPricingRuleNotFound) {
			http.Error(w, `{"error":"pricing rule not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"failed to update rule"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeHTTPResponse(w, []byte(`{"success":true}`))
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
		if errors.Is(err, billing.ErrPricingRuleNotFound) {
			http.Error(w, `{"error":"pricing rule not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"failed to delete rule"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	writeHTTPResponse(w, []byte(`{"success":true}`))
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

	req.UserID = strings.TrimSpace(req.UserID)
	req.Description = strings.TrimSpace(req.Description)
	if req.UserID == "" {
		http.Error(w, `{"error":"user_id required"}`, http.StatusBadRequest)
		return
	}
	if req.Amount == 0 || math.Abs(req.Amount) > 1_000_000_000 ||
		math.IsNaN(req.Amount) || math.IsInf(req.Amount, 0) {
		http.Error(w, `{"error":"invalid amount"}`, http.StatusBadRequest)
		return
	}
	if len([]rune(req.Description)) > 500 {
		http.Error(w, `{"error":"description is too long"}`, http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	claims := auth.GetUserClaims(ctx)
	target, lookupErr := h.store.GetUserByID(ctx, req.UserID)
	if lookupErr != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	if req.Amount > 0 {
		err := h.billing.AddBalance(ctx, req.UserID, req.Amount, "admin_adjustment", req.Description, &claims.UserID)
		if err != nil {
			http.Error(w, `{"error":"failed to adjust balance"}`, http.StatusInternalServerError)
			return
		}
	} else if req.Amount < 0 {
		err := h.billing.DeductBalance(ctx, req.UserID, -req.Amount, "admin_adjustment", req.Description)
		if err != nil {
			http.Error(w, `{"error":"failed to adjust balance"}`, http.StatusConflict)
			return
		}
	}

	// Return updated balance
	balance, err := h.billing.GetUserBalance(ctx, req.UserID)
	if err != nil {
		http.Error(w, `{"error":"failed to load updated balance"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, balance)
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
	target, lookupErr := h.store.GetUserByID(r.Context(), userID)
	if lookupErr != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if target == nil || (claims.Role != "super_admin" && target.TenantID != claims.TenantID) {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	balance, err := h.billing.GetUserBalance(r.Context(), userID)
	if err != nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}

	// Also get recent transactions
	txns, err := h.billing.GetBalanceHistory(r.Context(), userID, 50)
	if err != nil {
		http.Error(w, `{"error":"failed to get balance history"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, map[string]interface{}{
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
	basicStats, err := h.store.GetGlobalStats(ctx)
	if err != nil {
		http.Error(w, `{"error":"failed to get system statistics"}`, http.StatusInternalServerError)
		return
	}

	// Billing stats - provide defaults if billing not enabled or errors
	var billingStats interface{}
	if h.billing != nil {
		stats, err := h.billing.GetSystemStats(ctx)
		if err != nil {
			http.Error(w, `{"error":"failed to get billing statistics"}`, http.StatusInternalServerError)
			return
		}
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

	response := map[string]interface{}{
		"basic":   basicStats,
		"billing": billingStats,
		"time":    time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, response)
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
		encodeJSONResponse(w, map[string]string{
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
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			http.Error(w, `{"error":"failed to get system settings"}`, http.StatusInternalServerError)
			return
		}
		if val != "" {
			// Remove quotes from JSON string
			settings[key] = strings.Trim(val, `"`)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	encodeJSONResponse(w, settings)
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

	validated := make(map[string]string, len(settings))
	for key, value := range settings {
		value = strings.TrimSpace(value)
		switch key {
		case "billing_enabled", "allow_negative_balance", "allow_user_api_key":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				http.Error(w, `{"error":"invalid boolean system setting"}`, http.StatusBadRequest)
				return
			}
			validated[key] = strconv.FormatBool(parsed)
		case "free_tier_dreampoints":
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) ||
				parsed < 0 || parsed > 1_000_000_000 {
				http.Error(w, `{"error":"invalid free tier credit"}`, http.StatusBadRequest)
				return
			}
			validated[key] = strconv.FormatFloat(parsed, 'f', -1, 64)
		default:
			http.Error(w, `{"error":"unknown system setting"}`, http.StatusBadRequest)
			return
		}
	}
	if err := h.billing.SetSystemSettings(ctx, validated, &claims.UserID); err != nil {
		http.Error(w, `{"error":"failed to update settings"}`, http.StatusInternalServerError)
		return
	}
	if value, ok := validated["allow_user_api_key"]; ok {
		allow, _ := strconv.ParseBool(value)
		SetAllowUserAPIKey(allow)
	}

	w.Header().Set("Content-Type", "application/json")
	writeHTTPResponse(w, []byte(`{"success":true}`))
}

func validUserRole(role string) bool {
	switch role {
	case "user", "admin", "super_admin":
		return true
	default:
		return false
	}
}
