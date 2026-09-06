package store

import (
	"context"
	"encoding/json"
	"time"
)

// StudyHistoryEntry joins a saved answer to its original scenario, including
// older content versions. Reading history never generates teaching material.
type StudyHistoryEntry struct {
	ID         string          `json:"id"`
	SkillLabel string          `json:"skill_label"`
	Answer     string          `json:"answer"`
	Grade      string          `json:"grade"`
	Feedback   string          `json:"feedback"`
	NextStep   string          `json:"next_step"`
	XP         int             `json:"xp"`
	CreatedAt  time.Time       `json:"created_at"`
	Scenario   json.RawMessage `json:"scenario"`
}

// ListStudyHistory uses an owner-scoped cursor and stable ordering. A missing
// scenario leaves the answer visible rather than dropping the attempt.
func (s *PostgresStore) ListStudyHistory(ctx context.Context, userID, projectID, before string, limit int) ([]StudyHistoryEntry, error) {
	if limit < 1 || limit > 51 {
		limit = 51
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, COALESCE(s.skill_label, a.skill_key), a.answer, a.grade,
		       a.feedback, a.next_step, a.xp, a.created_at, COALESCE(s.content, 'null'::jsonb)
		FROM study_attempts a
		LEFT JOIN study_scenarios s ON s.id=a.scenario_id
		  AND s.user_id=a.user_id AND s.project_id=a.project_id AND s.tenant_id=a.tenant_id
		WHERE a.user_id=$1 AND a.project_id=$2
		  AND ($3='' OR (a.created_at, a.id) < (
		    SELECT c.created_at, c.id FROM study_attempts c
		    WHERE c.id=NULLIF($3, '')::uuid AND c.user_id=$1 AND c.project_id=$2
		  ))
		ORDER BY a.created_at DESC, a.id DESC LIMIT $4
	`, userID, projectID, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := make([]StudyHistoryEntry, 0, limit)
	for rows.Next() {
		var entry StudyHistoryEntry
		if err := rows.Scan(&entry.ID, &entry.SkillLabel, &entry.Answer, &entry.Grade,
			&entry.Feedback, &entry.NextStep, &entry.XP, &entry.CreatedAt, &entry.Scenario); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
