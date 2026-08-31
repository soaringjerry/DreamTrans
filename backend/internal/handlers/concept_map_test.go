package handlers

import (
	"strings"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/store"
)

func TestParseAIProjectRouteAcceptsConceptMap(t *testing.T) {
	route, status, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/concept-map",
	)
	if err != nil || status != 200 {
		t.Fatalf("expected concept-map route to parse, got status=%d err=%v", status, err)
	}
	if route.Resource != "concept-map" {
		t.Fatalf("resource = %q, want concept-map", route.Resource)
	}
	if _, _, err := parseAIProjectRoute(
		"/api/ai/projects/6f1de0f8-51d2-4c0b-9f0e-1f22a67f9a01/concept-map/extra",
	); err == nil {
		t.Fatal("expected trailing path segments after concept-map to be rejected")
	}
}

func TestParseGeneratedConceptMapToleratesFencesAndProse(t *testing.T) {
	content := "好的，这是地图：\n```json\n" +
		`{"topics":[{"label":"主题","children":[{"label":"概念","summary":"解释"}]}],"links":[]}` +
		"\n```\n"
	raw, err := parseGeneratedConceptMap(content)
	if err != nil {
		t.Fatalf("parse fenced output: %v", err)
	}
	if len(raw.Topics) != 1 || raw.Topics[0].Label != "主题" {
		t.Fatalf("unexpected parse result: %+v", raw)
	}
	if _, err := parseGeneratedConceptMap("抱歉，我无法生成。"); err == nil {
		t.Fatal("expected prose-only output to be rejected")
	}
	if _, err := parseGeneratedConceptMap(`{"topics":[]}`); err == nil {
		t.Fatal("expected empty topics to be rejected")
	}
}

func testConceptMapSessions() []store.ProjectSessionRef {
	return []store.ProjectSessionRef{
		{ID: "s-1", Title: "第一课", StartedAt: time.Now()},
		{ID: "s-2", Title: "第二课", StartedAt: time.Now()},
	}
}

func TestBuildConceptMapDocumentResolvesEvidenceAndLinks(t *testing.T) {
	raw, err := parseGeneratedConceptMap(`{
		"topics": [
			{"label": "语言学基础", "children": [
				{"label": "音位", "summary": "最小的语音单位", "evidence": [
					{"session": 1, "quote": "音位是最小的语音单位"},
					{"session": 5, "quote": "越界的会话编号"},
					{"session": 0, "quote": "非法编号"}
				]},
				{"label": "语素", "summary": "最小的语义单位"}
			]},
			{"label": "句法", "children": [
				{"label": "短语结构", "evidence": [{"session": 2, "quote": "短语结构规则"}]}
			]}
		],
		"links": [
			{"from": "音位", "to": "短语结构", "label": "层级递进"},
			{"from": "音位", "to": "不存在的概念"},
			{"from": "短语结构", "to": "音位", "label": "重复反向"},
			{"from": "语素", "to": "语素"}
		]
	}`)
	if err != nil {
		t.Fatal(err)
	}
	doc := buildConceptMapDocument(raw, testConceptMapSessions(), nil)
	if len(doc.Topics) != 2 {
		t.Fatalf("topics = %d, want 2", len(doc.Topics))
	}
	first := doc.Topics[0].Children[0]
	if first.ID != "t1-c1" || doc.Topics[1].Children[0].ID != "t2-c1" {
		t.Fatalf("unexpected ids: %q / %q", first.ID, doc.Topics[1].Children[0].ID)
	}
	if len(first.Evidence) != 1 {
		t.Fatalf("evidence = %d, want 1 (out-of-range ordinals dropped)", len(first.Evidence))
	}
	if first.Evidence[0].SessionID != "s-1" || first.Evidence[0].SessionTitle != "第一课" {
		t.Fatalf("evidence not resolved: %+v", first.Evidence[0])
	}
	if len(doc.Links) != 1 {
		t.Fatalf("links = %d, want 1 (unresolved/self/reverse-duplicate dropped)", len(doc.Links))
	}
	if doc.Links[0].From != "t1-c1" || doc.Links[0].To != "t2-c1" {
		t.Fatalf("link not resolved to ids: %+v", doc.Links[0])
	}
	// First generation: nothing is marked new.
	for _, topic := range doc.Topics {
		if topic.New {
			t.Fatal("first generation must not mark topics as new")
		}
		for _, child := range topic.Children {
			if child.New {
				t.Fatal("first generation must not mark concepts as new")
			}
		}
	}
}

