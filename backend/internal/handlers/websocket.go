package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	openai "github.com/dreamtrans/backend/internal/adapters/openai_provider"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/metrics"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		// WARNING: dev only. Restrict origins in production.
		return true
	},
}

type translateMode string

const (
	modeSpeechmatics translateMode = "speechmatics"
	modeAIRolling    translateMode = "ai_rolling"
	modeAICompressed translateMode = "ai_compressed"
)

type clientMessage struct {
	Type    string         `json:"type"`
	Mode    *translateMode `json:"mode,omitempty"`
	Config  *clientConfig  `json:"config,omitempty"`
	Payload *clientPayload `json:"payload,omitempty"`
}

type clientConfig struct {
	RollingWindowChars int `json:"rolling_window_chars,omitempty"`
	BacklogCharLimit   int `json:"backlog_char_limit,omitempty"`
	KeepLastSegments   int `json:"keep_last_segments,omitempty"`
	// Back-compat: 'model' used for translation model prior to v1.1
	Model string `json:"model,omitempty"`
	// New explicit per-feature models
	TranslateModel string `json:"translate_model,omitempty"`
	SummaryModel   string `json:"summary_model,omitempty"`
	SessionID      string `json:"session_id,omitempty"`
	// Aggregation controls to reduce choppy translations
	MinChunkChars   int     `json:"min_chunk_chars,omitempty"`
	FlushGapSeconds float64 `json:"flush_gap_seconds,omitempty"`
	// Paragraph batching
	ParagraphWindowSeconds float64 `json:"paragraph_window_seconds,omitempty"`
	MaxSentences           int     `json:"max_sentences,omitempty"`
	// Concurrency controls
	TranslateWorkers int `json:"translate_workers,omitempty"`

	// Experimental flags
	ExperimentalStreaming bool `json:"experimental_streaming,omitempty"`
	ExperimentalSmart     bool `json:"experimental_smart,omitempty"`

	// Low latency partials
	PartialMinChars        int     `json:"partial_min_chars,omitempty"`
	PartialMaxDelaySeconds float64 `json:"partial_max_delay_seconds,omitempty"`

	// Prompt overrides
	TranslatePrompt string `json:"translate_prompt,omitempty"`
	SummaryPrompt   string `json:"summary_prompt,omitempty"`

	// Summary rate limit (to reduce token cost)
	SummaryMinIntervalSeconds float64 `json:"summary_min_interval_seconds,omitempty"`
	SummaryMinChars           int     `json:"summary_min_chars,omitempty"`
	SummaryMaxBacklogChars    int     `json:"summary_max_backlog_chars,omitempty"`

	// How many translated ZH segments to keep in context
	KeepLastTranslatedSegments int `json:"keep_last_translated_segments,omitempty"`

	// Enable/disable summarization (LLM) paths
	// If DisableSummarization is true, server won't call LLM to summarize.
	// If SummarizationEnabled is true, it forces enabling summarization.
	DisableSummarization bool `json:"disable_summarization,omitempty"`
	SummarizationEnabled bool `json:"summarization_enabled,omitempty"`
	// Embeddings / RAG ingest toggle
	DisableEmbeddings bool `json:"disable_embeddings,omitempty"`
	EmbeddingsEnabled bool `json:"embeddings_enabled,omitempty"`
}

type clientPayload struct {
	Speaker    string  `json:"speaker"`
	Transcript string  `json:"transcript"`
	StartTime  float64 `json:"start_time"`
	EndTime    float64 `json:"end_time"`
}

type serverTranslation struct {
	Message string                 `json:"message"` // AddTranslation or AddPartialTranslation
	Results []serverTranslationOne `json:"results"`
}

type serverTranslationOne struct {
	Speaker   string  `json:"speaker"`
	Content   string  `json:"content"`
	Original  string  `json:"original,omitempty"`
	StartTime float64 `json:"start_time"`
	EndTime   float64 `json:"end_time"`
	Model     string  `json:"model,omitempty"`
	LatencyMs int64   `json:"latency_ms,omitempty"`
}

