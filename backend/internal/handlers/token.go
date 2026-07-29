package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
)

type TokenResponse struct {
	Token string `json:"token"`
}

type TokenHandler struct {
	tokenGen      *auth.TokenGenerator
	billing       *billing.Service
	billedMinutes float64
}

func NewTokenHandler(billingServices ...*billing.Service) (*TokenHandler, error) {
	tokenGen, err := auth.NewTokenGenerator()
	if err != nil {
		return nil, err
	}
	var billingSvc *billing.Service
	if len(billingServices) > 0 {
		billingSvc = billingServices[0]
	}
	billedMinutes := 10.0
	if value, parseErr := strconv.ParseFloat(os.Getenv("CLASSIC_TOKEN_BILLING_MINUTES"), 64); parseErr == nil && value > 0 && value <= 10 {
		billedMinutes = value
	}
	return &TokenHandler{tokenGen: tokenGen, billing: billingSvc, billedMinutes: billedMinutes}, nil
}

func (h *TokenHandler) HandleTokenRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if h.billing != nil {
		if claims == nil {
			http.Error(w, `{"error":"authenticated user required for billing"}`, http.StatusUnauthorized)
			return
		}
		if !strings.EqualFold(
			strings.TrimSpace(os.Getenv("ALLOW_UNMETERED_CLASSIC_TOKEN_WITH_BILLING")),
			"true",
		) {
			http.Error(
				w,
				`{"error":"direct Speechmatics tokens are disabled when billing is enabled; use /ws/speechmatics"}`,
				http.StatusForbidden,
			)
			return
		}
		allowed, billingErr := h.billing.CanAffordUsage(r.Context(), claims.UserID, &billing.UsageRecord{
			Action: "transcription", Model: "speechmatics-classic-token", Quantity: h.billedMinutes,
		})
		if billingErr != nil {
			http.Error(w, `{"error":"billing service unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		if !allowed {
			http.Error(w, `{"error":"insufficient balance"}`, http.StatusPaymentRequired)
			return
		}
	}

	reservationKey := ""
	if h.billing != nil && claims != nil {
		reservationID, reservationErr := normalizeClientSegmentID("")
		if reservationErr != nil {
			http.Error(w, `{"error":"failed to create usage reservation"}`, http.StatusInternalServerError)
			return
		}
		reservationKey = "classic-token:" + reservationID
		if _, reservationErr := h.billing.RecordUsage(r.Context(), &billing.UsageRecord{
			UserID: claims.UserID, TenantID: claims.TenantID,
			Action: "transcription", Model: "speechmatics-classic-token",
			Quantity: h.billedMinutes, IdempotencyKey: reservationKey,
		}); reservationErr != nil {
			log.Printf("failed to reserve classic transcription usage: %v", reservationErr)
			http.Error(w, `{"error":"usage charge failed or balance is insufficient"}`, http.StatusPaymentRequired)
			return
		}
	}

	token, err := h.tokenGen.GenerateTokenContext(r.Context())
	if err != nil {
		log.Printf("Failed to generate token: %v", err)
		if h.billing != nil && reservationKey != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if refundErr := h.billing.RefundUsage(ctx, reservationKey, "upstream token generation failed"); refundErr != nil {
				log.Printf("failed to refund classic token reservation: %v", refundErr)
			}
			cancel()
		}
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	response := TokenResponse{Token: token}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Failed to encode response: %v", err)
	}
}
