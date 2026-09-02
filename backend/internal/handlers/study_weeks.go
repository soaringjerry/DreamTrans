package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/dreamtrans/backend/internal/store"
)

// 按周学习: GET /api/ai/projects/{id}/study/weeks groups a course's sessions,
// materials and skills by teaching week and says where the learner stands.
// Assignment is automatic: a week number in a Moodle section or file name
// wins; otherwise the date falls into the week counted from week_start
// (set on the course, or inferred as the Monday of the earliest session).

const studyMaxWeeks = 24

type studyWeekSession struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	StartedAt time.Time `json:"started_at"`
}

type studyWeekSource struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SourceType string `json:"source_type"`
	Section    string `json:"section,omitempty"`
}

type studyWeekSkill struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Level   string `json:"level"`
	XPTotal int64  `json:"xp_total"`
}

type studyWeek struct {
	Week     int                `json:"week"`
	Label    string             `json:"label"`
	Start    string             `json:"start,omitempty"`
	End      string             `json:"end,omitempty"`
	Status   string             `json:"status"` // done | current | behind | upcoming | empty
	Sessions []studyWeekSession `json:"sessions"`
	Sources  []studyWeekSource  `json:"sources"`
	Skills   []studyWeekSkill   `json:"skills"`
}

type studyWeekFocus struct {
	Week       int    `json:"week"`
	SkillLabel string `json:"skill_label,omitempty"`
	Reason     string `json:"reason"`
}

type studyWeeksResponse struct {
	WeekStart         string          `json:"week_start,omitempty"`
	WeekStartInferred bool            `json:"week_start_inferred"`
	CurrentWeek       int             `json:"current_week"`
	Weeks             []studyWeek     `json:"weeks"`
	BehindWeeks       []int           `json:"behind_weeks"`
	Focus             *studyWeekFocus `json:"focus,omitempty"`
	Unassigned        studyWeekBucket `json:"unassigned"`
}

type studyWeekBucket struct {
	Sessions []studyWeekSession `json:"sessions"`
	Sources  []studyWeekSource  `json:"sources"`
	Skills   []studyWeekSkill   `json:"skills"`
}

