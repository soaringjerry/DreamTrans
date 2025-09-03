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
    mu         sync.Mutex
}

func defaultConnState() *connState {
    st := &connState{
        mode:               modeAIRolling,
        rollingWindowChars: 1000,
        backlogCharLimit:   1800,
        keepLastSegments:   6,
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
    if st.tr != nil {
        return nil
    }
    cfg, err := openai.NewConfigFromEnv()
    if err != nil {
        return err
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
}

func (st *connState) addSegmentEN(seg string) {
    st.mu.Lock()
    defer st.mu.Unlock()
    st.recentSegments = append(st.recentSegments, seg)
    // Update rolling buffer
    st.recentBuffer += "\n" + seg
    if len(st.recentBuffer) > st.rollingWindowChars {
        // Trim from start to fit window
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
            // Maintain state with original EN segment
            state.addSegmentEN(seg)
            // Possibly trigger async compression
            state.maybeCompressAsync(ctx)

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
                _ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"message":"Error","reason":"%s"}`, escapeJSON(err.Error()))))
                continue
            }

            // Translate (non-streaming final)
            tctx, cancel := context.WithTimeout(ctx, 25*time.Second)
            translated, err := state.tr.Translate(tctx, contextText, seg)
            cancel()
            if err != nil {
                log.Printf("translate error: %v", err)
                _ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(`{"message":"Error","reason":"%s"}`, escapeJSON(err.Error()))))
                continue
            }

            // Send final translation packet
            resp := serverTranslation{
                Message: "AddTranslation",
                Results: []serverTranslationOne{{
                    Speaker:   cli.Payload.Speaker,
                    Content:   strings.TrimSpace(translated),
                    StartTime: cli.Payload.StartTime,
                    EndTime:   cli.Payload.EndTime,
                }},
            }
            b, _ := json.Marshal(resp)
            _ = conn.WriteMessage(websocket.TextMessage, b)
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

