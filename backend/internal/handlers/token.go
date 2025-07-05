package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/dreamtrans/backend/internal/auth"
)

type TokenResponse struct {
	Token string `json:"token"`
}

type TokenHandler struct {
	tokenGen *auth.TokenGenerator
}

func NewTokenHandler() (*TokenHandler, error) {
	tokenGen, err := auth.NewTokenGenerator()
	if err != nil {
		return nil, err
	}
	return &TokenHandler{tokenGen: tokenGen}, nil
}

func (h *TokenHandler) HandleTokenRequest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token, err := h.tokenGen.GenerateToken()
	if err != nil {
		log.Printf("Failed to generate token: %v", err)
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
