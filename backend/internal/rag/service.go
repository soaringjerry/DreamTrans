package rag

import (
    "context"
    "fmt"
    "math"
    "os"
    "sort"
    "time"

    openaiprovider "github.com/dreamtrans/backend/internal/adapters/openai_provider"
)

// Service coordinates summarization, embedding and retrieval.
type Service struct {
    store     *Store
    embedder  EmbeddingProvider
    chatCfgFn func() (*openaiprovider.Config, error)
}

// NewServiceFromEnv builds a RAG service from environment variables.
func NewServiceFromEnv() (*Service, error) {
    dbPath := os.Getenv("RAG_DB_PATH")
    if dbPath == "" {
        dbPath = "./rag.db"
    }
    st, err := NewStore(dbPath)
    if err != nil { return nil, err }
    emb, err := NewOpenAIEmbeddingFromEnv()
    if err != nil { return nil, err }
    return &Service{store: st, embedder: emb, chatCfgFn: openaiprovider.NewConfigFromEnv}, nil
}

// Close closes underlying store.
func (s *Service) Close() error { return s.store.Close() }

// RecentDocuments exposes recent stored docs for diagnostics/stat endpoints.
func (s *Service) RecentDocuments(sessionID string, limit int) ([]Document, error) {
    return s.store.RecentDocuments(sessionID, limit)
}

// IngestParagraph summarizes the paragraph and stores summary embedding.
func (s *Service) IngestParagraph(ctx context.Context, sessionID, speaker, text string, start, end float64) error {
    // 1) get previous session summary
    prev, err := s.store.GetSessionSummary(sessionID)
    if err != nil { return err }
    // 2) summarize this paragraph (EN summary is preferred)
    cfg, err := s.chatCfgFn()
    if err != nil { return err }
    tr := openaiprovider.NewTranslator(cfg)
    // reuse summarization prompt
    cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
    defer cancel()
    paragraphSummary, err := tr.Summarize(cctx, "", text)
    if err != nil { return fmt.Errorf("summarize paragraph: %w", err) }
    // 3) update session summary with backlog=paragraphSummary
    cctx2, cancel2 := context.WithTimeout(ctx, 40*time.Second)
    updatedSummary, err := tr.Summarize(cctx2, prev, paragraphSummary)
    cancel2()
    if err == nil && updatedSummary != "" {
        _ = s.store.UpdateSessionSummary(sessionID, updatedSummary)
    }
    // 4) embed the paragraphSummary and store
    vec, err := s.embedder.Embed(ctx, paragraphSummary)
    if err != nil { return fmt.Errorf("embed: %w", err) }
    doc := &Document{SessionID: sessionID, Speaker: speaker, StartTime: start, EndTime: end, Original: text, Summary: paragraphSummary, CreatedAt: time.Now().UTC()}
    _, err = s.store.InsertDocumentWithEmbedding(doc, vec)
    return err
}

// QueryTopK returns top K most similar documents and current session summary.
func (s *Service) QueryTopK(ctx context.Context, sessionID, query string, topK, candidate int) ([]Document, string, error) {
    if topK <= 0 { topK = 5 }
    if candidate <= 0 { candidate = 300 }
    // get recent docs
    docs, err := s.store.RecentDocuments(sessionID, candidate)
    if err != nil { return nil, "", err }
    if len(docs) == 0 {
        sum, _ := s.store.GetSessionSummary(sessionID)
        return nil, sum, nil
    }
    ids := make([]int64, 0, len(docs))
    for _, d := range docs { ids = append(ids, d.ID) }
    vecs, err := s.store.LoadEmbeddingsForDocs(ids)
    if err != nil { return nil, "", err }
    qvec, err := s.embedder.Embed(ctx, query)
    if err != nil { return nil, "", err }
    type scored struct { d Document; score float64 }
    list := make([]scored, 0, len(docs))
    qnorm := norm(qvec)
    for _, d := range docs {
        v, ok := vecs[d.ID]
        if !ok { continue }
        s := cosine(qvec, v, qnorm)
        list = append(list, scored{d: d, score: s})
    }
    sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
    if len(list) > topK { list = list[:topK] }
    out := make([]Document, 0, len(list))
    for _, it := range list { out = append(out, it.d) }
    sum, _ := s.store.GetSessionSummary(sessionID)
    return out, sum, nil
}

func norm(v []float32) float64 { var n float64; for _, x := range v { n += float64(x*x) }; return math.Sqrt(n) }
func dot(a, b []float32) float64 {
    if len(a) != len(b) { n := imin(len(a), len(b)); a, b = a[:n], b[:n] }
    var s float64
    for i := range a { s += float64(a[i]) * float64(b[i]) }
    return s
}
func cosine(a, b []float32, anorm float64) float64 {
    if anorm == 0 { anorm = norm(a) }
    bn := norm(b)
    if anorm == 0 || bn == 0 { return 0 }
    return dot(a, b) / (anorm * bn)
}

// BuildAnswer uses retrieved docs and summary to compose an answer via LLM.
func (s *Service) BuildAnswer(ctx context.Context, sessionID, userQuery string, topK int) (string, error) {
    docs, summary, err := s.QueryTopK(ctx, sessionID, userQuery, topK, 300)
    if err != nil { return "", err }
    cfg, err := s.chatCfgFn()
    if err != nil { return "", err }
    tr := openaiprovider.NewTranslator(cfg)
    // Build prompt
    var ctxParts string
    if summary != "" {
        ctxParts += "[Session Summary]\n" + summary + "\n\n"
    }
    if len(docs) > 0 {
        ctxParts += "[Top Contexts]\n"
        for i, d := range docs {
            ctxParts += fmt.Sprintf("(%d) Speaker %s [%.1f-%.1f]: %s\n", i+1, safe(d.Speaker), d.StartTime, d.EndTime, d.Summary)
        }
    }
    sys := "You are a helpful learning assistant. Answer concisely in Chinese using only the provided context. If context is insufficient, say you are unsure."
    user := ctxParts + "\n[Question]\n" + userQuery
    msgs := []map[string]string{{"role":"system","content":sys},{"role":"user","content":user}}
    cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
    defer cancel()
    out, err := tr.Chat(cctx, msgs)
    if err != nil { return "", err }
    return out, nil
}

func safe(s string) string { if s=="" { return "?" }; return s }

func imin(a, b int) int { if a < b { return a }; return b }
