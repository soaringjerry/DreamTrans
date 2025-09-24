package rag

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	openaiprovider "github.com/dreamtrans/backend/internal/adapters/openai_provider"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/metrics"
)

// Service coordinates summarization, embedding and retrieval.
type Service struct {
	store     *Store
	embedder  EmbeddingProvider
	chatCfgFn func() (*openaiprovider.Config, error)
	// When false, IngestParagraph will not call LLM to summarize the paragraph;
	// it will directly use cleaned text for storage/embedding. Default false.
	ingestSummarizeEnabled bool
	// When false, the running session_summary will not be updated at all.
	// Useful to fully disable any summary output in UI.
	summaryOutputEnabled bool
	// When false, vector embeddings will not be computed or stored; retrieval uses only summary (if enabled).
	embedEnabled bool
	// live cache keeps freshest paragraphs before embeddings land
	liveMu         sync.RWMutex
	live           map[string]*liveBuffer
	liveMaxEntries int
	liveMaxAge     time.Duration
}

type liveEntry struct {
	Speaker   string
	Original  string
	Summary   string
	StartTime float64
	EndTime   float64
	AddedAt   time.Time
}

type liveBuffer struct {
	entries []liveEntry
}

func (b *liveBuffer) append(entry liveEntry, maxEntries int, maxAge time.Duration) {
	cutoff := time.Now().Add(-maxAge)
	filtered := b.entries[:0]
	for _, it := range b.entries {
		if it.AddedAt.After(cutoff) {
			filtered = append(filtered, it)
		}
	}
	b.entries = filtered
	for i, it := range b.entries {
		sameTime := math.Abs(it.StartTime-entry.StartTime) < 0.05
		sameText := strings.EqualFold(strings.TrimSpace(it.Original), strings.TrimSpace(entry.Original))
		if sameTime && sameText {
			b.entries[i] = entry
			return
		}
	}
	b.entries = append(b.entries, entry)
	if len(b.entries) > maxEntries {
		b.entries = b.entries[len(b.entries)-maxEntries:]
	}
}

// ChatOverrides allows request-scoped chat configuration.
type ChatOverrides struct {
	APIKey  string
	APIBase string
	Model   string
	Prompt  string
}

// NewServiceFromEnv builds a RAG service from environment variables.
func NewServiceFromEnv() (*Service, error) {
	dbPath := os.Getenv("RAG_DB_PATH")
	if dbPath == "" {
		dbPath = "./rag.db"
	}
	st, err := NewStore(dbPath)
	if err != nil {
		return nil, err
	}
	emb, err := NewOpenAIEmbeddingFromEnv()
	if err != nil {
		return nil, err
	}
	chatCfg := func() (*openaiprovider.Config, error) {
		cfg, err := openaiprovider.NewConfigFromEnv()
		if err != nil {
			return nil, err
		}
		// Use Chat default model for Q&A
		if m := config.Get().Models.Chat; m != "" {
			cfg.Model = m
		}
		return cfg, nil
	}
	return &Service{
		store:                  st,
		embedder:               emb,
		chatCfgFn:              chatCfg,
		ingestSummarizeEnabled: false,
		summaryOutputEnabled:   false,
		embedEnabled:           true,
		live:                   make(map[string]*liveBuffer),
		liveMaxEntries:         18,
		liveMaxAge:             4 * time.Minute,
	}, nil
}

// Close closes underlying store.
func (s *Service) Close() error { return s.store.Close() }

// RecentDocuments exposes recent stored docs for diagnostics/stat endpoints.
func (s *Service) RecentDocuments(sessionID string, limit int) ([]Document, error) {
	return s.store.RecentDocuments(sessionID, limit)
}

