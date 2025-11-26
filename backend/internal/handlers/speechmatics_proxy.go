package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	internalAuth "github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/gorilla/websocket"
)

const (
	speechmaticsRealtimeURL = "wss://eu2.rt.speechmatics.com/v2"
)

// SpeechmaticsProxyHandler proxies WebSocket connections to Speechmatics
type SpeechmaticsProxyHandler struct {
	tokenGenerator *internalAuth.TokenGenerator
	billing        *billing.Service
}

// NewSpeechmaticsProxyHandler creates a new Speechmatics proxy handler
func NewSpeechmaticsProxyHandler(billingSvc *billing.Service) (*SpeechmaticsProxyHandler, error) {
	tokenGen, err := internalAuth.NewTokenGenerator()
	if err != nil {
		return nil, fmt.Errorf("failed to create token generator: %w", err)
	}
	return &SpeechmaticsProxyHandler{tokenGenerator: tokenGen, billing: billingSvc}, nil
}

// HandleProxy handles the WebSocket proxy connection
func (h *SpeechmaticsProxyHandler) HandleProxy(w http.ResponseWriter, r *http.Request) {
	// Require authentication when billing is enabled so we can attribute usage
	claims := internalAuth.GetUserClaims(r.Context())
	if h.billing != nil && claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Track usage if user is authenticated
	var userID, tenantID string
	if claims != nil {
		userID = claims.UserID
		tenantID = claims.TenantID
	}

	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade client connection: %v", err)
		return
	}
	defer clientConn.Close()

	// Generate Speechmatics token
	token, err := h.tokenGenerator.GenerateToken()
	if err != nil {
		log.Printf("Failed to generate Speechmatics token: %v", err)
		sendErrorToClient(clientConn, "failed to generate token")
		return
	}

	// Build Speechmatics WebSocket URL
	smURL, _ := url.Parse(speechmaticsRealtimeURL)
	q := smURL.Query()
	q.Set("jwt", token)
	smURL.RawQuery = q.Encode()

	// Connect to Speechmatics
	smConn, _, err := websocket.DefaultDialer.Dial(smURL.String(), nil)
	if err != nil {
		log.Printf("Failed to connect to Speechmatics: %v", err)
		sendErrorToClient(clientConn, "failed to connect to Speechmatics")
		return
	}
	defer smConn.Close()

	log.Printf("Speechmatics proxy connected for user=%s tenant=%s", userID, tenantID)

	// Track transcription start time for usage metering
	startTime := time.Now()

	// Create context for managing goroutines
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var wg sync.WaitGroup
	errChan := make(chan error, 2)

	// Proxy: Client -> Speechmatics
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.proxyClientToSpeechmatics(ctx, clientConn, smConn, errChan)
	}()

	// Proxy: Speechmatics -> Client
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.proxySpeechmaticsToClient(ctx, smConn, clientConn, errChan)
	}()

	// Wait for error or completion
	select {
	case err := <-errChan:
		if err != nil {
			log.Printf("Proxy error: %v", err)
		}
	case <-ctx.Done():
	}

	cancel()
	wg.Wait()

	// Track usage and notify client of balance changes
	duration := time.Since(startTime)
	minutes := duration.Minutes()
	if userID != "" && tenantID != "" && minutes > 0 && h.billing != nil {
		log.Printf("Speechmatics session ended: user=%s tenant=%s duration=%.2f minutes", userID, tenantID, minutes)

		ctx, usageCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer usageCancel()

		cost, err := h.billing.RecordUsage(ctx, &billing.UsageRecord{
			UserID:   userID,
			TenantID: tenantID,
			Action:   "transcription",
			Model:    "speechmatics",
			Quantity: minutes,
		})
		if err != nil {
			log.Printf("failed to record Speechmatics usage: %v", err)
			return
		}
		if balance, err := h.billing.GetUserBalance(ctx, userID); err == nil && balance != nil {
			_ = clientConn.WriteJSON(map[string]interface{}{
				"message": "BalanceUpdated",
				"cost":    cost,
				"balance": balance,
			})
		}
	}
}

// proxyClientToSpeechmatics forwards messages from client to Speechmatics
func (h *SpeechmaticsProxyHandler) proxyClientToSpeechmatics(ctx context.Context, clientConn, smConn *websocket.Conn, errChan chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			messageType, data, err := clientConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					errChan <- fmt.Errorf("client read error: %w", err)
				}
				return
			}

			if err := smConn.WriteMessage(messageType, data); err != nil {
				errChan <- fmt.Errorf("speechmatics write error: %w", err)
				return
			}
		}
	}
}

// proxySpeechmaticsToClient forwards messages from Speechmatics to client
func (h *SpeechmaticsProxyHandler) proxySpeechmaticsToClient(ctx context.Context, smConn, clientConn *websocket.Conn, errChan chan<- error) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			messageType, data, err := smConn.ReadMessage()
			if err != nil {
				if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
					errChan <- fmt.Errorf("speechmatics read error: %w", err)
				}
				return
			}

			if err := clientConn.WriteMessage(messageType, data); err != nil {
				errChan <- fmt.Errorf("client write error: %w", err)
				return
			}
		}
	}
}

func sendErrorToClient(conn *websocket.Conn, msg string) {
	errMsg := map[string]interface{}{
		"message": "Error",
		"type":    "proxy_error",
		"reason":  msg,
	}
	data, _ := json.Marshal(errMsg)
	conn.WriteMessage(websocket.TextMessage, data)
}

// SystemSettingsHandler handles system settings
type SystemSettingsHandler struct {
	mu       sync.RWMutex
	settings SystemSettings
}

// SystemSettings represents system-wide settings
type SystemSettings struct {
	AllowUserAPIKey bool `json:"allow_user_api_key"`
}

var globalSystemSettings = &SystemSettingsHandler{
	settings: SystemSettings{
		AllowUserAPIKey: false, // Default: users cannot use their own API keys
	},
}

// NewSystemSettingsHandler returns the global system settings handler
func NewSystemSettingsHandler() *SystemSettingsHandler {
	return globalSystemSettings
}

// GetSettings returns current system settings
func (h *SystemSettingsHandler) GetSettings() SystemSettings {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.settings
}

// HandleGetSettings returns system settings (public endpoint)
func (h *SystemSettingsHandler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	h.mu.RLock()
	settings := h.settings
	h.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settings)
}

// HandleUpdateSettings updates system settings (admin only)
func (h *SystemSettingsHandler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPatch {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req SystemSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}

	h.mu.Lock()
	h.settings = req
	h.mu.Unlock()

	// Also save to environment or config file for persistence
	if req.AllowUserAPIKey {
		os.Setenv("ALLOW_USER_API_KEY", "true")
	} else {
		os.Setenv("ALLOW_USER_API_KEY", "false")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(req)
}

func init() {
	// Load setting from environment on startup
	if os.Getenv("ALLOW_USER_API_KEY") == "true" {
		globalSystemSettings.settings.AllowUserAPIKey = true
	}
}
