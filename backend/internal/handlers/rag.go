package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	openaiprovider "github.com/dreamtrans/backend/internal/adapters/openai_provider"
	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/billing"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/metrics"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/dreamtrans/backend/internal/store"
)

type ragBillingService interface {
	RecordUsage(context.Context, *billing.UsageRecord) (float64, error)
	SettleUsageReservation(context.Context, string, *billing.UsageRecord) (float64, error)
	RefundUsage(context.Context, string, string) error
	GetSystemSetting(context.Context, string) (string, error)
}

type ragAPIQuotaStore interface {
	ConsumeAPIRequest(context.Context, string, string) (store.APIQuotaStatus, error)
}

var (
	errRAGBillingUnavailable = errors.New("RAG billing failed")
	errRAGPaymentRequired    = errors.New("RAG payment required")
	errRAGQuotaUnavailable   = errors.New("RAG API quota service unavailable")
)

type RAGHandler struct {
	svc      *rag.Service
	billing  ragBillingService
	apiQuota ragAPIQuotaStore
}

func NewRAGHandler(
	billingSvc *billing.Service,
	quotaStores ...*store.PostgresStore,
) (*RAGHandler, error) {
	svc, err := rag.NewServiceFromEnv()
	if err != nil {
		return nil, err
	}
	// The Pro UI exposes a running summary. Paragraph LLM summarization remains
	// optional, but cleaned transcript bullets should always update that output.
	svc.SetSummaryOutputEnabled(true)
	var quotaStore ragAPIQuotaStore
	if len(quotaStores) > 0 && quotaStores[0] != nil {
		quotaStore = quotaStores[0]
	}
	var ragBilling ragBillingService
	if billingSvc != nil {
		ragBilling = billingSvc
	}
	return &RAGHandler{svc: svc, billing: ragBilling, apiQuota: quotaStore}, nil
}

func (h *RAGHandler) Close() { _ = h.svc.Close() }

type askRequest struct {
	SessionID string     `json:"session_id"`
	Query     string     `json:"query"`
	TopK      int        `json:"top_k"`
	Config    *askConfig `json:"config,omitempty"`
}

// usageDTO is a lightweight usage payload for API responses.
type usageDTO struct {
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Model            string `json:"model,omitempty"`
}

type askResponse struct {
	Answer    string    `json:"answer"`
	Usage     *usageDTO `json:"usage,omitempty"`
	LatencyMs int64     `json:"latency_ms,omitempty"`
}