// IngestParagraph summarizes the paragraph and stores summary embedding.
func (s *Service) IngestParagraph(ctx context.Context, sessionID, speaker, text string, start, end float64) error {
	// Pre-filter low-information/disfluency to avoid polluting store/summary
	base := cleanParagraph(text)
	if strings.TrimSpace(base) == "" {
		// skip storing pure-noise paragraphs
		return nil
	}
	// 1) get previous session summary
	prev, err := s.store.GetSessionSummary(sessionID)
	if err != nil {
		return err
	}
	// 2) summarize this paragraph（按需，对短段落若仍显嘈杂可直接跳过或轻量清洗）
	// Use Summary model for summarization
	sumCfg, err := openaiprovider.NewConfigFromEnv()
	if err != nil {
		return err
	}
	if m := os.Getenv("OPENAI_SUMMARY_MODEL"); m != "" {
		sumCfg.Model = m
	}
	if m2 := config.Get().Models.Summary; m2 != "" {
		sumCfg.Model = m2
	}
	tr := openaiprovider.NewTranslator(sumCfg)
	modelName := sumCfg.Model
	cconf := config.Get()
	paragraphSummary := ""
	if s.ingestSummarizeEnabled && len(base) >= cconf.Summary.ParMinChars {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		defSumPrompt := "You are a precise context compressor. Summarize English conversation while REMOVING filler/disfluencies, repeated questions, small talk, jokes, and ads. Keep only key facts, decisions, numbers, and topics. Be concise and information-dense. Output in English."
		start1 := time.Now()
		out, u1, err := tr.SummarizeWithSystemPromptUsageRetry(cctx, "", base, defSumPrompt, 3)
		if err != nil || strings.TrimSpace(out) == "" {
			paragraphSummary = base
		} else {
			paragraphSummary = out
		}
		if u1 != nil {
			metrics.RecordSummarize(&metrics.Usage{PromptTokens: u1.PromptTokens, CompletionTokens: u1.CompletionTokens, TotalTokens: u1.TotalTokens, Model: u1.Model}, time.Since(start1).Milliseconds())
		} else {
			metrics.RecordSummarizeNoUsage(modelName, time.Since(start1).Milliseconds())
		}
	} else {
		// for short paragraphs, keep the cleaned content; drop if still too short
		if charCountAlphaNum(base) < 8 {
			return nil
		}
		paragraphSummary = base
		// Record a no-usage summarize event so Performance API shows activity
		metrics.RecordSummarizeNoUsage(modelName, 0)
	}
	// record live context immediately for low-latency retrieval
	s.recordLiveParagraph(sessionID, speaker, base, paragraphSummary, start, end)

	// 3) 会话摘要：仅在开启时更新；否则完全不写入（UI 看起来就是关闭了摘要）
	if s.summaryOutputEnabled {
		bullet := strings.TrimSpace(paragraphSummary)
		if bullet != "" {
			bullet = "- " + bullet
		}
		updatedSummary := appendBullets(prev, bullet, cconf.Summary.MaxLines)
		_ = s.store.UpdateSessionSummary(sessionID, updatedSummary)
	}
	// 4) embed the paragraphSummary and store (only when embeddings enabled)
	if s.embedEnabled {
		vec, err := s.embedder.Embed(ctx, paragraphSummary)
		if err != nil {
			return fmt.Errorf("embed: %w", err)
		}
		doc := &Document{SessionID: sessionID, Speaker: speaker, StartTime: start, EndTime: end, Original: base, Summary: paragraphSummary, CreatedAt: time.Now().UTC()}
		_, err = s.store.InsertDocumentWithEmbedding(doc, vec)
		return err
	}
	return nil
}

