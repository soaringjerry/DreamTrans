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

	openai "github.com/dreamtrans/backend/internal/adapters/openai_provider"
	aicontext "github.com/dreamtrans/backend/internal/ai"
	"github.com/dreamtrans/backend/internal/config"
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
	h.writeSkillMapPayload(r.Context(), w, project, nil, false)
}

func (h *RAGHandler) handleGenerateProjectSkillMap(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	if h.svc == nil || h.store == nil {
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
	model := strings.TrimSpace(config.Get().Models.Summary)
	if h.modelCatalog != nil &&
		(req.Config == nil || strings.TrimSpace(req.Config.APIKey) == "") {
		summaryModel, modelErr := h.modelCatalog.EffectiveModel(
			r.Context(), project.UserID, modelcatalog.PurposeSummary,
		)
		if modelErr != nil {
			writeArtifactModelResolutionError(w, modelErr)
			return
		}
		model = summaryModel
	} else if req.Config != nil && strings.TrimSpace(req.Config.Model) != "" {
		model = strings.TrimSpace(req.Config.Model)
	}
	work, err := h.prepareSkillMapWork(r.Context(), project, req.ReasoningEffort, model)
	if err != nil {
		if errors.Is(err, errSkillMapNoSessions) {
			http.Error(w, "project has no linked sessions", http.StatusUnprocessableEntity)
			return
		}
		if errors.Is(err, errSkillMapNoTranscripts) {
			http.Error(w, "linked sessions have no transcript content", http.StatusUnprocessableEntity)
			return
		}
		http.Error(w, "failed to prepare skill map", http.StatusInternalServerError)
		return
	}
	job := &models.SkillMapJob{
		TenantID:        project.TenantID,
		UserID:          project.UserID,
		ProjectID:       project.ID,
		Model:           model,
		ReasoningEffort: req.ReasoningEffort,
		RequestHash:     work.requestHash,
		ClientRequestID: req.ClientRequestID,
		ChunkCount:      len(work.chunks),
	}
	created, err := h.store.CreateSkillMapJob(r.Context(), job)
	if err != nil {
		if errors.Is(err, store.ErrIdempotencyConflict) {
			http.Error(w, "client_request_id was already used for another skill map", http.StatusConflict)
			return
		}
		http.Error(w, "failed to queue skill map generation", http.StatusInternalServerError)
		return
	}
	if created {
		h.signalSkillMapJobs()
	}
	h.writeSkillMapPayload(r.Context(), w, project, job, !created)
}

func (h *RAGHandler) writeSkillMapPayload(
	ctx context.Context, w http.ResponseWriter, project *models.AIProject,
	job *models.SkillMapJob, replayed bool,
) {
	artifact, err := h.store.GetLatestAIArtifactByProject(
		ctx, project.UserID, project.ID, skillMapArtifactType,
	)
	if err != nil {
		http.Error(w, "failed to load skill map", http.StatusInternalServerError)
		return
	}
	if job == nil {
		active, jobErr := h.store.GetActiveSkillMapJob(ctx, project.UserID, project.ID)
		if jobErr != nil {
			http.Error(w, "failed to load skill map job", http.StatusInternalServerError)
			return
		}
		if active != nil {
			job = active
		} else {
			latest, latestErr := h.store.GetLatestSkillMapJob(ctx, project.UserID, project.ID)
			if latestErr != nil {
				http.Error(w, "failed to load skill map job", http.StatusInternalServerError)
				return
			}
			if latest != nil && latest.Status == "error" {
				job = latest
			}
		}
	}
	var doc *skillMapDocument
	if artifact != nil {
		doc = parseStoredSkillMap(artifact.Content)
	}
	WriteJSON(w, map[string]any{
		"artifact": artifact,
		"map":      doc,
		"job":      job,
		"replayed": replayed,
	})
}

var (
	errSkillMapNoSessions    = errors.New("project has no linked sessions")
	errSkillMapNoTranscripts = errors.New("linked sessions have no transcript content")
)

type skillMapWork struct {
	sessions        []store.ProjectSessionRef
	chunks          []string
	contextText     string
	previousDoc     *skillMapDocument
	previousContent string
	requestHash     string
	instruction     string
}

func (h *RAGHandler) prepareSkillMapWork(
	ctx context.Context, project *models.AIProject, reasoningEffort, model string,
) (*skillMapWork, error) {
	sessions, err := h.store.ListProjectSessionRefs(
		ctx, project.TenantID, project.UserID, project.ID, 0,
	)
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, errSkillMapNoSessions
	}
	texts, err := h.loadProjectSessionTranscripts(ctx, sessions)
	if err != nil {
		return nil, err
	}
	chunks := packSkillMapChunks(sessions, texts, skillMapTranscriptBudget(project.MaxContextTokens))
	if len(chunks) == 0 {
		return nil, errSkillMapNoTranscripts
	}
	previousArtifact, err := h.store.GetLatestAIArtifactByProject(
		ctx, project.UserID, project.ID, skillMapArtifactType,
	)
	if err != nil {
		return nil, err
	}
	work := &skillMapWork{
		sessions:    sessions,
		chunks:      chunks,
		contextText: strings.Join(chunks, "\n\n"),
	}
	if previousArtifact != nil {
		work.previousDoc = parseStoredSkillMap(previousArtifact.Content)
		work.previousContent = previousArtifact.Content
	}
	work.instruction = skillMapInstruction(work.previousDoc)
	work.requestHash, err = hashAIGenerationPayload(struct {
		RequestKind     string `json:"request_kind"`
		ProjectID       string `json:"project_id"`
		ContextDigest   string `json:"context_digest"`
		PreviousDigest  string `json:"previous_digest"`
		ReasoningEffort string `json:"reasoning_effort"`
		Model           string `json:"model"`
	}{
		RequestKind:     skillMapRequestKind,
		ProjectID:       project.ID,
		ContextDigest:   stableProviderOperationID("skill-map-context", work.contextText),
		PreviousDigest:  stableProviderOperationID("skill-map-previous", work.previousContent),
		ReasoningEffort: reasoningEffort,
		Model:           model,
	})
	if err != nil {
		return nil, err
	}
	return work, nil
}

