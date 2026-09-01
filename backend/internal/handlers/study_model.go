package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/dreamtrans/backend/internal/models"
)

// 学习模式 practice-loop domain logic. Everything here is pure so the grading
// endpoint stays a thin coordinator: fixed rubrics in, bounded documents out,
// deterministic XP and progression.

const (
	studyGradeF  = "F"
	studyGradeP  = "P"
	studyGradeC  = "C"
	studyGradeD  = "D"
	studyGradeHD = "HD"

	studyMaxAnswerRunes    = 4000
	studyMaxFeedbackRunes  = 160
	studyMaxSituationRunes = 900
	studyMaxQuestionRunes  = 400
	studyMaxHintRunes      = 200
	studyMaxAnchorRunes    = 400
	studyMaxRubricDescRune = 240
	studyScenarioBatchSize = 6 // background refill
	studyScenarioColdBatch = 3 // synchronous: the learner is waiting
	studyScenarioRefillAt  = 2
	studyMaxBonusCount     = 2
	studyMaxOptions        = 5
	studyMinOptions        = 3
	studyMaxOptionRunes    = 160
	studyMaxGlossary       = 4
	studyMaxGlossRunes     = 80
	studyMaxStarters       = 3
	studyMaxStarterRunes   = 120
	studyMaxTipRunes       = 160
	studyBonusXP           = 40
	studyNoHintXP          = 30
	studyFirstTryXP        = 50
)

// studyGradeRank orders grades; -1 means invalid.
func studyGradeRank(grade string) int {
	switch grade {
	case studyGradeF:
		return 0
	case studyGradeP:
		return 1
	case studyGradeC:
		return 2
	case studyGradeD:
		return 3
	case studyGradeHD:
		return 4
	}
	return -1
}

var studyGradeBaseXP = map[string]int{
	studyGradeF:  0,
	studyGradeP:  40,
	studyGradeC:  100,
	studyGradeD:  180,
	studyGradeHD: 280,
}

// Bonuses the model may award; anything else it emits is dropped.
var studyLLMBonuses = map[string]bool{
	"self_correction":         true,
	"precise_language":        true,
	"alternative_explanation": true,
	"hidden_insight":          true,
	"transfer":                true,
	"language_independence":   true,
}

const (
	studyEventMisconceptionBroken = "misconception_broken"
	studyEventTransferSuccess     = "transfer_success"
	studyEventCriticalInsight     = "critical_insight"
	studyEventSelfCorrection      = "self_correction"
	studyEventLanguageSave        = "language_save"
)

// Question formats. Recognition formats (single/multi/cloze) carry the low
// difficulties; open transfer questions are the only format at difficulty 3.
const (
	studyFormatOpen   = "open"
	studyFormatSingle = "single"
	studyFormatMulti  = "multi"
	studyFormatCloze  = "cloze"
)

// Server-detected bonuses.
const (
	studyBonusLanguageIndependence = "language_independence"
	studyLanguageIndependenceRun   = 3

	studyBonusNoHint   = "no_hint"
	studyBonusFirstTry = "first_try"
)

var studyLevels = []string{"learner", "supervised", "hazard", "independent", "mastered"}

// Hint-free ≥C streak required to leave each level.
var studyLevelUpStreak = map[string]int{
	"learner":     2,
	"supervised":  3,
	"hazard":      3,
	"independent": 4,
}

// studyDifficultyForLevel maps progression to the scenario difficulty served:
// scaffolded basics first, unfamiliar transfer material at the top.
func studyDifficultyForLevel(level string) int {
	switch level {
	case "hazard":
		return 2
	case "independent", "mastered":
		return 3
	default:
		return 1
	}
}

func studyValidLevel(level string) bool {
	for _, known := range studyLevels {
		if level == known {
			return true
		}
	}
	return false
}

