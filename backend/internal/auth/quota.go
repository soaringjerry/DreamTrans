package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

// QuotaMiddleware handles quota checking for API requests
type QuotaMiddleware struct {
	store quotaStore
}

type quotaStore interface {
	ConsumeAPIRequest(context.Context, string, string) (store.APIQuotaStatus, error)
	GetTenantByID(context.Context, string) (*models.Tenant, error)
	GetUsageSummary(context.Context, string, string) (*models.UsageSummary, error)
	CountActiveSessionsByUser(context.Context, string) (int, error)
}

// NewQuotaMiddleware creates a new quota middleware
func NewQuotaMiddleware(postgresStore *store.PostgresStore) *QuotaMiddleware {
	return newQuotaMiddleware(postgresStore)
}

func newQuotaMiddleware(quotaBackend quotaStore) *QuotaMiddleware {
	return &QuotaMiddleware{store: quotaBackend}
}

// QuotaError response
type QuotaError struct {
	Error     string `json:"error"`
	Limit     int    `json:"limit"`
	Used      int    `json:"used"`
	Plan      string `json:"plan"`
	ResetDate string `json:"reset_date"`
}

// CheckAPIRequests atomically consumes one request from the tenant's explicit
// monthly API quota. It is intended for provider-facing endpoints only.
func (m *QuotaMiddleware) CheckAPIRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		claims := GetUserClaims(r.Context())
		if claims == nil {
			next.ServeHTTP(w, r)
			return
		}

		status, err := m.store.ConsumeAPIRequest(r.Context(), claims.TenantID, claims.UserID)
		if errors.Is(err, store.ErrAPIQuota) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(QuotaError{
				Error:     "monthly API quota exceeded",
				Limit:     status.Limit,
				Used:      int(status.Used),
				Plan:      status.Plan,
				ResetDate: getNextMonthStart(),
			})
			return
		}
		if err != nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CheckTranscription checks if user has transcription quota remaining
func (m *QuotaMiddleware) CheckTranscription(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserClaims(r.Context())
		if claims == nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		tenant, err := m.store.GetTenantByID(ctx, claims.TenantID)
		if err != nil || tenant == nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		// Check if unlimited (-1)
		limits := models.PlanLimitsMap[tenant.Plan]
		if limits.TranscriptionMinutes < 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Get current usage
		monthKey := time.Now().UTC().Format("2006-01")
		summary, err := m.store.GetUsageSummary(ctx, tenant.ID, monthKey)
		if err != nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		if int(summary.TranscriptionMinutes) >= limits.TranscriptionMinutes {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(QuotaError{
				Error:     "transcription quota exceeded",
				Limit:     limits.TranscriptionMinutes,
				Used:      int(summary.TranscriptionMinutes),
				Plan:      tenant.Plan,
				ResetDate: getNextMonthStart(),
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// CheckRAGQueries checks if user has RAG query quota remaining
func (m *QuotaMiddleware) CheckRAGQueries(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims := GetUserClaims(r.Context())
		if claims == nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		tenant, err := m.store.GetTenantByID(ctx, claims.TenantID)
		if err != nil || tenant == nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		limits := models.PlanLimitsMap[tenant.Plan]
		if limits.RAGQueries < 0 {
			next.ServeHTTP(w, r)
			return
		}

		monthKey := time.Now().UTC().Format("2006-01")
		summary, err := m.store.GetUsageSummary(ctx, tenant.ID, monthKey)
		if err != nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		if summary.RAGQueryCount >= limits.RAGQueries {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(QuotaError{
				Error:     "RAG query quota exceeded",
				Limit:     limits.RAGQueries,
				Used:      summary.RAGQueryCount,
				Plan:      tenant.Plan,
				ResetDate: getNextMonthStart(),
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// CheckSessions checks if user can create more sessions
func (m *QuotaMiddleware) CheckSessions(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only check on POST (session creation)
		if r.Method != http.MethodPost {
			next.ServeHTTP(w, r)
			return
		}

		claims := GetUserClaims(r.Context())
		if claims == nil {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		tenant, err := m.store.GetTenantByID(ctx, claims.TenantID)
		if err != nil || tenant == nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		limit := tenant.MaxSessions
		if limit < 0 {
			next.ServeHTTP(w, r)
			return
		}

		// Count only active/paused sessions. Completed history must not consume
		// a concurrent-session slot.
		activeCount, err := m.store.CountActiveSessionsByUser(ctx, claims.UserID)
		if err != nil {
			http.Error(w, `{"error":"quota service unavailable"}`, http.StatusServiceUnavailable)
			return
		}

		if activeCount >= limit {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusPaymentRequired)
			_ = json.NewEncoder(w).Encode(QuotaError{
				Error: "session limit reached",
				Limit: limit,
				Used:  activeCount,
				Plan:  tenant.Plan,
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}

// UsageTracker tracks API usage
type UsageTracker struct {
	store *store.PostgresStore
}

// NewUsageTracker creates a new usage tracker
func NewUsageTracker(postgresStore *store.PostgresStore) *UsageTracker {
	return &UsageTracker{store: postgresStore}
}

// TrackTranscription records transcription usage
func (t *UsageTracker) TrackTranscription(ctx context.Context, userID, tenantID string, minutes float64, sessionID *string) error {
	return t.store.CreateUsageLog(ctx, &models.UsageLog{
		TenantID:  tenantID,
		UserID:    userID,
		Action:    "transcription",
		Quantity:  minutes,
		SessionID: sessionID,
	})
}

// TrackTranslation records translation usage
func (t *UsageTracker) TrackTranslation(ctx context.Context, userID, tenantID string, sessionID *string) error {
	return t.store.CreateUsageLog(ctx, &models.UsageLog{
		TenantID:  tenantID,
		UserID:    userID,
		Action:    "translation",
		Quantity:  1,
		SessionID: sessionID,
	})
}

// TrackRAGQuery records RAG query usage
func (t *UsageTracker) TrackRAGQuery(ctx context.Context, userID, tenantID string, sessionID *string) error {
	return t.store.CreateUsageLog(ctx, &models.UsageLog{
		TenantID:  tenantID,
		UserID:    userID,
		Action:    "rag_query",
		Quantity:  1,
		SessionID: sessionID,
	})
}

// TrackStorage records storage usage
func (t *UsageTracker) TrackStorage(ctx context.Context, userID, tenantID string, megabytes float64) error {
	return t.store.CreateUsageLog(ctx, &models.UsageLog{
		TenantID: tenantID,
		UserID:   userID,
		Action:   "storage",
		Quantity: megabytes,
	})
}

func getNextMonthStart() string {
	now := time.Now().UTC()
	nextMonth := now.AddDate(0, 1, 0)
	return time.Date(nextMonth.Year(), nextMonth.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}