func (h *RAGHandler) persistGeneratedSkillMap(
	ctx context.Context,
	project *models.AIProject,
	job *models.SkillMapJob,
	work *skillMapWork,
	rawMap *skillMapLLMOutput,
	usage *openai.Usage,
) error {
	doc := buildSkillMapDocument(rawMap, work.sessions, work.previousDoc)
	if len(doc.Skills) == 0 {
		return errSkillMapInvalidJSON
	}
	now := time.Now().UTC()
	doc.GeneratedAt = now
	doc.Truncated = false
	finalContent, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	projectID := project.ID
	artifact := models.AIArtifact{
		ID: uuid.NewString(), TenantID: job.TenantID, UserID: job.UserID,
		ProjectID: &projectID, ArtifactType: skillMapArtifactType,
		Title: skillMapArtifactTitle, Content: string(finalContent),
		ContextTokens:   aicontext.EstimateTokens(work.contextText),
		ClientRequestID: job.ClientRequestID, RequestHash: job.RequestHash,
		ContextPolicy: map[string]any{
			"mode":          "project_transcripts",
			"truncated":     false,
			"chunk_count":   len(work.chunks),
			"session_count": len(work.sessions),
			"job_id":        job.ID,
		},
		CreatedAt: now, UpdatedAt: now,
	}
	if usage != nil {
		artifact.Model = usage.Model
	}
	if err := h.store.CreateAIArtifact(ctx, &artifact); err != nil {
		return err
	}
	if _, err := h.store.DeleteAIArtifactsByProjectAndTypeExcept(
		ctx, job.UserID, project.ID, skillMapArtifactType, artifact.ID,
	); err != nil {
		log.Printf("remove superseded skill maps: %v", err)
	}
	return nil
}

