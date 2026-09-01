-- 学习模式 M3: adaptive instructor — real combo accounting, per-scenario
-- first try, language-vs-domain scaffolding, and practice-session identity.

ALTER TABLE study_attempts
  ADD COLUMN IF NOT EXISTS practice_session_id VARCHAR(128) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS used_zh BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS combo INT NOT NULL DEFAULT 0 CHECK (combo >= 0),
  ADD COLUMN IF NOT EXISTS events JSONB NOT NULL DEFAULT '[]'::jsonb,
  ADD COLUMN IF NOT EXISTS error_pattern TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_study_attempts_practice_session
  ON study_attempts(user_id, project_id, practice_session_id, created_at DESC)
  WHERE practice_session_id <> '';

CREATE INDEX IF NOT EXISTS idx_study_attempts_scenario
  ON study_attempts(user_id, project_id, scenario_id);

ALTER TABLE study_skill_state
  ADD COLUMN IF NOT EXISTS last_error_pattern TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS en_success_streak INT NOT NULL DEFAULT 0
    CHECK (en_success_streak >= 0),
  ADD COLUMN IF NOT EXISTS language_saves INT NOT NULL DEFAULT 0
    CHECK (language_saves >= 0);