// QueryTopK returns top K most similar documents and current session summary.
func (s *Service) QueryTopK(ctx context.Context, sessionID, query string, topK, candidate int) ([]Document, string, error) {
	if topK <= 0 {
		topK = 5
	}
	if candidate <= 0 {
		candidate = 300
	}
	if !s.embedEnabled {
		sum, _ := s.store.GetSessionSummary(sessionID)
		return nil, sum, nil
	}
	// get recent docs
	docs, err := s.store.RecentDocuments(sessionID, candidate)
	if err != nil {
		return nil, "", err
	}
	ids := make([]int64, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.ID)
	}
	vecs, err := s.store.LoadEmbeddingsForDocs(ids)
	if err != nil {
		return nil, "", err
	}
	qvec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, "", err
	}
	type scored struct {
		d     Document
		score float64
	}
	list := make([]scored, 0, len(docs)+8)
	qnorm := norm(qvec)
	now := time.Now()
	dedup := make(map[string]struct{}, len(docs)+8)
	for _, d := range docs {
		v, ok := vecs[d.ID]
		if !ok {
			continue
		}
		s := cosine(qvec, v, qnorm)
		s += recencyBoost(now.Sub(d.CreatedAt))
		key := documentKey(d)
		dedup[key] = struct{}{}
		list = append(list, scored{d: d, score: s})
	}
	liveDocs := s.recentLiveDocuments(sessionID, topK*2)
	for idx, ld := range liveDocs {
		key := documentKey(ld)
		if _, ok := dedup[key]; ok {
			continue
		}
		score := 1.15 + recencyBoost(now.Sub(ld.CreatedAt)) + float64(idx)*0.001
		list = append(list, scored{d: ld, score: score})
		dedup[key] = struct{}{}
	}
	if len(list) == 0 {
		sum, _ := s.store.GetSessionSummary(sessionID)
		return nil, sum, nil
	}
	sort.Slice(list, func(i, j int) bool { return list[i].score > list[j].score })
	if len(list) > topK {
		list = list[:topK]
	}
	out := make([]Document, 0, len(list))
	for _, it := range list {
		out = append(out, it.d)
	}
	sum, _ := s.store.GetSessionSummary(sessionID)
	return out, sum, nil
}

func (s *Service) recordLiveParagraph(sessionID, speaker, original, summary string, start, end float64) {
	original = strings.TrimSpace(original)
	summary = strings.TrimSpace(summary)
	if original == "" {
		return
	}
	if summary == "" {
		summary = original
	}
	if sessionID == "" {
		sessionID = "default"
	}
	entry := liveEntry{
		Speaker:   speaker,
		Original:  original,
		Summary:   summary,
		StartTime: start,
		EndTime:   end,
		AddedAt:   time.Now().UTC(),
	}
	s.liveMu.Lock()
	buf := s.live[sessionID]
	if buf == nil {
		buf = &liveBuffer{}
		s.live[sessionID] = buf
	}
	buf.append(entry, s.liveMaxEntries, s.liveMaxAge)
	s.liveMu.Unlock()
}

// RecordLiveParagraph exposes low-latency live context updates for callers outside the service.
func (s *Service) RecordLiveParagraph(sessionID, speaker, original, summary string, start, end float64) {
	s.recordLiveParagraph(sessionID, speaker, original, summary, start, end)
}

func (s *Service) recentLiveDocuments(sessionID string, limit int) []Document {
	if sessionID == "" {
		sessionID = "default"
	}
	s.liveMu.RLock()
	buf := s.live[sessionID]
	if buf == nil || len(buf.entries) == 0 {
		s.liveMu.RUnlock()
		return nil
	}
	snapshot := append([]liveEntry(nil), buf.entries...)
	s.liveMu.RUnlock()
	if len(snapshot) == 0 {
		return nil
	}
	cutoff := time.Now().Add(-s.liveMaxAge)
	out := make([]Document, 0, len(snapshot))
	for i := len(snapshot) - 1; i >= 0 && len(out) < limit; i-- {
		it := snapshot[i]
		if it.AddedAt.Before(cutoff) {
			continue
		}
		doc := Document{
			ID:        -1,
			SessionID: sessionID,
			Speaker:   it.Speaker,
			StartTime: it.StartTime,
			EndTime:   it.EndTime,
			Original:  it.Original,
			Summary:   it.Summary,
			CreatedAt: it.AddedAt,
			Ephemeral: true,
		}
		out = append(out, doc)
	}
	return out
}