// advanceStudyState applies one graded attempt to a learner's state and
// reports whether the skill leveled up. Rules: any grade counts an attempt;
// a hint-free grade ≥C extends the clean streak; a grade <C resets it; a
// hinted pass keeps it unchanged (support was still needed). Reaching the
// level's streak requirement spends the streak and moves one level up —
// mastery is only ever reached through repeated unaided transfer.
func advanceStudyState(level string, cleanStreak int, grade string, usedHint bool) (
	nextLevel string, nextStreak int, leveledUp bool,
) {
	if !studyValidLevel(level) {
		level = "learner"
	}
	nextLevel = level
	nextStreak = cleanStreak
	switch {
	case studyGradeRank(grade) >= studyGradeRank(studyGradeC) && !usedHint:
		nextStreak++
	case studyGradeRank(grade) < studyGradeRank(studyGradeC):
		nextStreak = 0
	}
	needed, canLevel := studyLevelUpStreak[level]
	if canLevel && nextStreak >= needed {
		for index, known := range studyLevels {
			if known == level && index+1 < len(studyLevels) {
				nextLevel = studyLevels[index+1]
				break
			}
		}
		nextStreak = 0
		leveledUp = true
	}
	return nextLevel, nextStreak, leveledUp
}

// studyAttemptXP computes the server-side XP for one attempt: grade base plus
// bounded bonuses. Bonuses only pay on a passing judgment (≥C) so reward
// tracks quality of thinking, not clicks.
func studyAttemptXP(grade string, usedHint, firstTry bool, llmBonuses []string) (int, []string) {
	base, known := studyGradeBaseXP[grade]
	if !known {
		return 0, nil
	}
	bonuses := make([]string, 0, studyMaxBonusCount+2)
	xp := base
	if studyGradeRank(grade) < studyGradeRank(studyGradeC) {
		return xp, bonuses
	}
	if !usedHint {
		bonuses = append(bonuses, studyBonusNoHint)
		xp += studyNoHintXP
	}
	if firstTry {
		bonuses = append(bonuses, studyBonusFirstTry)
		xp += studyFirstTryXP
	}
	seen := make(map[string]bool)
	kept := 0
	for _, bonus := range llmBonuses {
		if kept >= studyMaxBonusCount {
			break
		}
		if !studyLLMBonuses[bonus] || seen[bonus] {
			continue
		}
		seen[bonus] = true
		bonuses = append(bonuses, bonus)
		xp += studyBonusXP
		kept++
	}
	return xp, bonuses
}

func studyComboMultiplier(combo int) float64 {
	switch {
	case combo >= 12:
		return 3
	case combo >= 8:
		return 2
	case combo >= 5:
		return 1.5
	case combo >= 3:
		return 1.2
	case combo >= 2:
		return 1.1
	default:
		return 1
	}
}

func nextStudyCombo(previous int, grade string, usedHint bool) int {
	if studyGradeRank(grade) < studyGradeRank(studyGradeC) {
		return 0
	}
	if usedHint {
		if previous < 0 {
			return 0
		}
		return previous
	}
	return previous + 1
}

func applyStudyComboXP(xp, combo int) int {
	if xp <= 0 {
		return xp
	}
	return int(float64(xp)*studyComboMultiplier(combo) + 0.5)
}

// studyLanguageIndependence fires once: a learner who previously needed the
// Chinese probe to pass (language_saves > 0) completes their third
// consecutive pass in English. Later English passes are just the norm.
func studyLanguageIndependence(state *models.StudySkillState, usedZH bool, grade string) bool {
	if state == nil || usedZH || state.LanguageSaves == 0 {
		return false
	}
	if studyGradeRank(grade) < studyGradeRank(studyGradeC) {
		return false
	}
	return state.EnSuccessStreak == studyLanguageIndependenceRun-1
}

type studyScaffold struct {
	OfferZH       bool `json:"offer_zh"`
	ShowZH        bool `json:"show_zh"`
	OfferHint     bool `json:"offer_hint"`
	OfferGlossary bool `json:"offer_glossary"`
	OfferStarters bool `json:"offer_starters"`
}

func studyScaffoldFor(level string, last *models.StudyAttempt) studyScaffold {
	scaffold := studyScaffold{OfferZH: true, OfferHint: true}
	switch level {
	case "hazard":
		scaffold.OfferHint = false
	case "independent", "mastered":
		scaffold.OfferZH = false
		scaffold.OfferHint = false
	}
	// Language scaffolds withdraw one level earlier than the domain ones:
	// glossary through 辅助, sentence starters only at 入门.
	scaffold.OfferGlossary = level == "learner" || level == "supervised"
	scaffold.OfferStarters = level == "learner"
	if last == nil {
		return scaffold
	}
	failed := studyGradeRank(last.Grade) < studyGradeRank(studyGradeC)
	if failed && level != "independent" && level != "mastered" {
		// A miss brings the glossary back; starters only while still 辅助.
		scaffold.OfferGlossary = true
		scaffold.OfferStarters = level == "learner" || level == "supervised"
	}
	switch {
	case failed && !last.UsedZH:
		scaffold.OfferZH = true
		scaffold.ShowZH = true
		scaffold.OfferHint = scaffold.OfferHint || level == "learner"
	case failed && last.UsedZH:
		scaffold.OfferZH = true
		scaffold.ShowZH = true
		scaffold.OfferHint = true
	case !failed && !last.UsedZH && !last.UsedHint:
		scaffold.ShowZH = false
		switch level {
		case "learner":
			scaffold.OfferZH = true
			scaffold.OfferHint = true
		case "supervised":
			scaffold.OfferZH = true
			scaffold.OfferHint = false
		default:
			scaffold.OfferZH = false
			scaffold.OfferHint = false
		}
	}
	return scaffold
}