type connState struct {
	mode translateMode

	// Rolling context (chars-based window)
	rollingWindowChars int
	recentBuffer       string   // concatenated last N chars (original EN transcript)
	recentSegments     []string // recent segments list (EN)

	// Compressed context
	summary          string
	backlogBuf       bytes.Buffer
	backlogCharLimit int
	keepLastSegments int

	// Translators per feature
	trTrans *openai.Translator
	trSum   *openai.Translator
	// Selected models per feature
	selectedModelTranslate string
	selectedModelSummary   string
	mu                     sync.Mutex

	// Init handshake received
	inited bool

	// Aggregation state per speaker
	speakers map[string]*aggState

	// Aggregation config
	minChunkChars   int
	flushGapSeconds float64

	// Paragraph batching per speaker
	paragraphs             map[string]*paraState
	paragraphWindowSeconds float64
	maxSentences           int

	// Translation job system
	translateWorkers int

	// RAG
	sessionID string
	ragSvc    *rag.Service

	// Experimental flags
	experimentalStreaming bool
	experimentalSmart     bool

	// Partial translation params
	partialMinChars        int
	partialMaxDelaySeconds float64

	// Prompt overrides
	translatePrompt string
	summaryPrompt   string

	// Recent translated ZH segments for style/logic continuity
	recentTranslated   []string
	keepLastTranslated int

	// Incremental summary rate limit
	lastSummaryAt          time.Time
	summaryBacklog         bytes.Buffer
	summaryMinIntervalSec  float64
	summaryMinChars        int
	summaryMaxBacklogChars int

	// Feature toggles
	summarizationEnabled bool

	// RAG live batching (decoupled from translation batching)
	ragBuffers         map[string]*ragState
	ragMinChars        int
	ragMinSpanSeconds  float64
	ragFlushGapSeconds float64
}

type aggState struct {
	buffer    string
	startTime float64
	lastEnd   float64
}

type ragState struct {
	buffer    string
	startTime float64
	lastEnd   float64
	charCount int
}

type sentence struct {
	text      string
	startTime float64
	endTime   float64
}

type paraState struct {
	list      []sentence
	firstTime float64
	lastTime  float64
}

func defaultConnState() *connState {
	st := &connState{
		mode:               modeAIRolling,
		rollingWindowChars: 1000,
		backlogCharLimit:   1800,
		keepLastSegments:   6,
		speakers:           make(map[string]*aggState),
		// Conservative defaults (avoid over-fragmentation)
		// More responsive defaults for short utterances
		minChunkChars:          16,
		flushGapSeconds:        0.9,
		paragraphs:             make(map[string]*paraState),
		paragraphWindowSeconds: 1.8,
		maxSentences:           2,

		translateWorkers:     3,
		summarizationEnabled: false,
		ragBuffers:           make(map[string]*ragState),
		ragMinChars:          80,
		ragMinSpanSeconds:    3.5,
		ragFlushGapSeconds:   2.5,
	}
	applyCentralDefaults(st)
	// Allow env overrides for server-side defaults
	if v := os.Getenv("ROLLING_CONTEXT_CHARS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			st.rollingWindowChars = n
		}
	}
	if v := os.Getenv("COMPRESS_BACKLOG_CHARS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			st.backlogCharLimit = n
		}
	}
	if v := os.Getenv("COMPRESS_KEEP_LAST_SEGMENTS"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			st.keepLastSegments = n
		}
	}
	return st
}

func applyCentralDefaults(st *connState) {
	cfg := config.Get()
	st.selectedModelTranslate = cfg.Models.Translate
	if st.selectedModelTranslate == "" {
		st.selectedModelTranslate = "gpt-4.1-mini"
	}
	st.selectedModelSummary = cfg.Models.Summary
	if st.selectedModelSummary == "" {
		st.selectedModelSummary = "gpt-5-chat-latest"
	}
	// Defaults for partial translations
	st.partialMinChars = 5
	st.partialMaxDelaySeconds = 0.5
	// Recent translated ZH segments
	if cfg.Translation.KeepLastTranslated > 0 {
		st.keepLastTranslated = cfg.Translation.KeepLastTranslated
	} else {
		st.keepLastTranslated = 3
	}
	// Summary rate limit defaults
	if cfg.Summary.MinIntervalSeconds > 0 {
		st.summaryMinIntervalSec = cfg.Summary.MinIntervalSeconds
	} else {
		st.summaryMinIntervalSec = 30
	}
	if cfg.Summary.MinChars > 0 {
		st.summaryMinChars = cfg.Summary.MinChars
	} else {
		st.summaryMinChars = 300
	}
	if cfg.Summary.MaxBacklogChars > 0 {
		st.summaryMaxBacklogChars = cfg.Summary.MaxBacklogChars
	} else {
		st.summaryMaxBacklogChars = 1200
	}
	// Prompts defaults from config
	st.translatePrompt = cfg.Prompts.Translate
	st.summaryPrompt = cfg.Prompts.Summary
}

