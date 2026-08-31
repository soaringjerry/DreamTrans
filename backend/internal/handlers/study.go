package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/metrics"
	"github.com/dreamtrans/backend/internal/modelcatalog"
	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/rag"
	"github.com/google/uuid"
)

// 学习模式 practice endpoints under /api/ai/projects/{id}/study/:
//
//	GET  study/state    — every skill's learner state (lights the map)
//	POST study/next     — serve a scenario, generating rubric+bank on demand
//	POST study/attempts — grade one answer against the frozen rubric
//
// Generation and grading run through the shared AI generation pipeline for
// idempotency and reserve→settle billing, on the summary-purpose model.

const (
	studyNextRequestKind  = "study_next"
	studyGradeRequestKind = "study_grade"
)

func (h *RAGHandler) handleProjectStudy(
	w http.ResponseWriter, r *http.Request, project *models.AIProject, action string,
) {
	switch {
	case action == "state" && r.Method == http.MethodGet:
		h.handleStudyState(w, r, project)
	case action == "next" && r.Method == http.MethodPost:
		h.handleStudyNext(w, r, project)
	case action == "attempts" && r.Method == http.MethodPost:
		h.handleStudyAttempt(w, r, project)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *RAGHandler) handleStudyState(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	states, err := h.store.ListStudySkillStates(r.Context(), project.UserID, project.ID)
	if err != nil {
		http.Error(w, "failed to load study state", http.StatusInternalServerError)
		return
	}
	WriteJSON(w, map[string]any{"states": states})
}

type studyNextRequest struct {
	SkillLabel      string `json:"skill_label"`
	ClientRequestID string `json:"client_request_id"`
}

// studyServe is the wire shape of one served scenario.
type studyServe struct {
	ScenarioID string               `json:"scenario_id"`
	Difficulty int                  `json:"difficulty"`
	Level      string               `json:"level"`
	Generated  bool                 `json:"generated"`
	Scenario   studyScenarioContent `json:"scenario"`
}

//nolint:gocyclo // Coordinates bank lookup, on-demand generation, and idempotency.
func (h *RAGHandler) handleStudyNext(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	if h.svc == nil {
		http.Error(w, "AI service is unavailable", http.StatusServiceUnavailable)
		return
	}
	var req studyNextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	skillLabel := clampStudyText(req.SkillLabel, skillMapMaxLabelRunes)
	skillKey := skillMapLabelKey(skillLabel)
	if skillKey == "" {
		http.Error(w, "skill_label is required", http.StatusBadRequest)
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 {
		http.Error(w, "client_request_id is required and must be at most 128 characters", http.StatusBadRequest)
		return
	}
	state, err := h.store.GetStudySkillState(
		r.Context(), project.UserID, project.ID, skillKey,
	)
	if err != nil {
		http.Error(w, "failed to load study state", http.StatusInternalServerError)
		return
	}
	level := "learner"
	if state != nil {
		level = state.Level
	}
	difficulty := studyDifficultyForLevel(level)
	scenario, err := h.store.PickStudyScenario(
		r.Context(), project.UserID, project.ID, skillKey, difficulty,
	)
	if err != nil {
		http.Error(w, "failed to pick a scenario", http.StatusInternalServerError)
		return
	}
	if scenario != nil {
		var content studyScenarioContent
		if json.Unmarshal(scenario.Content, &content) == nil && content.Question != "" {
			if touchErr := h.store.TouchStudyScenarioUse(r.Context(), scenario.ID); touchErr != nil {
				log.Printf("touch study scenario use: %v", touchErr)
			}
			WriteJSON(w, studyServe{
				ScenarioID: scenario.ID,
				Difficulty: scenario.Difficulty,
				Level:      level,
				Scenario:   content,
			})
			return
		}
		log.Printf("study scenario %s content is unreadable; regenerating bank", scenario.ID)
	}

	// Bank is empty for this skill: generate rubric (once) and a batch.
	skill := h.findSkillMapSkill(r.Context(), project, skillKey)
	if skill == nil {
		http.Error(w, "skill is not in this course's skill map; regenerate the map first", http.StatusUnprocessableEntity)
		return
	}
	rubric, err := h.store.GetStudyRubric(r.Context(), project.UserID, project.ID, skillKey)
	if err != nil {
		http.Error(w, "failed to load rubric", http.StatusInternalServerError)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	model, modelErr := h.studyModel(r.Context(), project.UserID)
	if modelErr != nil {
		writeArtifactModelResolutionError(w, modelErr)
		return
	}
	requestHash, err := hashAIGenerationPayload(struct {
		RequestKind string `json:"request_kind"`
		ProjectID   string `json:"project_id"`
		SkillKey    string `json:"skill_key"`
		Difficulty  int    `json:"difficulty"`
	}{studyNextRequestKind, project.ID, skillKey, difficulty})
	if err != nil {
		http.Error(w, "failed to identify AI request", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	generationClaim, replay, err := h.beginAIGeneration(
		ctx, req.ClientRequestID, studyNextRequestKind, requestHash, "",
	)
	if err != nil {
		writeAIGenerationBeginError(w, err)
		return
	}
	if replay != nil {
		if err := writeAIGenerationReplay(w, replay); err != nil {
			log.Printf("write replayed study scenario: %v", err)
		}
		return
	}
	generationCompleted := false
	defer func() {
		if !generationCompleted {
			h.failAIGeneration(generationClaim, "study bank generation did not complete")
		}
	}()
	ctx = h.withRAGMeter(ctx, "", aiGenerationBillingNamespace(generationClaim))
	generationCtx := rag.WithProviderOperationID(
		ctx, stableProviderOperationID("study-bank", requestHash),
	)
	content, usage, duration, err := h.svc.BuildArtifactFromContextWithConfigUsage(
		generationCtx,
		"project/"+project.ID+"/study_bank",
		studyBankInstruction(rubric == nil),
		studyBankContext(skill),
		"",
		&rag.ChatOverrides{Model: model, ReasoningEffort: "low"},
	)
	if err != nil {
		if h.isRAGAccountingError(err) {
			h.writeRAGAccountingError(w, err)
			return
		}
		log.Printf("generate study bank: %v", err)
		http.Error(w, "practice generation failed", ragServiceErrorStatus(err))
		return
	}
	if usage != nil {
		metrics.RecordSummarize(&metrics.Usage{
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
			TotalTokens: usage.TotalTokens, CachedTokens: usage.CachedTokens,
			CacheWriteTokens: usage.CacheWriteTokens, Model: usage.Model,
		}, duration.Milliseconds())
	}
	var raw studyBankLLMOutput
	if err := studyExtractJSON(content, &raw); err != nil {
		http.Error(w, "practice generation returned an invalid result; please retry", http.StatusBadGateway)
		return
	}
	if rubric == nil {
		rubricJSON, rubricErr := validateStudyRubric(raw.Rubric)
		if rubricErr != nil {
			log.Printf("generate study rubric: %v", rubricErr)
			http.Error(w, "practice generation returned an invalid rubric; please retry", http.StatusBadGateway)
			return
		}
		newRubric := &models.StudyRubric{
			TenantID: claims.TenantID, UserID: claims.UserID, ProjectID: project.ID,
			SkillKey: skillKey, SkillLabel: skillLabel, Rubric: rubricJSON,
		}
		if usage != nil {
			newRubric.Model = usage.Model
		}
		if err := h.store.CreateStudyRubric(r.Context(), newRubric); err != nil {
			http.Error(w, "failed to save rubric", http.StatusInternalServerError)
			return
		}
	}
	contents := validateStudyScenarios(&raw)
	if len(contents) == 0 {
		http.Error(w, "practice generation returned no usable scenarios; please retry", http.StatusBadGateway)
		return
	}
	scenarios := make([]*models.StudyScenario, 0, len(contents))
	for index, entry := range contents {
		encoded, encodeErr := json.Marshal(entry)
		if encodeErr != nil {
			http.Error(w, "failed to serialize scenarios", http.StatusInternalServerError)
			return
		}
		entryDifficulty := index + 1
		if index < len(raw.Scenarios) {
			entryDifficulty = clampStudyDifficulty(raw.Scenarios[index].Difficulty)
		}
		row := &models.StudyScenario{
			TenantID: claims.TenantID, UserID: claims.UserID, ProjectID: project.ID,
			SkillKey: skillKey, SkillLabel: skillLabel,
			Difficulty: entryDifficulty, Content: encoded,
		}
		if usage != nil {
			row.Model = usage.Model
		}
		scenarios = append(scenarios, row)
	}
	if err := h.store.CreateStudyScenarios(r.Context(), scenarios); err != nil {
		http.Error(w, "failed to save scenarios", http.StatusInternalServerError)
		return
	}
	served := scenarios[0]
	for _, candidate := range scenarios[1:] {
		if absInt(candidate.Difficulty-difficulty) < absInt(served.Difficulty-difficulty) {
			served = candidate
		}
	}
	var servedContent studyScenarioContent
	if err := json.Unmarshal(served.Content, &servedContent); err != nil {
		http.Error(w, "failed to serialize scenarios", http.StatusInternalServerError)
		return
	}
	if touchErr := h.store.TouchStudyScenarioUse(r.Context(), served.ID); touchErr != nil {
		log.Printf("touch study scenario use: %v", touchErr)
	}
	response := studyServe{
		ScenarioID: served.ID,
		Difficulty: served.Difficulty,
		Level:      level,
		Generated:  true,
		Scenario:   servedContent,
	}
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer completeCancel()
	if err := h.completeAIGeneration(completeCtx, generationClaim, response); err != nil {
		log.Printf("complete study bank generation: %v", err)
		http.Error(w, "practice response could not be committed", http.StatusInternalServerError)
		return
	}
	generationCompleted = true
	WriteJSON(w, response)
}

type studyAttemptRequest struct {
	ScenarioID      string `json:"scenario_id"`
	Answer          string `json:"answer"`
	UsedHint        bool   `json:"used_hint"`
	ClientRequestID string `json:"client_request_id"`
}

//nolint:gocyclo // Coordinates rubric-anchored grading, XP, and progression.
func (h *RAGHandler) handleStudyAttempt(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	if h.svc == nil {
		http.Error(w, "AI service is unavailable", http.StatusServiceUnavailable)
		return
	}
	var req studyAttemptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if uuid.Validate(strings.TrimSpace(req.ScenarioID)) != nil {
		http.Error(w, "scenario_id must be a UUID", http.StatusBadRequest)
		return
	}
	answer := clampStudyText(req.Answer, studyMaxAnswerRunes)
	if answer == "" {
		http.Error(w, "answer is required", http.StatusBadRequest)
		return
	}
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)
	if req.ClientRequestID == "" || len(req.ClientRequestID) > 128 {
		http.Error(w, "client_request_id is required and must be at most 128 characters", http.StatusBadRequest)
		return
	}
	scenario, err := h.store.GetStudyScenario(
		r.Context(), project.UserID, project.ID, strings.TrimSpace(req.ScenarioID),
	)
	if err != nil {
		http.Error(w, "failed to load scenario", http.StatusInternalServerError)
		return
	}
	if scenario == nil {
		http.Error(w, "scenario not found", http.StatusNotFound)
		return
	}
	var content studyScenarioContent
	if err := json.Unmarshal(scenario.Content, &content); err != nil || content.Question == "" {
		http.Error(w, "scenario content is unreadable", http.StatusInternalServerError)
		return
	}
	rubric, err := h.store.GetStudyRubric(
		r.Context(), project.UserID, project.ID, scenario.SkillKey,
	)
	if err != nil {
		http.Error(w, "failed to load rubric", http.StatusInternalServerError)
		return
	}
	if rubric == nil {
		http.Error(w, "this skill has no rubric yet; request a scenario first", http.StatusConflict)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	model, modelErr := h.studyModel(r.Context(), project.UserID)
	if modelErr != nil {
		writeArtifactModelResolutionError(w, modelErr)
		return
	}
	requestHash, err := hashAIGenerationPayload(struct {
		RequestKind string `json:"request_kind"`
		ScenarioID  string `json:"scenario_id"`
		Answer      string `json:"answer"`
		UsedHint    bool   `json:"used_hint"`
	}{studyGradeRequestKind, scenario.ID, answer, req.UsedHint})
	if err != nil {
		http.Error(w, "failed to identify AI request", http.StatusInternalServerError)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	generationClaim, replay, err := h.beginAIGeneration(
		ctx, req.ClientRequestID, studyGradeRequestKind, requestHash, "",
	)
	if err != nil {
		writeAIGenerationBeginError(w, err)
		return
	}
	if replay != nil {
		if err := writeAIGenerationReplay(w, replay); err != nil {
			log.Printf("write replayed study grade: %v", err)
		}
		return
	}
	generationCompleted := false
	defer func() {
		if !generationCompleted {
			h.failAIGeneration(generationClaim, "study grading did not complete")
		}
	}()
	ctx = h.withRAGMeter(ctx, "", aiGenerationBillingNamespace(generationClaim))
	generationCtx := rag.WithProviderOperationID(
		ctx, stableProviderOperationID("study-grade", requestHash),
	)
	rawContent, usage, duration, err := h.svc.BuildArtifactFromContextWithConfigUsage(
		generationCtx,
		"project/"+project.ID+"/study_grade",
		studyGradeInstruction(),
		studyGradeContext(rubric.Rubric, &content, answer),
		"",
		&rag.ChatOverrides{Model: model, ReasoningEffort: "low"},
	)
	if err != nil {
		if h.isRAGAccountingError(err) {
			h.writeRAGAccountingError(w, err)
			return
		}
		log.Printf("grade study attempt: %v", err)
		http.Error(w, "grading failed", ragServiceErrorStatus(err))
		return
	}
	if usage != nil {
		metrics.RecordSummarize(&metrics.Usage{
			PromptTokens: usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
			TotalTokens: usage.TotalTokens, CachedTokens: usage.CachedTokens,
			CacheWriteTokens: usage.CacheWriteTokens, Model: usage.Model,
		}, duration.Milliseconds())
	}
	var rawGrade studyGradeLLMOutput
	if err := studyExtractJSON(rawContent, &rawGrade); err != nil {
		http.Error(w, "grading returned an invalid result; please retry", http.StatusBadGateway)
		return
	}
	graded, err := validateStudyGrade(&rawGrade)
	if err != nil {
		log.Printf("grade study attempt: %v", err)
		http.Error(w, "grading returned an invalid result; please retry", http.StatusBadGateway)
		return
	}
	previous, err := h.store.GetStudySkillState(
		r.Context(), project.UserID, project.ID, scenario.SkillKey,
	)
	if err != nil {
		http.Error(w, "failed to load study state", http.StatusInternalServerError)
		return
	}
	state := models.StudySkillState{
		UserID: project.UserID, ProjectID: project.ID, SkillKey: scenario.SkillKey,
		TenantID: claims.TenantID, SkillLabel: scenario.SkillLabel, Level: "learner",
	}
	if previous != nil {
		state = *previous
	}
	firstTry := state.AttemptsCount == 0
	xp, bonuses := studyAttemptXP(graded.Grade, req.UsedHint, firstTry, graded.Bonuses)
	nextLevel, nextStreak, leveledUp := advanceStudyState(
		state.Level, state.CleanStreak, graded.Grade, req.UsedHint,
	)
	state.Level = nextLevel
	state.CleanStreak = nextStreak
	state.AttemptsCount++
	state.XPTotal += int64(xp)
	state.LastGrade = graded.Grade
	state.SkillLabel = scenario.SkillLabel
	bonusesJSON, err := json.Marshal(bonuses)
	if err != nil {
		http.Error(w, "failed to serialize attempt", http.StatusInternalServerError)
		return
	}
	scenarioID := scenario.ID
	attempt := models.StudyAttempt{
		TenantID: claims.TenantID, UserID: claims.UserID, ProjectID: project.ID,
		ScenarioID: &scenarioID, SkillKey: scenario.SkillKey, Answer: answer,
		Grade: graded.Grade, Feedback: graded.Feedback, NextStep: graded.NextStep,
		Bonuses: bonusesJSON, XP: xp, UsedHint: req.UsedHint,
		ClientRequestID: req.ClientRequestID,
	}
	if usage != nil {
		attempt.Model = usage.Model
	}
	if err := h.store.CreateStudyAttempt(r.Context(), &attempt); err != nil {
		http.Error(w, "failed to save attempt", http.StatusInternalServerError)
		return
	}
	if err := h.store.UpsertStudySkillState(r.Context(), &state); err != nil {
		http.Error(w, "failed to save study state", http.StatusInternalServerError)
		return
	}
	response := map[string]any{
		"grade":      graded.Grade,
		"feedback":   graded.Feedback,
		"next_step":  graded.NextStep,
		"bonuses":    bonuses,
		"xp":         xp,
		"used_hint":  req.UsedHint,
		"leveled_up": leveledUp,
		"state":      state,
	}
	completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer completeCancel()
	if err := h.completeAIGeneration(completeCtx, generationClaim, response); err != nil {
		log.Printf("complete study grading: %v", err)
		http.Error(w, "grading response could not be committed", http.StatusInternalServerError)
		return
	}
	generationCompleted = true
	WriteJSON(w, response)
}

// findSkillMapSkill resolves one skill from the course's latest skill map.
func (h *RAGHandler) findSkillMapSkill(
	ctx context.Context, project *models.AIProject, skillKey string,
) *skillMapSkill {
	artifact, err := h.store.GetLatestAIArtifactByProject(
		ctx, project.UserID, project.ID, skillMapArtifactType,
	)
	if err != nil || artifact == nil {
		return nil
	}
	doc := parseStoredSkillMap(artifact.Content)
	if doc == nil {
		return nil
	}
	for index := range doc.Skills {
		if skillMapLabelKey(doc.Skills[index].Label) == skillKey {
			return &doc.Skills[index]
		}
	}
	return nil
}

// studyModel resolves the model practice runs on (summary purpose).
func (h *RAGHandler) studyModel(ctx context.Context, userID string) (string, error) {
	if h.modelCatalog == nil {
		return config.Get().Models.Summary, nil
	}
	return h.modelCatalog.EffectiveModel(ctx, userID, modelcatalog.PurposeSummary)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