type askConfig struct {
	APIKey  string `json:"api_key,omitempty"`
	APIBase string `json:"api_base,omitempty"`
	Model   string `json:"model,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

func (h *RAGHandler) HandleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireRAGPrincipal(w, r) {
		return
	}
	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	rawSessionID := req.SessionID
	req.SessionID = scopedRAGSessionID(r, rawSessionID)
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" || len([]rune(req.Query)) > 20_000 {
		http.Error(w, "query is required and must be at most 20000 characters", http.StatusBadRequest)
		return
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	if req.TopK > 20 {
		req.TopK = 20
	}
	if err := h.validateOverrides(r.Context(), req.Config); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if err := h.consumeRAGQuery(r.Context(), rawSessionID); err != nil {
		h.writeRAGAccountingError(w, err)
		return
	}
	// deadline
	ctx := h.withRAGMeter(r.Context(), rawSessionID)
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}

	// Unified execution path to reduce complexity
	var (
		ans   string
		usage *openaiprovider.Usage
		dur   time.Duration
		err   error
	)
	// Build compact chat history for better pronoun resolution
	history := getSessionHistory(req.SessionID)
	if req.Config != nil {
		ov := &rag.ChatOverrides{APIKey: req.Config.APIKey, APIBase: req.Config.APIBase, Model: req.Config.Model, Prompt: req.Config.Prompt}
		ans, usage, dur, err = h.svc.BuildAnswerWithHistoryWithConfigUsage(ctx, req.SessionID, req.Query, req.TopK, ov, history)
	} else {
		// fallback without overrides
		ans, usage, dur, err = h.svc.BuildAnswerWithHistoryWithConfigUsage(ctx, req.SessionID, req.Query, req.TopK, nil, history)
	}
	if err != nil {
		log.Printf("rag ask error: %v", err)
		if h.isRAGAccountingError(err) {
			h.writeRAGAccountingError(w, err)
			return
		}
		http.Error(w, "upstream answer service failed", http.StatusBadGateway)
		return
	}

	// Build usage DTO
	var u *usageDTO
	if usage != nil {
		u = &usageDTO{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model}
	}

	// Record metrics (with debug logging)
	if usage != nil {
		metrics.RecordChat(&metrics.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model}, dur.Milliseconds())
		if os.Getenv("OPENAI_DEBUG") == "1" {
			//nolint:gosec // G706: the provider model is escaped with strconv.Quote.
			log.Printf("metrics.chat model=%s tokens p=%d c=%d t=%d latency=%dms", strconv.Quote(usage.Model), usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, dur.Milliseconds())
		}
	} else {
		model := ""
		if req.Config != nil && req.Config.Model != "" {
			model = req.Config.Model
		}
		metrics.RecordChatNoUsage(model, dur.Milliseconds())
		if os.Getenv("OPENAI_DEBUG") == "1" {
			//nolint:gosec // G706: the request model is escaped with strconv.Quote.
			log.Printf("metrics.chat usage missing; model=%s latency=%dms", strconv.Quote(model), dur.Milliseconds())
		}
	}
	// Update in-memory chat history
	appendHistory(req.SessionID, "user", req.Query)
	appendHistory(req.SessionID, "assistant", ans)
	WriteJSON(w, askResponse{Answer: ans, Usage: u, LatencyMs: dur.Milliseconds()})
}

// HandleSummary returns current session summary.
func (h *RAGHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireRAGPrincipal(w, r) {
		return
	}
	sessionID := scopedRAGSessionID(r, r.URL.Query().Get("session_id"))
	sum, err := h.svc.StoreSummary(sessionID)
	if err != nil {
		log.Printf("rag summary error: %v", err)
		http.Error(w, "summary service failed", http.StatusBadGateway)
		return
	}
	WriteJSON(w, map[string]any{"summary": sum})
}

// HandleTitle generates a short Chinese title based on current session summary.
func (h *RAGHandler) HandleTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireRAGPrincipal(w, r) {
		return
	}
	rawSessionID := r.URL.Query().Get("session_id")
	sessionID := scopedRAGSessionID(r, rawSessionID)
	// return cached title if present
	if title, _ := h.svc.StoreGetTitle(sessionID); strings.TrimSpace(title) != "" {
		WriteJSON(w, map[string]any{"title": title})
		return
	}
	sum, err := h.svc.StoreSummary(sessionID)
	if err != nil {
		log.Printf("rag title summary error: %v", err)
		http.Error(w, "summary service failed", http.StatusBadGateway)
		return
	}
	if sum == "" {
		WriteJSON(w, map[string]any{"title": ""})
		return
	}
	cfg, err := openaiprovider.NewConfigFromEnv()
	if err != nil {
		log.Printf("rag title configuration error: %v", err)
		http.Error(w, "title service is unavailable", http.StatusServiceUnavailable)
		return
	}
	// prefer summary/chat model from centralized config
	if m := os.Getenv("OPENAI_SUMMARY_MODEL"); m != "" {
		cfg.Model = m
	}
	if m2 := config.Get().Models.Summary; m2 != "" {
		cfg.Model = m2
	}
	const titleMaxOutputTokens = 128
	cfg.MaxOutputTokens = titleMaxOutputTokens
	tr := openaiprovider.NewTranslator(cfg)
	sys := "你是标题生成器。请基于给定的摘要生成一个简短中文标题（不超过12个字），不要添加标点符号或引号。"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": sum}}
	reservation, err := h.reserveRAGProviderUsage(
		r.Context(),
		rawSessionID,
		rag.ProviderUsage{
			Action:       "chat",
			Model:        cfg.Model,
			InputTokens:  conservativeRAGTokens(sys, sum),
			OutputTokens: titleMaxOutputTokens,
		},
	)
	if err != nil {
		h.writeRAGAccountingError(w, err)
		return
	}
	cctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	out, usage, err := tr.ChatWithUsageRetry(cctx, msgs, 3)
	dur := time.Since(start)
	if err != nil {
		if refundErr := refundRAGProviderReservation(
			reservation,
			"RAG title provider request failed",
		); refundErr != nil {
			log.Printf("rag title reservation refund error: %v", refundErr)
			http.Error(w, "usage refund failed", http.StatusServiceUnavailable)
			return
		}
		log.Printf("rag title upstream error: %v", err)
		http.Error(w, "upstream title service failed", http.StatusBadGateway)
		return
	}
	if usage != nil {
		metrics.RecordChat(&metrics.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model}, dur.Milliseconds())
	} else {
		metrics.RecordChatNoUsage(cfg.Model, dur.Milliseconds())
	}
	actualUsage := rag.ProviderUsage{
		Action:       "chat",
		Model:        cfg.Model,
		InputTokens:  conservativeRAGTokens(sys, sum),
		OutputTokens: titleMaxOutputTokens,
	}
	if usage != nil {
		actualUsage.Model = usage.Model
		actualUsage.InputTokens = usage.PromptTokens
		actualUsage.OutputTokens = usage.CompletionTokens
	}
	if reservation != nil {
		if err := reservation.Settle(r.Context(), actualUsage); err != nil {
			h.writeRAGAccountingError(w, err)
			return
		}
	}
	title := strings.TrimSpace(out)
	if len([]rune(title)) > 12 {
		rs := []rune(title)
		title = string(rs[:12])
	}
	// cache
	_ = h.svc.StoreSetTitle(sessionID, title)
	WriteJSON(w, map[string]any{"title": title})
}

// IngestRequest is for Pro frontend to send confirmed transcripts for vector embedding.
type ingestRequest struct {
	SessionID string  `json:"session_id"`
	Speaker   string  `json:"speaker"`
	Text      string  `json:"text"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
}

