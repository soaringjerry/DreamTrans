package handlers

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/store"
)

func TestParseAIProjectRouteAcceptsSkillMap(t *testing.T) {
	route, status, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/skill-map",
	)
	if err != nil || status != 200 {
		t.Fatalf("expected skill-map route to parse, got status=%d err=%v", status, err)
	}
	if route.Resource != "skill-map" {
		t.Fatalf("resource = %q, want skill-map", route.Resource)
	}
	if _, _, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/skill-map/extra",
	); err == nil {
		t.Fatal("expected trailing path segments after skill-map to be rejected")
	}
	if _, _, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/concept-map",
	); err == nil {
		t.Fatal("expected the retired concept-map route to be rejected")
	}
}

func TestParseGeneratedSkillMapToleratesFencesAndProse(t *testing.T) {
	content := "好的，这是技能地图：\n```json\n" +
		`{"skills":[{"label":"区分相关与因果","summary":"说明","outcome":"能判断"}]}` +
		"\n```\n"
	raw, err := parseGeneratedSkillMap(content)
	if err != nil {
		t.Fatalf("parse fenced output: %v", err)
	}
	if len(raw.Skills) != 1 || raw.Skills[0].Label != "区分相关与因果" {
		t.Fatalf("unexpected parse result: %+v", raw)
	}
	if _, err := parseGeneratedSkillMap("抱歉，我无法生成。"); err == nil {
		t.Fatal("expected prose-only output to be rejected")
	}
	if _, err := parseGeneratedSkillMap(`{"skills":[]}`); err == nil {
		t.Fatal("expected empty skills to be rejected")
	}
}

func testSkillMapSessions() []store.ProjectSessionRef {
	return []store.ProjectSessionRef{
		{ID: "s-1", Title: "第一讲", StartedAt: time.Now()},
		{ID: "s-2", Title: "第二讲", StartedAt: time.Now()},
	}
}

