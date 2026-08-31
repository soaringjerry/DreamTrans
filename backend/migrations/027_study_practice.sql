-- 学习模式 M2: the practice loop (Situation → Answer → Grade → Feedback).
-- Skills are keyed by normalized label (skill_key) so rubrics, scenarios,
-- attempts, and learner state survive skill-map regeneration. Rubrics are
-- generated once per skill and frozen; grading always runs against the
-- stored rubric, never a fresh one (see handlers/study.go).

CREATE TABLE IF NOT EXISTS study_rubrics (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  skill_key VARCHAR(160) NOT NULL,
  skill_label VARCHAR(160) NOT NULL,
  -- {"levels":{"F":{"description":...,"anchor":...},...,"HD":{...}}}
  rubric JSONB NOT NULL,
  model VARCHAR(200) NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (project_id, skill_key)
);

CREATE TABLE IF NOT EXISTS study_scenarios (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  skill_key VARCHAR(160) NOT NULL,
  skill_label VARCHAR(160) NOT NULL,
  difficulty SMALLINT NOT NULL DEFAULT 1 CHECK (difficulty BETWEEN 1 AND 3),
  -- {"situation":...,"question":...,"question_zh":...,"hint":...}
  content JSONB NOT NULL,
  status VARCHAR(16) NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'retired')),
  model VARCHAR(200) NOT NULL DEFAULT '',
  used_count INT NOT NULL DEFAULT 0 CHECK (used_count >= 0),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_study_scenarios_pick
  ON study_scenarios(project_id, skill_key, status, difficulty, used_count);

CREATE TABLE IF NOT EXISTS study_attempts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  scenario_id UUID REFERENCES study_scenarios(id) ON DELETE SET NULL,
  skill_key VARCHAR(160) NOT NULL,
  answer TEXT NOT NULL,
  grade VARCHAR(2) NOT NULL CHECK (grade IN ('F', 'P', 'C', 'D', 'HD')),
  -- A grade never appears alone: feedback names the gap, next_step the exit.
  feedback TEXT NOT NULL DEFAULT '',
  next_step TEXT NOT NULL DEFAULT '',
  bonuses JSONB NOT NULL DEFAULT '[]'::jsonb,
  xp INT NOT NULL DEFAULT 0 CHECK (xp >= 0),
  used_hint BOOLEAN NOT NULL DEFAULT FALSE,
  model VARCHAR(200) NOT NULL DEFAULT '',
  client_request_id VARCHAR(128) NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_study_attempts_user_skill
  ON study_attempts(user_id, project_id, skill_key, created_at DESC);

CREATE TABLE IF NOT EXISTS study_skill_state (
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  skill_key VARCHAR(160) NOT NULL,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  skill_label VARCHAR(160) NOT NULL DEFAULT '',
  level VARCHAR(16) NOT NULL DEFAULT 'learner'
    CHECK (level IN ('learner', 'supervised', 'hazard', 'independent', 'mastered')),
  xp_total BIGINT NOT NULL DEFAULT 0 CHECK (xp_total >= 0),
  attempts_count INT NOT NULL DEFAULT 0 CHECK (attempts_count >= 0),
  -- Consecutive hint-free attempts at C or above; level-ups spend it.
  clean_streak INT NOT NULL DEFAULT 0 CHECK (clean_streak >= 0),
  last_grade VARCHAR(2) NOT NULL DEFAULT '',
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  PRIMARY KEY (user_id, project_id, skill_key)
);

DROP TRIGGER IF EXISTS update_study_rubrics_updated_at ON study_rubrics;
CREATE TRIGGER update_study_rubrics_updated_at BEFORE UPDATE ON study_rubrics
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_study_scenarios_updated_at ON study_scenarios;
CREATE TRIGGER update_study_scenarios_updated_at BEFORE UPDATE ON study_scenarios
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_study_skill_state_updated_at ON study_skill_state;
CREATE TRIGGER update_study_skill_state_updated_at BEFORE UPDATE ON study_skill_state
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