// HandleIngest allows Pro frontend to send confirmed transcripts for vector embedding.
func (h *RAGHandler) HandleIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireRAGPrincipal(w, r) {
		return
	}
	var req ingestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	rawSessionID := req.SessionID
	req.SessionID = scopedRAGSessionID(r, rawSessionID)
	if strings.TrimSpace(req.Text) == "" {
		WriteJSON(w, map[string]any{"status": "skipped", "reason": "empty text"})
		return
	}
	if len([]rune(req.Text)) > 50_000 || len([]rune(req.Speaker)) > 100 {
		http.Error(w, "transcript payload is too large", http.StatusBadRequest)
		return
	}
	if req.StartTime < 0 || req.EndTime < req.StartTime {
		http.Error(w, "invalid transcript timing", http.StatusBadRequest)
		return
	}
	ctx := h.withRAGMeter(r.Context(), rawSessionID)
	result, err := h.svc.IngestParagraphWithResult(ctx, req.SessionID, req.Speaker, req.Text, req.StartTime, req.EndTime)
	if err != nil {
		log.Printf("rag ingest error: %v", err)
		if h.isRAGAccountingError(err) {
			h.writeRAGAccountingError(w, err)
			return
		}
		http.Error(w, "RAG ingest failed", http.StatusBadGateway)
		return
	}
	if !result.Embedded {
		reason := "not embeddable"
		if result.Duplicate {
			reason = "duplicate"
		}
		WriteJSON(w, map[string]any{"status": "skipped", "reason": reason})
		return
	}
	WriteJSON(w, map[string]any{"status": "ok"})
}

type queryRequest struct {
	SessionID string `json:"session_id"`
	Query     string `json:"query"`
	TopK      int    `json:"top_k"`
	Candidate int    `json:"candidate"`
}

type queryResponse struct {
	Summary string           `json:"summary"`
	Docs    []queryDocResult `json:"docs"`
}

