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
    modeSpeechmatics  translateMode = "speechmatics"
    modeAIRolling     translateMode = "ai_rolling"
    modeAICompressed  translateMode = "ai_compressed"
)

type clientMessage struct {
    Type    string           `json:"type"`
    Mode    *translateMode   `json:"mode,omitempty"`
    Config  *clientConfig    `json:"config,omitempty"`
    Payload *clientPayload   `json:"payload,omitempty"`
}

type clientConfig struct {
    RollingWindowChars int `json:"rolling_window_chars,omitempty"`
    BacklogCharLimit   int `json:"backlog_char_limit,omitempty"`
    KeepLastSegments   int `json:"keep_last_segments,omitempty"`
    Model              string `json:"model,omitempty"`
    // Aggregation controls to reduce choppy translations
    MinChunkChars      int     `json:"min_chunk_chars,omitempty"`
    FlushGapSeconds    float64 `json:"flush_gap_seconds,omitempty"`
    // Paragraph batching
    ParagraphWindowSeconds float64 `json:"paragraph_window_seconds,omitempty"`
    MaxSentences           int     `json:"max_sentences,omitempty"`
    // Concurrency controls
    TranslateWorkers       int     `json:"translate_workers,omitempty"`
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
}

type connState struct {
    mode translateMode

    // Rolling context (chars-based window)
    rollingWindowChars int
    recentBuffer       string   // concatenated last N chars (original EN transcript)
    recentSegments     []string // recent segments list (EN)

    // Compressed context
    summary           string
    backlogBuf        bytes.Buffer
    backlogCharLimit  int
    keepLastSegments  int

    // Shared
    tr         *openai.Translator
    selectedModel string
    mu         sync.Mutex

    // Init handshake received
    inited bool

    // Aggregation state per speaker
    speakers map[string]*aggState

    // Aggregation config
    minChunkChars   int
    flushGapSeconds float64

    // Paragraph batching per speaker
    paragraphs map[string]*paraState
    paragraphWindowSeconds float64
    maxSentences           int

    // Translation job system
    translateWorkers int
}

type aggState struct {
    buffer     string
    startTime  float64
    lastEnd    float64
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
        minChunkChars:      24,
        flushGapSeconds:    1.4,
        paragraphs:              make(map[string]*paraState),
        paragraphWindowSeconds:  2.5,
        maxSentences:            2,
    
        translateWorkers:        3,
    }
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

func (st *connState) ensureTranslator() error {
    // Always create translator if nil. Recreate if selectedModel differs from env default.
    if st.tr != nil {
        return nil
    }
    cfg, err := openai.NewConfigFromEnv()
    if err != nil {
        return err
    }
    if st.selectedModel != "" {
        cfg.Model = st.selectedModel
    }
    st.tr = openai.NewTranslator(cfg)
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
    if c.RollingWindowChars > 0 {
        st.rollingWindowChars = c.RollingWindowChars
    }
    if c.BacklogCharLimit > 0 {
        st.backlogCharLimit = c.BacklogCharLimit
    }
    if c.KeepLastSegments > 0 {
        st.keepLastSegments = c.KeepLastSegments
    }
    if c.Model != "" {
        // Update desired model and force re-init of translator
        st.selectedModel = c.Model
        st.tr = nil
    }
    if c.MinChunkChars > 0 {
        st.minChunkChars = c.MinChunkChars
    }
    if c.FlushGapSeconds > 0 {
        st.flushGapSeconds = c.FlushGapSeconds
    }
    if c.ParagraphWindowSeconds > 0 {
        st.paragraphWindowSeconds = c.ParagraphWindowSeconds
    }
    if c.MaxSentences > 0 {
        st.maxSentences = c.MaxSentences
    }

    if c.TranslateWorkers > 0 {
        st.translateWorkers = c.TranslateWorkers
    }
}

func isSentenceEnding(s string) bool {
    s = strings.TrimSpace(s)
    if s == "" {
        return false
    }
    // Check common end punctuation
    ends := []string{".", "?", "!", "銆?, "锛?, "锛?}
    for _, e := range ends {
        if strings.HasSuffix(s, e) {
            return true
        }
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
    if (ps.lastTime-ps.firstTime) >= st.paragraphWindowSeconds {
        combined, s, e = combineSentences(ps.list)
        ps.list = nil
        return true, combined, s, e
    }
    return false, "", 0, 0
}

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
    // Update rolling buffer
    st.recentBuffer += "\\n" + seg
    if len(st.recentBuffer) > st.rollingWindowChars {
        overflow := len(st.recentBuffer) - st.rollingWindowChars
        if overflow > 0 && overflow < len(st.recentBuffer) {
            st.recentBuffer = st.recentBuffer[overflow:]
        }
    }

    // Update backlog for compressed mode
    st.backlogBuf.WriteString("\n")
    st.backlogBuf.WriteString(seg)
    // Update backlog for compressed mode
    st.backlogBuf.WriteString("\\n")
    st.backlogBuf.WriteString(seg)
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
        builder.WriteString("Summary:
")
        builder.WriteString(st.summary)
        builder.WriteString("
---
")
    }
    builder.WriteString("Recent:
")
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

        // Ensure translator exists
        if err := st.ensureTranslator(); err != nil {
            log.Printf("compress init error: %v", err)
            return
        }
        // Call summarization with generous timeout to avoid blocking real-time path
        cctx, cancel := context.WithTimeout(ctx, 50*time.Second)
        defer cancel()
        summary, err := st.tr.Summarize(cctx, prev, backlog)
        if err != nil {
            log.Printf("summarize error: %v", err)
            return
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
    ctx := r.Context()

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
            if !inited { continue }
            // Maintain state with original EN segment
            state.addSegmentEN(seg)
            // Possibly trigger async compression
            if state.mode == modeAICompressed { state.maybeCompressAsync(ctx) }

            // Build context
            var contextText string
            switch state.mode {
            case modeAIRolling:
                contextText = state.contextForRolling()
            case modeAICompressed:
                contextText = state.contextForCompressed()
            default:
                // If not AI mode, ignore
                continue
            }

            // Ensure translator configured
            if err := state.ensureTranslator(); err != nil {
                log.Printf("translator init error: %v", err)
                // Inform client
                _ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"message":"Error","reason":%q}`, escapeJSON(err.Error()))))
                continue
            }

            // Append to aggregator and flush sentences -> batch into paragraphs
            if flushed, sentText, sSent, eSent := state.handleAggregation(cli.Payload.Speaker, seg, cli.Payload.StartTime, cli.Payload.EndTime); flushed {
                if doFlush, paraText, sPara, ePara := state.enqueueSentence(cli.Payload.Speaker, sentText, sSent, eSent); doFlush {
                    tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
                    translated, err := state.tr.Translate(tctx, contextText, paraText)
                    cancel()
                    if err != nil {
                        log.Printf("translate error: %v", err)
                        _ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"message":"Error","reason":%q}`, escapeJSON(err.Error()))))
                        continue
                    }

                    resp := serverTranslation{
                        Message: "AddTranslation",
                        Results: []serverTranslationOne{{
                            Speaker:   cli.Payload.Speaker,
                            Content:   strings.TrimSpace(translated),
                            StartTime: sPara,
                            EndTime:   ePara,
                        }},
                    }
                    b, _ := json.Marshal(resp)
                    _ = conn.WriteMessage(websocket.TextMessage, b)
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
type translateJob struct {
    seq       int
    speaker   string
    text      string
    startTime float64
    endTime   float64
    context   string
}

type translateResult struct {
    seq       int
    speaker   string
    content   string
    startTime float64
    endTime   float64
    err       error
}