// Digits must not continue ("week04_reading" is week 4, "week 2024" is not).
var studyWeekPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:week|wk)\s*[-_ ]?0?(\d{1,2})(?:\D|$)`),
	regexp.MustCompile(`第\s*0?(\d{1,2})\s*周`),
	regexp.MustCompile(`(?i)\bw\s*0?(\d{1,2})(?:\D|$)`),
	regexp.MustCompile(`(?i)\b(?:lecture|lec)\s*[-_ ]?0?(\d{1,2})(?:\D|$)`),
}

// studyWeekFromText finds a teaching-week number in a section or file name.
func studyWeekFromText(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	for _, pattern := range studyWeekPatterns {
		if match := pattern.FindStringSubmatch(text); match != nil {
			week, err := strconv.Atoi(match[1])
			if err == nil && week >= 1 && week <= studyMaxWeeks {
				return week
			}
		}
	}
	return 0
}

// studyWeekFromDate counts weeks from the Monday of week 1.
func studyWeekFromDate(at, weekStart time.Time) int {
	if weekStart.IsZero() || at.IsZero() {
		return 0
	}
	days := int(at.Sub(weekStart).Hours() / 24)
	if days < 0 {
		return 0
	}
	week := days/7 + 1
	if week > studyMaxWeeks {
		return 0
	}
	return week
}

// studyMondayOf returns the Monday (00:00 UTC) of the week containing t.
func studyMondayOf(t time.Time) time.Time {
	t = t.UTC()
	offset := (int(t.Weekday()) + 6) % 7
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return day.AddDate(0, 0, -offset)
}

type studyWeekInputs struct {
	weekStart   time.Time
	inferred    bool
	now         time.Time
	sessions    []store.ProjectSessionRef
	sources     []models.KnowledgeSource
	lmsBySource map[string]store.LMSSourceRef
	skills      []skillMapSkill
	states      map[string]models.StudySkillState
}

func studyWeekLabel(week int) string {
	return fmt.Sprintf("第 %d 周", week)
}

// buildStudyWeeks is pure: everything it needs is in inputs.
func buildStudyWeeks(in *studyWeekInputs) studyWeeksResponse {
	response := studyWeeksResponse{
		WeekStartInferred: in.inferred,
		Weeks:             make([]studyWeek, 0),
		BehindWeeks:       make([]int, 0),
		Unassigned: studyWeekBucket{
			Sessions: make([]studyWeekSession, 0), Sources: make([]studyWeekSource, 0), Skills: make([]studyWeekSkill, 0),
		},
	}
	if !in.weekStart.IsZero() {
		response.WeekStart = in.weekStart.Format("2006-01-02")
	}
	byWeek := map[int]*studyWeek{}
	week := func(n int) *studyWeek {
		if existing := byWeek[n]; existing != nil {
			return existing
		}
		created := &studyWeek{
			Week: n, Label: studyWeekLabel(n),
			Sessions: make([]studyWeekSession, 0), Sources: make([]studyWeekSource, 0), Skills: make([]studyWeekSkill, 0),
		}
		if !in.weekStart.IsZero() {
			start := in.weekStart.AddDate(0, 0, (n-1)*7)
			created.Start = start.Format("2006-01-02")
			created.End = start.AddDate(0, 0, 6).Format("2006-01-02")
		}
		byWeek[n] = created
		return created
	}

	sessionWeek := map[string]int{}
	for _, session := range in.sessions {
		n := studyWeekFromText(session.Title)
		if n == 0 {
			n = studyWeekFromDate(session.StartedAt, in.weekStart)
		}
		entry := studyWeekSession{ID: session.ID, Title: session.Title, StartedAt: session.StartedAt}
		if n == 0 {
			response.Unassigned.Sessions = append(response.Unassigned.Sessions, entry)
			continue
		}
		sessionWeek[session.ID] = n
		week(n).Sessions = append(week(n).Sessions, entry)
	}

	sourceWeek := map[string]int{}
	for index := range in.sources {
		source := &in.sources[index]
		if source.Status != "ready" {
			continue
		}
		entry := studyWeekSource{ID: source.ID, Name: source.Name, SourceType: source.SourceType}
		n := 0
		if ref, ok := in.lmsBySource[source.ID]; ok {
			var lms struct {
				Section      string `json:"section"`
				TimeModified int64  `json:"timemodified"`
			}
			_ = json.Unmarshal(ref.LMS, &lms)
			entry.Section = lms.Section
			n = studyWeekFromText(lms.Section)
			if n == 0 && lms.TimeModified > 0 {
				n = studyWeekFromDate(time.Unix(lms.TimeModified, 0).UTC(), in.weekStart)
			}
		}
		if n == 0 {
			n = studyWeekFromText(source.Name)
		}
		if n == 0 {
			response.Unassigned.Sources = append(response.Unassigned.Sources, entry)
			continue
		}
		sourceWeek[source.ID] = n
		week(n).Sources = append(week(n).Sources, entry)
	}

	for _, skill := range in.skills {
		state, known := in.states[skillMapLabelKey(skill.Label)]
		entry := studyWeekSkill{ID: skill.ID, Label: skill.Label, Level: "unlit"}
		if known {
			entry.Level = state.Level
			entry.XPTotal = state.XPTotal
		}
		n := 0
		for _, evidence := range skill.Evidence {
			candidate := 0
			if evidence.SessionID != "" {
				candidate = sessionWeek[evidence.SessionID]
			} else if evidence.SourceID != "" {
				candidate = sourceWeek[evidence.SourceID]
			}
			if candidate > 0 && (n == 0 || candidate < n) {
				n = candidate
			}
		}
		if n == 0 {
			response.Unassigned.Skills = append(response.Unassigned.Skills, entry)
			continue
		}
		week(n).Skills = append(week(n).Skills, entry)
	}

	current := studyWeekFromDate(in.now, in.weekStart)
	maxWeek := current
	for n := range byWeek {
		if n > maxWeek {
			maxWeek = n
		}
	}
	if maxWeek == 0 {
		return response
	}
	response.CurrentWeek = current
	for n := 1; n <= maxWeek; n++ {
		entry := week(n)
		entry.Status = studyWeekStatus(entry, n, current)
		if entry.Status == "behind" {
			response.BehindWeeks = append(response.BehindWeeks, n)
		}
		response.Weeks = append(response.Weeks, *entry)
	}
	response.Focus = studyWeekFocusFor(response.Weeks, current)
	return response
}

func studyWeekHeld(level string) bool {
	return studyLevelRank(level) >= studyLevelRank("supervised")
}

func studyWeekStatus(entry *studyWeek, n, current int) string {
	empty := len(entry.Sessions) == 0 && len(entry.Sources) == 0 && len(entry.Skills) == 0
	switch {
	case current > 0 && n > current:
		return "upcoming"
	case empty:
		return "empty"
	case current > 0 && n == current:
		return "current"
	}
	for _, skill := range entry.Skills {
		if !studyWeekHeld(skill.Level) {
			return "behind"
		}
	}
	if len(entry.Skills) == 0 {
		// Materials but no skills yet: nothing to practice, nothing to owe.
		return "done"
	}
	return "done"
}

// studyWeekFocusFor picks where to start: the earliest week still owed,
// otherwise this week. The wording never scolds.
func studyWeekFocusFor(weeks []studyWeek, current int) *studyWeekFocus {
	firstOpen := func(entry *studyWeek) string {
		for _, skill := range entry.Skills {
			if skill.Level != "mastered" && !studyWeekHeld(skill.Level) {
				return skill.Label
			}
		}
		for _, skill := range entry.Skills {
			if skill.Level != "mastered" {
				return skill.Label
			}
		}
		return ""
	}
	for index := range weeks {
		entry := &weeks[index]
		if entry.Status == "behind" {
			label := firstOpen(entry)
			return &studyWeekFocus{
				Week: entry.Week, SkillLabel: label,
				Reason: fmt.Sprintf("第 %d 周的内容还没练熟，先从这里补，补上就能跟上本周。", entry.Week),
			}
		}
	}
	for index := range weeks {
		entry := &weeks[index]
		if entry.Status == "current" {
			label := firstOpen(entry)
			if label == "" && len(entry.Skills) == 0 {
				return &studyWeekFocus{Week: entry.Week, Reason: "本周的材料还没进技能路线，加好课堂或资料后重新生成路线。"}
			}
			return &studyWeekFocus{Week: entry.Week, SkillLabel: label, Reason: "这是本周的内容，跟上节奏就好。"}
		}
	}
	if current == 0 && len(weeks) > 0 {
		last := &weeks[len(weeks)-1]
		return &studyWeekFocus{Week: last.Week, SkillLabel: firstOpen(last), Reason: "从最新一周开始。"}
	}
	return nil
}

func (h *RAGHandler) handleStudyWeeks(
	w http.ResponseWriter, r *http.Request, project *models.AIProject,
) {
	sessions, err := h.store.ListProjectSessionRefs(r.Context(), project.TenantID, project.UserID, project.ID, 0)
	if err != nil {
		http.Error(w, "failed to load course sessions", http.StatusInternalServerError)
		return
	}
	sources, err := h.store.ListKnowledgeSources(r.Context(), project.ID, project.UserID)
	if err != nil {
		http.Error(w, "failed to load course materials", http.StatusInternalServerError)
		return
	}
	lmsRefs, err := h.store.ListLMSSources(r.Context(), project.ID, project.UserID)
	if err != nil {
		http.Error(w, "failed to load synced materials", http.StatusInternalServerError)
		return
	}
	lmsBySource := make(map[string]store.LMSSourceRef, len(lmsRefs))
	for _, ref := range lmsRefs {
		lmsBySource[ref.ID] = ref
	}
	states, err := h.store.ListStudySkillStates(r.Context(), project.UserID, project.ID)
	if err != nil {
		http.Error(w, "failed to load study state", http.StatusInternalServerError)
		return
	}
	stateByKey := make(map[string]models.StudySkillState, len(states))
	for index := range states {
		stateByKey[states[index].SkillKey] = states[index]
	}
	var skills []skillMapSkill
	if artifact, artErr := h.store.GetLatestAIArtifactByProject(
		r.Context(), project.UserID, project.ID, skillMapArtifactType,
	); artErr == nil && artifact != nil {
		if doc := parseStoredSkillMap(artifact.Content); doc != nil {
			skills = doc.Skills
		}
	}
	weekStart, inferred := resolveStudyWeekStart(project, sessions, lmsRefs)
	WriteJSON(w, buildStudyWeeks(&studyWeekInputs{
		weekStart: weekStart, inferred: inferred, now: time.Now().UTC(),
		sessions: sessions, sources: sources, lmsBySource: lmsBySource,
		skills: skills, states: stateByKey,
	}))
}

// resolveStudyWeekStart uses the course setting, else the Monday of the
// earliest session (or synced material) as week 1.
func resolveStudyWeekStart(
	project *models.AIProject, sessions []store.ProjectSessionRef, lms []store.LMSSourceRef,
) (time.Time, bool) {
	if project != nil && project.WeekStart != nil {
		if parsed, err := time.Parse("2006-01-02", *project.WeekStart); err == nil {
			return parsed.UTC(), false
		}
	}
	var earliest time.Time
	for _, session := range sessions {
		if !session.StartedAt.IsZero() && (earliest.IsZero() || session.StartedAt.Before(earliest)) {
			earliest = session.StartedAt
		}
	}
	if earliest.IsZero() {
		times := make([]int64, 0, len(lms))
		for _, ref := range lms {
			var meta struct {
				TimeModified int64 `json:"timemodified"`
			}
			if json.Unmarshal(ref.LMS, &meta) == nil && meta.TimeModified > 0 {
				times = append(times, meta.TimeModified)
			}
		}
		if len(times) > 0 {
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			earliest = time.Unix(times[0], 0).UTC()
		}
	}
	if earliest.IsZero() {
		return time.Time{}, true
	}
	return studyMondayOf(earliest), true
}