func documentKey(d Document) string {
	base := strings.TrimSpace(d.Summary)
	if base == "" {
		base = strings.TrimSpace(d.Original)
	}
	return fmt.Sprintf("%.2f|%.2f|%s|%s", d.StartTime, d.EndTime, strings.ToLower(strings.TrimSpace(d.Speaker)), strings.ToLower(base))
}

func recencyBoost(age time.Duration) float64 {
	if age < 0 {
		age = 0
	}
	sec := age.Seconds()
	if sec <= 30 {
		return 0.35
	}
	if sec > 900 {
		return 0
	}
	return 0.35 * math.Exp(-sec/240)
}

func norm(v []float32) float64 {
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	return math.Sqrt(n)
}
func dot(a, b []float32) float64 {
	if len(a) != len(b) {
		n := imin(len(a), len(b))
		a, b = a[:n], b[:n]
	}
	var s float64
	for i := range a {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}
func cosine(a, b []float32, anorm float64) float64 {
	if anorm == 0 {
		anorm = norm(a)
	}
	bn := norm(b)
	if anorm == 0 || bn == 0 {
		return 0
	}
	return dot(a, b) / (anorm * bn)
}

// BuildAnswer uses retrieved docs and summary to compose an answer via LLM.
func (s *Service) BuildAnswer(ctx context.Context, sessionID, userQuery string, topK int) (string, error) {
	docs, summary, err := s.QueryTopK(ctx, sessionID, userQuery, topK, 300)
	if err != nil {
		return "", err
	}
	cfg, err := s.chatCfgFn()
	if err != nil {
		return "", err
	}
	tr := openaiprovider.NewTranslator(cfg)
	// Build prompt
	var ctxParts string
	if summary != "" {
		ctxParts += "[Session Summary]\n" + summary + "\n\n"
	}
	if len(docs) > 0 {
		ctxParts += "[Top Contexts]\n"
		for i, d := range docs {
			label := fmt.Sprintf("(%d)", i+1)
			if d.Ephemeral {
				label = fmt.Sprintf("(%d) [LIVE]", i+1)
			}
			summaryText := strings.TrimSpace(d.Summary)
			if summaryText == "" {
				summaryText = strings.TrimSpace(d.Original)
			}
			ctxParts += fmt.Sprintf("%s Speaker %s [%.1f-%.1f]: %s\n", label, safe(d.Speaker), d.StartTime, d.EndTime, summaryText)
		}
	}
	sys := strings.Join([]string{
		"You are a helpful learning assistant.",
		"Answer in Chinese, structured and easy to skim.",
		"If context is insufficient, say you are unsure.",
		"Format rules:",
		"- Use short paragraphs and bullet points.",
		"- Start bullets with '- ' and put each on a new line.",
		"- Preserve line breaks for readability.",
	}, " ")
	user := ctxParts + "\n[Question]\n" + userQuery + "\n[Format]\n- 简短概括\n- 关键要点（每点一行）\n- 必要时给出下一步建议"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": user}}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := tr.Chat(cctx, msgs)
	if err != nil {
		return "", err
	}
	return out, nil
}

func safe(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// StoreSummary returns the current summary for a session.
func (s *Service) StoreSummary(sessionID string) (string, error) {
	return s.store.GetSessionSummary(sessionID)
}

// StoreGetTitle returns cached session title (if any).
func (s *Service) StoreGetTitle(sessionID string) (string, error) {
	return s.store.GetSessionTitle(sessionID)
}

// StoreSetTitle caches the session title (best-effort).
func (s *Service) StoreSetTitle(sessionID, title string) error {
	if strings.TrimSpace(title) == "" {
		return nil
	}
	return s.store.UpdateSessionTitle(sessionID, title)
}

// appendBullets merges new bullet lines into previous bullet list, deduplicates, and trims to maxLines.
func appendBullets(prev, added string, maxLines int) string {
	toLines := func(s string) []string {
		var out []string
		for _, ln := range strings.Split(s, "\n") {
			L := strings.TrimSpace(ln)
			if L == "" {
				continue
			}
			if !strings.HasPrefix(L, "- ") {
				L = "- " + L
			}
			out = append(out, L)
		}
		return out
	}
	norm := func(s string) string { return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, "- "))) }
	prevLines := toLines(prev)
	seen := make(map[string]struct{}, len(prevLines))
	for _, l := range prevLines {
		seen[norm(l)] = struct{}{}
	}
	addLines := toLines(added)
	for _, l := range addLines {
		k := norm(l)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		prevLines = append(prevLines, l)
		seen[k] = struct{}{}
	}
	if maxLines > 0 && len(prevLines) > maxLines {
		prevLines = prevLines[len(prevLines)-maxLines:]
	}
	return strings.Join(prevLines, "\n")
}