func (st *connState) ensureTranslatorTrans() error {
	if st.trTrans != nil {
		return nil
	}
	cfg, err := openai.NewConfigFromEnv()
	if err != nil {
		return err
	}
	if st.selectedModelTranslate != "" {
		cfg.Model = st.selectedModelTranslate
	}
	st.trTrans = openai.NewTranslator(cfg)
	return nil
}

func (st *connState) ensureTranslatorSum() error {
	if st.trSum != nil {
		return nil
	}
	cfg, err := openai.NewConfigFromEnv()
	if err != nil {
		return err
	}
	if st.selectedModelSummary != "" {
		cfg.Model = st.selectedModelSummary
	}
	st.trSum = openai.NewTranslator(cfg)
	return nil
}

func (st *connState) setMode(m translateMode) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.mode = m
}

func (st *connState) applyConfig(c *clientConfig) {
	if c == nil {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.applyNumericConfig(c)
	st.applyModelConfig(c)
	st.applyPromptConfig(c)
	st.applySummaryRateConfig(c)
	st.applyFeatureToggles(c)
}

func (st *connState) applyNumericConfig(c *clientConfig) {
	setPosInt := func(dst *int, v int) {
		if v > 0 {
			*dst = v
		}
	}
	setPosFloat := func(dst *float64, v float64) {
		if v > 0 {
			*dst = v
		}
	}
	setPosInt(&st.rollingWindowChars, c.RollingWindowChars)
	setPosInt(&st.backlogCharLimit, c.BacklogCharLimit)
	setPosInt(&st.keepLastSegments, c.KeepLastSegments)
	if c.SessionID != "" {
		st.sessionID = c.SessionID
	}
	setPosInt(&st.minChunkChars, c.MinChunkChars)
	setPosFloat(&st.flushGapSeconds, c.FlushGapSeconds)
	setPosFloat(&st.paragraphWindowSeconds, c.ParagraphWindowSeconds)
	setPosInt(&st.maxSentences, c.MaxSentences)
	setPosInt(&st.translateWorkers, c.TranslateWorkers)
	// partials
	setPosInt(&st.partialMinChars, c.PartialMinChars)
	setPosFloat(&st.partialMaxDelaySeconds, c.PartialMaxDelaySeconds)
	// experimental flags
	st.experimentalStreaming = c.ExperimentalStreaming
	st.experimentalSmart = c.ExperimentalSmart
	// recent ZH context
	setPosInt(&st.keepLastTranslated, c.KeepLastTranslatedSegments)
}

func (st *connState) applyModelConfig(c *clientConfig) {
	if strings.TrimSpace(c.Model) != "" {
		st.selectedModelTranslate = c.Model
		st.trTrans = nil
	}
	if strings.TrimSpace(c.TranslateModel) != "" {
		st.selectedModelTranslate = c.TranslateModel
		st.trTrans = nil
	}
	if strings.TrimSpace(c.SummaryModel) != "" {
		st.selectedModelSummary = c.SummaryModel
		st.trSum = nil
	}
}

func (st *connState) applyPromptConfig(c *clientConfig) {
	if strings.TrimSpace(c.TranslatePrompt) != "" {
		st.translatePrompt = c.TranslatePrompt
	}
	if strings.TrimSpace(c.SummaryPrompt) != "" {
		st.summaryPrompt = c.SummaryPrompt
	}
}

func (st *connState) applySummaryRateConfig(c *clientConfig) {
	setPosInt := func(dst *int, v int) {
		if v > 0 {
			*dst = v
		}
	}
	setPosFloat := func(dst *float64, v float64) {
		if v > 0 {
			*dst = v
		}
	}
	setPosFloat(&st.summaryMinIntervalSec, c.SummaryMinIntervalSeconds)
	setPosInt(&st.summaryMinChars, c.SummaryMinChars)
	setPosInt(&st.summaryMaxBacklogChars, c.SummaryMaxBacklogChars)
}

func (st *connState) applyFeatureToggles(c *clientConfig) {
	// summarization
	if c.DisableSummarization {
		st.summarizationEnabled = false
		if st.ragSvc != nil {
			st.ragSvc.SetIngestSummarizeEnabled(false)
			st.ragSvc.SetSummaryOutputEnabled(false)
		}
	}
	if c.SummarizationEnabled {
		st.summarizationEnabled = true
		if st.ragSvc != nil {
			st.ragSvc.SetIngestSummarizeEnabled(true)
			st.ragSvc.SetSummaryOutputEnabled(true)
		}
	}
	// embeddings
	if c.DisableEmbeddings {
		if st.ragSvc != nil {
			st.ragSvc.SetEmbedEnabled(false)
		}
	}
	if c.EmbeddingsEnabled {
		if st.ragSvc != nil {
			st.ragSvc.SetEmbedEnabled(true)
		}
	}
}

func isSentenceEnding(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	rs := []rune(s)
	if len(rs) == 0 {
		return false
	}
	last := rs[len(rs)-1]
	switch last {
	case '.', '?', '!', ';', '\n', '\u3002', '\uFF1F', '\uFF01', '\uFF1B', '\u2026':
		return true
	}
	return false
}

// handleAggregation appends the segment to speaker buffer and decides whether to flush.
// Returns: (flushed, text, start, end)
func (st *connState) handleAggregation(speaker, seg string, start, end float64) (flushed bool, text string, s, e float64) {
	st.mu.Lock()
	defer st.mu.Unlock()

	a := st.speakers[speaker]
	if a == nil {
		a = &aggState{}
		st.speakers[speaker] = a
	}

	// If there is a long gap between previous end and current start, flush first
	if a.buffer != "" && a.lastEnd > 0 && (start-a.lastEnd) > st.flushGapSeconds {
		text = strings.TrimSpace(a.buffer)
		s := a.startTime
		e := a.lastEnd
		// reset and start new with current
		a.buffer = ""
		a.startTime = 0
		a.lastEnd = 0

		// initialize with current seg after releasing flush
		a.buffer = strings.TrimSpace(seg)
		a.startTime = start
		a.lastEnd = end
		if text != "" {
			return true, text, s, e
		}
		// if empty, fallthrough
		return false, "", 0, 0
	}

	// Normal append
	if a.buffer == "" {
		a.startTime = start
		a.buffer = strings.TrimSpace(seg)
		a.lastEnd = end
	} else {
		// add space if needed between words
		if !strings.HasSuffix(a.buffer, " ") && !strings.HasPrefix(seg, " ") {
			a.buffer += " "
		}
		a.buffer += strings.TrimSpace(seg)
		a.lastEnd = end
	}

	// Decide flush
	// Flush on sentence ending regardless of minChunkChars to avoid missing short utterances
	if isSentenceEnding(seg) {
		text = strings.TrimSpace(a.buffer)
		s := a.startTime
		e := a.lastEnd
		// reset
		a.buffer = ""
		a.startTime = 0
		a.lastEnd = 0
		if text != "" {
			return true, text, s, e
		}
	}
	return false, "", 0, 0
}

// handleRAGAggregation manages a dedicated buffer for RAG ingestion so that
// translation batching can remain conservative while chat retrieval gets fresher context.
func (st *connState) handleRAGAggregation(speaker, seg string, start, end float64) (flushed bool, text string, s, e float64) {
	trimmed := strings.TrimSpace(seg)
	if trimmed == "" {
		return false, "", 0, 0
	}

	st.mu.Lock()
	defer st.mu.Unlock()

	rs := st.ragBuffers[speaker]
	if rs == nil {
		rs = &ragState{}
		st.ragBuffers[speaker] = rs
	}

	if rs.buffer == "" {
		rs.startTime = start
	} else if start-rs.lastEnd >= st.ragFlushGapSeconds {
		text = strings.TrimSpace(rs.buffer)
		s = rs.startTime
		e = rs.lastEnd
		rs.buffer = trimmed
		rs.startTime = start
		rs.lastEnd = end
		rs.charCount = utf8.RuneCountInString(trimmed)
		if text != "" {
			return true, text, s, e
		}
		return false, "", 0, 0
	}

	if rs.buffer != "" && !strings.HasSuffix(rs.buffer, " ") {
		rs.buffer += " "
	}
	rs.buffer += trimmed
	rs.lastEnd = end
	rs.charCount = utf8.RuneCountInString(rs.buffer)

	if isSentenceEnding(seg) {
		text = strings.TrimSpace(rs.buffer)
		s = rs.startTime
		e = rs.lastEnd
		rs.buffer = ""
		rs.startTime = 0
		rs.lastEnd = 0
		rs.charCount = 0
		if text != "" {
			return true, text, s, e
		}
		return false, "", 0, 0
	}

	span := end - rs.startTime
	if rs.charCount >= st.ragMinChars && span >= st.ragMinSpanSeconds {
		text = strings.TrimSpace(rs.buffer)
		s = rs.startTime
		e = rs.lastEnd
		rs.buffer = ""
		rs.startTime = 0
		rs.lastEnd = 0
		rs.charCount = 0
		if text != "" {
			return true, text, s, e
		}
	}
	return false, "", 0, 0
}

// enqueueSentence adds a completed sentence to a paragraph batch and decides whether to flush.
// Returns (flushed, text, start, end)
func (st *connState) enqueueSentence(speaker, text string, start, end float64) (flushed bool, combined string, s, e float64) {
	st.mu.Lock()
	defer st.mu.Unlock()

	ps := st.paragraphs[speaker]
	if ps == nil {
		ps = &paraState{}
		st.paragraphs[speaker] = ps
	}

	if len(ps.list) == 0 {
		ps.firstTime = start
	}
	ps.list = append(ps.list, sentence{text: strings.TrimSpace(text), startTime: start, endTime: end})
	ps.lastTime = end

	// Flush on max sentences
	if len(ps.list) >= st.maxSentences {
		combined, s, e = combineSentences(ps.list)
		ps.list = nil
		return true, combined, s, e
	}
	// Flush if window exceeded
	if (ps.lastTime - ps.firstTime) >= st.paragraphWindowSeconds {
		combined, s, e = combineSentences(ps.list)
		ps.list = nil
		return true, combined, s, e
	}
	return false, "", 0, 0
}

// nolint:gocritic // return names are unnecessary here; keep concise signature
func combineSentences(list []sentence) (string, float64, float64) {
	if len(list) == 0 {
		return "", 0, 0
	}
	var b strings.Builder
	for i, s := range list {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(s.text))
	}
	return strings.TrimSpace(b.String()), list[0].startTime, list[len(list)-1].endTime
}