// loadProjectSessionTranscripts reads every confirmed transcript in each
// linked session. It never drops a session tail: a later packing step splits
// oversized text into consecutive model-sized chunks instead.
func (h *RAGHandler) loadProjectSessionTranscripts(
	ctx context.Context,
	sessions []store.ProjectSessionRef,
) ([]string, error) {
	texts := make([]string, len(sessions))
	for index, session := range sessions {
		var builder strings.Builder
		var cursor *store.TranscriptPageCursor
		for {
			rows, hasMore, err := h.store.GetTranscriptsPageBySession(
				ctx, session.ID, skillMapTranscriptPageSize, cursor,
			)
			if err != nil {
				return nil, err
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
			if !hasMore || len(rows) == 0 {
				break
			}
		}
		texts[index] = strings.TrimSpace(builder.String())
	}
	return texts, nil
}

func (h *RAGHandler) generateSkillMapFromChunks(
	ctx context.Context,
	claim *aiGenerationClaim,
	projectID, requestHash, singleShotInstruction string,
	chunks []string,
	previous *skillMapDocument,
	overrides *rag.ChatOverrides,
	maxContextTokens int,
	onProgress func(done, total int),
) (*skillMapLLMOutput, *openai.Usage, time.Duration, error) {
	if len(chunks) <= 1 {
		content := ""
		if len(chunks) == 1 {
			content = chunks[0]
		}
		raw, usage, duration, err := h.completeSkillMapModelCall(
			ctx, claim, projectID, requestHash, "answer",
			singleShotInstruction, content, overrides,
		)
		if onProgress != nil {
			onProgress(1, 1)
		}
		return raw, usage, duration, err
	}
	drafts := make([]*skillMapLLMOutput, 0, len(chunks))
	var usage *openai.Usage
	var duration time.Duration
	chunkInstruction := skillMapChunkInstruction()
	for index, chunk := range chunks {
		raw, callUsage, callDuration, err := h.completeSkillMapModelCall(
			ctx, claim, projectID, requestHash, fmt.Sprintf("chunk-%d", index),
			chunkInstruction, chunk, overrides,
		)
		if err != nil {
			return nil, usage, duration + callDuration, err
		}
		usage = addSkillMapUsage(usage, callUsage)
		duration += callDuration
		if raw != nil && len(raw.Skills) > 0 {
			drafts = append(drafts, raw)
		}
		if onProgress != nil {
			onProgress(index+1, len(chunks)+1)
		}
	}
	if len(drafts) == 0 {
		return nil, usage, duration, errSkillMapInvalidJSON
	}
	merged, mergeUsage, mergeDuration, err := h.mergeSkillMapDrafts(
		ctx, claim, projectID, requestHash, drafts, previous, overrides,
		skillMapTranscriptBudget(maxContextTokens),
	)
	if onProgress != nil {
		onProgress(len(chunks)+1, len(chunks)+1)
	}
	return merged, addSkillMapUsage(usage, mergeUsage), duration + mergeDuration, err
}

func (h *RAGHandler) mergeSkillMapDrafts(
	ctx context.Context,
	claim *aiGenerationClaim,
	projectID, requestHash string,
	drafts []*skillMapLLMOutput,
	previous *skillMapDocument,
	overrides *rag.ChatOverrides,
	budget int,
) (*skillMapLLMOutput, *openai.Usage, time.Duration, error) {
	var usage *openai.Usage
	var duration time.Duration
	round := 0
	for len(drafts) > 1 {
		groups := groupSkillMapDrafts(drafts, budget)
		if len(groups) == 1 && len(groups[0]) == len(drafts) && round > 0 {
			// A second pass with the same grouping cannot shrink; concatenate.
			return concatenateSkillMapDrafts(drafts), usage, duration, nil
		}
		next := make([]*skillMapLLMOutput, 0, len(groups))
		for groupIndex, group := range groups {
			if len(group) == 1 {
				next = append(next, group[0])
				continue
			}
			draftJSON, err := json.Marshal(struct {
				Drafts []*skillMapLLMOutput `json:"drafts"`
			}{Drafts: group})
			if err != nil {
				return nil, usage, duration, err
			}
			merged, callUsage, callDuration, err := h.completeSkillMapModelCall(
				ctx, claim, projectID, requestHash,
				fmt.Sprintf("merge-%d-%d", round, groupIndex),
				skillMapMergeInstruction(previous), string(draftJSON), overrides,
			)
			usage = addSkillMapUsage(usage, callUsage)
			duration += callDuration
			if err != nil {
				if errors.Is(err, errSkillMapInvalidJSON) {
					next = append(next, concatenateSkillMapDrafts(group))
					continue
				}
				return nil, usage, duration, err
			}
			next = append(next, merged)
		}
		drafts = next
		round++
	}
	if len(drafts) == 0 {
		return nil, usage, duration, errSkillMapInvalidJSON
	}
	return drafts[0], usage, duration, nil
}

func concatenateSkillMapDrafts(drafts []*skillMapLLMOutput) *skillMapLLMOutput {
	combined := &skillMapLLMOutput{}
	for _, draft := range drafts {
		if draft == nil {
			continue
		}
		combined.Skills = append(combined.Skills, draft.Skills...)
	}
	return combined
}

func groupSkillMapDrafts(drafts []*skillMapLLMOutput, budget int) [][]*skillMapLLMOutput {
	if budget < 1 {
		budget = 1
	}
	groups := make([][]*skillMapLLMOutput, 0)
	current := make([]*skillMapLLMOutput, 0, len(drafts))
	currentSize := 2
	for _, draft := range drafts {
		encoded, err := json.Marshal(draft)
		size := 2
		if err == nil {
			size = len(encoded)
		}
		if len(current) > 0 && currentSize+size+1 > budget {
			groups = append(groups, current)
			current = nil
			currentSize = 2
		}
		current = append(current, draft)
		currentSize += size + 1
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}

func (h *RAGHandler) completeSkillMapModelCall(
	ctx context.Context,
	claim *aiGenerationClaim,
	projectID, requestHash, stage, instruction, contextText string,
	overrides *rag.ChatOverrides,
) (*skillMapLLMOutput, *openai.Usage, time.Duration, error) {
	if err := h.renewSkillMapGeneration(ctx, claim); err != nil {
		return nil, nil, 0, err
	}
	generationCtx := rag.WithProviderOperationID(
		ctx,
		stableProviderOperationID("skill-map-"+stage, requestHash),
	)
	content, usage, duration, err := h.svc.BuildArtifactFromContextWithConfigUsage(
		generationCtx,
		"project/"+projectID+"/skill_map/"+stage,
		instruction,
		contextText,
		"",
		overrides,
	)
	if err != nil {
		return nil, usage, duration, err
	}
	raw, err := parseGeneratedSkillMap(content)
	if err != nil {
		return nil, usage, duration, err
	}
	return raw, usage, duration, nil
}

func (h *RAGHandler) renewSkillMapGeneration(
	ctx context.Context, claim *aiGenerationClaim,
) error {
	if claim == nil || h.store == nil || strings.TrimSpace(claim.request.ID) == "" {
		return nil
	}
	ok, err := h.store.RenewAIGenerationRequestLease(
		ctx,
		claim.request.ID, claim.request.TenantID, claim.request.UserID,
		claim.request.LeaseOwner, 5*time.Minute,
	)
	if err != nil {
		return err
	}
	if !ok {
		return store.ErrLeaseLost
	}
	return nil
}

func addSkillMapUsage(total, next *openai.Usage) *openai.Usage {
	if next == nil {
		return total
	}
	if total == nil {
		copyUsage := *next
		return &copyUsage
	}
	total.PromptTokens += next.PromptTokens
	total.CompletionTokens += next.CompletionTokens
	total.TotalTokens += next.TotalTokens
	total.CachedTokens += next.CachedTokens
	total.CacheWriteTokens += next.CacheWriteTokens
	if next.Model != "" {
		total.Model = next.Model
	}
	return total
}
