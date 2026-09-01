package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	openai "github.com/dreamtrans/backend/internal/adapters/openai_provider"
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
	var recommended *studyContinue
	if artifact, artErr := h.store.GetLatestAIArtifactByProject(
		r.Context(), project.UserID, project.ID, skillMapArtifactType,
	); artErr == nil && artifact != nil {
		recommended = recommendStudySkill(parseStoredSkillMap(artifact.Content), states)
	}
	WriteJSON(w, map[string]any{"states": states, "continue": recommended})
}

type studyNextRequest struct {
	SkillLabel        string `json:"skill_label"`
	ClientRequestID   string `json:"client_request_id"`
	PracticeSessionID string `json:"practice_session_id"`
}

// studyServe is the wire shape of one served scenario.
type studyServe struct {
	ScenarioID string               `json:"scenario_id"`
	Difficulty int                  `json:"difficulty"`
	Level      string               `json:"level"`
	Generated  bool                 `json:"generated"`
	Scenario   studyScenarioContent `json:"scenario"`
	Scaffold   studyScaffold        `json:"scaffold"`
	CoachLine  string               `json:"coach_line,omitempty"`
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
	req.PracticeSessionID = strings.TrimSpace(req.PracticeSessionID)
	if len(req.PracticeSessionID) > 128 {
		http.Error(w, "practice_session_id must be at most 128 characters", http.StatusBadRequest)
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
	last, err := h.store.GetLastStudyAttempt(r.Context(), project.UserID, project.ID, skillKey)
	if err != nil {
		http.Error(w, "failed to load study history", http.StatusInternalServerError)
		return
	}
	scaffold := studyScaffoldFor(level, last)
	exclude, err := h.store.ListPracticeSessionScenarioIDs(
		r.Context(), project.UserID, project.ID, req.PracticeSessionID,
	)
	if err != nil {
		http.Error(w, "failed to load practice session", http.StatusInternalServerError)
		return
	}
	minUses, err := h.store.MinStudyScenarioUses(
		r.Context(), project.UserID, project.ID, skillKey, difficulty,
	)
	if err != nil {
		http.Error(w, "failed to inspect scenario bank", http.StatusInternalServerError)
		return
	}
	count, err := h.store.CountStudyScenarios(
		r.Context(), project.UserID, project.ID, skillKey, difficulty,
	)
	if err != nil {
		http.Error(w, "failed to inspect scenario bank", http.StatusInternalServerError)
		return
	}
	claims := auth.GetUserClaims(r.Context())
	if count > 0 {
		scenario, pickErr := h.store.PickStudyScenario(
			r.Context(), project.UserID, project.ID, skillKey, difficulty, exclude,
		)
		if pickErr != nil {
			http.Error(w, "failed to pick a scenario", http.StatusInternalServerError)
			return
		}
		if scenario != nil {
			if minUses >= studyScenarioRefillAt {
				// Worn bank: serve what exists now, top up behind the learner.
				h.refillStudyBankAsync(project, claims.TenantID, skillKey, skillLabel, last)
			}
			if h.writeServedScenario(r.Context(), w, scenario, level, scaffold, last, false) {
				return
			}
			log.Printf("study scenario %s content is unreadable; regenerating bank", scenario.ID)
		}
	}

	// Bank is empty or this session already used every item: the learner is
	// waiting, so generate the rubric (once) and a small batch synchronously.
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
	model, modelErr := h.studyModel(r.Context(), project.UserID)
	if modelErr != nil {
		writeArtifactModelResolutionError(w, modelErr)
		return
	}
	existingCount, err := h.store.CountStudyScenarios(
		r.Context(), project.UserID, project.ID, skillKey, 0,
	)
	if err != nil {
		http.Error(w, "failed to inspect scenario bank", http.StatusInternalServerError)
		return
	}
	requestHash, err := hashAIGenerationPayload(struct {
		RequestKind    string `json:"request_kind"`
		ProjectID      string `json:"project_id"`
		SkillKey       string `json:"skill_key"`
		Difficulty     int    `json:"difficulty"`
		ExistingCount  int    `json:"existing_count"`
		RefillSequence int    `json:"refill_sequence"`
	}{studyNextRequestKind, project.ID, skillKey, difficulty, existingCount, existingCount / studyScenarioColdBatch})
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
	bank, err := h.generateStudyBank(generationCtx, &studyBankRequest{
		project: project, tenantID: claims.TenantID,
		skillKey: skillKey, skillLabel: skillLabel, skill: skill,
		rubric: rubric, model: model, last: last, batch: studyScenarioColdBatch,
	})
	if err != nil {
		h.writeStudyBankError(w, err)
		return
	}
	served := bank.scenarios[0]
	for _, candidate := range bank.scenarios[1:] {
		if absInt(candidate.Difficulty-difficulty) < absInt(served.Difficulty-difficulty) {
			served = candidate
		}
	}
	response, ok := h.buildServedScenario(r.Context(), served, level, scaffold, last, true)
	if !ok {
		http.Error(w, "failed to serve generated scenario", http.StatusInternalServerError)
		return
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

type studyBankRequest struct {
	project    *models.AIProject
	tenantID   string
	skillKey   string
	skillLabel string
	skill      *skillMapSkill
	rubric     *models.StudyRubric
	model      string
	last       *models.StudyAttempt
	batch      int
}

type studyBankResult struct {
	scenarios []*models.StudyScenario
	usage     *openai.Usage
}

var (
	errStudyInvalidRubric = errors.New("practice generation returned an invalid rubric")
	errStudyNoScenarios   = errors.New("practice generation returned no usable scenarios")
)

// studyStoreError marks a persistence failure so the HTTP layer answers 500
// instead of blaming the model.
type studyStoreError struct{ err error }

func (e *studyStoreError) Error() string { return e.err.Error() }
func (e *studyStoreError) Unwrap() error { return e.err }

// generateStudyBank asks the model for a batch of scenarios (plus the frozen
// rubric when the skill has none yet) and persists them. Billing and
// idempotency come from ctx; both the synchronous cold start and the
// background refill go through here.
func (h *RAGHandler) generateStudyBank(
	ctx context.Context, req *studyBankRequest,
) (*studyBankResult, error) {
	content, usage, duration, err := h.svc.BuildArtifactFromContextWithConfigUsage(
		ctx,
		"project/"+req.project.ID+"/study_bank",
		studyBankInstruction(req.rubric == nil, req.batch),
		studyBankContext(req.skill, req.last),
		"",
		&rag.ChatOverrides{Model: req.model, ReasoningEffort: "low"},
	)
	if err != nil {
		return nil, err
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
		return nil, errStudyInvalidJSON
	}
	if req.rubric == nil {
		rubricJSON, rubricErr := validateStudyRubric(raw.Rubric)
		if rubricErr != nil {
			log.Printf("generate study rubric: %v", rubricErr)
			return nil, errStudyInvalidRubric
		}
		newRubric := &models.StudyRubric{
			TenantID: req.tenantID, UserID: req.project.UserID, ProjectID: req.project.ID,
			SkillKey: req.skillKey, SkillLabel: req.skillLabel, Rubric: rubricJSON,
		}
		if usage != nil {
			newRubric.Model = usage.Model
		}
		if err := h.store.CreateStudyRubric(ctx, newRubric); err != nil {
			return nil, &studyStoreError{err: fmt.Errorf("save rubric: %w", err)}
		}
	}
	contents := validateStudyScenarios(&raw)
	if len(contents) == 0 {
		return nil, errStudyNoScenarios
	}
	scenarios := make([]*models.StudyScenario, 0, len(contents))
	for index, entry := range contents {
		encoded, encodeErr := json.Marshal(entry)
		if encodeErr != nil {
			return nil, &studyStoreError{err: fmt.Errorf("serialize scenario: %w", encodeErr)}
		}
		entryDifficulty := index + 1
		if index < len(raw.Scenarios) {
			entryDifficulty = clampStudyDifficulty(raw.Scenarios[index].Difficulty)
		}
		row := &models.StudyScenario{
			TenantID: req.tenantID, UserID: req.project.UserID, ProjectID: req.project.ID,
			SkillKey: req.skillKey, SkillLabel: req.skillLabel,
			Difficulty: entryDifficulty, Content: encoded,
		}
		if usage != nil {
			row.Model = usage.Model
		}
		scenarios = append(scenarios, row)
	}
	if err := h.store.CreateStudyScenarios(ctx, scenarios); err != nil {
		return nil, &studyStoreError{err: fmt.Errorf("save scenarios: %w", err)}
	}
	return &studyBankResult{scenarios: scenarios, usage: usage}, nil
}

func (h *RAGHandler) writeStudyBankError(w http.ResponseWriter, err error) {
	var storeErr *studyStoreError
	switch {
	case h.isRAGAccountingError(err):
		h.writeRAGAccountingError(w, err)
	case errors.As(err, &storeErr):
		log.Printf("persist study bank: %v", err)
		http.Error(w, "failed to save practice material", http.StatusInternalServerError)
	case errors.Is(err, errStudyInvalidJSON):
		http.Error(w, "practice generation returned an invalid result; please retry", http.StatusBadGateway)
	case errors.Is(err, errStudyInvalidRubric):
		http.Error(w, "practice generation returned an invalid rubric; please retry", http.StatusBadGateway)
	case errors.Is(err, errStudyNoScenarios):
		http.Error(w, "practice generation returned no usable scenarios; please retry", http.StatusBadGateway)
	default:
		log.Printf("generate study bank: %v", err)
		http.Error(w, "practice generation failed", ragServiceErrorStatus(err))
	}
}

// One refill per (learner, course, skill) at a time.
var studyRefillInFlight sync.Map

// refillStudyBankAsync tops up a worn bank without making the learner wait.
// The next request finds fresh items; a failure only shows up in the log.
func (h *RAGHandler) refillStudyBankAsync(
	project *models.AIProject, tenantID, skillKey, skillLabel string,
	last *models.StudyAttempt,
) {
	if h.svc == nil || h.store == nil {
		return
	}
	key := project.UserID + "|" + project.ID + "|" + skillKey
	if _, loaded := studyRefillInFlight.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	go func() {
		defer studyRefillInFlight.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
		defer cancel()
		skill := h.findSkillMapSkill(ctx, project, skillKey)
		if skill == nil {
			return
		}
		rubric, err := h.store.GetStudyRubric(ctx, project.UserID, project.ID, skillKey)
		if err != nil || rubric == nil {
			log.Printf("refill study bank %s: rubric unavailable: %v", skillKey, err)
			return
		}
		model, err := h.studyModel(ctx, project.UserID)
		if err != nil {
			log.Printf("refill study bank %s: %v", skillKey, err)
			return
		}
		count, err := h.store.CountStudyScenarios(ctx, project.UserID, project.ID, skillKey, 0)
		if err != nil {
			log.Printf("refill study bank %s: %v", skillKey, err)
			return
		}
		identity := fmt.Sprintf("%s|%s|%d", project.ID, skillKey, count)
		meter := &ragHTTPUsageMeter{
			billing:         h.billing,
			userID:          project.UserID,
			tenantID:        tenantID,
			stableNamespace: "study-refill:" + identity,
		}
		ctx = rag.WithProviderUsageMeter(ctx, meter)
		ctx = rag.WithProviderOperationID(ctx, stableProviderOperationID("study-refill", identity))
		if _, err := h.generateStudyBank(ctx, &studyBankRequest{
			project: project, tenantID: tenantID,
			skillKey: skillKey, skillLabel: skillLabel, skill: skill,
			rubric: rubric, model: model, last: last, batch: studyScenarioBatchSize,
		}); err != nil {
			log.Printf("refill study bank %s: %v", skillKey, err)
		}
	}()
}

func (h *RAGHandler) buildServedScenario(
	ctx context.Context,
	scenario *models.StudyScenario,
	level string,
	scaffold studyScaffold,
	last *models.StudyAttempt,
	generated bool,
) (studyServe, bool) {
	var content studyScenarioContent
	if scenario == nil || json.Unmarshal(scenario.Content, &content) != nil || content.Question == "" {
		return studyServe{}, false
	}
	if touchErr := h.store.TouchStudyScenarioUse(ctx, scenario.ID); touchErr != nil {
		log.Printf("touch study scenario use: %v", touchErr)
	}
	return studyServe{
		ScenarioID: scenario.ID,
		Difficulty: scenario.Difficulty,
		Level:      level,
		Generated:  generated,
		Scenario:   publicStudyScenario(&content, scaffold),
		Scaffold:   scaffold,
		CoachLine:  studyCoachLine(scaffold, last),
	}, true
}

func (h *RAGHandler) writeServedScenario(
	ctx context.Context,
	w http.ResponseWriter,
	scenario *models.StudyScenario,
	level string,
	scaffold studyScaffold,
	last *models.StudyAttempt,
	generated bool,
) bool {
	response, ok := h.buildServedScenario(ctx, scenario, level, scaffold, last, generated)
	if !ok {
		return false
	}
	WriteJSON(w, response)
	return true
}

type studyAttemptRequest struct {
	ScenarioID        string `json:"scenario_id"`
	Answer            string `json:"answer"`
	UsedHint          bool   `json:"used_hint"`
	UsedZH            bool   `json:"used_zh"`
	PracticeSessionID string `json:"practice_session_id"`
	ClientRequestID   string `json:"client_request_id"`
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
	req.PracticeSessionID = strings.TrimSpace(req.PracticeSessionID)
	if len(req.PracticeSessionID) > 128 {
		http.Error(w, "practice_session_id must be at most 128 characters", http.StatusBadRequest)
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
		UsedZH      bool   `json:"used_zh"`
	}{studyGradeRequestKind, scenario.ID, answer, req.UsedHint, req.UsedZH})
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
		studyGradeContext(rubric.Rubric, &content, answer, req.UsedHint, req.UsedZH),
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
	priorAttempts, countErr := h.store.CountScenarioAttempts(
		r.Context(), project.UserID, project.ID, scenario.ID,
	)
	if countErr != nil {
		http.Error(w, "failed to load scenario history", http.StatusInternalServerError)
		return
	}
	firstTry := priorAttempts == 0
	sessionLast, sessionErr := h.store.GetLastPracticeSessionAttempt(
		r.Context(), project.UserID, project.ID, req.PracticeSessionID,
	)
	if sessionErr != nil {
		http.Error(w, "failed to load practice session", http.StatusInternalServerError)
		return
	}
	prevCombo := 0
	if sessionLast != nil {
		prevCombo = sessionLast.Combo
	}
	combo := nextStudyCombo(prevCombo, graded.Grade, req.UsedHint)
	if studyLanguageIndependence(&state, req.UsedZH, graded.Grade) {
		graded.Bonuses = append(graded.Bonuses, studyBonusLanguageIndependence)
	}
	xp, bonuses := studyAttemptXP(graded.Grade, req.UsedHint, firstTry, graded.Bonuses)
	xp = applyStudyComboXP(xp, combo)
	leveledUp := false
	if !graded.LanguageIssue || studyGradeRank(graded.Grade) >= studyGradeRank(studyGradeC) {
		var up bool
		state.Level, state.CleanStreak, up = advanceStudyState(
			state.Level, state.CleanStreak, graded.Grade, req.UsedHint,
		)
		leveledUp = up
	}
	if !req.UsedZH && studyGradeRank(graded.Grade) >= studyGradeRank(studyGradeC) {
		state.EnSuccessStreak++
	} else if !req.UsedZH {
		state.EnSuccessStreak = 0
	}
	events := studyEvents(
		graded.Grade, scenario.Difficulty, req.UsedHint, req.UsedZH, firstTry,
		bonuses, state.LastErrorPattern, graded.ErrorPattern, sessionLast,
	)
	for _, event := range events {
		if event == studyEventLanguageSave {
			state.LanguageSaves++
		}
		if event == studyEventMisconceptionBroken {
			state.LastErrorPattern = ""
		}
	}
	if graded.ErrorPattern != "" {
		state.LastErrorPattern = graded.ErrorPattern
	}
	state.AttemptsCount++
	state.XPTotal += int64(xp)
	state.LastGrade = graded.Grade
	state.SkillLabel = scenario.SkillLabel
	bonusesJSON, err := json.Marshal(bonuses)
	if err != nil {
		http.Error(w, "failed to serialize attempt", http.StatusInternalServerError)
		return
	}
	eventsJSON, err := json.Marshal(events)
	if err != nil {
		http.Error(w, "failed to serialize attempt", http.StatusInternalServerError)
		return
	}
	scenarioID := scenario.ID
	attempt := models.StudyAttempt{
		TenantID: claims.TenantID, UserID: claims.UserID, ProjectID: project.ID,
		ScenarioID: &scenarioID, SkillKey: scenario.SkillKey, Answer: answer,
		Grade: graded.Grade, Feedback: graded.Feedback, NextStep: graded.NextStep,
		Bonuses: bonusesJSON, XP: xp, UsedHint: req.UsedHint, UsedZH: req.UsedZH,
		PracticeSessionID: req.PracticeSessionID, Combo: combo, Events: eventsJSON,
		ErrorPattern: graded.ErrorPattern, ClientRequestID: req.ClientRequestID,
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
		"combo":      combo,
		"events":     events,
		"used_hint":  req.UsedHint,
		"used_zh":    req.UsedZH,
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