func (st *connState) addSegmentEN(seg string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.recentSegments = append(st.recentSegments, seg)
	// Update rolling buffer
	st.recentBuffer += "\n" + seg
	if len(st.recentBuffer) > st.rollingWindowChars {
		overflow := len(st.recentBuffer) - st.rollingWindowChars
		if overflow > 0 && overflow < len(st.recentBuffer) {
			st.recentBuffer = st.recentBuffer[overflow:]
		}
	}
	// Update backlog for compressed mode
	st.backlogBuf.WriteString("\n")
	st.backlogBuf.WriteString(seg)
}

func (st *connState) contextForRolling() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	// recentBuffer already limited by chars window
	return st.recentBuffer
}

func (st *connState) contextForCompressed() string {
	st.mu.Lock()
	defer st.mu.Unlock()
	// Build context from summary + last K segments
	var builder strings.Builder
	if st.summary != "" {
		builder.WriteString("Summary:\n")
		builder.WriteString(st.summary)
		builder.WriteString("\n---\n")
	}
	builder.WriteString("Recent:\n")
	start := 0
	if len(st.recentSegments) > st.keepLastSegments {
		start = len(st.recentSegments) - st.keepLastSegments
	}
	for i := start; i < len(st.recentSegments); i++ {
		builder.WriteString("- ")
		builder.WriteString(st.recentSegments[i])
		builder.WriteString("\n")
	}
	if len(st.recentTranslated) > 0 {
		builder.WriteString("---\nRecentZH:\n")
		tzStart := 0
		if len(st.recentTranslated) > st.keepLastTranslated {
			tzStart = len(st.recentTranslated) - st.keepLastTranslated
		}
		for i := tzStart; i < len(st.recentTranslated); i++ {
			builder.WriteString("- ")
			builder.WriteString(st.recentTranslated[i])
			builder.WriteString("\n")
		}
	}
	return builder.String()
}