func studyCoachLine(scaffold studyScaffold, last *models.StudyAttempt) string {
	if last == nil {
		return ""
	}
	failed := studyGradeRank(last.Grade) < studyGradeRank(studyGradeC)
	switch {
	case failed && !last.UsedZH && scaffold.ShowZH:
		return "先确认是不是题干英文卡住了。"
	case !failed && !last.UsedZH && !scaffold.OfferZH:
		return "下一题不给中文。"
	case !failed && !last.UsedHint && !scaffold.OfferHint:
		return "下一题把提示拿掉。"
	default:
		return ""
	}
}

func studyEvents(
	grade string, difficulty int, usedHint, usedZH, firstTry bool,
	bonuses []string,
	previousError string,
	newError string,
	lastAttempt *models.StudyAttempt,
) []string {
	events := make([]string, 0, 4)
	seen := map[string]bool{}
	add := func(event string) {
		if event == "" || seen[event] {
			return
		}
		seen[event] = true
		events = append(events, event)
	}
	for _, bonus := range bonuses {
		if bonus == "self_correction" {
			add(studyEventSelfCorrection)
		}
		if bonus == "hidden_insight" {
			for _, other := range bonuses {
				if other == "alternative_explanation" {
					add(studyEventCriticalInsight)
				}
			}
		}
		if bonus == "transfer" {
			add(studyEventTransferSuccess)
		}
	}
	if difficulty >= 3 && studyGradeRank(grade) >= studyGradeRank(studyGradeC) && !usedHint {
		add(studyEventTransferSuccess)
	}
	if previousError != "" && newError == "" &&
		studyGradeRank(grade) >= studyGradeRank(studyGradeD) && !usedHint {
		add(studyEventMisconceptionBroken)
	}
	if lastAttempt != nil && studyGradeRank(lastAttempt.Grade) < studyGradeRank(studyGradeC) &&
		!lastAttempt.UsedZH && usedZH && studyGradeRank(grade) >= studyGradeRank(studyGradeC) {
		add(studyEventLanguageSave)
	}
	if lastAttempt != nil && lastAttempt.ScenarioID != nil &&
		studyGradeRank(lastAttempt.Grade) < studyGradeRank(studyGradeC) &&
		studyGradeRank(grade) >= studyGradeRank(studyGradeC) && !usedHint && !firstTry {
		add(studyEventSelfCorrection)
	}
	return events
}

type studyContinue struct {
	SkillLabel string `json:"skill_label"`
	Level      string `json:"level"`
}

func studyLevelRank(level string) int {
	for index, known := range studyLevels {
		if level == known {
			return index
		}
	}
	return -1
}

