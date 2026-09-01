package handlers

import (
	"strings"
	"testing"
)

// The teaching layer: every item carries an explanation and model answer
// that are stripped before answering and revealed after.

func TestParseAIProjectRouteAcceptsLessonAndReveal(t *testing.T) {
	for _, action := range []string{"lesson", "reveal"} {
		route, status, err := parseAIProjectRoute(
			"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/study/" + action,
		)
		if err != nil || status != 200 || route.Action != action {
			t.Fatalf("study/%s: status=%d err=%v route=%+v", action, status, err, route)
		}
	}
}

func TestApplyStudyFormatAcceptsTrueFalse(t *testing.T) {
	yes := true
	item := studyScenarioContent{Question: "Random assignment makes a design experimental."}
	applyStudyFormat(&item, "tf", nil, nil, "", &yes)
	if item.Format != studyFormatTF || item.AnswerBool == nil || !*item.AnswerBool {
		t.Fatalf("tf with a key: %+v", item)
	}
	broken := studyScenarioContent{Question: "?"}
	applyStudyFormat(&broken, "tf", nil, nil, "", nil)
	if broken.Format != studyFormatOpen || broken.AnswerBool != nil {
		t.Fatalf("tf without a key falls back to open: %+v", broken)
	}
}

func TestClampStudyDifficultyForFormat(t *testing.T) {
	cases := []struct {
		format string
		in     int
		want   int
	}{
		{studyFormatTF, 3, 1},
		{studyFormatSingle, 2, 1},
		{studyFormatMulti, 3, 2},
		{studyFormatMulti, 1, 1},
		{studyFormatCloze, 1, 2},
		{studyFormatOpen, 1, 2},
		{studyFormatOpen, 3, 3},
	}
	for _, c := range cases {
		if got := clampStudyDifficultyForFormat(c.in, c.format); got != c.want {
			t.Fatalf("%s@%d = %d, want %d", c.format, c.in, got, c.want)
		}
	}
}

func TestBuildStudySubmissionTrueFalse(t *testing.T) {
	no := false
	content := &studyScenarioContent{Format: studyFormatTF, AnswerBool: &no, Question: "x"}
	if _, err := buildStudySubmission(content, &studyAttemptRequest{}); err == nil {
		t.Fatal("tf without answer_bool must be rejected")
	}
	yes := true
	sub, err := buildStudySubmission(content, &studyAttemptRequest{AnswerBool: &yes, Reason: "because"})
	if err != nil || sub.Correct == nil || *sub.Correct {
		t.Fatalf("wrong judgment should be marked incorrect: %+v / %v", sub, err)
	}
	if got := capStudyGrade(studyGradeD, sub, nil); got != studyGradeP {
		t.Fatalf("wrong judgment caps at P, got %s", got)
	}
	right, _ := buildStudySubmission(content, &studyAttemptRequest{AnswerBool: &no})
	if got := capStudyGrade(studyGradeHD, right, nil); got != studyGradeC {
		t.Fatalf("bare correct judgment is C, got %s", got)
	}
	if !strings.HasPrefix(studySubmissionText(right, content), "Judged: FALSE") {
		t.Fatalf("submission text: %q", studySubmissionText(right, content))
	}
}

func TestPublicStudyScenarioHidesTeachingLayer(t *testing.T) {
	yes := true
	content := studyScenarioContent{
		Situation: "s", Question: "q", Format: studyFormatTF, AnswerBool: &yes,
		Explanation: "看到相关先想第三变量", ModelAnswer: "No, the data are correlational.",
		GapToC: "说出相关不等于因果", Targets: []string{"correlation ≠ causation"},
		OptionNotes: []string{"a"}, Lang: "中文框架 · EN 术语",
	}
	public := publicStudyScenario(&content, studyScaffold{OfferHint: true, OfferZH: true})
	if public.Explanation != "" || public.ModelAnswer != "" || public.GapToC != "" ||
		public.Targets != nil || public.OptionNotes != nil || public.AnswerBool != nil {
		t.Fatalf("teaching layer leaked before answering: %+v", public)
	}
	if public.Lang == "" {
		t.Fatal("language tier is display data and must survive")
	}
	reveal := studyRevealFor(&content)
	if reveal.Explanation == "" || reveal.ModelAnswer == "" || reveal.AnswerBool == nil ||
		len(reveal.Targets) != 1 || reveal.Format != studyFormatTF {
		t.Fatalf("reveal must carry the full teaching layer: %+v", reveal)
	}
	legacy := studyScenarioContent{Question: "q", CAnchor: "just C", DAnchor: "also D"}
	if got := studyRevealFor(&legacy); got.ModelAnswer != "also D" || studyHasTeaching(&legacy) {
		t.Fatalf("legacy items fall back to the D anchor: %+v", got)
	}
}

