package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	studyScenarioBatchSize = 3
	studyMaxBonusCount     = 2
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
}

// Server-detected bonuses.
const (
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

// The scenario content persisted in study_scenarios.content.
type studyScenarioContent struct {
	Situation  string `json:"situation"`
	Question   string `json:"question"`
	QuestionZH string `json:"question_zh,omitempty"`
	Hint       string `json:"hint,omitempty"`
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
	} `json:"scenarios"`
}

// What the grading model emits.
type studyGradeLLMOutput struct {
	Grade    string   `json:"grade"`
	Feedback string   `json:"feedback"`
	NextStep string   `json:"next_step"`
	Bonuses  []string `json:"bonuses"`
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
	for _, entry := range raw.Scenarios {
		if len(scenarios) >= studyScenarioBatchSize {
			break
		}
		scenario := studyScenarioContent{
			Situation:  clampStudyText(entry.Situation, studyMaxSituationRunes),
			Question:   clampStudyText(entry.Question, studyMaxQuestionRunes),
			QuestionZH: clampStudyText(entry.QuestionZH, studyMaxQuestionRunes),
			Hint:       clampStudyText(entry.Hint, studyMaxHintRunes),
		}
		if scenario.Situation == "" || scenario.Question == "" {
			continue
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
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
		Grade:    raw.Grade,
		Feedback: clampStudyText(raw.Feedback, studyMaxFeedbackRunes),
		NextStep: clampStudyText(raw.NextStep, studyMaxFeedbackRunes),
		Bonuses:  raw.Bonuses,
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
func studyBankInstruction(includeRubric bool) string {
	var builder strings.Builder
	builder.WriteString(`你是课程练习设计助手。根据下面这项课程能力，设计练习材料，只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{`)
	if includeRubric {
		builder.WriteString(`"rubric":{"levels":{"F":{"description":"…","anchor":"…"},"P":{…},"C":{…},"D":{…},"HD":{…}}},`)
	}
	builder.WriteString(`"scenarios":[{"situation":"…","question":"…","question_zh":"…","hint":"…","difficulty":1}]}

要求：`)
	if includeRubric {
		builder.WriteString(`
- rubric 是这项能力的评分标准，之后会被冻结复用：每级 description 用中文写可观察的行为标准，anchor 给该等级的英文示例回答。F=核心判断错误；P=方向对但很浅或依赖提示；C=判断正确并有基本解释；D=解释完整、概念准确、能识别关键问题；HD=还能主动发现隐藏问题、提出替代解释或成功迁移。`)
	}
	builder.WriteString(`
- scenarios 恰好 3 条，难度分别为 1、2、3。situation 用英文写一个具体情境（2-4 句），要贴近课堂内容但不是课堂原题；难度 3 换陌生表述与陌生情境（考迁移）。
- question 用英文提出一个要求判断并解释的问题（不是背诵定义）；question_zh 是同一问题的中文版；hint 用中文给一条不剧透答案的思考方向。
- 先给情境，不先讲道理。`)
	return builder.String()
}

// studyBankContext renders the skill (from the course skill map) the batch is
// grounded in.
func studyBankContext(skill *skillMapSkill) string {
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
	return strings.TrimSpace(builder.String())
}

func studyGradeInstruction() string {
	return `你是课程练习的评分助手。对照给定的固定评分标准（rubric）为学生的回答定级，只输出一个严格合法的 JSON 对象（不要 Markdown 代码块，不要任何解释文字）。

JSON 结构：
{"grade":"C","feedback":"…","next_step":"…","bonuses":[]}

要求：
- grade 只能是 F、P、C、D、HD 之一；严格对照 rubric 各级 description 判定，拿不准时取较低一级；不无脑夸。
- feedback 用中文一句话（40 字内）说清这次回答差在哪或好在哪，只纠正最关键的一点。
- next_step 用中文一句话（40 字内）告诉学生要到更高一级还差什么。
- bonuses 只能从这些值里选，且只在回答明确表现出该行为时给出：self_correction（自己发现并修正错误）、precise_language（准确使用专业表达）、alternative_explanation（主动提出其他合理解释）、hidden_insight（发现题目未明示的问题）、transfer（把已学知识用到陌生情境）。没有就给空数组。`
}

// studyGradeContext renders what the grader sees: frozen rubric, scenario,
// then the answer.
func studyGradeContext(rubricJSON json.RawMessage, scenario *studyScenarioContent, answer string) string {
	var builder strings.Builder
	builder.WriteString("评分标准（rubric）：\n")
	builder.Write(rubricJSON)
	builder.WriteString("\n\n情境：\n" + scenario.Situation)
	builder.WriteString("\n\n问题：\n" + scenario.Question)
	builder.WriteString("\n\n学生的回答：\n" + answer)
	return builder.String()
}
