package handlers

import (
	"strings"
	"testing"
)

func TestParseAIProjectRouteAcceptsStudyActions(t *testing.T) {
	for _, action := range []string{"state", "next", "attempts"} {
		route, status, err := parseAIProjectRoute(
			"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/study/" + action,
		)
		if err != nil || status != 200 {
			t.Fatalf("study/%s: status=%d err=%v", action, status, err)
		}
		if route.Resource != "study" || route.Action != action {
			t.Fatalf("study/%s parsed as %+v", action, route)
		}
	}
	if _, _, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/study",
	); err == nil {
		t.Fatal("bare study resource must be rejected")
	}
	if _, _, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/study/unknown",
	); err == nil {
		t.Fatal("unknown study action must be rejected")
	}
}

func TestAdvanceStudyStateProgression(t *testing.T) {
	// Two clean ≥C attempts move learner → supervised, spending the streak.
	level, streak, up := advanceStudyState("learner", 1, "C", false)
	if level != "supervised" || streak != 0 || !up {
		t.Fatalf("learner with streak: got %s/%d/%v", level, streak, up)
	}
	// A hinted pass keeps the streak (support was still needed).
	level, streak, up = advanceStudyState("supervised", 2, "D", true)
	if level != "supervised" || streak != 2 || up {
		t.Fatalf("hinted pass: got %s/%d/%v", level, streak, up)
	}
	// A failing grade resets the streak.
	level, streak, up = advanceStudyState("hazard", 2, "P", false)
	if level != "hazard" || streak != 0 || up {
		t.Fatalf("failing grade: got %s/%d/%v", level, streak, up)
	}
	// Independent needs a longer unaided run to reach mastery.
	level, _, up = advanceStudyState("independent", 3, "HD", false)
	if level != "mastered" || !up {
		t.Fatalf("mastery: got %s up=%v", level, up)
	}
	// Mastered has no further level and never levels up again.
	level, streak, up = advanceStudyState("mastered", 9, "HD", false)
	if level != "mastered" || up || streak != 10 {
		t.Fatalf("mastered: got %s/%d/%v", level, streak, up)
	}
	// Unknown stored levels degrade safely to learner.
	if level, _, _ := advanceStudyState("corrupt", 0, "F", false); level != "learner" {
		t.Fatalf("corrupt level: got %s", level)
	}
}

func TestStudyAttemptXPRewardsQuality(t *testing.T) {
	// Below C: base XP only, no bonuses even without hints.
	xp, bonuses := studyAttemptXP("P", false, true, []string{"transfer"})
	if xp != 40 || len(bonuses) != 0 {
		t.Fatalf("P attempt: xp=%d bonuses=%v", xp, bonuses)
	}
	// HD, unaided, first try, with two valid + one invalid + one duplicate bonus.
	xp, bonuses = studyAttemptXP("HD", false, true, []string{
		"transfer", "transfer", "made_up", "hidden_insight", "precise_language",
	})
	wantXP := 280 + studyNoHintXP + studyFirstTryXP + 2*studyBonusXP
	if xp != wantXP {
		t.Fatalf("HD attempt xp=%d, want %d", xp, wantXP)
	}
	if len(bonuses) != 4 || bonuses[0] != studyBonusNoHint || bonuses[1] != studyBonusFirstTry {
		t.Fatalf("HD bonuses=%v", bonuses)
	}
	// Hinted C on a later attempt: base only.
	xp, bonuses = studyAttemptXP("C", true, false, nil)
	if xp != 100 || len(bonuses) != 0 {
		t.Fatalf("hinted C: xp=%d bonuses=%v", xp, bonuses)
	}
	// Unknown grades pay nothing.
	if xp, _ := studyAttemptXP("A+", false, false, nil); xp != 0 {
		t.Fatalf("unknown grade xp=%d", xp)
	}
}

func TestValidateStudyRubricRequiresAllLevels(t *testing.T) {
	var raw studyBankLLMOutput
	if err := studyExtractJSON("```json\n"+`{
		"rubric": {"levels": {
			"F": {"description": "核心判断错误"},
			"P": {"description": "方向对但很浅"},
			"C": {"description": "判断正确并有基本解释", "anchor": "Correlation is not causation."},
			"D": {"description": "解释完整"},
			"HD": {"description": "能主动发现隐藏问题"}
		}},
		"scenarios": []
	}`+"\n```", &raw); err != nil {
		t.Fatal(err)
	}
	encoded, err := validateStudyRubric(raw.Rubric)
	if err != nil {
		t.Fatalf("full rubric rejected: %v", err)
	}
	if !strings.Contains(string(encoded), "核心判断错误") {
		t.Fatalf("rubric content lost: %s", encoded)
	}
	raw.Rubric.Levels["D"] = studyRubricLevel{}
	if _, err := validateStudyRubric(raw.Rubric); err == nil {
		t.Fatal("missing level description must be rejected")
	}
	if _, err := validateStudyRubric(nil); err == nil {
		t.Fatal("nil rubric must be rejected")
	}
}

func TestValidateStudyScenariosDropsUnusableEntries(t *testing.T) {
	var raw studyBankLLMOutput
	if err := studyExtractJSON(`{
		"scenarios": [
			{"situation": "A study finds coffee drinkers have higher GPAs.",
			 "question": "Can we conclude coffee improves grades?",
			 "question_zh": "能得出咖啡提升成绩的结论吗？",
			 "hint": "想想还有什么变量", "difficulty": 9},
			{"situation": "", "question": "no situation"},
			{"situation": "has situation", "question": ""},
			{"situation": "s2", "question": "q2", "difficulty": 2},
			{"situation": "s3", "question": "q3"},
			{"situation": "s4", "question": "overflow"}
		]
	}`, &raw); err != nil {
		t.Fatal(err)
	}
	scenarios := validateStudyScenarios(&raw)
	if len(scenarios) != studyScenarioBatchSize {
		t.Fatalf("scenarios=%d, want %d", len(scenarios), studyScenarioBatchSize)
	}
	if scenarios[0].QuestionZH == "" || scenarios[0].Hint == "" {
		t.Fatalf("scaffold fields lost: %+v", scenarios[0])
	}
	if clampStudyDifficulty(9) != 3 || clampStudyDifficulty(0) != 1 {
		t.Fatal("difficulty must clamp to 1..3")
	}
}

func TestValidateStudyGradeRequiresExit(t *testing.T) {
	if _, err := validateStudyGrade(&studyGradeLLMOutput{Grade: "B", Feedback: "x"}); err == nil {
		t.Fatal("unknown grade must be rejected")
	}
	if _, err := validateStudyGrade(&studyGradeLLMOutput{Grade: "C"}); err == nil {
		t.Fatal("a grade without feedback must be rejected")
	}
	graded, err := validateStudyGrade(&studyGradeLLMOutput{Grade: "C", Feedback: "核心判断对了"})
	if err != nil {
		t.Fatal(err)
	}
	if graded.NextStep == "" {
		t.Fatal("next_step must be backfilled: a grade never appears alone")
	}
}

func TestStudyDifficultyForLevel(t *testing.T) {
	cases := map[string]int{
		"learner": 1, "supervised": 1, "hazard": 2,
		"independent": 3, "mastered": 3, "unknown": 1,
	}
	for level, want := range cases {
		if got := studyDifficultyForLevel(level); got != want {
			t.Fatalf("difficulty(%s)=%d, want %d", level, got, want)
		}
	}
}
