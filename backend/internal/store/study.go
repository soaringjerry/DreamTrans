package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/lib/pq"
)

// 学习模式 practice storage. Every query is scoped by the owning user (and
// project) so learners can never touch each other's rubrics, banks, or state.

// GetStudyRubric returns the frozen rubric for one skill, or nil.
func (s *PostgresStore) GetStudyRubric(
	ctx context.Context, userID, projectID, skillKey string,
) (*models.StudyRubric, error) {
	var rubric models.StudyRubric
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, skill_key, skill_label,
		       rubric, model, created_at, updated_at
		FROM study_rubrics
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3 AND content_version=$4
	`, userID, projectID, skillKey, StudyContentVersion(ctx)).Scan(
		&rubric.ID, &rubric.TenantID, &rubric.UserID, &rubric.ProjectID,
		&rubric.SkillKey, &rubric.SkillLabel, &rubric.Rubric, &rubric.Model,
		&rubric.CreatedAt, &rubric.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &rubric, nil
}

// CreateStudyRubric freezes a rubric. A concurrent writer wins silently; the
// caller re-reads so grading always uses the stored copy.
func (s *PostgresStore) CreateStudyRubric(
	ctx context.Context, rubric *models.StudyRubric,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO study_rubrics (
			tenant_id, user_id, project_id, skill_key, skill_label, rubric, model, content_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (project_id, skill_key, content_version) DO NOTHING
	`, rubric.TenantID, rubric.UserID, rubric.ProjectID,
		rubric.SkillKey, rubric.SkillLabel, rubric.Rubric, rubric.Model, StudyContentVersion(ctx))
	return err
}

