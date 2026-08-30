package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
)

// AdjustBalanceRequest credits or debits a wallet by hand.
type AdjustBalanceRequest struct {
	UserID        string  `json:"user_id"`
	Amount        float64 `json:"amount"`
	Description   string  `json:"description"`
	AllowNegative bool    `json:"allow_negative"`
}

func billingAdminErrorStatus(err error) int {
	switch {
	case errors.Is(err, billing.ErrInvalidBillingInput):
		return http.StatusBadRequest
	case errors.Is(err, billing.ErrFeatureNotIncluded):
		return http.StatusForbidden
	case errors.Is(err, billing.ErrInsufficientBalance):
		return http.StatusPaymentRequired
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, billing.ErrAccountNotFound), errors.Is(err, billing.ErrPlanNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeBillingAdminError(w http.ResponseWriter, operation string, err error) {
	status := billingAdminErrorStatus(err)
	switch status {
	case http.StatusBadRequest, http.StatusForbidden, http.StatusPaymentRequired:
		http.Error(w, `{"error":"`+safeJSONError(err)+`"}`, status)
	case http.StatusNotFound:
		http.Error(w, `{"error":"billing resource not found"}`, status)
	default:
		log.Printf("%s: %v", operation, err)
		http.Error(w, `{"error":"billing operation failed"}`, status)
	}
}

func requireActor(w http.ResponseWriter, r *http.Request) (*auth.UserClaims, bool) {
	claims := auth.GetUserClaims(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return nil, false
	}
	return claims, true
}

// ---------------------------------------------------------------------------
// Costs & markup
// ---------------------------------------------------------------------------

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

func (h *AdminHandler) HandleBillingMarkup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input billing.MarkupInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims, ok := requireActor(w, r)
	if !ok {
		return
	}
	catalog, err := h.billing.UpdateMarkup(r.Context(), input, claims.UserID)
	if err != nil {
		writeBillingAdminError(w, "update markup", err)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingModelCost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var input billing.ModelCostPerMillion
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	claims, ok := requireActor(w, r)
	if !ok {
		return
	}
	catalog, err := h.billing.UpsertModelCost(r.Context(), input, claims.UserID)
	if err != nil {
		writeBillingAdminError(w, "update model cost", err)
		return
	}
	WriteJSON(w, catalog)
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
	claims, ok := requireActor(w, r)
	if !ok {
		return
	}
	catalog, err := h.billing.UpsertProviderCostOverride(r.Context(), input, claims.UserID)
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
	claims, ok := requireActor(w, r)
	if !ok {
		return
	}
	catalog, err := h.billing.DeleteProviderCostOverride(
		r.Context(),
		r.URL.Query().Get("provider"), r.URL.Query().Get("sku"),
		r.URL.Query().Get("service"), r.URL.Query().Get("unit_type"),
		claims.UserID,
	)
	if err != nil {
		writeBillingAdminError(w, "delete provider cost override", err)
		return
	}
	WriteJSON(w, catalog)
}

func (h *AdminHandler) HandleBillingAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	analytics, err := h.billing.GetBillingAnalytics(r.Context(), r.URL.Query().Get("month"))
	if err != nil {
		writeBillingAdminError(w, "load billing analytics", err)
		return
	}
	WriteJSON(w, analytics)
}

// ---------------------------------------------------------------------------
// Plans & top-up tiers
// ---------------------------------------------------------------------------

func (h *AdminHandler) HandlePlans(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		plans, err := h.billing.ListPlans(r.Context(), true)
		if err != nil {
			writeBillingAdminError(w, "list plans", err)
			return
		}
		WriteJSON(w, map[string]any{"plans": plans})
	case http.MethodPut, http.MethodPost:
		var plan billing.Plan
		if err := json.NewDecoder(r.Body).Decode(&plan); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		claims, ok := requireActor(w, r)
		if !ok {
			return
		}
		saved, err := h.billing.UpsertPlan(r.Context(), plan, claims.UserID)
		if err != nil {
			writeBillingAdminError(w, "upsert plan", err)
			return
		}
		WriteJSON(w, saved)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) HandleTopupTiers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		tiers, err := h.billing.ListTopupTiers(r.Context(), true)
		if err != nil {
			writeBillingAdminError(w, "list top-up tiers", err)
			return
		}
		WriteJSON(w, map[string]any{"tiers": tiers})
	case http.MethodPut, http.MethodPost:
		var tier billing.TopupTier
		if err := json.NewDecoder(r.Body).Decode(&tier); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		claims, ok := requireActor(w, r)
		if !ok {
			return
		}
		if err := h.billing.UpsertTopupTier(r.Context(), tier, claims.UserID); err != nil {
			writeBillingAdminError(w, "upsert top-up tier", err)
			return
		}
		tiers, err := h.billing.ListTopupTiers(r.Context(), true)
		if err != nil {
			writeBillingAdminError(w, "list top-up tiers", err)
			return
		}
		WriteJSON(w, map[string]any{"tiers": tiers})
	case http.MethodDelete:
		amount, err := strconv.ParseFloat(r.URL.Query().Get("amount_usd"), 64)
		if err != nil {
			http.Error(w, `{"error":"amount_usd is required"}`, http.StatusBadRequest)
			return
		}
		claims, ok := requireActor(w, r)
		if !ok {
			return
		}
		if err := h.billing.DeleteTopupTier(r.Context(), amount, claims.UserID); err != nil {
			writeBillingAdminError(w, "delete top-up tier", err)
			return
		}
		tiers, err := h.billing.ListTopupTiers(r.Context(), true)
		if err != nil {
			writeBillingAdminError(w, "list top-up tiers", err)
			return
		}
		WriteJSON(w, map[string]any{"tiers": tiers})
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ---------------------------------------------------------------------------
// Customers
// ---------------------------------------------------------------------------

func (h *AdminHandler) HandleCustomers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	customers, total, err := h.billing.ListCustomers(r.Context(), r.URL.Query().Get("search"), limit, offset)
	if err != nil {
		writeBillingAdminError(w, "list customers", err)
		return
	}
	WriteJSON(w, map[string]any{"customers": customers, "total": total})
}