// ------ helpers: clean noisy paragraphs ------
func charCountAlphaNum(s string) int {
	c := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			c++
		}
	}
	return c
}

func cleanParagraph(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	lower := strings.ToLower(t)
	// remove very common disfluencies
	repl := []string{" ah ", " uh ", " um ", " hmm ", " okay ", " ok ", " huh ", " ah.", " uh.", " um.", " okay.", " ok.", " hmm.", " huh."}
	for _, r := range repl {
		lower = strings.ReplaceAll(lower, r, " ")
	}
	// normalize punctuation to periods
	lower = strings.NewReplacer("?", ".", "!", ".", "\n", ". ").Replace(lower)
	parts := strings.Split(lower, ".")
	seen := make(map[string]struct{})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		L := strings.TrimSpace(p)
		if L == "" {
			continue
		}
		L = strings.Join(strings.Fields(L), " ")
		if len([]rune(L)) < 12 && !strings.ContainsAny(L, "0123456789$") {
			continue
		}
		if strings.Count(L, "how much") >= 2 || strings.Count(L, "how many") >= 2 {
			L = "price inquiry"
		}
		if _, ok := seen[L]; ok {
			continue
		}
		seen[L] = struct{}{}
		out = append(out, L)
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "; ")
}

// SetChatConfigProvider allows overriding the config provider (e.g., to enforce a per-session model from WS).
func (s *Service) SetChatConfigProvider(fn func() (*openaiprovider.Config, error)) {
	if fn != nil {
		s.chatCfgFn = fn
	}
}

// SetIngestSummarizeEnabled toggles whether IngestParagraph calls the LLM to summarize.
// When disabled, cleaned text is used directly without LLM calls.
func (s *Service) SetIngestSummarizeEnabled(enabled bool) {
	s.ingestSummarizeEnabled = enabled
}

// SetSummaryOutputEnabled toggles whether to update session_summary at all.
func (s *Service) SetSummaryOutputEnabled(enabled bool) {
	s.summaryOutputEnabled = enabled
}

// SetEmbedEnabled toggles embeddings compute/store and retrieval.
func (s *Service) SetEmbedEnabled(enabled bool) {
	s.embedEnabled = enabled
}

// BuildAnswerWithUsage returns answer and usage/latency using current env config.
func (s *Service) BuildAnswerWithUsage(ctx context.Context, sessionID, userQuery string, topK int) (string, *openaiprovider.Usage, time.Duration, error) {
	docs, summary, err := s.QueryTopK(ctx, sessionID, userQuery, topK, 300)
	if err != nil {
		return "", nil, 0, err
	}
	cfg, err := s.chatCfgFn()
	if err != nil {
		return "", nil, 0, err
	}
	tr := openaiprovider.NewTranslator(cfg)
	var ctxParts string
	if summary != "" {
		ctxParts += "[Session Summary]\n" + summary + "\n\n"
	}
	if len(docs) > 0 {
		ctxParts += "[Top Contexts]\n"
		for i, d := range docs {
			label := fmt.Sprintf("(%d)", i+1)
			if d.Ephemeral {
				label = fmt.Sprintf("(%d) [LIVE]", i+1)
			}
			summaryText := strings.TrimSpace(d.Summary)
			if summaryText == "" {
				summaryText = strings.TrimSpace(d.Original)
			}
			ctxParts += fmt.Sprintf("%s Speaker %s [%.1f-%.1f]: %s\n", label, safe(d.Speaker), d.StartTime, d.EndTime, summaryText)
		}
	}
	sys := strings.Join([]string{
		"You are a helpful learning assistant.",
		"Answer in Chinese, structured and easy to skim.",
		"If context is insufficient, say you are unsure.",
		"Format rules:",
		"- Use short paragraphs and bullet points.",
		"- Start bullets with '- ' and put each on a new line.",
		"- Preserve line breaks for readability.",
	}, " ")
	user := ctxParts + "\n[Question]\n" + userQuery + "\n[Format]\n- 简短概括\n- 关键要点（每点一行）\n- 必要时给出下一步建议"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": user}}
	start := time.Now()
	out, usage, err := tr.ChatWithUsageRetry(ctx, msgs, 3)
	dur := time.Since(start)
	if err != nil {
		return "", nil, dur, err
	}
	return out, usage, dur, nil
}