func recommendStudySkill(
	doc *skillMapDocument, states []models.StudySkillState,
) *studyContinue {
	if doc == nil {
		return nil
	}
	byKey := make(map[string]models.StudySkillState, len(states))
	for index := range states {
		byKey[states[index].SkillKey] = states[index]
	}
	idToSkill := make(map[string]skillMapSkill, len(doc.Skills))
	for _, skill := range doc.Skills {
		idToSkill[skill.ID] = skill
	}
	for _, skill := range doc.Skills {
		key := skillMapLabelKey(skill.Label)
		state, known := byKey[key]
		level := "learner"
		if known {
			if state.Level == "mastered" {
				continue
			}
			level = state.Level
		}
		blocked := false
		for _, prerequisiteID := range skill.Prerequisites {
			prerequisite, exists := idToSkill[prerequisiteID]
			if !exists {
				continue
			}
			preState, preKnown := byKey[skillMapLabelKey(prerequisite.Label)]
			if !preKnown || studyLevelRank(preState.Level) < studyLevelRank("supervised") {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		return &studyContinue{SkillLabel: skill.Label, Level: level}
	}
	return nil
}

// The scenario content persisted in study_scenarios.content.
type studyScenarioContent struct {
	Situation  string `json:"situation"`
	Question   string `json:"question"`
	QuestionZH string `json:"question_zh,omitempty"`
	Hint       string `json:"hint,omitempty"`
	Variant    string `json:"variant,omitempty"`
	CAnchor    string `json:"c_anchor,omitempty"`
	DAnchor    string `json:"d_anchor,omitempty"`
	// Format-specific material. Answer keys never leave the server.
	Format        string   `json:"format,omitempty"`
	Options       []string `json:"options,omitempty"`
	AnswerIndexes []int    `json:"answer_indexes,omitempty"`
	AnswerText    string   `json:"answer_text,omitempty"`
	// Language scaffolds, withdrawn by level.
	Glossary []studyGlossaryEntry `json:"glossary,omitempty"`
	Starters []string             `json:"starters,omitempty"`
}

type studyGlossaryEntry struct {
	Term  string `json:"term"`
	Gloss string `json:"gloss"`
}

// One rubric level: an observable standard plus an anchor answer.
type studyRubricLevel struct {
	Description string `json:"description"`
	Anchor      string `json:"anchor,omitempty"`
}

type studyRubricDocument struct {
	Levels map[string]studyRubricLevel `json:"levels"`
}

// What the bank-generation model emits.
type studyBankLLMOutput struct {
	Rubric    *studyRubricDocument `json:"rubric"`
	Scenarios []struct {
		Situation  string `json:"situation"`
		Question   string `json:"question"`
		QuestionZH string `json:"question_zh"`
		Hint       string `json:"hint"`
		Difficulty int    `json:"difficulty"`
		Variant    string `json:"variant"`
		CAnchor    string `json:"c_anchor"`
		DAnchor    string `json:"d_anchor"`

		Format        string               `json:"format"`
		Options       []string             `json:"options"`
		AnswerIndexes []int                `json:"answer_indexes"`
		AnswerText    string               `json:"answer_text"`
		Glossary      []studyGlossaryEntry `json:"glossary"`
		Starters      []string             `json:"starters"`
	} `json:"scenarios"`
}

// What the grading model emits.
type studyGradeLLMOutput struct {
	Grade         string   `json:"grade"`
	Feedback      string   `json:"feedback"`
	NextStep      string   `json:"next_step"`
	Bonuses       []string `json:"bonuses"`
	ErrorPattern  string   `json:"error_pattern"`
	LanguageIssue bool     `json:"language_issue"`
	// Cloze only: whether the filled term is acceptable.
	AnswerCorrect *bool `json:"answer_correct"`
	// One phrasing correction for the learner's English (may be empty).
	LanguageTip string `json:"language_tip"`
}

var errStudyInvalidJSON = errors.New("study generation returned invalid JSON")

// studyExtractJSON tolerates code fences and prose around one JSON object.
func studyExtractJSON(content string, target any) error {
	trimmed := strings.TrimSpace(content)
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start < 0 || end <= start {
		return errStudyInvalidJSON
	}
	if err := json.Unmarshal([]byte(trimmed[start:end+1]), target); err != nil {
		return errStudyInvalidJSON
	}
	return nil
}

func clampStudyText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > maxRunes {
		return strings.TrimSpace(string(runes[:maxRunes]))
	}
	return value
}

// validateStudyRubric bounds a generated rubric and requires all five levels.
func validateStudyRubric(rubric *studyRubricDocument) (json.RawMessage, error) {
	if rubric == nil {
		return nil, errors.New("rubric is missing")
	}
	grades := []string{studyGradeF, studyGradeP, studyGradeC, studyGradeD, studyGradeHD}
	cleaned := studyRubricDocument{Levels: make(map[string]studyRubricLevel, len(grades))}
	for _, grade := range grades {
		level, exists := rubric.Levels[grade]
		description := clampStudyText(level.Description, studyMaxRubricDescRune)
		if !exists || description == "" {
			return nil, fmt.Errorf("rubric level %s is missing", grade)
		}
		cleaned.Levels[grade] = studyRubricLevel{
			Description: description,
			Anchor:      clampStudyText(level.Anchor, studyMaxAnchorRunes),
		}
	}
	return json.Marshal(cleaned)
}

// validateStudyScenarios bounds a generated batch, dropping unusable entries.
func validateStudyScenarios(raw *studyBankLLMOutput) []studyScenarioContent {
	if raw == nil {
		return nil
	}
	scenarios := make([]studyScenarioContent, 0, studyScenarioBatchSize)
	for entryIndex := range raw.Scenarios {
		entry := &raw.Scenarios[entryIndex]
		if len(scenarios) >= studyScenarioBatchSize {
			break
		}
		variant := strings.ToLower(strings.TrimSpace(entry.Variant))
		if variant != "structural" {
			variant = "surface"
		}
		scenario := studyScenarioContent{
			Situation:  clampStudyText(entry.Situation, studyMaxSituationRunes),
			Question:   clampStudyText(entry.Question, studyMaxQuestionRunes),
			QuestionZH: clampStudyText(entry.QuestionZH, studyMaxQuestionRunes),
			Hint:       clampStudyText(entry.Hint, studyMaxHintRunes),
			Variant:    variant,
			CAnchor:    clampStudyText(entry.CAnchor, studyMaxAnchorRunes),
			DAnchor:    clampStudyText(entry.DAnchor, studyMaxAnchorRunes),
		}
		if scenario.Situation == "" || scenario.Question == "" {
			continue
		}
		if scenario.CAnchor == "" || scenario.DAnchor == "" {
			continue
		}
		applyStudyFormat(&scenario, entry.Format, entry.Options, entry.AnswerIndexes, entry.AnswerText)
		for _, item := range entry.Glossary {
			if len(scenario.Glossary) >= studyMaxGlossary {
				break
			}
			term := clampStudyText(item.Term, studyMaxGlossRunes)
			gloss := clampStudyText(item.Gloss, studyMaxGlossRunes)
			if term == "" || gloss == "" {
				continue
			}
			scenario.Glossary = append(scenario.Glossary, studyGlossaryEntry{Term: term, Gloss: gloss})
		}
		for _, starter := range entry.Starters {
			if len(scenario.Starters) >= studyMaxStarters {
				break
			}
			if cleaned := clampStudyText(starter, studyMaxStarterRunes); cleaned != "" {
				scenario.Starters = append(scenario.Starters, cleaned)
			}
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

// applyStudyFormat normalizes the model's format fields. Anything that does
// not hold up as a choice or cloze item degrades to an open question rather
// than shipping a broken key.
func applyStudyFormat(
	scenario *studyScenarioContent, format string, options []string,
	indexes []int, answerText string,
) {
	format = strings.ToLower(strings.TrimSpace(format))
	cleanedOptions := make([]string, 0, len(options))
	for _, option := range options {
		if len(cleanedOptions) >= studyMaxOptions {
			break
		}
		if cleaned := clampStudyText(option, studyMaxOptionRunes); cleaned != "" {
			cleanedOptions = append(cleanedOptions, cleaned)
		}
	}
	seen := map[int]bool{}
	cleanedIndexes := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(cleanedOptions) || seen[index] {
			continue
		}
		seen[index] = true
		cleanedIndexes = append(cleanedIndexes, index)
	}
	sort.Ints(cleanedIndexes)
	switch format {
	case studyFormatSingle:
		if len(cleanedOptions) >= studyMinOptions && len(cleanedIndexes) == 1 {
			scenario.Format = studyFormatSingle
			scenario.Options = cleanedOptions
			scenario.AnswerIndexes = cleanedIndexes
			return
		}
	case studyFormatMulti:
		if len(cleanedOptions) >= studyMinOptions &&
			len(cleanedIndexes) >= 2 && len(cleanedIndexes) < len(cleanedOptions) {
			scenario.Format = studyFormatMulti
			scenario.Options = cleanedOptions
			scenario.AnswerIndexes = cleanedIndexes
			return
		}
	case studyFormatCloze:
		answer := clampStudyText(answerText, studyMaxOptionRunes)
		if answer != "" && strings.Contains(scenario.Question, "___") {
			scenario.Format = studyFormatCloze
			scenario.AnswerText = answer
			return
		}
	}
	scenario.Format = studyFormatOpen
	scenario.Options = nil
	scenario.AnswerIndexes = nil
	scenario.AnswerText = ""
}

const studyOptionLetters = "ABCDEFGHIJ"

func studyOptionLetter(index int) string {
	if index < 0 || index >= len(studyOptionLetters) {
		return strconv.Itoa(index + 1)
	}
	return studyOptionLetters[index : index+1]
}

// studySubmission is one graded input, whatever the format.
type studySubmission struct {
	Format  string
	Choices []int  // single / multi
	Fill    string // cloze
	Reason  string // explanation; for open questions this is the answer
	Correct *bool  // server-decided for single / multi
}

// evaluateStudyChoices decides a choice question deterministically.
func evaluateStudyChoices(scenario *studyScenarioContent, choices []int) bool {
	if len(choices) != len(scenario.AnswerIndexes) {
		return false
	}
	sorted := append([]int(nil), choices...)
	sort.Ints(sorted)
	for index, choice := range sorted {
		if choice != scenario.AnswerIndexes[index] {
			return false
		}
	}
	return true
}

// studySubmissionText is the stored, human-readable form of a submission.
func studySubmissionText(sub *studySubmission, scenario *studyScenarioContent) string {
	switch sub.Format {
	case studyFormatSingle, studyFormatMulti:
		letters := make([]string, 0, len(sub.Choices))
		for _, choice := range sub.Choices {
			if choice >= 0 && choice < len(scenario.Options) {
				letters = append(letters, studyOptionLetter(choice))
			}
		}
		text := "Selected: " + strings.Join(letters, ", ")
		if sub.Reason != "" {
			text += "\nReason: " + sub.Reason
		}
		return text
	case studyFormatCloze:
		text := "Fill: " + sub.Fill
		if sub.Reason != "" {
			text += "\nReason: " + sub.Reason
		}
		return text
	default:
		return sub.Reason
	}
}

func lowerStudyGrade(grade, ceiling string) string {
	if studyGradeRank(grade) > studyGradeRank(ceiling) {
		return ceiling
	}
	return grade
}

// capStudyGrade enforces what the key already decided: a wrong choice or
// fill never passes, and a bare correct choice is only C — the rubric
// decides D/HD from the reasoning.
func capStudyGrade(grade string, sub *studySubmission, answerCorrect *bool) string {
	switch sub.Format {
	case studyFormatSingle, studyFormatMulti:
		if sub.Correct == nil || !*sub.Correct {
			return lowerStudyGrade(grade, studyGradeP)
		}
		if sub.Reason == "" {
			return lowerStudyGrade(grade, studyGradeC)
		}
	case studyFormatCloze:
		if answerCorrect != nil && !*answerCorrect {
			return lowerStudyGrade(grade, studyGradeP)
		}
	}
	return grade
}

func clampStudyDifficulty(difficulty int) int {
	if difficulty < 1 {
		return 1
	}
	if difficulty > 3 {
		return 3
	}
	return difficulty
}

// validateStudyGrade bounds the grading output; grade must be a known level
// and both exits must exist (a grade never appears alone).
func validateStudyGrade(raw *studyGradeLLMOutput) (*studyGradeLLMOutput, error) {
	if raw == nil || studyGradeRank(raw.Grade) < 0 {
		return nil, errors.New("grade must be one of F, P, C, D, HD")
	}
	cleaned := &studyGradeLLMOutput{
		Grade:         raw.Grade,
		Feedback:      clampStudyText(raw.Feedback, studyMaxFeedbackRunes),
		NextStep:      clampStudyText(raw.NextStep, studyMaxFeedbackRunes),
		Bonuses:       raw.Bonuses,
		ErrorPattern:  clampStudyText(raw.ErrorPattern, 80),
		LanguageIssue: raw.LanguageIssue,
		AnswerCorrect: raw.AnswerCorrect,
		LanguageTip:   clampStudyText(raw.LanguageTip, studyMaxTipRunes),
	}
	if cleaned.Feedback == "" {
		return nil, errors.New("feedback is required")
	}
	if cleaned.NextStep == "" {
		cleaned.NextStep = "换一个情境再试一次，巩固这次的判断。"
	}
	return cleaned, nil
}

// studyBankInstruction asks for scenarios — and the frozen rubric only when
// one does not exist yet.
func studyBankInstruction(includeRubric bool, batch int) string {
	if batch < 1 || batch > studyScenarioBatchSize {
		batch = studyScenarioBatchSize
	}
	var builder strings.Builder
	builder.WriteString(`你是课程练习设计助手。根据下面这项课程能力，设计练习材料，只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{`)
	if includeRubric {
		builder.WriteString(`"rubric":{"levels":{"F":{"description":"…","anchor":"…"},"P":{…},"C":{…},"D":{…},"HD":{…}}},`)
	}
	builder.WriteString(`"scenarios":[{"situation":"…","question":"…","question_zh":"…","hint":"…","difficulty":1,"variant":"surface","format":"single","options":["…","…","…"],"answer_indexes":[1],"answer_text":"","glossary":[{"term":"confound","gloss":"混淆变量"}],"starters":["The study cannot establish … because …"],"c_anchor":"…","d_anchor":"…"}]}

要求：`)
	if includeRubric {
		builder.WriteString(`
- rubric 是这项能力的评分标准，之后会被冻结复用：每级 description 用中文写可观察的行为标准，anchor 给该等级的英文示例回答。F=核心判断错误；P=方向对但很浅或依赖提示；C=判断正确并有基本解释；D=解释完整、概念准确、能识别关键问题；HD=还能主动发现隐藏问题、提出替代解释或成功迁移。`)
	}
	builder.WriteString(`
- scenarios 恰好 `)
	builder.WriteString(strconv.Itoa(batch))
	if batch <= 3 {
		builder.WriteString(` 条：难度 1 一条（format 用 single 或 multi），难度 2 一条（cloze 或 multi），难度 3 一条（open）`)
	} else {
		builder.WriteString(` 条：难度 1、2、3 各至少 2 条。难度 1 用 single 和 multi；难度 2 用 cloze、multi 或 open（至少一条 open）；难度 3 只用 open`)
	}
	builder.WriteString(`。situation 用英文写具体情境（2-4 句），必须贴近下面的课堂原文，但不得整句抄原题。
- 难度 1 换表面细节（人名、数字、场景）；难度 2 换结构（反向因果、不同 confound）；难度 3 用陌生表述和陌生情境考迁移。variant 只能是 surface 或 structural。
- format 只能是 single（单选）、multi（多选）、cloze（填空）、open（问答）。single/multi 给 3–5 个英文 options，干扰项各对应一个常见误区；answer_indexes 是正确选项下标（从 0 开始），single 恰好一个，multi 至少两个且不能全选。cloze 的 question 里用 ____ 留一个空，answer_text 是应填的术语。open 不给 options。所有题型的 question 都要求学生说明理由。
- glossary 给 2–4 个题干里的关键英文术语及中文释义；starters 给 1–3 个英文学术句式起手（不含答案，如 "This design cannot rule out … because …"）。
- question 用英文要求判断并解释（不是背定义）；question_zh 是同一问题的中文版，只翻译障碍不翻译专业术语；hint 用中文给一条不剧透的思考方向。
- 每条必须有 c_anchor 和 d_anchor：各一句英文，说明「只到 C 的回答长什么样」和「到 D 还要多说什么」。分不出 C/D 的题丢掉。
- 先给情境，不先讲道理。`)
	return builder.String()
}

func publicStudyScenario(content *studyScenarioContent, scaffold studyScaffold) studyScenarioContent {
	public := *content
	public.CAnchor = ""
	public.DAnchor = ""
	public.AnswerIndexes = nil
	public.AnswerText = ""
	if public.Format == "" {
		public.Format = studyFormatOpen
	}
	if !scaffold.OfferHint {
		public.Hint = ""
	}
	if !scaffold.OfferZH {
		public.QuestionZH = ""
	}
	if !scaffold.OfferGlossary {
		public.Glossary = nil
	}
	if !scaffold.OfferStarters {
		public.Starters = nil
	}
	return public
}

// studyBankContext renders the skill (from the course skill map) the batch is
// grounded in.
func studyBankContext(skill *skillMapSkill, last *models.StudyAttempt) string {
	var builder strings.Builder
	builder.WriteString("能力：" + skill.Label + "\n")
	if skill.Summary != "" {
		builder.WriteString("说明：" + skill.Summary + "\n")
	}
	if skill.Outcome != "" {
		builder.WriteString("可观察行为：" + skill.Outcome + "\n")
	}
	for _, evidence := range skill.Evidence {
		builder.WriteString("课堂原文摘录：" + evidence.Quote + "\n")
	}
	if last != nil && last.ErrorPattern != "" {
		builder.WriteString("学生最近的错误模式：" + last.ErrorPattern + "\n")
		builder.WriteString("新题应针对这个误区换情境，不要只换人名或数字。\n")
	}
	return strings.TrimSpace(builder.String())
}

func studyGradeInstruction() string {
	return `你是课程练习的评分助手。对照给定的固定评分标准（rubric）为学生的回答定级，只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"grade":"C","feedback":"…","next_step":"…","bonuses":[],"error_pattern":"","language_issue":false,"answer_correct":true,"language_tip":""}

要求：
- grade 只能是 F、P、C、D、HD 之一；严格对照 rubric 各级 description 判定，拿不准时取较低一级；不无脑夸。
- feedback 用中文一句话（40 字内）说清这次回答差在哪或好在哪，只纠正最关键的一点。
- next_step 用中文一句话（40 字内）告诉学生要到更高一级还差什么。
- bonuses 只能从这些值里选，且只在回答明确表现出该行为时给出：self_correction、precise_language、alternative_explanation、hidden_insight、transfer、language_independence（过去常靠中文现在纯英文完成）。没有就给空数组。
- error_pattern 用简短英文标签描述本次错误模式（如 correlation_as_causation）；没有明显学科错误给空字符串。
- language_issue 在学生概念对但明显卡在英文题干时为 true，否则 false。
- 选择题会附标准答案和学生是否选对：选错时 grade 不得高于 P；选对但理由为空或错误 = C；理由完整、概念准确才给 D，HD 还要有额外洞察。
- 填空题时 answer_correct 表示学生填的词是否可接受（同义或等价表述算对）；选错/填错时 grade 不得高于 P。其他题型 answer_correct 给 true。
- language_tip 用一句话（60 字内）指出学生英文表达里最值得改的一处，格式如「你写的 "…" → 学术写法 "…"」；表达没问题或学生只写了中文/只做了选择时给空字符串。`
}

// studyGradeContext renders what the grader sees: frozen rubric, scenario,
// then the answer.
func studyGradeContext(
	rubricJSON json.RawMessage,
	scenario *studyScenarioContent,
	sub *studySubmission,
	usedHint, usedZH bool,
) string {
	var builder strings.Builder
	builder.WriteString("评分标准（rubric）：\n")
	builder.Write(rubricJSON)
	builder.WriteString("\n\n情境：\n" + scenario.Situation)
	builder.WriteString("\n\n问题：\n" + scenario.Question)
	switch scenario.Format {
	case studyFormatSingle, studyFormatMulti:
		builder.WriteString("\n\n题型：")
		if scenario.Format == studyFormatSingle {
			builder.WriteString("单选")
		} else {
			builder.WriteString("多选")
		}
		builder.WriteString("\n选项：")
		for index, option := range scenario.Options {
			builder.WriteString("\n" + studyOptionLetter(index) + ". " + option)
		}
		builder.WriteString("\n标准答案：")
		for index, answer := range scenario.AnswerIndexes {
			if index > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(studyOptionLetter(answer))
		}
		builder.WriteString("\n学生的选择：")
		for index, choice := range sub.Choices {
			if index > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(studyOptionLetter(choice))
		}
		if sub.Correct != nil && *sub.Correct {
			builder.WriteString("（选对了）")
		} else {
			builder.WriteString("（选错了）")
		}
	case studyFormatCloze:
		builder.WriteString("\n\n题型：填空\n标准答案：" + scenario.AnswerText)
		builder.WriteString("\n学生填的：" + sub.Fill)
	}
	if scenario.CAnchor != "" {
		builder.WriteString("\n\nC 级锚点（判断对、解释浅）：\n" + scenario.CAnchor)
	}
	if scenario.DAnchor != "" {
		builder.WriteString("\nD 级锚点（还要指出关键问题）：\n" + scenario.DAnchor)
	}
	if sub.Format == studyFormatOpen || sub.Format == "" {
		builder.WriteString("\n\n学生的回答：\n" + sub.Reason)
	} else if sub.Reason != "" {
		builder.WriteString("\n学生的理由：\n" + sub.Reason)
	} else {
		builder.WriteString("\n学生的理由：（没有写）")
	}
	builder.WriteString("\n\n学生是否查看了中文题面：")
	if usedZH {
		builder.WriteString("是")
	} else {
		builder.WriteString("否")
	}
	builder.WriteString("\n学生是否使用了提示：")
	if usedHint {
		builder.WriteString("是")
	} else {
		builder.WriteString("否")
	}
	builder.WriteString("\n若英文题干看不懂但概念是对的，language_issue 为 true；看了中文仍然错，则是学科问题，language_issue 为 false。")
	return builder.String()
}
