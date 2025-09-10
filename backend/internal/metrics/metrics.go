package metrics

import (
    "sync"
    "time"
)

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
    Model            string
}

type FeatureTotals struct {
    Requests   int            `json:"requests"`
    Prompt     int            `json:"prompt_tokens"`
    Completion int            `json:"completion_tokens"`
    Total      int            `json:"total_tokens"`
    PerModel   map[string]*FeatureTotals `json:"per_model,omitempty"`
}

type LogEntry struct {
    TS      time.Time `json:"ts"`
    Feature string    `json:"feature"`
    Model   string    `json:"model"`
    Prompt  int       `json:"prompt_tokens"`
    Completion int    `json:"completion_tokens"`
    Total   int       `json:"total_tokens"`
    Latency int64     `json:"latency_ms"`
}

type Collector struct {
    mu sync.Mutex
    Chat FeatureTotals
    Translate FeatureTotals
    Summarize FeatureTotals
    Logs []LogEntry
    maxLogs int
}

var defaultCollector = &Collector{maxLogs: 200}

func ensurePerModel(ft *FeatureTotals, model string) *FeatureTotals {
    if ft.PerModel == nil { ft.PerModel = make(map[string]*FeatureTotals) }
    m := ft.PerModel[model]
    if m == nil { m = &FeatureTotals{}; ft.PerModel[model] = m }
    return m
}

func (c *Collector) pushLog(le *LogEntry) {
    // store by value to keep ring buffer stable even if caller mutates
    c.Logs = append(c.Logs, *le)
    if len(c.Logs) > c.maxLogs {
        c.Logs = c.Logs[len(c.Logs)-c.maxLogs:]
    }
}

func RecordChat(u *Usage, latencyMs int64) {
    if u == nil { return }
    c := defaultCollector
    c.mu.Lock()
    defer c.mu.Unlock()
    c.Chat.Requests++
    c.Chat.Prompt += u.PromptTokens
    c.Chat.Completion += u.CompletionTokens
    c.Chat.Total += u.TotalTokens
    pm := ensurePerModel(&c.Chat, u.Model)
    pm.Requests++; pm.Prompt += u.PromptTokens; pm.Completion += u.CompletionTokens; pm.Total += u.TotalTokens
    c.pushLog(&LogEntry{TS: time.Now().UTC(), Feature: "chat", Model: u.Model, Prompt: u.PromptTokens, Completion: u.CompletionTokens, Total: u.TotalTokens, Latency: latencyMs})
}

// RecordChatNoUsage increments request counter even if provider didn't return usage tokens.
func RecordChatNoUsage(model string, latencyMs int64) {
    c := defaultCollector
    c.mu.Lock()
    defer c.mu.Unlock()
    c.Chat.Requests++
    pm := ensurePerModel(&c.Chat, model)
    pm.Requests++
    c.pushLog(&LogEntry{TS: time.Now().UTC(), Feature: "chat", Model: model, Prompt: 0, Completion: 0, Total: 0, Latency: latencyMs})
}

func RecordTranslate(u *Usage, latencyMs int64) {
    if u == nil { return }
    c := defaultCollector
    c.mu.Lock(); defer c.mu.Unlock()
    c.Translate.Requests++
    c.Translate.Prompt += u.PromptTokens
    c.Translate.Completion += u.CompletionTokens
    c.Translate.Total += u.TotalTokens
    pm := ensurePerModel(&c.Translate, u.Model)
    pm.Requests++; pm.Prompt += u.PromptTokens; pm.Completion += u.CompletionTokens; pm.Total += u.TotalTokens
    c.pushLog(&LogEntry{TS: time.Now().UTC(), Feature: "translate", Model: u.Model, Prompt: u.PromptTokens, Completion: u.CompletionTokens, Total: u.TotalTokens, Latency: latencyMs})
}

func RecordTranslateNoUsage(model string, latencyMs int64) {
    c := defaultCollector
    c.mu.Lock(); defer c.mu.Unlock()
    c.Translate.Requests++
    pm := ensurePerModel(&c.Translate, model)
    pm.Requests++
    c.pushLog(&LogEntry{TS: time.Now().UTC(), Feature: "translate", Model: model, Prompt: 0, Completion: 0, Total: 0, Latency: latencyMs})
}

func RecordSummarize(u *Usage, latencyMs int64) {
    if u == nil { return }
    c := defaultCollector
    c.mu.Lock(); defer c.mu.Unlock()
    c.Summarize.Requests++
    c.Summarize.Prompt += u.PromptTokens
    c.Summarize.Completion += u.CompletionTokens
    c.Summarize.Total += u.TotalTokens
    pm := ensurePerModel(&c.Summarize, u.Model)
    pm.Requests++; pm.Prompt += u.PromptTokens; pm.Completion += u.CompletionTokens; pm.Total += u.TotalTokens
    c.pushLog(&LogEntry{TS: time.Now().UTC(), Feature: "summarize", Model: u.Model, Prompt: u.PromptTokens, Completion: u.CompletionTokens, Total: u.TotalTokens, Latency: latencyMs})
}

func RecordSummarizeNoUsage(model string, latencyMs int64) {
    c := defaultCollector
    c.mu.Lock(); defer c.mu.Unlock()
    c.Summarize.Requests++
    pm := ensurePerModel(&c.Summarize, model)
    pm.Requests++
    c.pushLog(&LogEntry{TS: time.Now().UTC(), Feature: "summarize", Model: model, Prompt: 0, Completion: 0, Total: 0, Latency: latencyMs})
}

type Snapshot struct {
    Chat FeatureTotals `json:"chat"`
    Translate FeatureTotals `json:"translate"`
    Summarize FeatureTotals `json:"summarize"`
    Overall FeatureTotals `json:"overall"`
    LastLogs []LogEntry `json:"last_logs"`
}

func SnapshotMetrics() Snapshot {
    c := defaultCollector
    c.mu.Lock(); defer c.mu.Unlock()
    overall := FeatureTotals{}
    overall.Requests = c.Chat.Requests + c.Translate.Requests + c.Summarize.Requests
    overall.Prompt = c.Chat.Prompt + c.Translate.Prompt + c.Summarize.Prompt
    overall.Completion = c.Chat.Completion + c.Translate.Completion + c.Summarize.Completion
    overall.Total = c.Chat.Total + c.Translate.Total + c.Summarize.Total
    // shallow copy is fine for read-only UI
    return Snapshot{Chat: c.Chat, Translate: c.Translate, Summarize: c.Summarize, Overall: overall, LastLogs: append([]LogEntry(nil), c.Logs...)}
}

// Accessor for tests or external packages if needed
func DefaultCollector() *Collector { return defaultCollector }

// Reset clears all counters and logs. Intended for UX "session reset" of API metrics.
func Reset() {
    c := defaultCollector
    c.mu.Lock()
    defer c.mu.Unlock()
    c.Chat = FeatureTotals{}
    c.Translate = FeatureTotals{}
    c.Summarize = FeatureTotals{}
    c.Logs = nil
}
