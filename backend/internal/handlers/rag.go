package handlers

import (
    "context"
    "encoding/json"
    "log"
    "net/http"
    "strconv"
    "time"

    "github.com/dreamtrans/backend/internal/rag"
)

type RAGHandler struct {
    svc *rag.Service
}

func NewRAGHandler() (*RAGHandler, error) {
    svc, err := rag.NewServiceFromEnv()
    if err != nil { return nil, err }
    return &RAGHandler{svc: svc}, nil
}

func (h *RAGHandler) Close() { _ = h.svc.Close() }

type askRequest struct {
    SessionID string `json:"session_id"`
    Query     string `json:"query"`
    TopK      int    `json:"top_k"`
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
    Answer string `json:"answer"`
    Usage  *usageDTO `json:"usage,omitempty"`
    LatencyMs int64  `json:"latency_ms,omitempty"`
}

type askConfig struct {
    APIKey  string `json:"api_key,omitempty"`
    APIBase string `json:"api_base,omitempty"`
    Model   string `json:"model,omitempty"`
    Prompt  string `json:"prompt,omitempty"`
}

func (h *RAGHandler) HandleAsk(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
    var req askRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "bad json", http.StatusBadRequest); return }
    if req.SessionID == "" { req.SessionID = "default" }
    if req.TopK <= 0 { req.TopK = 5 }
    // deadline
    ctx := r.Context()
    if _, ok := ctx.Deadline(); !ok {
        var cancel context.CancelFunc
        ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
        defer cancel()
    }
    if req.Config != nil {
        ov := &rag.ChatOverrides{APIKey: req.Config.APIKey, APIBase: req.Config.APIBase, Model: req.Config.Model, Prompt: req.Config.Prompt}
        ans, usage, dur, err := h.svc.BuildAnswerWithConfigUsage(r.Context(), req.SessionID, req.Query, req.TopK, ov)
        if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
        var u *usageDTO
        if usage != nil { u = &usageDTO{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model} }
        writeJSON(w, askResponse{Answer: ans, Usage: u, LatencyMs: dur.Milliseconds()})
        return
    }
    ans, usage, dur, err := h.svc.BuildAnswerWithUsage(r.Context(), req.SessionID, req.Query, req.TopK)
    if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
    var u *usageDTO
    if usage != nil { u = &usageDTO{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model} }
    writeJSON(w, askResponse{Answer: ans, Usage: u, LatencyMs: dur.Milliseconds()})
}

// HandleSummary returns current session summary.
func (h *RAGHandler) HandleSummary(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
    sessionID := r.URL.Query().Get("session_id")
    if sessionID == "" { sessionID = "default" }
    sum, err := h.svc.StoreSummary(sessionID)
    if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
    writeJSON(w, map[string]any{"summary": sum})
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
}

func (h *RAGHandler) HandleQuery(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost { http.Error(w, "Method not allowed", http.StatusMethodNotAllowed); return }
    var req queryRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil { http.Error(w, "bad json", http.StatusBadRequest); return }
    if req.SessionID == "" { req.SessionID = "default" }
    if req.TopK <= 0 { req.TopK = 5 }
    if req.Candidate <= 0 { req.Candidate = 300 }
    docs, summary, err := h.svc.QueryTopK(r.Context(), req.SessionID, req.Query, req.TopK, req.Candidate)
    if err != nil { http.Error(w, err.Error(), http.StatusBadGateway); return }
    out := queryResponse{Summary: summary}
    for _, d := range docs {
        out.Docs = append(out.Docs, queryDocResult{ID: d.ID, Speaker: d.Speaker, StartTime: d.StartTime, EndTime: d.EndTime, Original: d.Original, Summary: d.Summary})
    }
    writeJSON(w, out)
}

func (h *RAGHandler) HandleStats(w http.ResponseWriter, r *http.Request) {
    sessionID := r.URL.Query().Get("session_id")
    if sessionID == "" { sessionID = "default" }
    limit := 50
    if v := r.URL.Query().Get("limit"); v != "" {
        if n, err := strconv.Atoi(v); err == nil && n > 0 { limit = n }
    }
    docs, _ := h.svc.RecentDocuments(sessionID, limit)
    writeJSON(w, map[string]any{"session_id": sessionID, "recent_count": len(docs)})
}

// Helpers
func writeJSON(w http.ResponseWriter, v any) {
    w.Header().Set("Content-Type", "application/json")
    if err := json.NewEncoder(w).Encode(v); err != nil { log.Printf("write json: %v", err) }
}

// no extra helpers needed