// customerUserID extracts {id} from /api/admin/customers/{id}[/action].
func customerUserID(path string) (string, string) {
	trimmed := strings.TrimPrefix(path, "/api/admin/customers/")
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", ""
	}
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	return parts[0], action
}

// HandleCustomer serves /api/admin/customers/{id} (GET detail) and its
// sub-actions: POST grant, POST adjust, PUT plan.
func (h *AdminHandler) HandleCustomer(w http.ResponseWriter, r *http.Request) {
	userID, action := customerUserID(r.URL.Path)
	if userID == "" {
		http.Error(w, `{"error":"user id required"}`, http.StatusBadRequest)
		return
	}
	target, err := h.store.GetUserByID(r.Context(), userID)
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if target == nil {
		http.Error(w, `{"error":"user not found"}`, http.StatusNotFound)
		return
	}
	claims, ok := requireActor(w, r)
	if !ok {
		return
	}
	switch {
	case action == "" && r.Method == http.MethodGet:
		h.writeCustomerDetail(w, r, userID)
	case action == "grant" && r.Method == http.MethodPost:
		var req struct {
			AmountUSD  float64 `json:"amount_usd"`
			Kind       string  `json:"kind"`
			ExpiresAt  string  `json:"expires_at"`
			ExpiryDays int     `json:"expiry_days"`
			Note       string  `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		input := billing.GrantInput{UserID: userID, Kind: req.Kind, AmountUSD: req.AmountUSD, Note: req.Note, CreatedBy: claims.UserID}
		if input.Kind == "" {
			input.Kind = billing.GrantPromo
		}
		if expires, parseErr := parseOptionalRFC3339(req.ExpiresAt); parseErr != nil {
			http.Error(w, `{"error":"expires_at must use RFC3339"}`, http.StatusBadRequest)
			return
		} else if expires != nil {
			input.ExpiresAt = expires
		} else if req.ExpiryDays > 0 {
			expiry := timeNow().Add(timeDays(req.ExpiryDays))
			input.ExpiresAt = &expiry
		}
		if _, err := h.billing.AddGrant(r.Context(), input); err != nil {
			writeBillingAdminError(w, "add grant", err)
			return
		}
		h.writeCustomerDetail(w, r, userID)
	case action == "adjust" && r.Method == http.MethodPost:
		var req AdjustBalanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		if _, err := h.billing.AdjustWallet(r.Context(), billing.WalletAdjustment{
			UserID: userID, AmountUSD: req.Amount, Description: req.Description,
			CreatedBy: claims.UserID, AllowNegative: req.AllowNegative,
		}); err != nil {
			writeBillingAdminError(w, "adjust wallet", err)
			return
		}
		h.writeCustomerDetail(w, r, userID)
	case action == "plan" && (r.Method == http.MethodPut || r.Method == http.MethodPost):
		var req struct {
			PlanCode              string   `json:"plan_code"`
			MemberUntil           string   `json:"member_until"`
			CustomDiscountPercent *float64 `json:"custom_discount_percent"`
			CustomMarkupPercent   *float64 `json:"custom_markup_percent"`
			Note                  string   `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
			return
		}
		memberUntil, parseErr := parseOptionalRFC3339(req.MemberUntil)
		if parseErr != nil {
			http.Error(w, `{"error":"member_until must use RFC3339"}`, http.StatusBadRequest)
			return
		}
		if _, err := h.billing.SetAccountPlan(r.Context(), billing.PlanAssignment{
			UserID: userID, PlanCode: req.PlanCode, MemberUntil: memberUntil,
			CustomDiscountPercent: req.CustomDiscountPercent, CustomMarkupPercent: req.CustomMarkupPercent,
			Actor: claims.UserID, Note: req.Note,
		}); err != nil {
			writeBillingAdminError(w, "set account plan", err)
			return
		}
		h.writeCustomerDetail(w, r, userID)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func (h *AdminHandler) writeCustomerDetail(w http.ResponseWriter, r *http.Request, userID string) {
	summary, err := h.billing.GetAccountSummary(r.Context(), userID)
	if err != nil {
		writeBillingAdminError(w, "load account", err)
		return
	}
	ledger, err := h.billing.GetBalanceHistory(r.Context(), userID, 100)
	if err != nil {
		writeBillingAdminError(w, "load ledger", err)
		return
	}
	usage, err := h.billing.GetAdminUsage(r.Context(), userID, 100)
	if err != nil {
		writeBillingAdminError(w, "load usage", err)
		return
	}
	payments, err := h.billing.ListPayments(r.Context(), userID, 50)
	if err != nil {
		writeBillingAdminError(w, "load payments", err)
		return
	}
	WriteJSON(w, map[string]any{
		"account": summary, "ledger": ledger, "usage": usage, "payments": payments,
	})
}

func safeJSONError(err error) string {
	if err == nil {
		return ""
	}
	message, _ := json.Marshal(err.Error())
	return strings.Trim(string(message), `"`)
}
