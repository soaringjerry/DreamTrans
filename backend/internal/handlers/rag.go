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

	openaiprovider "github.com/dreamtrans/backend/internal/adapters/openai_provider"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/metrics"
	"github.com/dreamtrans/backend/internal/rag"
)

type RAGHandler struct {
	svc *rag.Service
}

func NewRAGHandler() (*RAGHandler, error) {
	svc, err := rag.NewServiceFromEnv()
	if err != nil {
		return nil, err
	}
	return &RAGHandler{svc: svc}, nil
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
	var req askRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	// deadline
	ctx := r.Context()
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
		http.Error(w, err.Error(), http.StatusBadGateway)
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
			log.Printf("metrics.chat model=%s tokens p=%d c=%d t=%d latency=%dms", usage.Model, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, dur.Milliseconds())
		}
	} else {
		model := ""
		if req.Config != nil && req.Config.Model != "" {
			model = req.Config.Model
		}
		metrics.RecordChatNoUsage(model, dur.Milliseconds())
		if os.Getenv("OPENAI_DEBUG") == "1" {
			log.Printf("metrics.chat usage missing; model=%s latency=%dms", model, dur.Milliseconds())
		}
	}

	// Update in-memory chat history
	appendHistory(req.SessionID, "user", req.Query)
	appendHistory(req.SessionID, "assistant", ans)
	writeJSON(w, askResponse{Answer: ans, Usage: u, LatencyMs: dur.Milliseconds()})
}

// HandleSummary returns current session summary.
func (h *RAGHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = "default"
	}
	sum, err := h.svc.StoreSummary(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"summary": sum})
}

// HandleTitle generates a short Chinese title based on current session summary.
func (h *RAGHandler) HandleTitle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = "default"
	}
	// return cached title if present
	if title, _ := h.svc.StoreGetTitle(sessionID); strings.TrimSpace(title) != "" {
		writeJSON(w, map[string]any{"title": title})
		return
	}
	sum, err := h.svc.StoreSummary(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if sum == "" {
		writeJSON(w, map[string]any{"title": ""})
		return
	}
	cfg, err := openaiprovider.NewConfigFromEnv()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	// prefer summary/chat model from centralized config
	if m := os.Getenv("OPENAI_SUMMARY_MODEL"); m != "" {
		cfg.Model = m
	}
	if m2 := config.Get().Models.Summary; m2 != "" {
		cfg.Model = m2
	}
	tr := openaiprovider.NewTranslator(cfg)
	sys := "你是标题生成器。请基于给定的摘要生成一个简短中文标题（不超过12个字），不要添加标点符号或引号。"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": sum}}
	cctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	start := time.Now()
	out, usage, err := tr.ChatWithUsageRetry(cctx, msgs, 3)
	dur := time.Since(start)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if usage != nil {
		metrics.RecordChat(&metrics.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model}, dur.Milliseconds())
	} else {
		metrics.RecordChatNoUsage(cfg.Model, dur.Milliseconds())
	}
	title := strings.TrimSpace(out)
	if len([]rune(title)) > 12 {
		rs := []rune(title)
		title = string(rs[:12])
	}
	// cache
	_ = h.svc.StoreSetTitle(sessionID, title)
	writeJSON(w, map[string]any{"title": title})
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
	var req queryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.SessionID == "" {
		req.SessionID = "default"
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	if req.Candidate <= 0 {
		req.Candidate = 300
	}
	docs, summary, err := h.svc.QueryTopK(r.Context(), req.SessionID, req.Query, req.TopK, req.Candidate)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	out := queryResponse{Summary: summary}
	for _, d := range docs {
		out.Docs = append(out.Docs, queryDocResult{ID: d.ID, Speaker: d.Speaker, StartTime: d.StartTime, EndTime: d.EndTime, Original: d.Original, Summary: d.Summary, IsLive: d.Ephemeral})
	}
	writeJSON(w, out)
}

func (h *RAGHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		sessionID = "default"
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	docs, _ := h.svc.RecentDocuments(sessionID, limit)
	writeJSON(w, map[string]any{"session_id": sessionID, "recent_count": len(docs)})
}

// Helpers
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// no extra helpers needed

// ---- simple in-memory chat history per session ----
type chatTurn struct{ Role, Content string }

var hist = struct{ m map[string][]chatTurn }{m: map[string][]chatTurn{}}

func getSessionHistory(sessionID string) string {
	if sessionID == "" {
		sessionID = "default"
	}
	turns := hist.m[sessionID]
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
	arr := hist.m[sessionID]
	arr = append(arr, chatTurn{Role: role, Content: content})
	if len(arr) > 12 {
		arr = arr[len(arr)-12:]
	}
	hist.m[sessionID] = arr
}