type queryDocResult struct {
	ID        int64   `json:"id"`
	Speaker   string  `json:"speaker"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Original  string  `json:"original_text"`
	Summary   string  `json:"summary"`
	IsLive    bool    `json:"is_live,omitempty"`
}

func (h *RAGHandler) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireRAGPrincipal(w, r) {
		return
	}
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	rawSessionID := req.SessionID
	req.SessionID = scopedRAGSessionID(r, rawSessionID)
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" || len([]rune(req.Query)) > 20_000 {
		http.Error(w, "query is required and must be at most 20000 characters", http.StatusBadRequest)
		return
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	if req.TopK > 20 {
		req.TopK = 20
	}
	if req.Candidate <= 0 {
		req.Candidate = 300
	}
	if req.Candidate > 500 {
		req.Candidate = 500
	}
	if err := h.consumeRAGQuery(r.Context(), rawSessionID); err != nil {
		h.writeRAGAccountingError(w, err)
		return
	}
	ctx := h.withRAGMeter(r.Context(), rawSessionID)
	docs, summary, err := h.svc.QueryTopK(ctx, req.SessionID, req.Query, req.TopK, req.Candidate)
	if err != nil {
		log.Printf("rag query error: %v", err)
		if h.isRAGAccountingError(err) {
			h.writeRAGAccountingError(w, err)
			return
		}
		http.Error(w, "RAG query service failed", http.StatusBadGateway)
		return
	}
	out := queryResponse{Summary: summary}
	for _, d := range docs {
		out.Docs = append(out.Docs, queryDocResult{ID: d.ID, Speaker: d.Speaker, StartTime: d.StartTime, EndTime: d.EndTime, Original: d.Original, Summary: d.Summary, IsLive: d.Ephemeral})
	}
	WriteJSON(w, out)
}

func (h *RAGHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireRAGPrincipal(w, r) {
		return
	}
	sessionID := scopedRAGSessionID(r, r.URL.Query().Get("session_id"))
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	docs, err := h.svc.RecentDocuments(sessionID, limit)
	if err != nil {
		log.Printf("rag stats error: %v", err)
		http.Error(w, "RAG stats service failed", http.StatusBadGateway)
		return
	}
	WriteJSON(w, map[string]any{"session_id": sessionID, "recent_count": len(docs)})
}

// WriteJSON is a helper to write JSON responses
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// no extra helpers needed

// ---- simple in-memory chat history per session ----
type chatTurn struct{ Role, Content string }

type chatHistoryEntry struct {
	turns    []chatTurn
	lastUsed time.Time
}

var hist = struct {
	sync.Mutex
	m map[string]chatHistoryEntry
}{m: map[string]chatHistoryEntry{}}

const (
	maxChatHistorySessions = 2048
	chatHistoryTTL         = 2 * time.Hour
)

func getSessionHistory(sessionID string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	now := time.Now()
	hist.Lock()
	entry, ok := hist.m[sessionID]
	if ok && now.Sub(entry.lastUsed) <= chatHistoryTTL {
		entry.lastUsed = now
		hist.m[sessionID] = entry
	} else if ok {
		delete(hist.m, sessionID)
		entry = chatHistoryEntry{}
	}
	turns := append([]chatTurn(nil), entry.turns...)
	hist.Unlock()
	// keep last 8 turns
	if len(turns) > 8 {
		turns = turns[len(turns)-8:]
	}
	// compact
	var b strings.Builder
	for _, t := range turns {
		c := strings.TrimSpace(t.Content)
		if len([]rune(c)) > 140 {
			c = string([]rune(c)[:140]) + "…"
		}
		if c == "" {
			continue
		}
		if t.Role == "user" {
			b.WriteString("U: ")
		} else {
			b.WriteString("A: ")
		}
		b.WriteString(c)
		b.WriteString("\n")
	}
	return b.String()
}

func appendHistory(sessionID, role, content string) {
	if sessionID == "" {
		sessionID = "default"
	}
	hist.Lock()
	defer hist.Unlock()
	now := time.Now()
	pruneChatHistoryLocked(now)
	entry := hist.m[sessionID]
	arr := entry.turns
	arr = append(arr, chatTurn{Role: role, Content: content})
	if len(arr) > 12 {
		arr = arr[len(arr)-12:]
	}
	hist.m[sessionID] = chatHistoryEntry{turns: arr, lastUsed: now}
}

func pruneChatHistoryLocked(now time.Time) {
	for sessionID, entry := range hist.m {
		if now.Sub(entry.lastUsed) > chatHistoryTTL {
			delete(hist.m, sessionID)
		}
	}
	for len(hist.m) >= maxChatHistorySessions {
		var oldestID string
		var oldest time.Time
		for sessionID, entry := range hist.m {
			if oldestID == "" || entry.lastUsed.Before(oldest) {
				oldestID = sessionID
				oldest = entry.lastUsed
			}
		}
		if oldestID == "" {
			break
		}
		delete(hist.m, oldestID)
	}
}

func scopedRAGSessionID(r *http.Request, sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	if len([]rune(sessionID)) > 200 {
		sessionID = string([]rune(sessionID)[:200])
	}
	if claims := auth.GetUserClaims(r.Context()); claims != nil {
		return "tenant/" + claims.TenantID + "/user/" + claims.UserID + "/session/" + sessionID
	}
	return "anonymous/session/" + sessionID
}

func (h *RAGHandler) validateOverrides(ctx context.Context, overrides *askConfig) error {
	if overrides == nil {
		return nil
	}
	if len(overrides.APIKey) > 4096 || len(overrides.APIBase) > 2048 ||
		len(overrides.Model) > 200 || len([]rune(overrides.Prompt)) > 20_000 {
		return fmt.Errorf("invalid configuration override")
	}
	if overrides.APIKey == "" && overrides.APIBase != "" {
		return fmt.Errorf("api_base requires a request-scoped api_key")
	}
	if overrides.APIKey == "" {
		if strings.TrimSpace(overrides.Model) != "" {
			return fmt.Errorf("model override requires a request-scoped api_key")
		}
		return nil
	}
	allowed := strings.EqualFold(os.Getenv("ALLOW_USER_API_KEY"), "true")
	if h.billing != nil {
		if value, err := h.billing.GetSystemSetting(ctx, "allow_user_api_key"); err == nil {
			parsed, parseErr := strconv.ParseBool(strings.Trim(strings.TrimSpace(value), `"`))
			allowed = parseErr == nil && parsed
		}
	}
	if !allowed {
		return fmt.Errorf("user API key overrides are disabled")
	}
	if overrides.APIBase != "" && !allowedUserAPIBase(overrides.APIBase) {
		return fmt.Errorf("api_base is not in USER_API_BASE_ALLOWLIST")
	}
	return nil
}