func TestStudyAttemptXPPaysSelfCorrection(t *testing.T) {
	xp, bonuses := studyAttemptXP("C", false, false, true, []string{"self_correction"})
	if xp != 100+studyNoHintXP+studySelfCorrectionXP {
		t.Fatalf("self-corrected C xp=%d", xp)
	}
	count := 0
	for _, bonus := range bonuses {
		if bonus == studyBonusSelfCorrection {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("self_correction must be awarded exactly once by the server: %v", bonuses)
	}
	if applyStudyDifficultyXP(100, 3) != 150 || applyStudyDifficultyXP(100, 2) != 125 || applyStudyDifficultyXP(100, 1) != 100 {
		t.Fatal("difficulty multipliers are ×1 / ×1.25 / ×1.5")
	}
}

func TestValidateStudyLesson(t *testing.T) {
	var raw studyLessonDocument
	if err := studyExtractJSON(`{
		"rule": "看到 correlation，先想第三变量",
		"concepts": [{"term": "confounding variable", "gloss": "同时影响两边的变量", "quote": "a third variable"}, {"term": "", "gloss": "x"}],
		"misconceptions": [{"label": "correlation_as_causation", "how_to_tell": "一看到一起变就说因为"}],
		"example": {"situation": "喝咖啡多的学生 GPA 高", "question": "能说咖啡提升成绩吗？", "answer": "No. The data are correlational.", "walkthrough": "先判数据类型"}
	}`, &raw); err != nil {
		t.Fatal(err)
	}
	encoded, err := validateStudyLesson(&raw)
	if err != nil {
		t.Fatalf("valid lesson rejected: %v", err)
	}
	if !strings.Contains(string(encoded), "confounding variable") || strings.Contains(string(encoded), `"term":""`) {
		t.Fatalf("lesson content: %s", encoded)
	}
	raw.Example.Answer = ""
	if _, err := validateStudyLesson(&raw); err == nil {
		t.Fatal("an example without an answer teaches nothing")
	}
	if _, err := validateStudyLesson(nil); err == nil {
		t.Fatal("nil lesson must be rejected")
	}
}

func TestValidateStudyScenariosKeepsTeachingFields(t *testing.T) {
	var raw studyBankLLMOutput
	if err := studyExtractJSON(`{"scenarios": [{
		"situation": "一项研究发现喝咖啡多的学生 GPA 更高。",
		"question": "研究者说 coffee improves grades，站得住吗？",
		"difficulty": 1, "format": "tf", "answer_bool": false,
		"explanation": "看到相关先想第三变量。", "model_answer": "No, the data are correlational.",
		"gap_to_c": "说出相关不等于因果", "targets": ["correlation ≠ causation", "confound", "correlation ≠ causation"],
		"c_anchor": "c", "d_anchor": "d"
	}, {
		"situation": "s", "question": "Which is the confound?", "difficulty": 1, "format": "single",
		"options": ["Study hours", "Coffee price", "GPA scale"], "answer_indexes": [0],
		"option_notes": ["同时影响两边", "只碰一边", "是尺度"],
		"c_anchor": "c", "d_anchor": "d"
	}]}`, &raw); err != nil {
		t.Fatal(err)
	}
	scenarios := validateStudyScenarios(&raw)
	if len(scenarios) != 2 {
		t.Fatalf("kept %d scenarios, want 2", len(scenarios))
	}
	tf := scenarios[0]
	if tf.Format != studyFormatTF || tf.AnswerBool == nil || *tf.AnswerBool || tf.Explanation == "" ||
		len(tf.Targets) != 2 || tf.Lang != studyLangForDifficulty(1) {
		t.Fatalf("tf item: %+v", tf)
	}
	single := scenarios[1]
	if len(single.OptionNotes) != 3 || single.OptionNotes[0] != "同时影响两边" {
		t.Fatalf("option notes must align with options: %+v", single)
	}
}

func TestStudyGradeInstructionAsksForTeachingOnlyWhenMissing(t *testing.T) {
	plain := studyGradeInstruction(false)
	if strings.Contains(plain, `"explanation"`) || !strings.Contains(plain, "你抓住了") {
		t.Fatalf("plain grading must not request teaching fields, must lead with what was right:\n%s", plain)
	}
	teach := studyGradeInstruction(true)
	if !strings.Contains(teach, `"explanation"`) || !strings.Contains(teach, "model_answer") {
		t.Fatal("legacy items must get an explanation written")
	}
	yes := true
	ctx := studyGradeContext([]byte(`{}`), &studyScenarioContent{
		Situation: "s", Question: "q", Format: studyFormatTF, AnswerBool: &yes,
	}, &studySubmission{Format: studyFormatTF, Bool: &yes, Correct: &yes}, 2, false, true)
	for _, want := range []string{"题型：判断", "标准答案：TRUE", "判对了", "本题难度：2（应用）"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("grade context missing %q:\n%s", want, ctx)
		}
	}
}

func TestRecommendStudySkillExplainsWhy(t *testing.T) {
	doc := &skillMapDocument{Skills: []skillMapSkill{{ID: "s1", Label: "相关"}}}
	got := recommendStudySkill(doc, nil)
	if got == nil || got.Reason == "" {
		t.Fatalf("a fresh course still explains the first stop: %+v", got)
	}
}