func TestBuildConceptMapDocumentMarksNewAgainstPrevious(t *testing.T) {
	previous := &conceptMapDocument{
		Version: 1,
		Topics: []conceptMapTopic{
			{ID: "t1", Label: "语言学基础", Children: []conceptMapNode{
				{ID: "t1-c1", Label: "音位"},
			}},
		},
	}
	raw, err := parseGeneratedConceptMap(`{
		"topics": [
			{"label": "语言学基础", "children": [
				{"label": "音位"},
				{"label": "语素"}
			]}
		],
		"links": []
	}`)
	if err != nil {
		t.Fatal(err)
	}
	doc := buildConceptMapDocument(raw, testConceptMapSessions(), previous)
	topic := doc.Topics[0]
	if topic.New {
		t.Fatal("existing topic must not be marked new")
	}
	if topic.Children[0].New {
		t.Fatal("existing concept must not be marked new")
	}
	if !topic.Children[1].New {
		t.Fatal("added concept must be marked new")
	}
}

func TestBuildConceptMapDocumentEnforcesBounds(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`{"topics":[`)
	for topic := 0; topic < 20; topic++ {
		if topic > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"label":"主题` + string(rune('A'+topic)) +
			strings.Repeat("长", 100) + `","children":[`)
		for child := 0; child < 40; child++ {
			if child > 0 {
				builder.WriteString(",")
			}
			builder.WriteString(`{"label":"概念` + string(rune('A'+topic)) +
				string(rune('a'+child%26)) + strings.Repeat("x", child/26) + `"}`)
		}
		builder.WriteString(`]}`)
	}
	builder.WriteString(`],"links":[]}`)
	raw, err := parseGeneratedConceptMap(builder.String())
	if err != nil {
		t.Fatal(err)
	}
	doc := buildConceptMapDocument(raw, testConceptMapSessions(), nil)
	if len(doc.Topics) > conceptMapMaxTopics {
		t.Fatalf("topics = %d, want at most %d", len(doc.Topics), conceptMapMaxTopics)
	}
	total := 0
	for _, topic := range doc.Topics {
		if len([]rune(topic.Label)) > conceptMapMaxLabelRunes {
			t.Fatalf("label exceeds cap: %d runes", len([]rune(topic.Label)))
		}
		if len(topic.Children) > conceptMapMaxChildrenPer {
			t.Fatalf("children = %d, want at most %d", len(topic.Children), conceptMapMaxChildrenPer)
		}
		total += len(topic.Children)
	}
	if total > conceptMapMaxConcepts {
		t.Fatalf("total concepts = %d, want at most %d", total, conceptMapMaxConcepts)
	}
}

func TestTruncateConceptMapTextNeverSplitsRunes(t *testing.T) {
	text := strings.Repeat("知识地图", 100)
	truncatedText := truncateConceptMapText(text, 10)
	if truncatedText == "" {
		t.Fatal("expected a non-empty prefix")
	}
	if len(truncatedText) > 10 {
		t.Fatalf("byte length = %d, want at most 10", len(truncatedText))
	}
	if !strings.HasPrefix(text, truncatedText) {
		t.Fatal("truncation must return a clean prefix")
	}
	if truncateConceptMapText("abc", 0) != "" {
		t.Fatal("zero budget must return empty")
	}
	if truncateConceptMapText("abc", 10) != "abc" {
		t.Fatal("under-budget text must be unchanged")
	}
}

func TestParseStoredConceptMapRejectsForeignContent(t *testing.T) {
	if parseStoredConceptMap("# 这是一份 markdown 摘要") != nil {
		t.Fatal("markdown content must not parse as a concept map")
	}
	if parseStoredConceptMap(`{"version":0,"topics":[]}`) != nil {
		t.Fatal("empty/versionless documents must not parse")
	}
	doc := parseStoredConceptMap(
		`{"version":1,"topics":[{"id":"t1","label":"主题","children":[]}],"links":[]}`,
	)
	if doc == nil || doc.Topics[0].Label != "主题" {
		t.Fatalf("valid stored document failed to parse: %+v", doc)
	}
}

func TestConceptMapInstructionEmbedsPreviousSkeleton(t *testing.T) {
	withoutPrevious := conceptMapInstruction(nil)
	if strings.Contains(withoutPrevious, "上一版地图") {
		t.Fatal("first generation must not mention a previous map")
	}
	previous := &conceptMapDocument{
		Version: 1,
		Topics: []conceptMapTopic{
			{ID: "t1", Label: "语言学基础", Children: []conceptMapNode{
				{ID: "t1-c1", Label: "音位"},
			}},
		},
	}
	withPrevious := conceptMapInstruction(previous)
	if !strings.Contains(withPrevious, "语言学基础") ||
		!strings.Contains(withPrevious, "音位") {
		t.Fatal("previous labels must appear in the skeleton")
	}
}
