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

	openai "github.com/dreamtrans/backend/internal/adapters/openai_provider"
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
    RollingWindowChars int    `json:"rolling_window_chars,omitempty"`
    BacklogCharLimit   int    `json:"backlog_char_limit,omitempty"`
    KeepLastSegments   int    `json:"keep_last_segments,omitempty"`
    // Back-compat: 'model' used for translation model prior to v1.1
    Model              string `json:"model,omitempty"`
    // New explicit per-feature models
    TranslateModel     string `json:"translate_model,omitempty"`
    SummaryModel       string `json:"summary_model,omitempty"`
    SessionID          string `json:"session_id,omitempty"`
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
    trTrans  *openai.Translator
    trSum    *openai.Translator
    // Selected models per feature
    selectedModelTranslate string
    selectedModelSummary   string
    mu            sync.Mutex

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
}

type aggState struct {
    buffer    string
    startTime float64
    lastEnd   float64
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
        mode:                   modeAIRolling,
        rollingWindowChars:     1000,
        backlogCharLimit:       1800,
        keepLastSegments:       6,
        speakers:               make(map[string]*aggState),
        // Conservative defaults (avoid over-fragmentation)
        // More responsive defaults for short utterances
        minChunkChars:          16,
        flushGapSeconds:        0.9,
        paragraphs:             make(map[string]*paraState),
        paragraphWindowSeconds: 1.8,
        maxSentences:           2,

        translateWorkers: 3,
    }
    // Default models
    st.selectedModelTranslate = "gpt-5-mini"
    st.selectedModelSummary = "gpt-5-mini"
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
    // Defaults for partial translations
    st.partialMinChars = 5
    st.partialMaxDelaySeconds = 0.5
    return st
}

func (st *connState) ensureTranslatorTrans() error {
    if st.trTrans != nil { return nil }
    cfg, err := openai.NewConfigFromEnv()
    if err != nil { return err }
    if st.selectedModelTranslate != "" { cfg.Model = st.selectedModelTranslate }
    st.trTrans = openai.NewTranslator(cfg)
    return nil
}

func (st *connState) ensureTranslatorSum() error {
    if st.trSum != nil { return nil }
    cfg, err := openai.NewConfigFromEnv()
    if err != nil { return err }
    if st.selectedModelSummary != "" { cfg.Model = st.selectedModelSummary }
    st.trSum = openai.NewTranslator(cfg)
    return nil
}

func (st *connState) setMode(m translateMode) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.mode = m
}