// BuildAnswerWithConfigUsage returns answer and usage/latency using overrides.
func (s *Service) BuildAnswerWithConfigUsage(ctx context.Context, sessionID, userQuery string, topK int, ov *ChatOverrides) (string, *openaiprovider.Usage, time.Duration, error) {
	docs, summary, err := s.QueryTopK(ctx, sessionID, userQuery, topK, 300)
	if err != nil {
		return "", nil, 0, err
	}
	baseCfg, err := s.chatCfgFn()
	if err != nil {
		return "", nil, 0, err
	}
	if ov != nil {
		if ov.APIKey != "" {
			baseCfg.APIKey = ov.APIKey
		}
		if ov.APIBase != "" {
			baseCfg.BaseURL = ov.APIBase
		}
		if ov.Model != "" {
			baseCfg.Model = ov.Model
		}
	}
	tr := openaiprovider.NewTranslator(baseCfg)
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
	sys := strings.Join([]string{
		"You are a helpful learning assistant.",
		"Answer in Chinese, structured and easy to skim.",
		"If context is insufficient, say you are unsure.",
		"Format rules:",
		"- Use short paragraphs and bullet points.",
		"- Start bullets with '- ' and put each on a new line.",
		"- Preserve line breaks for readability.",
	}, " ")
	if ov != nil && ov.Prompt != "" {
		sys = sys + " Additional guidance: " + ov.Prompt
	}
	user := ctxParts + "\n[Question]\n" + userQuery + "\n[Format]\n- 简短概括\n- 关键要点（每点一行）\n- 必要时给出下一步建议"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": user}}
	start := time.Now()
	out, usage, err := tr.ChatWithUsageRetry(ctx, msgs, 3)
	dur := time.Since(start)
	if err != nil {
		return "", nil, dur, err
	}
	return out, usage, dur, nil
}

