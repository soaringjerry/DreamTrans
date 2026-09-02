package handlers

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

func TestStudyWeekFromText(t *testing.T) {
	cases := map[string]int{
		"Week 6":              6,
		"week06 slides.pdf":   6,
		"Wk 3 - Correlation":  3,
		"第 4 周 讲义":            4,
		"W02":                 2,
		"Lecture 7 recording": 7,
		"Lec-09":              9,
		"Assessment guide":    0,
		"Week 99":             0,
		"":                    0,
		"Weekly reflection":   0,
		"2 March - 8 March":   0,
		"week 2024 notes":     0,
		"week04_reading.pdf":  4,
	}
	for text, want := range cases {
		if got := studyWeekFromText(text); got != want {
			t.Fatalf("weekFromText(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestStudyWeekFromDateAndMonday(t *testing.T) {
	monday := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	if got := studyMondayOf(time.Date(2026, 3, 5, 13, 0, 0, 0, time.UTC)); !got.Equal(monday) {
		t.Fatalf("monday of Thursday = %v", got)
	}
	if got := studyMondayOf(time.Date(2026, 3, 8, 23, 0, 0, 0, time.UTC)); !got.Equal(monday) {
		t.Fatalf("monday of Sunday = %v", got)
	}
	if studyWeekFromDate(monday, monday) != 1 || studyWeekFromDate(monday.AddDate(0, 0, 6), monday) != 1 {
		t.Fatal("first seven days are week 1")
	}
	if studyWeekFromDate(monday.AddDate(0, 0, 7), monday) != 2 {
		t.Fatal("day 8 is week 2")
	}
	if studyWeekFromDate(monday.AddDate(0, 0, -1), monday) != 0 {
		t.Fatal("before week 1 is unassigned")
	}
	if studyWeekFromDate(monday, time.Time{}) != 0 {
		t.Fatal("no week start means no date weeks")
	}
}

func TestBuildStudyWeeksGroupsAndFocuses(t *testing.T) {
	weekStart := time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)
	now := weekStart.AddDate(0, 0, 7*3+2) // Wednesday of week 4
	lms := map[string]store.LMSSourceRef{
		"src-w2":   {ID: "src-w2", LMS: json.RawMessage(`{"section":"Week 2","timemodified":0}`)},
		"src-date": {ID: "src-date", LMS: json.RawMessage(`{"section":"Topic","timemodified":` + itoa(weekStart.AddDate(0, 0, 8).Unix()) + `}`)},
	}
	in := studyWeekInputs{
		weekStart: weekStart, inferred: true, now: now,
		sessions: []store.ProjectSessionRef{
			{ID: "s1", Title: "Lecture 1", StartedAt: weekStart.AddDate(0, 0, 1)},
			{ID: "s3", Title: "Tuesday class", StartedAt: weekStart.AddDate(0, 0, 15)},
			{ID: "s-old", Title: "Orientation", StartedAt: weekStart.AddDate(0, 0, -10)},
		},
		sources: []models.KnowledgeSource{
			{ID: "src-w2", Name: "Week 2 · slides.pdf", SourceType: "lms", Status: "ready"},
			{ID: "src-date", Name: "Topic · notes.pdf", SourceType: "lms", Status: "ready"},
			{ID: "src-up", Name: "week04_reading.pdf", SourceType: "file", Status: "ready"},
			{ID: "src-x", Name: "syllabus.pdf", SourceType: "file", Status: "ready"},
			{ID: "src-q", Name: "Week 1 queued.pdf", SourceType: "file", Status: "queued"},
		},
		lmsBySource: lms,
		skills: []skillMapSkill{
			{ID: "k1", Label: "相关", Evidence: []skillMapEvidence{{SessionID: "s1", Quote: "q"}}},
			{ID: "k2", Label: "混淆", Evidence: []skillMapEvidence{{SourceID: "src-w2", Quote: "q"}, {SessionID: "s3", Quote: "q"}}},
			{ID: "k3", Label: "设计", Evidence: []skillMapEvidence{{SessionID: "s3", Quote: "q"}}},
			{ID: "k4", Label: "无证据"},
		},
		states: map[string]models.StudySkillState{
			"相关": {SkillKey: "相关", Level: "mastered", XPTotal: 900},
		},
	}
	got := buildStudyWeeks(&in)
	if got.CurrentWeek != 4 || got.WeekStart != "2026-03-02" || !got.WeekStartInferred {
		t.Fatalf("header: %+v", got)
	}
	if len(got.Weeks) != 4 {
		t.Fatalf("weeks = %d, want 4", len(got.Weeks))
	}
	w1, w2, w3, w4 := got.Weeks[0], got.Weeks[1], got.Weeks[2], got.Weeks[3]
	if len(w1.Sessions) != 1 || len(w1.Skills) != 1 || w1.Status != "done" {
		t.Fatalf("week 1: %+v", w1)
	}
	if len(w2.Sources) != 2 || len(w2.Skills) != 1 || w2.Skills[0].Label != "混淆" || w2.Status != "behind" {
		t.Fatalf("week 2 should hold the section-named and date-assigned sources and be owed: %+v", w2)
	}
	if len(w3.Sessions) != 1 || len(w3.Skills) != 1 || w3.Status != "behind" {
		t.Fatalf("week 3: %+v", w3)
	}
	if len(w4.Sources) != 1 || w4.Status != "current" {
		t.Fatalf("week 4: %+v", w4)
	}
	if len(got.BehindWeeks) != 2 || got.BehindWeeks[0] != 2 {
		t.Fatalf("behind = %v", got.BehindWeeks)
	}
	if got.Focus == nil || got.Focus.Week != 2 || got.Focus.SkillLabel != "混淆" {
		t.Fatalf("focus should be the earliest owed week: %+v", got.Focus)
	}
	if len(got.Unassigned.Sessions) != 1 || len(got.Unassigned.Sources) != 1 || len(got.Unassigned.Skills) != 1 {
		t.Fatalf("unassigned: %+v", got.Unassigned)
	}
}

func TestBuildStudyWeeksWithoutDates(t *testing.T) {
	got := buildStudyWeeks(&studyWeekInputs{
		now:     time.Now(),
		sources: []models.KnowledgeSource{{ID: "a", Name: "Week 3 slides", SourceType: "file", Status: "ready"}},
		states:  map[string]models.StudySkillState{},
	})
	if got.CurrentWeek != 0 || len(got.Weeks) != 3 || got.Weeks[2].Status != "current" && got.Weeks[2].Status != "behind" && got.Weeks[2].Status != "done" {
		t.Fatalf("named weeks still group without a calendar: %+v", got)
	}
	if got.Focus == nil || got.Focus.Week != 3 {
		t.Fatalf("focus falls back to the latest named week: %+v", got.Focus)
	}
}

func TestResolveStudyWeekStart(t *testing.T) {
	set := "2026-03-09"
	if start, inferred := resolveStudyWeekStart(&models.AIProject{WeekStart: &set}, nil, nil); inferred || start.Format("2006-01-02") != set {
		t.Fatalf("explicit week start wins: %v %v", start, inferred)
	}
	sessions := []store.ProjectSessionRef{{ID: "a", StartedAt: time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC)}}
	if start, inferred := resolveStudyWeekStart(&models.AIProject{}, sessions, nil); !inferred || start.Format("2006-01-02") != "2026-03-02" {
		t.Fatalf("inferred from earliest session: %v %v", start, inferred)
	}
	if start, inferred := resolveStudyWeekStart(&models.AIProject{}, nil, nil); !inferred || !start.IsZero() {
		t.Fatalf("nothing to infer from: %v %v", start, inferred)
	}
}

func itoa(value int64) string {
	return strconv.FormatInt(value, 10)
}