// maybeCompressAsync was replaced by incremental summarization; keep removed to satisfy linters.

// updateSummaryIncremental merges the previous summary with a small new paragraph chunk.
// Append new paragraph into backlog and maybe update summary according to rate limits.
func (st *connState) updateSummaryIncremental(ctx context.Context, para string) {
	st.mu.Lock()
	if !st.summarizationEnabled {
		st.mu.Unlock()
		return
	}
	// append into backlog
	if para != "" {
		if st.summaryBacklog.Len() > 0 {
			st.summaryBacklog.WriteString("\n")
		}
		st.summaryBacklog.WriteString(para)
		// cap backlog size
		if st.summaryBacklog.Len() > st.summaryMaxBacklogChars {
			s := st.summaryBacklog.String()
			s = s[len(s)-st.summaryMaxBacklogChars:]
			st.summaryBacklog.Reset()
			st.summaryBacklog.WriteString(s)
		}
	}
	// check rate limit
	dueByTime := time.Since(st.lastSummaryAt) >= time.Duration(st.summaryMinIntervalSec*float64(time.Second))
	dueBySize := st.summaryBacklog.Len() >= st.summaryMinChars
	backlog := st.summaryBacklog.String()
	prev := st.summary
	if !(dueByTime || dueBySize) {
		st.mu.Unlock()
		return
	}
	// we will flush backlog now
	st.summaryBacklog.Reset()
	st.mu.Unlock()

	if err := st.ensureTranslatorSum(); err != nil {
		log.Printf("summarize init error: %v", err)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := time.Now()
	var (
		out string
		u   *openai.Usage
		err error
	)
	if strings.TrimSpace(st.summaryPrompt) != "" {
		out, u, err = st.trSum.SummarizeWithSystemPromptUsageRetry(cctx, prev, backlog, st.summaryPrompt, 3)
	} else {
		constSys := "You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English."
		out, u, err = st.trSum.SummarizeWithSystemPromptUsageRetry(cctx, prev, backlog, constSys, 3)
	}
	if err != nil {
		log.Printf("incremental summarize error: %v", err)
		return
	}
	dur := time.Since(start).Milliseconds()
	if u != nil {
		metrics.RecordSummarize(&metrics.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, Model: u.Model}, dur)
		if os.Getenv("OPENAI_DEBUG") == "1" {
			log.Printf("metrics.summarize model=%s tokens p=%d c=%d t=%d latency=%dms", u.Model, u.PromptTokens, u.CompletionTokens, u.TotalTokens, dur)
		}
	} else {
		metrics.RecordSummarizeNoUsage(st.selectedModelSummary, dur)
		if os.Getenv("OPENAI_DEBUG") == "1" {
			log.Printf("metrics.summarize usage missing; model=%s latency=%dms", st.selectedModelSummary, dur)
		}
	}
	st.mu.Lock()
	st.summary = out
	st.lastSummaryAt = time.Now()
	st.mu.Unlock()
}

