-- 学习模式教学闭环: every skill gets one frozen lesson card (rule, key terms,
-- misconceptions, a worked example) generated before the first practice.
-- Scenario teaching fields (explanation, model_answer, targets…) live inside
-- study_scenarios.content and are filled lazily for legacy items.

CREATE TABLE IF NOT EXISTS study_lessons (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  skill_key VARCHAR(160) NOT NULL,
  skill_label VARCHAR(160) NOT NULL,
  -- {"rule":…,"concepts":[…],"misconceptions":[…],"example":{…}}
  content JSONB NOT NULL,
  model VARCHAR(200) NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (project_id, skill_key)
);

-- Retry detection reads the latest attempt per scenario.
CREATE INDEX IF NOT EXISTS idx_study_attempts_scenario_latest
  ON study_attempts(user_id, project_id, scenario_id, created_at DESC);
