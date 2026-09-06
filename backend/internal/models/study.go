package models

import (
	"encoding/json"
	"time"
)

// 学习模式 practice-loop rows (migration 027). Skills are keyed by normalized
// label (skill_key) so these survive skill-map regeneration.

// StudyRubric is the frozen F/P/C/D/HD standard for one skill. Grading always
// runs against the stored rubric so the same answer earns the same grade.
type StudyRubric struct {
	ID         string          `json:"id"`
	TenantID   string          `json:"tenant_id,omitempty"`
	UserID     string          `json:"user_id,omitempty"`
	ProjectID  string          `json:"project_id"`
	SkillKey   string          `json:"skill_key"`
	SkillLabel string          `json:"skill_label"`
	Rubric     json.RawMessage `json:"rubric"`
	Model      string          `json:"model,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// StudyLesson is the frozen 讲解卡 for one skill: a one-line rule, key terms,
// common misconceptions and a worked example. Generated once, like the rubric.
type StudyLesson struct {
	ID         string          `json:"id"`
	TenantID   string          `json:"tenant_id,omitempty"`
	UserID     string          `json:"user_id,omitempty"`
	ProjectID  string          `json:"project_id"`
	SkillKey   string          `json:"skill_key"`
	SkillLabel string          `json:"skill_label"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

// StudyScenario is one bank entry: a situation plus a judgment question, with
// an optional Chinese scaffold and one non-spoiling hint.
type StudyScenario struct {
	ContentVersion string          `json:"content_version,omitempty"`
	ID             string          `json:"id"`
	TenantID       string          `json:"tenant_id,omitempty"`
	UserID         string          `json:"user_id,omitempty"`
	ProjectID      string          `json:"project_id"`
	SkillKey       string          `json:"skill_key"`
	SkillLabel     string          `json:"skill_label"`
	Difficulty     int             `json:"difficulty"`
	Content        json.RawMessage `json:"content"`
	Status         string          `json:"status"`
	Model          string          `json:"model,omitempty"`
	UsedCount      int             `json:"used_count"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// StudyAttempt is one graded answer. A grade never appears alone: feedback
// names the gap, next_step names the exit.
type StudyAttempt struct {
	ID                string          `json:"id"`
	TenantID          string          `json:"tenant_id,omitempty"`
	UserID            string          `json:"user_id,omitempty"`
	ProjectID         string          `json:"project_id"`
	ScenarioID        *string         `json:"scenario_id,omitempty"`
	SkillKey          string          `json:"skill_key"`
	Answer            string          `json:"answer"`
	Grade             string          `json:"grade"`
	Feedback          string          `json:"feedback"`
	NextStep          string          `json:"next_step"`
	Bonuses           json.RawMessage `json:"bonuses"`
	XP                int             `json:"xp"`
	UsedHint          bool            `json:"used_hint"`
	UsedZH            bool            `json:"used_zh,omitempty"`
	PracticeSessionID string          `json:"practice_session_id,omitempty"`
	Combo             int             `json:"combo,omitempty"`
	Events            json.RawMessage `json:"events,omitempty"`
	ErrorPattern      string          `json:"error_pattern,omitempty"`
	Model             string          `json:"model,omitempty"`
	ClientRequestID   string          `json:"client_request_id,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
}

// StudySkillState is one learner's progression on one skill.
type StudySkillState struct {
	UserID           string    `json:"user_id,omitempty"`
	ProjectID        string    `json:"project_id"`
	SkillKey         string    `json:"skill_key"`
	TenantID         string    `json:"tenant_id,omitempty"`
	SkillLabel       string    `json:"skill_label"`
	Level            string    `json:"level"`
	XPTotal          int64     `json:"xp_total"`
	AttemptsCount    int       `json:"attempts_count"`
	CleanStreak      int       `json:"clean_streak"`
	LastGrade        string    `json:"last_grade,omitempty"`
	LastErrorPattern string    `json:"last_error_pattern,omitempty"`
	EnSuccessStreak  int       `json:"en_success_streak,omitempty"`
	LanguageSaves    int       `json:"language_saves,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}