func allowedUserAPIBase(raw string) bool {
	candidate, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || candidate.Scheme != "https" || candidate.Host == "" || candidate.User != nil {
		return false
	}
	allowedValues := make([]string, 1, 1+strings.Count(os.Getenv("USER_API_BASE_ALLOWLIST"), ",")+1)
	allowedValues[0] = os.Getenv("OPENAI_API_BASE")
	if allowedValues[0] == "" {
		allowedValues[0] = os.Getenv("OPENAI_BASE")
	}
	if allowedValues[0] == "" {
		allowedValues[0] = "https://api.openai.com/v1"
	}
	allowedValues = append(allowedValues, strings.Split(os.Getenv("USER_API_BASE_ALLOWLIST"), ",")...)
	for _, value := range allowedValues {
		allowed, parseErr := url.Parse(strings.TrimSpace(value))
		if parseErr == nil && allowed.Scheme == candidate.Scheme &&
			strings.EqualFold(allowed.Host, candidate.Host) {
			return true
		}
	}
	return false
}

func (h *RAGHandler) requireRAGPrincipal(w http.ResponseWriter, r *http.Request) bool {
	// Standalone deployments intentionally support RAG without the Postgres
	// billing stack. Once either database-backed quota component is configured,
	// every RAG route must be tied to a tenant/user principal; a service key must
	// not silently create unmetered anonymous data.
	if h.billing == nil && h.apiQuota == nil {
		return true
	}
	claims := auth.GetUserClaims(r.Context())
	if claims == nil ||
		strings.TrimSpace(claims.TenantID) == "" ||
		strings.TrimSpace(claims.UserID) == "" {
		http.Error(w, "user authentication required", http.StatusUnauthorized)
		return false
	}
	return true
}

type ragHTTPReservationState uint8

const (
	ragHTTPReservationOpen ragHTTPReservationState = iota
	ragHTTPReservationSettled
	ragHTTPReservationRefunded
	ragHTTPReservationSettlementFailed
)

// ragHTTPUsageMeter is invoked immediately before each logical upstream RAG
// operation. API quota is consumed first, then a conservative DreamPoint
// reservation is recorded. No provider call starts unless both succeed.
type ragHTTPUsageMeter struct {
	apiQuota ragAPIQuotaStore
	billing  ragBillingService

	userID    string
	tenantID  string
	sessionID *string
}

type ragHTTPUsageReservation struct {
	mu sync.Mutex

	billing ragBillingService
	key     string

	userID        string
	tenantID      string
	sessionID     *string
	reservedUsage rag.ProviderUsage
	state         ragHTTPReservationState
}