// CreateStudyScenarios stores one generated bank batch.
func (s *PostgresStore) CreateStudyScenarios(
	ctx context.Context, scenarios []*models.StudyScenario,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, scenario := range scenarios {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO study_scenarios (
				tenant_id, user_id, project_id, skill_key, skill_label,
				difficulty, content, model, content_version
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id
		`, scenario.TenantID, scenario.UserID, scenario.ProjectID,
			scenario.SkillKey, scenario.SkillLabel, scenario.Difficulty,
			scenario.Content, scenario.Model, StudyContentVersion(ctx),
		).Scan(&scenario.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// CountStudyScenarios returns how many active bank items exist for a skill,
// optionally at one difficulty. difficulty <= 0 counts every difficulty.
func (s *PostgresStore) CountStudyScenarios(
	ctx context.Context, userID, projectID, skillKey string, difficulty int,
) (int, error) {
	query := `
		SELECT COUNT(*) FROM study_scenarios
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3 AND status = 'active' AND content_version=$4
	`
	args := []any{userID, projectID, skillKey, StudyContentVersion(ctx)}
	if difficulty > 0 {
		query += ` AND difficulty = $5`
		args = append(args, difficulty)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// MinStudyScenarioUses is the lowest used_count at a difficulty, or -1 if none.
func (s *PostgresStore) MinStudyScenarioUses(
	ctx context.Context, userID, projectID, skillKey string, difficulty int,
) (int, error) {
	var lowest sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT MIN(used_count) FROM study_scenarios
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3
		  AND status = 'active' AND difficulty = $4 AND content_version=$5
	`, userID, projectID, skillKey, difficulty, StudyContentVersion(ctx)).Scan(&lowest)
	if err != nil {
		return -1, err
	}
	if !lowest.Valid {
		return -1, nil
	}
	return int(lowest.Int64), nil
}

// PickStudyScenario returns the least-used active scenario at exactly the
// wanted difficulty, skipping excludeIDs. Unused items come first. Nil when
// nothing at that difficulty is left; the caller generates instead of
// serving an easier item.
func (s *PostgresStore) PickStudyScenario(
	ctx context.Context, userID, projectID, skillKey string, difficulty int,
	excludeIDs []string,
) (*models.StudyScenario, error) {
	var scenario models.StudyScenario
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, skill_key, skill_label,
		       difficulty, content, status, model, used_count, created_at, updated_at, content_version
		FROM study_scenarios
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3
		  AND status = 'active' AND difficulty = $4 AND content_version=$6
		  AND ($5::uuid[] IS NULL OR CARDINALITY($5::uuid[]) = 0 OR NOT (id = ANY($5::uuid[])))
		ORDER BY CASE WHEN used_count = 0 THEN 0 ELSE 1 END, used_count, RANDOM()
		LIMIT 1
	`, userID, projectID, skillKey, difficulty, pq.Array(excludeIDs), StudyContentVersion(ctx)).Scan(
		&scenario.ID, &scenario.TenantID, &scenario.UserID, &scenario.ProjectID,
		&scenario.SkillKey, &scenario.SkillLabel, &scenario.Difficulty,
		&scenario.Content, &scenario.Status, &scenario.Model,
		&scenario.UsedCount, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.ContentVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scenario, nil
}

// GetStudyScenario loads one scenario, verifying ownership.
func (s *PostgresStore) GetStudyScenario(
	ctx context.Context, userID, projectID, scenarioID string,
) (*models.StudyScenario, error) {
	var scenario models.StudyScenario
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, skill_key, skill_label,
		       difficulty, content, status, model, used_count, created_at, updated_at, content_version
		FROM study_scenarios
		WHERE id = $1 AND user_id = $2 AND project_id = $3
	`, scenarioID, userID, projectID).Scan(
		&scenario.ID, &scenario.TenantID, &scenario.UserID, &scenario.ProjectID,
		&scenario.SkillKey, &scenario.SkillLabel, &scenario.Difficulty,
		&scenario.Content, &scenario.Status, &scenario.Model,
		&scenario.UsedCount, &scenario.CreatedAt, &scenario.UpdatedAt, &scenario.ContentVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scenario, nil
}

// UpdateStudyScenarioContent rewrites one scenario's content, used to fill
// the teaching layer (explanation, model answer) into legacy bank items.
func (s *PostgresStore) UpdateStudyScenarioContent(
	ctx context.Context, userID, projectID, scenarioID string, content json.RawMessage,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE study_scenarios SET content = $4
		WHERE id = $1 AND user_id = $2 AND project_id = $3
	`, scenarioID, userID, projectID, content)
	return err
}

// GetStudyLesson returns the frozen lesson card for one skill, or nil.
func (s *PostgresStore) GetStudyLesson(
	ctx context.Context, userID, projectID, skillKey string,
) (*models.StudyLesson, error) {
	var lesson models.StudyLesson
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, skill_key, skill_label,
		       content, model, created_at
		FROM study_lessons
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3 AND content_version=$4
	`, userID, projectID, skillKey, StudyContentVersion(ctx)).Scan(
		&lesson.ID, &lesson.TenantID, &lesson.UserID, &lesson.ProjectID,
		&lesson.SkillKey, &lesson.SkillLabel, &lesson.Content, &lesson.Model,
		&lesson.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &lesson, nil
}

// CreateStudyLesson freezes a lesson card. Like rubrics, a concurrent writer
// wins silently and the caller re-reads the stored copy.
func (s *PostgresStore) CreateStudyLesson(
	ctx context.Context, lesson *models.StudyLesson,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO study_lessons (
			tenant_id, user_id, project_id, skill_key, skill_label, content, model, content_version
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (project_id, skill_key, content_version) DO NOTHING
	`, lesson.TenantID, lesson.UserID, lesson.ProjectID,
		lesson.SkillKey, lesson.SkillLabel, lesson.Content, lesson.Model, StudyContentVersion(ctx))
	return err
}

// GetLastScenarioAttempt returns the learner's latest attempt on one
// scenario, or nil. A pass right after a miss here is a self-correction.
func (s *PostgresStore) GetLastScenarioAttempt(
	ctx context.Context, userID, projectID, scenarioID string,
) (*models.StudyAttempt, error) {
	return s.scanStudyAttempt(s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, scenario_id, skill_key, answer,
		       grade, feedback, next_step, bonuses, xp, used_hint, model,
		       client_request_id, practice_session_id, used_zh, combo, events,
		       error_pattern, created_at
		FROM study_attempts
		WHERE user_id=$1 AND project_id=$2 AND scenario_id=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID, projectID, scenarioID))
}

// TouchStudyScenarioUse bumps a scenario's rotation counter.
func (s *PostgresStore) TouchStudyScenarioUse(
	ctx context.Context, scenarioID string,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE study_scenarios SET used_count = used_count + 1 WHERE id = $1
	`, scenarioID)
	return err
}

// CreateStudyAttempt records one graded answer.
func (s *PostgresStore) CreateStudyAttempt(
	ctx context.Context, attempt *models.StudyAttempt,
) error {
	if len(attempt.Events) == 0 {
		attempt.Events = []byte("[]")
	}
	return s.db.QueryRowContext(ctx, `
		INSERT INTO study_attempts (
			tenant_id, user_id, project_id, scenario_id, skill_key, answer,
			grade, feedback, next_step, bonuses, xp, used_hint, model,
			client_request_id, practice_session_id, used_zh, combo, events,
			error_pattern
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19)
		RETURNING id, created_at
	`, attempt.TenantID, attempt.UserID, attempt.ProjectID, attempt.ScenarioID,
		attempt.SkillKey, attempt.Answer, attempt.Grade, attempt.Feedback,
		attempt.NextStep, attempt.Bonuses, attempt.XP, attempt.UsedHint,
		attempt.Model, attempt.ClientRequestID, attempt.PracticeSessionID,
		attempt.UsedZH, attempt.Combo, attempt.Events, attempt.ErrorPattern,
	).Scan(&attempt.ID, &attempt.CreatedAt)
}

func (s *PostgresStore) CountScenarioAttempts(
	ctx context.Context, userID, projectID, scenarioID string,
) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM study_attempts
		WHERE user_id=$1 AND project_id=$2 AND scenario_id=$3
	`, userID, projectID, scenarioID).Scan(&count)
	return count, err
}

func (s *PostgresStore) GetLastStudyAttempt(
	ctx context.Context, userID, projectID, skillKey string,
) (*models.StudyAttempt, error) {
	return s.scanStudyAttempt(s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, scenario_id, skill_key, answer,
		       grade, feedback, next_step, bonuses, xp, used_hint, model,
		       client_request_id, practice_session_id, used_zh, combo, events,
		       error_pattern, created_at
		FROM study_attempts
		WHERE user_id=$1 AND project_id=$2 AND skill_key=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID, projectID, skillKey))
}

func (s *PostgresStore) GetLastPracticeSessionAttempt(
	ctx context.Context, userID, projectID, practiceSessionID string,
) (*models.StudyAttempt, error) {
	if strings.TrimSpace(practiceSessionID) == "" {
		return nil, nil
	}
	return s.scanStudyAttempt(s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, scenario_id, skill_key, answer,
		       grade, feedback, next_step, bonuses, xp, used_hint, model,
		       client_request_id, practice_session_id, used_zh, combo, events,
		       error_pattern, created_at
		FROM study_attempts
		WHERE user_id=$1 AND project_id=$2 AND practice_session_id=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID, projectID, practiceSessionID))
}

func (s *PostgresStore) ListPracticeSessionScenarioIDs(
	ctx context.Context, userID, projectID, practiceSessionID string,
) ([]string, error) {
	if strings.TrimSpace(practiceSessionID) == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT scenario_id::text FROM study_attempts
		WHERE user_id=$1 AND project_id=$2 AND practice_session_id=$3
		  AND scenario_id IS NOT NULL
	`, userID, projectID, practiceSessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *PostgresStore) scanStudyAttempt(row *sql.Row) (*models.StudyAttempt, error) {
	var attempt models.StudyAttempt
	err := row.Scan(
		&attempt.ID, &attempt.TenantID, &attempt.UserID, &attempt.ProjectID,
		&attempt.ScenarioID, &attempt.SkillKey, &attempt.Answer, &attempt.Grade,
		&attempt.Feedback, &attempt.NextStep, &attempt.Bonuses, &attempt.XP,
		&attempt.UsedHint, &attempt.Model, &attempt.ClientRequestID,
		&attempt.PracticeSessionID, &attempt.UsedZH, &attempt.Combo,
		&attempt.Events, &attempt.ErrorPattern, &attempt.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &attempt, nil
}

// GetStudySkillState returns one learner×skill state, or nil before the
// first attempt.
func (s *PostgresStore) GetStudySkillState(
	ctx context.Context, userID, projectID, skillKey string,
) (*models.StudySkillState, error) {
	var state models.StudySkillState
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, project_id, skill_key, tenant_id, skill_label, level,
		       xp_total, attempts_count, clean_streak, last_grade,
		       last_error_pattern, en_success_streak, language_saves, updated_at
		FROM study_skill_state
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3
	`, userID, projectID, skillKey).Scan(
		&state.UserID, &state.ProjectID, &state.SkillKey, &state.TenantID,
		&state.SkillLabel, &state.Level, &state.XPTotal, &state.AttemptsCount,
		&state.CleanStreak, &state.LastGrade, &state.LastErrorPattern,
		&state.EnSuccessStreak, &state.LanguageSaves, &state.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// ListStudySkillStates returns every skill state a learner holds in a project.
func (s *PostgresStore) ListStudySkillStates(
	ctx context.Context, userID, projectID string,
) ([]models.StudySkillState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, project_id, skill_key, tenant_id, skill_label, level,
		       xp_total, attempts_count, clean_streak, last_grade,
		       last_error_pattern, en_success_streak, language_saves, updated_at
		FROM study_skill_state
		WHERE user_id = $1 AND project_id = $2
	`, userID, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	states := make([]models.StudySkillState, 0)
	for rows.Next() {
		var state models.StudySkillState
		if err := rows.Scan(
			&state.UserID, &state.ProjectID, &state.SkillKey, &state.TenantID,
			&state.SkillLabel, &state.Level, &state.XPTotal, &state.AttemptsCount,
			&state.CleanStreak, &state.LastGrade, &state.LastErrorPattern,
			&state.EnSuccessStreak, &state.LanguageSaves, &state.UpdatedAt,
		); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// UpsertStudySkillState writes the post-attempt progression snapshot.
func (s *PostgresStore) UpsertStudySkillState(
	ctx context.Context, state *models.StudySkillState,
) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO study_skill_state (
			user_id, project_id, skill_key, tenant_id, skill_label, level,
			xp_total, attempts_count, clean_streak, last_grade,
			last_error_pattern, en_success_streak, language_saves
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (user_id, project_id, skill_key) DO UPDATE SET
			skill_label = excluded.skill_label,
			level = excluded.level,
			xp_total = excluded.xp_total,
			attempts_count = excluded.attempts_count,
			clean_streak = excluded.clean_streak,
			last_grade = excluded.last_grade,
			last_error_pattern = excluded.last_error_pattern,
			en_success_streak = excluded.en_success_streak,
			language_saves = excluded.language_saves
	`, state.UserID, state.ProjectID, state.SkillKey, state.TenantID,
		state.SkillLabel, state.Level, state.XPTotal, state.AttemptsCount,
		state.CleanStreak, state.LastGrade, state.LastErrorPattern,
		state.EnSuccessStreak, state.LanguageSaves)
	return err
}