// BuildAnswerWithHistoryWithConfigUsage is like BuildAnswerWithConfigUsage but includes recent chat history
// to improve coreference resolution and continuity.
func (s *Service) BuildAnswerWithHistoryWithConfigUsage(ctx context.Context, sessionID, userQuery string, topK int, ov *ChatOverrides, history string) (string, *openaiprovider.Usage, time.Duration, error) {
	docs, summary, err := s.QueryTopK(ctx, sessionID, userQuery, topK, 300)
	if err != nil {
		return "", nil, 0, err
	}
	baseCfg, err := s.chatCfgFn()
	if err != nil {
		return "", nil, 0, err
	}
	if ov != nil {
		if ov.APIKey != "" {
			baseCfg.APIKey = ov.APIKey
		}
		if ov.APIBase != "" {
			baseCfg.BaseURL = ov.APIBase
		}
		if ov.Model != "" {
			baseCfg.Model = ov.Model
		}
	}
	tr := openaiprovider.NewTranslator(baseCfg)
	var ctxParts string
	if summary != "" {
		ctxParts += "[Session Summary]\n" + summary + "\n\n"
	}
	if len(docs) > 0 {
		ctxParts += "[Top Contexts]\n"
		for i, d := range docs {
			label := fmt.Sprintf("(%d)", i+1)
			if d.Ephemeral {
				label = fmt.Sprintf("(%d) [LIVE]", i+1)
			}
			summaryText := strings.TrimSpace(d.Summary)
			if summaryText == "" {
				summaryText = strings.TrimSpace(d.Original)
			}
			ctxParts += fmt.Sprintf("%s Speaker %s [%.1f-%.1f]: %s\n", label, safe(d.Speaker), d.StartTime, d.EndTime, summaryText)
		}
		ctxParts += "\n"
	}
	if strings.TrimSpace(history) != "" {
		ctxParts += "[Chat History]\n" + strings.TrimSpace(history) + "\n\n"
	}
	sys := strings.Join([]string{
		"You are a helpful learning assistant.",
		"Answer in Chinese, concise and easy to skim.",
		"Use 'Chat History' to resolve pronouns and follow-ups.",
		"If the referent is still ambiguous, ask a brief clarifying question before answering.",
		"Prefer bullet points; preserve important names and entities.",
	}, " ")
	user := ctxParts + "[Question]\n" + userQuery + "\n[Format]\n- 简短概括\n- 关键要点（每点一行）\n- 必要时先澄清再回答"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": user}}
	start := time.Now()
	out, usage, err := tr.ChatWithUsageRetry(ctx, msgs, 3)
	dur := time.Since(start)
	if err != nil {
		return "", nil, dur, err
	}
	return out, usage, dur, nil
}

// BuildAnswerWithConfig is like BuildAnswer but allows overriding API settings and prompt per request.
func (s *Service) BuildAnswerWithConfig(ctx context.Context, sessionID, userQuery string, topK int, ov *ChatOverrides) (string, error) {
	docs, summary, err := s.QueryTopK(ctx, sessionID, userQuery, topK, 300)
	if err != nil {
		return "", err
	}

	baseCfg, err := s.chatCfgFn()
	if err != nil {
		return "", err
	}
	if ov != nil {
		if ov.APIKey != "" {
			baseCfg.APIKey = ov.APIKey
		}
		if ov.APIBase != "" {
			baseCfg.BaseURL = ov.APIBase
		}
		if ov.Model != "" {
			baseCfg.Model = ov.Model
		}
	}
	tr := openaiprovider.NewTranslator(baseCfg)

	var ctxParts string
	if summary != "" {
		ctxParts += "[Session Summary]\n" + summary + "\n\n"
	}
	if len(docs) > 0 {
		ctxParts += "[Top Contexts]\n"
		for i, d := range docs {
			label := fmt.Sprintf("(%d)", i+1)
			if d.Ephemeral {
				label = fmt.Sprintf("(%d) [LIVE]", i+1)
			}
			summaryText := strings.TrimSpace(d.Summary)
			if summaryText == "" {
				summaryText = strings.TrimSpace(d.Original)
			}
			ctxParts += fmt.Sprintf("%s Speaker %s [%.1f-%.1f]: %s\n", label, safe(d.Speaker), d.StartTime, d.EndTime, summaryText)
		}
	}
	sys := strings.Join([]string{
		"You are a helpful learning assistant.",
		"Answer in Chinese, structured and easy to skim.",
		"If context is insufficient, say you are unsure.",
		"Format rules:",
		"- Use short paragraphs and bullet points.",
		"- Start bullets with '- ' and put each on a new line.",
		"- Preserve line breaks for readability.",
	}, " ")
	if ov != nil && ov.Prompt != "" {
		sys = sys + " Additional guidance: " + ov.Prompt
	}
	user := ctxParts + "\n[Question]\n" + userQuery + "\n[Format]\n- 简短概括\n- 关键要点（每点一行）\n- 必要时给出下一步建议"
	msgs := []map[string]string{{"role": "system", "content": sys}, {"role": "user", "content": user}}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := tr.Chat(cctx, msgs)
	if err != nil {
		return "", err
	}
	return out, nil
}