func (m *ragHTTPUsageMeter) ReserveProviderUsage(
	ctx context.Context,
	usage rag.ProviderUsage,
) (rag.ProviderUsageReservation, error) {
	if strings.TrimSpace(m.tenantID) == "" || strings.TrimSpace(m.userID) == "" {
		return nil, fmt.Errorf("%w: missing tenant or user principal", errRAGBillingUnavailable)
	}
	if m.apiQuota != nil {
		if _, err := m.apiQuota.ConsumeAPIRequest(ctx, m.tenantID, m.userID); err != nil {
			if errors.Is(err, store.ErrAPIQuota) {
				return nil, fmt.Errorf("monthly API quota exceeded: %w", err)
			}
			return nil, fmt.Errorf("%w: %w", errRAGQuotaUnavailable, err)
		}
	}

	action := strings.TrimSpace(usage.Action)
	if action == "" {
		return nil, fmt.Errorf("%w: provider action is required", errRAGBillingUnavailable)
	}
	reservation := &ragHTTPUsageReservation{
		userID:        m.userID,
		tenantID:      m.tenantID,
		sessionID:     m.sessionID,
		reservedUsage: usage,
		state:         ragHTTPReservationOpen,
	}
	if m.billing == nil || usage.CustomerFunded {
		return reservation, nil
	}
	reservation.billing = m.billing

	reservationID, err := normalizeClientSegmentID("")
	if err != nil {
		return nil, fmt.Errorf("%w: create reservation id: %w", errRAGBillingUnavailable, err)
	}
	reservation.key = "http-rag-" + action + ":" + reservationID
	if _, err := m.billing.RecordUsage(ctx, &billing.UsageRecord{
		UserID:         m.userID,
		TenantID:       m.tenantID,
		SessionID:      m.sessionID,
		Action:         action,
		Model:          strings.TrimSpace(usage.Model),
		InputTokens:    usage.InputTokens,
		OutputTokens:   usage.OutputTokens,
		IdempotencyKey: reservation.key,
	}); err != nil {
		return nil, wrapRAGBillingError("reserve "+action+" usage", err)
	}
	return reservation, nil
}

func (r *ragHTTPUsageReservation) Settle(
	_ context.Context,
	actual rag.ProviderUsage,
) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case ragHTTPReservationSettled:
		return nil
	case ragHTTPReservationRefunded:
		return fmt.Errorf("%w: usage reservation was already refunded", errRAGBillingUnavailable)
	case ragHTTPReservationSettlementFailed:
		return fmt.Errorf("%w: usage reservation settlement already failed", errRAGBillingUnavailable)
	}
	if r.billing == nil {
		r.state = ragHTTPReservationSettled
		return nil
	}

	action := strings.TrimSpace(actual.Action)
	if action == "" {
		action = strings.TrimSpace(r.reservedUsage.Action)
	}
	if action != strings.TrimSpace(r.reservedUsage.Action) {
		r.state = ragHTTPReservationSettlementFailed
		return fmt.Errorf("%w: settlement action does not match reservation", errRAGBillingUnavailable)
	}
	model := strings.TrimSpace(actual.Model)
	if model == "" {
		model = strings.TrimSpace(r.reservedUsage.Model)
	}
	// Settlement/refund must survive a request cancellation after the provider
	// has completed, otherwise clients could disconnect to avoid payment.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := r.billing.SettleUsageReservation(ctx, r.key, &billing.UsageRecord{
		UserID:       r.userID,
		TenantID:     r.tenantID,
		SessionID:    r.sessionID,
		Action:       action,
		Model:        model,
		InputTokens:  actual.InputTokens,
		OutputTokens: actual.OutputTokens,
	}); err != nil {
		r.state = ragHTTPReservationSettlementFailed
		return wrapRAGBillingError("settle "+action+" usage", err)
	}
	r.state = ragHTTPReservationSettled
	return nil
}

func (r *ragHTTPUsageReservation) Refund(reason string) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.state {
	case ragHTTPReservationRefunded:
		return nil
	case ragHTTPReservationSettled:
		return fmt.Errorf("%w: settled usage cannot be refunded", errRAGBillingUnavailable)
	case ragHTTPReservationSettlementFailed:
		return fmt.Errorf("%w: failed settlement cannot be refunded", errRAGBillingUnavailable)
	}
	if r.billing == nil {
		r.state = ragHTTPReservationRefunded
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.billing.RefundUsage(ctx, r.key, reason); err != nil {
		return fmt.Errorf("%w: refund provider usage: %w", errRAGBillingUnavailable, err)
	}
	r.state = ragHTTPReservationRefunded
	return nil
}

