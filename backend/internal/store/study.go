package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/dreamtrans/backend/internal/models"
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
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3
	`, userID, projectID, skillKey).Scan(
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
			tenant_id, user_id, project_id, skill_key, skill_label, rubric, model
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (project_id, skill_key) DO NOTHING
	`, rubric.TenantID, rubric.UserID, rubric.ProjectID,
		rubric.SkillKey, rubric.SkillLabel, rubric.Rubric, rubric.Model)
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
				difficulty, content, model
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id
		`, scenario.TenantID, scenario.UserID, scenario.ProjectID,
			scenario.SkillKey, scenario.SkillLabel, scenario.Difficulty,
			scenario.Content, scenario.Model,
		).Scan(&scenario.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PickStudyScenario returns the least-used active scenario nearest the wanted
// difficulty, or nil when the bank is empty for this skill.
func (s *PostgresStore) PickStudyScenario(
	ctx context.Context, userID, projectID, skillKey string, difficulty int,
) (*models.StudyScenario, error) {
	var scenario models.StudyScenario
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, project_id, skill_key, skill_label,
		       difficulty, content, status, model, used_count, created_at, updated_at
		FROM study_scenarios
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3
		  AND status = 'active'
		ORDER BY ABS(difficulty - $4), used_count, RANDOM()
		LIMIT 1
	`, userID, projectID, skillKey, difficulty).Scan(
		&scenario.ID, &scenario.TenantID, &scenario.UserID, &scenario.ProjectID,
		&scenario.SkillKey, &scenario.SkillLabel, &scenario.Difficulty,
		&scenario.Content, &scenario.Status, &scenario.Model,
		&scenario.UsedCount, &scenario.CreatedAt, &scenario.UpdatedAt,
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
		       difficulty, content, status, model, used_count, created_at, updated_at
		FROM study_scenarios
		WHERE id = $1 AND user_id = $2 AND project_id = $3
	`, scenarioID, userID, projectID).Scan(
		&scenario.ID, &scenario.TenantID, &scenario.UserID, &scenario.ProjectID,
		&scenario.SkillKey, &scenario.SkillLabel, &scenario.Difficulty,
		&scenario.Content, &scenario.Status, &scenario.Model,
		&scenario.UsedCount, &scenario.CreatedAt, &scenario.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &scenario, nil
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
	return s.db.QueryRowContext(ctx, `
		INSERT INTO study_attempts (
			tenant_id, user_id, project_id, scenario_id, skill_key, answer,
			grade, feedback, next_step, bonuses, xp, used_hint, model,
			client_request_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, created_at
	`, attempt.TenantID, attempt.UserID, attempt.ProjectID, attempt.ScenarioID,
		attempt.SkillKey, attempt.Answer, attempt.Grade, attempt.Feedback,
		attempt.NextStep, attempt.Bonuses, attempt.XP, attempt.UsedHint,
		attempt.Model, attempt.ClientRequestID,
	).Scan(&attempt.ID, &attempt.CreatedAt)
}

// GetStudySkillState returns one learner×skill state, or nil before the
// first attempt.
func (s *PostgresStore) GetStudySkillState(
	ctx context.Context, userID, projectID, skillKey string,
) (*models.StudySkillState, error) {
	var state models.StudySkillState
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, project_id, skill_key, tenant_id, skill_label, level,
		       xp_total, attempts_count, clean_streak, last_grade, updated_at
		FROM study_skill_state
		WHERE user_id = $1 AND project_id = $2 AND skill_key = $3
	`, userID, projectID, skillKey).Scan(
		&state.UserID, &state.ProjectID, &state.SkillKey, &state.TenantID,
		&state.SkillLabel, &state.Level, &state.XPTotal, &state.AttemptsCount,
		&state.CleanStreak, &state.LastGrade, &state.UpdatedAt,
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
		       xp_total, attempts_count, clean_streak, last_grade, updated_at
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
			&state.CleanStreak, &state.LastGrade, &state.UpdatedAt,
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
			xp_total, attempts_count, clean_streak, last_grade
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (user_id, project_id, skill_key) DO UPDATE SET
			skill_label = excluded.skill_label,
			level = excluded.level,
			xp_total = excluded.xp_total,
			attempts_count = excluded.attempts_count,
			clean_streak = excluded.clean_streak,
			last_grade = excluded.last_grade
	`, state.UserID, state.ProjectID, state.SkillKey, state.TenantID,
		state.SkillLabel, state.Level, state.XPTotal, state.AttemptsCount,
		state.CleanStreak, state.LastGrade)
	return err
}