func (st *connState) applyConfig(c *clientConfig) {
    if c == nil { return }
    st.mu.Lock()
    defer st.mu.Unlock()

    setPosInt := func(dst *int, v int) { if v > 0 { *dst = v } }
    setPosFloat := func(dst *float64, v float64) { if v > 0 { *dst = v } }

    setPosInt(&st.rollingWindowChars, c.RollingWindowChars)
    setPosInt(&st.backlogCharLimit, c.BacklogCharLimit)
    setPosInt(&st.keepLastSegments, c.KeepLastSegments)
    // Back-compat: c.Model is used as translate model if provided
    if c.Model != "" { st.selectedModelTranslate = c.Model; st.trTrans = nil }
    if c.TranslateModel != "" { st.selectedModelTranslate = c.TranslateModel; st.trTrans = nil }
    if c.SummaryModel != "" { st.selectedModelSummary = c.SummaryModel; st.trSum = nil }
    if c.SessionID != "" { st.sessionID = c.SessionID }
    setPosInt(&st.minChunkChars, c.MinChunkChars)
    setPosFloat(&st.flushGapSeconds, c.FlushGapSeconds)
    setPosFloat(&st.paragraphWindowSeconds, c.ParagraphWindowSeconds)
    setPosInt(&st.maxSentences, c.MaxSentences)
    setPosInt(&st.translateWorkers, c.TranslateWorkers)

    // Experimental flags
    st.experimentalStreaming = c.ExperimentalStreaming
    st.experimentalSmart = c.ExperimentalSmart

    // Partials (kept for compatibility; currently unused in translator)
    setPosInt(&st.partialMinChars, c.PartialMinChars)
    setPosFloat(&st.partialMaxDelaySeconds, c.PartialMaxDelaySeconds)

    // Prompts
    if c.TranslatePrompt != "" { st.translatePrompt = c.TranslatePrompt }
    if c.SummaryPrompt != "" { st.summaryPrompt = c.SummaryPrompt }
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
	case '.', '?', '!', '\u3002', '\uFF1F', '\uFF01':
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
		if text != "" && len([]rune(text)) >= st.minChunkChars {
			return true, text, s, e
		}
		// if too short, treat as not flushed
		// fallthrough to no flush
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
	if isSentenceEnding(seg) && len([]rune(a.buffer)) >= st.minChunkChars {
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
	return builder.String()
}
func (st *connState) maybeCompressAsync(ctx context.Context) {
	st.mu.Lock()
	if !st.inited {
		st.mu.Unlock()
		return
	}
	need := st.backlogBuf.Len() >= st.backlogCharLimit
	backlog := st.backlogBuf.String()
	prev := st.summary
	st.mu.Unlock()
	if !need {
		return
	}
	go func() {
		// Avoid overlapping compress jobs by resetting backlog quickly
		st.mu.Lock()
		st.backlogBuf.Reset()
		st.mu.Unlock()

	    // Call summarization with generous timeout to avoid blocking real-time path
    cctx, cancel := context.WithTimeout(ctx, 50*time.Second)
    defer cancel()
                    // Always use usage-capable summarization for accurate API metrics
                    var summary string
                    var err error
                    var u *openai.Usage
                    startAt := time.Now()
                    // Ensure summarization translator
                    if e := st.ensureTranslatorSum(); e != nil { log.Printf("summarize init error: %v", e); return }
                    if strings.TrimSpace(st.summaryPrompt) != "" {
                        var e error
                        summary, u, e = st.trSum.SummarizeWithSystemPromptUsage(cctx, prev, backlog, st.summaryPrompt)
                        err = e
                    } else {
                        // Default summarization system prompt (kept concise)
                        constSys := "You are a precise context compressor. Summarize English conversation text for downstream translation. Keep names, entities, topics, and unresolved references. Keep it concise and information-dense. Output in English."
                        var e error
                        summary, u, e = st.trSum.SummarizeWithSystemPromptUsage(cctx, prev, backlog, constSys)
                        err = e
                    }
                    if err != nil {
                        log.Printf("summarize error: %v", err)
                        return
                    }
                    if u != nil {
                        metrics.RecordSummarize(&metrics.Usage{PromptTokens: u.PromptTokens, CompletionTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, Model: u.Model}, time.Since(startAt).Milliseconds())
                    }
		st.mu.Lock()
		st.summary = summary
		st.mu.Unlock()
	}()
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
        seq int64
        speaker string
        context string
        text string
        s, e float64
    }
    type translateResult struct {
        seq int64
        speaker string
        content string
        s, e float64
        model string
        latencyMs int64
        err error
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
                    if !ok { return }
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
                        out, usage, err = state.trTrans.TranslateWithSystemPromptUsage(tctx, job.context, job.text, state.translatePrompt)
                    } else {
                        out, usage, err = state.trTrans.TranslateWithUsage(tctx, job.context, job.text)
                    }
                    cancel()
                    if err != nil {
                        results <- translateResult{seq: job.seq, speaker: job.speaker, s: job.s, e: job.e, err: err}
                        continue
                    }
                    latency := time.Since(startAt).Milliseconds()
                    if usage != nil {
                        metrics.RecordTranslate(&metrics.Usage{PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens, TotalTokens: usage.TotalTokens, Model: usage.Model}, latency)
                    }
                    results <- translateResult{seq: job.seq, speaker: job.speaker, content: strings.TrimSpace(out), s: job.s, e: job.e, model: state.selectedModelTranslate, latencyMs: latency}
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
                if !ok { return }
                if res.err != nil {
                    _ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"message":"Error","reason":%q}`, escapeJSON(res.err.Error()))))
                    continue
                }
                buffer[res.seq] = res
                for {
                    r, ok := buffer[expectSeq]
                    if !ok { break }
                    resp := serverTranslation{Message: "AddTranslation", Results: []serverTranslationOne{{
                        Speaker: r.speaker, Content: r.content, StartTime: r.s, EndTime: r.e, Model: r.model, LatencyMs: r.latencyMs,
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
			if state.mode == modeAICompressed {
				state.maybeCompressAsync(ctx)
			}

			// Build context (only for AI translation). RAG ingestion runs regardless of mode.
			var contextText string
			aiActive := false
            switch state.mode {
            case modeAIRolling:
                if state.experimentalSmart {
                    contextText = state.contextForCompressed(); aiActive = true
                } else {
                    contextText = state.contextForRolling(); aiActive = true
                }
            case modeAICompressed:
                contextText = state.contextForCompressed(); aiActive = true
            }

			// Ingest via RAG when paragraph flush happens (below). Translation only if aiActive.

			// Append to aggregator and flush sentences -> batch into paragraphs
            if flushed, sentText, sSent, eSent := state.handleAggregation(cli.Payload.Speaker, seg, cli.Payload.StartTime, cli.Payload.EndTime); flushed {
                if doFlush, paraText, sPara, ePara := state.enqueueSentence(cli.Payload.Speaker, sentText, sSent, eSent); doFlush {
					// RAG ingestion (best-effort)
					if state.ragSvc != nil {
						go func(sessionID, speaker, text string, sT, eT float64) {
							if sessionID == "" { sessionID = "default" }
							if err := state.ragSvc.IngestParagraph(ctx, sessionID, speaker, text, sT, eT); err != nil {
								log.Printf("rag ingest error: %v", err)
							}
						}(state.sessionID, cli.Payload.Speaker, paraText, sPara, ePara)
					}

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