func (h *RAGHandler) withRAGMeter(ctx context.Context, rawSessionID string) context.Context {
	if h.billing == nil && h.apiQuota == nil {
		return ctx
	}
	claims := auth.GetUserClaims(ctx)
	if claims == nil {
		return ctx
	}
	return rag.WithProviderUsageMeter(ctx, &ragHTTPUsageMeter{
		apiQuota:  h.apiQuota,
		billing:   h.billing,
		userID:    claims.UserID,
		tenantID:  claims.TenantID,
		sessionID: billingSessionReference(rawSessionID),
	})
}

func (h *RAGHandler) reserveRAGProviderUsage(
	ctx context.Context,
	rawSessionID string,
	usage rag.ProviderUsage,
) (rag.ProviderUsageReservation, error) {
	if h.billing == nil && h.apiQuota == nil {
		return nil, nil
	}
	claims := auth.GetUserClaims(ctx)
	if claims == nil {
		return nil, fmt.Errorf("%w: missing user principal", errRAGBillingUnavailable)
	}
	return (&ragHTTPUsageMeter{
		apiQuota:  h.apiQuota,
		billing:   h.billing,
		userID:    claims.UserID,
		tenantID:  claims.TenantID,
		sessionID: billingSessionReference(rawSessionID),
	}).ReserveProviderUsage(ctx, usage)
}

func refundRAGProviderReservation(reservation rag.ProviderUsageReservation, reason string) error {
	if reservation == nil {
		return nil
	}
	return reservation.Refund(reason)
}

func (h *RAGHandler) consumeRAGQuery(ctx context.Context, rawSessionID string) error {
	if h.billing == nil {
		return nil
	}
	claims := auth.GetUserClaims(ctx)
	if claims == nil {
		return fmt.Errorf("%w: missing user principal", errRAGBillingUnavailable)
	}
	requestID, err := normalizeClientSegmentID("")
	if err != nil {
		return fmt.Errorf("%w: create RAG query id: %w", errRAGBillingUnavailable, err)
	}
	// This atomic ledger insertion is the authoritative plan-limit check. It
	// deliberately counts an accepted query attempt even if a later upstream
	// operation fails, matching the API-request quota's attempt semantics.
	if _, err := h.billing.RecordUsage(ctx, &billing.UsageRecord{
		UserID:         claims.UserID,
		TenantID:       claims.TenantID,
		SessionID:      billingSessionReference(rawSessionID),
		Action:         "rag_query",
		Quantity:       1,
		IdempotencyKey: "http-rag-query:" + requestID,
	}); err != nil {
		return wrapRAGBillingError("consume RAG query", err)
	}
	return nil
}

func (h *RAGHandler) isRAGAccountingError(err error) bool {
	return errors.Is(err, store.ErrAPIQuota) ||
		errors.Is(err, billing.ErrPlanQuotaExceeded) ||
		errors.Is(err, errRAGPaymentRequired) ||
		errors.Is(err, errRAGBillingUnavailable) ||
		errors.Is(err, errRAGQuotaUnavailable)
}

func (h *RAGHandler) writeRAGAccountingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrAPIQuota):
		http.Error(w, "monthly API quota exceeded", http.StatusPaymentRequired)
	case errors.Is(err, billing.ErrPlanQuotaExceeded):
		http.Error(w, "monthly RAG quota exceeded", http.StatusPaymentRequired)
	case errors.Is(err, errRAGPaymentRequired):
		http.Error(w, "insufficient balance", http.StatusPaymentRequired)
	case errors.Is(err, errRAGQuotaUnavailable):
		http.Error(w, "quota service unavailable", http.StatusServiceUnavailable)
	case errors.Is(err, errRAGBillingUnavailable):
		http.Error(w, "billing service unavailable", http.StatusServiceUnavailable)
	default:
		http.Error(w, "usage accounting failed", http.StatusServiceUnavailable)
	}
}

func wrapRAGBillingError(operation string, err error) error {
	sentinel := errRAGBillingUnavailable
	if errors.Is(err, billing.ErrPlanQuotaExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "insufficient balance") {
		sentinel = errRAGPaymentRequired
	}
	return fmt.Errorf("%w: %s: %w", sentinel, operation, err)
}

func conservativeRAGTokens(parts ...string) int {
	const framingAllowance = 256
	total := framingAllowance
	for _, part := range parts {
		total += len(part)
	}
	if total < 1 {
		return 1
	}
	return total
}