// nolint:gocyclo
func HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade connection: %v", err)
		return
	}
	defer conn.Close()

	log.Printf("WebSocket connection established from %s", r.RemoteAddr)

	state := defaultConnState()
	ragSvc, err := rag.NewServiceFromEnv()
	if err != nil {
		log.Printf("RAG init error: %v", err)
	} else {
		state.ragSvc = ragSvc
		defer ragSvc.Close()
	}
	ctx := r.Context()

	// Translation concurrency: queue + workers + in-order delivery
	type translateJob struct {
		seq     int64
		speaker string
		context string
		text    string
		s, e    float64
	}
	type translateResult struct {
		seq       int64
		speaker   string
		content   string
		original  string
		s, e      float64
		model     string
		latencyMs int64
		err       error
	}
	jobs := make(chan translateJob, 128)
	results := make(chan translateResult, 128)
	var nextSeq int64 = 1
	var expectSeq int64 = 1
	// Workers
	for i := 0; i < state.translateWorkers; i++ {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job, ok := <-jobs:
					if !ok {
						return
					}
					if err := state.ensureTranslatorTrans(); err != nil {
						results <- translateResult{seq: job.seq, speaker: job.speaker, s: job.s, e: job.e, err: err}
						continue
					}
					tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
					startAt := time.Now()
					var out string
					var err error
					var usage *openai.Usage
					if strings.TrimSpace(state.translatePrompt) != "" {
						out, usage, err = state.trTrans.TranslateWithSystemPromptUsageRetry(tctx, job.context, job.text, state.translatePrompt, 3)
					} else {
						// translate default path with retry via system prompt wrapper using default sys
						out, usage, err = state.trTrans.TranslateWithSystemPromptUsageRetry(tctx, job.context, job.text, "", 3)
					}
					cancel()
					if err != nil {
						results <- translateResult{seq: job.seq, speaker: job.speaker, s: job.s, e: job.e, err: err}
						continue
					}
					latency := time.Since(startAt).Milliseconds()
					if usage != nil {
						metrics.RecordTranslate(&metrics.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model}, latency)
						if os.Getenv("OPENAI_DEBUG") == "1" {
							log.Printf("metrics.translate model=%s tokens p=%d c=%d t=%d latency=%dms", usage.Model, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, latency)
						}
					} else {
						metrics.RecordTranslateNoUsage(state.selectedModelTranslate, latency)
						if os.Getenv("OPENAI_DEBUG") == "1" {
							log.Printf("metrics.translate usage missing; model=%s latency=%dms", state.selectedModelTranslate, latency)
						}
					}
					// Keep recent translated ZH for better continuity
					state.mu.Lock()
					state.recentTranslated = append(state.recentTranslated, strings.TrimSpace(out))
					if len(state.recentTranslated) > state.keepLastTranslated {
						state.recentTranslated = state.recentTranslated[len(state.recentTranslated)-state.keepLastTranslated:]
					}
					state.mu.Unlock()
					results <- translateResult{seq: job.seq, speaker: job.speaker, content: strings.TrimSpace(out), original: job.text, s: job.s, e: job.e, model: state.selectedModelTranslate, latencyMs: latency}
				}
			}
		}()
	}
	// Delivery: ensure in-order writes
	go func() {
		buffer := map[int64]translateResult{}
		for {
			select {
			case <-ctx.Done():
				return
			case res, ok := <-results:
				if !ok {
					return
				}
				if res.err != nil {
					_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"message":"Error","reason":%q}`, escapeJSON(res.err.Error()))))
					continue
				}
				buffer[res.seq] = res
				for {
					r, ok := buffer[expectSeq]
					if !ok {
						break
					}
					resp := serverTranslation{Message: "AddTranslation", Results: []serverTranslationOne{{
						Speaker: r.speaker, Content: r.content, Original: r.original, StartTime: r.s, EndTime: r.e, Model: r.model, LatencyMs: r.latencyMs,
					}}}
					b, _ := json.Marshal(resp)
					_ = conn.WriteMessage(websocket.TextMessage, b)
					delete(buffer, expectSeq)
					expectSeq++
				}
			}
		}
	}()

	// Read loop
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket error: %v", err)
			}
			log.Printf("WebSocket connection closed from %s", r.RemoteAddr)
			break
		}

		if messageType == websocket.BinaryMessage {
			// Not used
			continue
		}

		var cli clientMessage
		if err := json.Unmarshal(message, &cli); err != nil {
			log.Printf("WS: invalid JSON: %v", err)
			// ignore malformed
			continue
		}

		switch strings.ToLower(cli.Type) {
		case "init":
			if cli.Mode != nil {
				state.setMode(*cli.Mode)
			}
			state.applyConfig(cli.Config)
			// If we have a RAG service and a summary model override, enforce it via custom provider
			if state.ragSvc != nil {
				model := state.selectedModelSummary
				state.ragSvc.SetChatConfigProvider(func() (*openai.Config, error) {
					cfg, err := openai.NewConfigFromEnv()
					if err != nil {
						return nil, err
					}
					if model != "" {
						cfg.Model = model
					}
					return cfg, nil
				})
			}
			state.mu.Lock()
			state.inited = true
			state.mu.Unlock()
			// Acknowledge
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"message":"Info","reason":"translator initialized"}`))

		case "transcript":
			if cli.Payload == nil {
				continue
			}
			seg := strings.TrimSpace(cli.Payload.Transcript)
			if seg == "" {
				continue
			}
			// Require init before processing transcripts
			state.mu.Lock()
			inited := state.inited
			state.mu.Unlock()
			if !inited {
				continue
			}
			// Maintain state with original EN segment
			state.addSegmentEN(seg)
			// Possibly trigger async compression
			// Compressed模式不再按大块重压缩，改为在段落flush时做增量摘要，节省tokens

			// RAG live ingestion runs on its own buffers so translation batching can stay conservative.
			if state.ragSvc != nil || state.summarizationEnabled {
				if flushed, ragText, rStart, rEnd := state.handleRAGAggregation(cli.Payload.Speaker, seg, cli.Payload.StartTime, cli.Payload.EndTime); flushed {
					filteredRAG := filterLowInfoText(ragText)
					if state.ragSvc != nil && strings.TrimSpace(filteredRAG) != "" {
						state.ragSvc.RecordLiveParagraph(state.sessionID, cli.Payload.Speaker, ragText, filteredRAG, rStart, rEnd)
						go func(sessionID, speaker, text string, sT, eT float64) {
							if sessionID == "" {
								sessionID = "default"
							}
							if err := state.ragSvc.IngestParagraph(ctx, sessionID, speaker, text, sT, eT); err != nil {
								log.Printf("rag ingest error: %v", err)
							}
						}(state.sessionID, cli.Payload.Speaker, filteredRAG, rStart, rEnd)
					}
					if state.summarizationEnabled && strings.TrimSpace(filteredRAG) != "" && (state.mode == modeAICompressed || (state.mode == modeAIRolling && state.experimentalSmart)) {
						go state.updateSummaryIncremental(ctx, filteredRAG)
					}
				}
			}

			// Build context (only for AI translation).
			var contextText string
			aiActive := false
			switch state.mode {
			case modeAIRolling:
				if state.experimentalSmart {
					contextText = state.contextForCompressed()
					aiActive = true
				} else {
					contextText = state.contextForRolling()
					aiActive = true
				}
			case modeAICompressed:
				contextText = state.contextForCompressed()
				aiActive = true
			}

			// Append to aggregator and flush sentences -> batch into paragraphs
			if flushed, sentText, sSent, eSent := state.handleAggregation(cli.Payload.Speaker, seg, cli.Payload.StartTime, cli.Payload.EndTime); flushed {
				if doFlush, paraText, sPara, ePara := state.enqueueSentence(cli.Payload.Speaker, sentText, sSent, eSent); doFlush {
					if aiActive {
						seq := nextSeq
						nextSeq++
						jobs <- translateJob{seq: seq, speaker: cli.Payload.Speaker, context: contextText, text: paraText, s: sPara, e: ePara}
					}
				}
			}
		default:
			// ignore
		}
	}
}

