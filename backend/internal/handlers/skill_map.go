package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	aicontext "github.com/dreamtrans/backend/internal/ai"
	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/metrics"
	"github.com/dreamtrans/backend/internal/modelcatalog"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/google/uuid"
)

// 学习模式: course skill maps. A skill map is generated from every transcript
// in a course's linked sessions and stored as one skill_map artifact whose
// content is a validated skillMapDocument. It is the progress surface the
// study view lights up as the learner advances.

const (
	skillMapArtifactType  = "skill_map"
	skillMapArtifactTitle = "技能地图"
	skillMapRequestKind   = "skill_map"
)

type skillMapGenerateRequest struct {
	ClientRequestID string     `json:"client_request_id"`
	ReasoningEffort string     `json:"reasoning_effort,omitempty"`
	Config          *askConfig `json:"config,omitempty"`
}

func (h *RAGHandler) handleProjectSkillMap(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	switch r.Method {
	case http.MethodGet:
		h.handleGetProjectSkillMap(w, r, project)
	case http.MethodPost:
		h.handleGenerateProjectSkillMap(w, r, project)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *RAGHandler) handleGetProjectSkillMap(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	artifact, err := h.store.GetLatestAIArtifactByProject(
		r.Context(), project.UserID, project.ID, skillMapArtifactType,
	)
	if err != nil {
		http.Error(w, "failed to load skill map", http.StatusInternalServerError)
		return
	}
	if artifact == nil {
		WriteJSON(w, map[string]any{"artifact": nil, "map": nil})
		return
	}
	WriteJSON(w, map[string]any{
		"artifact": artifact,
		"map":      parseStoredSkillMap(artifact.Content),
	})
}

//nolint:gocyclo // Coordinates validation, context assembly, idempotency, generation, accounting, and persistence.
func (h *RAGHandler) handleGenerateProjectSkillMap(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	if h.svc == nil {
		http.Error(w, "AI service is unavailable", http.StatusServiceUnavailable)
		return
	}
	var req skillMapGenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 {
		http.Error(w, "client_request_id is required and must be at most 128 characters", http.StatusBadRequest)
		return
	}
	normalizedReasoning, validReasoning := rag.NormalizeReasoningEffort(req.ReasoningEffort)
	if !validReasoning {
		http.Error(w, "reasoning_effort must be low, medium, or high", http.StatusBadRequest)
		return
	}
	req.ReasoningEffort = normalizedReasoning
	if err := h.validateOverrides(r.Context(), req.Config); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if h.modelCatalog != nil &&
		(req.Config == nil || strings.TrimSpace(req.Config.APIKey) == "") {
		summaryModel, modelErr := h.modelCatalog.EffectiveModel(
			r.Context(), project.UserID, modelcatalog.PurposeSummary,
		)
		if modelErr != nil {
			writeArtifactModelResolutionError(w, modelErr)
			return
		}
		if req.Config == nil {
			req.Config = &askConfig{}
		}
		req.Config.Model = summaryModel
	}
	sessions, err := h.store.ListProjectSessionRefs(
		r.Context(), project.TenantID, project.UserID, project.ID,
		skillMapContextSessionCap,
	)
	if err != nil {
		http.Error(w, "failed to list project sessions", http.StatusInternalServerError)
		return
	}
	if len(sessions) == 0 {
		http.Error(w, "project has no linked sessions", http.StatusUnprocessableEntity)
		return
	}
	contextText, truncated, err := h.buildProjectTranscriptContext(
		r.Context(), sessions, project.MaxContextTokens,
	)
	if err != nil {
		http.Error(w, "failed to load project transcripts", http.StatusInternalServerError)
		return
	}
	if contextText == "" {
		http.Error(w, "linked sessions have no transcript content", http.StatusUnprocessableEntity)
		return
	}
	previousArtifact, err := h.store.GetLatestAIArtifactByProject(
		r.Context(), project.UserID, project.ID, skillMapArtifactType,
	)
	if err != nil {
		http.Error(w, "failed to load previous skill map", http.StatusInternalServerError)
		return
	}
	var previousDoc *skillMapDocument
	previousContent := ""
	if previousArtifact != nil {
		previousDoc = parseStoredSkillMap(previousArtifact.Content)
		previousContent = previousArtifact.Content
	}
	instruction := skillMapInstruction(previousDoc)
	requestHash, err := hashAIGenerationPayload(struct {
		RequestKind     string                     `json:"request_kind"`
		ProjectID       string                     `json:"project_id"`
		ContextDigest   string                     `json:"context_digest"`
		PreviousDigest  string                     `json:"previous_digest"`
		ReasoningEffort string                     `json:"reasoning_effort"`
		SystemPrompt    string                     `json:"system_prompt"`
		Config          aiGenerationConfigIdentity `json:"config"`
	}{
		RequestKind:     skillMapRequestKind,
		ProjectID:       project.ID,
		ContextDigest:   stableProviderOperationID("skill-map-context", contextText),
		PreviousDigest:  stableProviderOperationID("skill-map-previous", previousContent),
		ReasoningEffort: req.ReasoningEffort,
		SystemPrompt:    chatSystemPrompt(req.Config),
		Config: aiGenerationConfigIdentityFor(
			req.Config,
			config.Get().Models.Summary,
		),
	})
	if err != nil {
		http.Error(w, "failed to identify AI request", http.StatusInternalServerError)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	existing, err := h.store.GetAIArtifactByClientRequestID(
		r.Context(), claims.TenantID, claims.UserID, req.ClientRequestID,
	)
	if err != nil {
		http.Error(w, "failed to check skill map request", http.StatusInternalServerError)
		return
	}
	if existing != nil {
		if existing.ArtifactType != skillMapArtifactType ||
			!optionalArtifactScopeMatches(existing.ProjectID, project.ID) ||
			existing.RequestHash != requestHash {
			http.Error(w, "client_request_id was already used for another artifact", http.StatusConflict)
			return
		}
		writeStoredSkillMapReplay(w, existing)
		return
	}
	ctx, cancel := context.WithTimeout(
		r.Context(),
		rag.GenerationTimeoutForReasoning(skillMapGenerationTimeoutBase, req.ReasoningEffort),
	)
	defer cancel()
	generationClaim, replay, err := h.beginAIGeneration(
		ctx, req.ClientRequestID, skillMapRequestKind, requestHash, "",
	)
	if err != nil {
		writeAIGenerationBeginError(w, err)
		return
	}
	if replay != nil {
		if err := h.materializeArtifactReplay(
			r.Context(), replay, requestHash, skillMapRequestKind,
		); err != nil {
			log.Printf("materialize replayed skill map: %v", err)
			if errors.Is(err, store.ErrStorageQuota) {
				http.Error(w, "tenant storage quota exceeded", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "replayed skill map could not be saved", http.StatusInternalServerError)
			return
		}
		if err := writeAIGenerationReplay(w, replay); err != nil {
			log.Printf("write replayed skill map response: %v", err)
		}
		return
	}
	ctx = h.withRAGMeter(ctx, "", aiGenerationBillingNamespace(generationClaim))
	generationCompleted := false
	defer func() {
		if !generationCompleted {
			h.failAIGeneration(generationClaim, "skill map generation did not complete")
		}
	}()
	var overrides *rag.ChatOverrides
	if req.Config != nil {
		overrides = &rag.ChatOverrides{
			APIKey: req.Config.APIKey, APIBase: req.Config.APIBase,
			Model: req.Config.Model, Prompt: req.Config.Prompt,
			ReasoningEffort: req.ReasoningEffort,
		}
	} else {
		overrides = &rag.ChatOverrides{
			Model:           config.Get().Models.Summary,
			ReasoningEffort: req.ReasoningEffort,
		}
	}
	generationCtx := rag.WithProviderOperationID(
		ctx,
		stableProviderOperationID("skill-map-answer", requestHash),
	)
	content, usage, duration, err := h.svc.BuildArtifactFromContextWithConfigUsage(
		generationCtx,
		"project/"+project.ID+"/skill_map",
		instruction,
		contextText,
		"",
		overrides,
	)
	if err != nil {
		if h.isRAGAccountingError(err) {
			h.writeRAGAccountingError(w, err)
			return
		}
		log.Printf("generate skill map: %v", err)
		http.Error(w, "skill map generation failed", ragServiceErrorStatus(err))
		return
	}
	if usage != nil {
		metrics.RecordSummarize(&metrics.Usage{
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
			TotalTokens: usage.TotalTokens, CachedTokens: usage.CachedTokens,
			CacheWriteTokens: usage.CacheWriteTokens, Model: usage.Model,
		}, duration.Milliseconds())
	}
	rawMap, err := parseGeneratedSkillMap(content)
	if err != nil {
		log.Printf("generate skill map: model output was not a valid map")
		http.Error(w, "skill map generation returned an invalid result; please retry", http.StatusBadGateway)
		return
	}
	doc := buildSkillMapDocument(rawMap, sessions, previousDoc)
	if len(doc.Skills) == 0 {
		http.Error(w, "skill map generation returned an empty map; please retry", http.StatusBadGateway)
		return
	}
	now := time.Now().UTC()
	doc.GeneratedAt = now
	doc.Truncated = truncated
	finalContent, err := json.Marshal(doc)
	if err != nil {
		http.Error(w, "failed to serialize skill map", http.StatusInternalServerError)
		return
	}
	projectID := project.ID
	artifact := models.AIArtifact{
		ID: uuid.NewString(), TenantID: claims.TenantID, UserID: claims.UserID,
		ProjectID: &projectID, ArtifactType: skillMapArtifactType,
		Title: skillMapArtifactTitle, Content: string(finalContent),
		ContextTokens:   aicontext.EstimateTokens(contextText),
		ClientRequestID: req.ClientRequestID, RequestHash: requestHash,
		ContextPolicy: map[string]any{
			"mode":          "project_transcripts",
			"truncated":     truncated,
			"session_count": len(sessions),
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if usage != nil {
		artifact.Model = usage.Model
	}
	response := map[string]any{
		"artifact":   artifact,
		"map":        doc,
		"replayed":   false,
		"usage":      usage,
		"latency_ms": duration.Milliseconds(),
	}
	replayResponse, err := json.Marshal(response)
	if err != nil {
		http.Error(w, "failed to serialize skill map response", http.StatusInternalServerError)
		return
	}
	artifact.ReplayResponse = replayResponse
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if err := h.completeAIGeneration(completeCtx, generationClaim, response); err != nil {
		completeCancel()
		log.Printf("complete skill map generation request: %v", err)
		http.Error(w, "skill map response could not be committed", http.StatusInternalServerError)
		return
	}
	completeCancel()
	generationCompleted = true
	if err := h.store.CreateAIArtifact(r.Context(), &artifact); err != nil {
		log.Printf("persist skill map artifact: %v", err)
		if errors.Is(err, store.ErrStorageQuota) {
			http.Error(w, "tenant storage quota exceeded", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "skill map was generated but could not be saved", http.StatusInternalServerError)
		return
	}
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
	if cleanupErr := h.store.DeleteAIGenerationRequestByClientRequestID(
		cleanupCtx, claims.TenantID, claims.UserID,
		req.ClientRequestID, skillMapRequestKind,
	); cleanupErr != nil {
		log.Printf("remove temporary skill map replay: %v", cleanupErr)
	}
	if _, staleErr := h.store.DeleteAIArtifactsByProjectAndTypeExcept(
		cleanupCtx, claims.UserID, project.ID, skillMapArtifactType, artifact.ID,
	); staleErr != nil {
		log.Printf("remove superseded skill maps: %v", staleErr)
	}
	cleanupCancel()
	WriteJSON(w, response)
}

func writeStoredSkillMapReplay(w http.ResponseWriter, artifact *models.AIArtifact) {
	if artifact != nil && len(artifact.ReplayResponse) > 0 {
		var response map[string]any
		if json.Unmarshal(artifact.ReplayResponse, &response) == nil &&
			len(response) > 0 {
			response["artifact"] = artifact
			response["replayed"] = true
			WriteJSON(w, response)
			return
		}
	}
	var doc *skillMapDocument
	if artifact != nil {
		doc = parseStoredSkillMap(artifact.Content)
	}
	WriteJSON(w, map[string]any{
		"artifact": artifact,
		"map":      doc,
		"replayed": true,
	})
}

// buildProjectTranscriptContext concatenates every linked session's confirmed
// transcript, oldest session first, under the project's context budget. When
// the total is over budget every non-empty session is truncated to an equal
// share so late sessions are never silently dropped.
func (h *RAGHandler) buildProjectTranscriptContext(
	ctx context.Context,
	sessions []store.ProjectSessionRef,
	maxContextTokens int,
) (string, bool, error) {
	budget := maxContextTokens
	if systemMax := aicontext.MaxContextTokens(); budget <= 0 || budget > systemMax {
		budget = systemMax
	}
	truncated := false
	texts := make([]string, len(sessions))
	// Bound what a single runaway session can buffer regardless of budget.
	perSessionByteCap := budget * 2
	for index, session := range sessions {
		var builder strings.Builder
		var cursor *store.TranscriptPageCursor
		for {
			rows, hasMore, err := h.store.GetTranscriptsPageBySession(
				ctx, session.ID, skillMapTranscriptPageSize, cursor,
			)
			if err != nil {
				return "", false, err
			}
			for rowIndex := range rows {
				transcript := &rows[rowIndex]
				if transcript.IsPartial {
					continue
				}
				text := strings.TrimSpace(transcript.Text)
				if text == "" {
					continue
				}
				builder.WriteString(text)
				builder.WriteByte('\n')
			}
			if len(rows) > 0 {
				last := rows[len(rows)-1]
				cursor = &store.TranscriptPageCursor{
					StartTime: last.StartTime, ID: last.ID,
				}
			}
			if builder.Len() > perSessionByteCap {
				truncated = true
				break
			}
			if !hasMore || len(rows) == 0 {
				break
			}
		}
		texts[index] = strings.TrimSpace(builder.String())
	}
	total := 0
	nonEmpty := 0
	for _, text := range texts {
		if text == "" {
			continue
		}
		total += aicontext.EstimateTokens(text)
		nonEmpty++
	}
	if nonEmpty == 0 {
		return "", false, nil
	}
	if total > budget {
		truncated = true
		allowance := budget / nonEmpty
		for index, text := range texts {
			texts[index] = truncateContextText(text, allowance)
		}
	}
	var out strings.Builder
	for index, session := range sessions {
		if texts[index] == "" {
			continue
		}
		title := strings.TrimSpace(session.Title)
		if title == "" {
			title = "未命名会话"
		}
		fmt.Fprintf(
			&out, "[会话 %d] %s（%s）\n%s\n\n",
			index+1, title, session.StartedAt.Format("2006-01-02"), texts[index],
		)
	}
	return strings.TrimSpace(out.String()), truncated, nil
}

// truncateContextText keeps the longest prefix within the token budget
// without splitting a UTF-8 rune. EstimateTokens is byte-based, so this walks
// runes accumulating bytes.
func truncateContextText(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	if aicontext.EstimateTokens(text) <= maxTokens {
		return text
	}
	bytes := 0
	for index, r := range text {
		runeLen := len(string(r))
		if bytes+runeLen > maxTokens {
			return strings.TrimSpace(text[:index])
		}
		bytes += runeLen
	}
	return strings.TrimSpace(text)
}