func TestBuildSkillMapDocumentResolvesEvidenceAndPrerequisites(t *testing.T) {
	raw, err := parseGeneratedSkillMap(`{
		"skills": [
			{"label": "识别相关关系", "summary": "看懂相关", "outcome": "能识别变量间的相关",
				"prerequisites": ["区分相关与因果"],
				"evidence": [
					{"session": 1, "quote": "相关不等于因果"},
					{"session": 5, "quote": "越界的会话编号"},
					{"session": 0, "quote": "非法编号"}
				]},
			{"label": "区分相关与因果", "summary": "因果推断的前提", "outcome": "能判断结论是否越界",
				"prerequisites": ["识别相关关系", "识别相关关系", "区分相关与因果", "不存在的能力"],
				"evidence": [{"session": 2, "quote": "混淆变量的例子"}]}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	doc := buildSkillMapDocument(raw, testSkillMapSessions(), nil)
	if len(doc.Skills) != 2 {
		t.Fatalf("skills = %d, want 2", len(doc.Skills))
	}
	first, second := doc.Skills[0], doc.Skills[1]
	if first.ID != "s1" || second.ID != "s2" {
		t.Fatalf("unexpected ids: %q / %q", first.ID, second.ID)
	}
	// Forward references (skill 1 depending on the later skill 2) are dropped:
	// prerequisites may only point at earlier skills, keeping the map acyclic.
	if len(first.Prerequisites) != 0 {
		t.Fatalf("forward prerequisite survived: %+v", first.Prerequisites)
	}
	if len(second.Prerequisites) != 1 || second.Prerequisites[0] != "s1" {
		t.Fatalf("prerequisites = %+v, want [s1] (dedup, no self, no unknown)", second.Prerequisites)
	}
	if len(first.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1 (out-of-range ordinals dropped)", len(first.Evidence))
	}
	if first.Evidence[0].SessionID != "s-1" || first.Evidence[0].SessionTitle != "第一讲" {
		t.Fatalf("evidence not resolved: %+v", first.Evidence[0])
	}
	// First generation: nothing is marked new.
	for _, skill := range doc.Skills {
		if skill.New {
			t.Fatal("first generation must not mark skills as new")
		}
	}
}

func TestBuildSkillMapDocumentMarksNewAgainstPrevious(t *testing.T) {
	previous := &skillMapDocument{
		Version: 1,
		Skills: []skillMapSkill{
			{ID: "s1", Label: "识别相关关系"},
		},
	}
	raw, err := parseGeneratedSkillMap(`{
		"skills": [
			{"label": "识别相关关系"},
			{"label": "区分相关与因果"}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	doc := buildSkillMapDocument(raw, testSkillMapSessions(), previous)
	if doc.Skills[0].New {
		t.Fatal("existing skill must not be marked new")
	}
	if !doc.Skills[1].New {
		t.Fatal("added skill must be marked new")
	}
}

func TestBuildSkillMapDocumentEnforcesBounds(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`{"skills":[`)
	for skill := 0; skill < 30; skill++ {
		if skill > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"label":"能力` + strconv.Itoa(skill) +
			strings.Repeat("长", 100) + `","summary":"` + strings.Repeat("说", 500) + `"}`)
	}
	builder.WriteString(`]}`)
	raw, err := parseGeneratedSkillMap(builder.String())
	if err != nil {
		t.Fatal(err)
	}
	doc := buildSkillMapDocument(raw, testSkillMapSessions(), nil)
	if len(doc.Skills) > skillMapMaxSkills {
		t.Fatalf("skills = %d, want at most %d", len(doc.Skills), skillMapMaxSkills)
	}
	for _, skill := range doc.Skills {
		if len([]rune(skill.Label)) > skillMapMaxLabelRunes {
			t.Fatalf("label exceeds cap: %d runes", len([]rune(skill.Label)))
		}
		if len([]rune(skill.Summary)) > skillMapMaxSummaryRunes {
			t.Fatalf("summary exceeds cap: %d runes", len([]rune(skill.Summary)))
		}
	}
	// Duplicate labels collapse to one skill.
	duplicated, err := parseGeneratedSkillMap(
		`{"skills":[{"label":"识别相关关系"},{"label":"  识别相关关系  "}]}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if doc := buildSkillMapDocument(duplicated, testSkillMapSessions(), nil); len(doc.Skills) != 1 {
		t.Fatalf("duplicate labels produced %d skills, want 1", len(doc.Skills))
	}
}

func TestTruncateContextTextNeverSplitsRunes(t *testing.T) {
	text := strings.Repeat("技能地图", 100)
	truncatedText := truncateContextText(text, 10)
	if truncatedText == "" {
		t.Fatal("expected a non-empty prefix")
	}
	if len(truncatedText) > 10 {
		t.Fatalf("byte length = %d, want at most 10", len(truncatedText))
	}
	if !strings.HasPrefix(text, truncatedText) {
		t.Fatal("truncation must return a clean prefix")
	}
	if truncateContextText("abc", 0) != "" {
		t.Fatal("zero budget must return empty")
	}
	if truncateContextText("abc", 10) != "abc" {
		t.Fatal("under-budget text must be unchanged")
	}
}

func TestParseStoredSkillMapRejectsForeignContent(t *testing.T) {
	if parseStoredSkillMap("# 这是一份 markdown 摘要") != nil {
		t.Fatal("markdown content must not parse as a skill map")
	}
	if parseStoredSkillMap(`{"version":0,"skills":[]}`) != nil {
		t.Fatal("empty/versionless documents must not parse")
	}
	doc := parseStoredSkillMap(
		`{"version":1,"skills":[{"id":"s1","label":"识别相关关系"}]}`,
	)
	if doc == nil || doc.Skills[0].Label != "识别相关关系" {
		t.Fatalf("valid stored document failed to parse: %+v", doc)
	}
}

func TestSkillMapInstructionEmbedsPreviousSkeleton(t *testing.T) {
	withoutPrevious := skillMapInstruction(nil)
	if strings.Contains(withoutPrevious, "上一版技能地图") {
		t.Fatal("first generation must not mention a previous map")
	}
	previous := &skillMapDocument{
		Version: 1,
		Skills: []skillMapSkill{
			{ID: "s1", Label: "识别相关关系"},
			{ID: "s2", Label: "区分相关与因果", Prerequisites: []string{"s1"}},
		},
	}
	withPrevious := skillMapInstruction(previous)
	if !strings.Contains(withPrevious, "识别相关关系") ||
		!strings.Contains(withPrevious, "区分相关与因果") {
		t.Fatal("previous labels must appear in the skeleton")
	}
}