func escapeJSON(s string) string {
	// minimal escape for embedding into JSON string contexts
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

// Translation job model
// note: removed unused translate job types to satisfy linters

// --------- Noise filtering for incremental summary & RAG ingestion ---------
// filterLowInfoText removes filler/disfluency and very short/repetitive fragments to avoid
// polluting incremental summaries and RAG store. Keeps only lines with minimal signal.
func filterLowInfoText(s string) string {
	// quick path
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	lower := strings.ToLower(t)
	// remove common disfluencies
	repl := []string{" ah ", " uh ", " um ", " hmm ", " okay ", " ok ", " huh ", " ah.", " uh.", " um.", " okay.", " ok.", " hmm.", " huh."}
	for _, r := range repl {
		lower = strings.ReplaceAll(lower, r, " ")
	}
	// normalize punctuation into sentence breaks
	norm := strings.NewReplacer("?", ".", "!", ".", "\n", ". ")
	lower = norm.Replace(lower)
	// split into candidate sentences by period
	parts := strings.Split(lower, ".")
	seen := make(map[string]struct{})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		L := strings.TrimSpace(p)
		if L == "" {
			continue
		}
		// collapse spaces
		L = strings.Join(strings.Fields(L), " ")
		// skip very short lines without numbers
		if len([]rune(L)) < 12 && !strings.ContainsAny(L, "0123456789$") {
			continue
		}
		// skip if dominated by repeats of 'how much' etc.
		if strings.Count(L, "how much") >= 2 || strings.Count(L, "how many") >= 2 {
			L = "price inquiry"
		}
		key := L
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, L)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "; ")
}
