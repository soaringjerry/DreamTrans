-- 学习模式计费可见性: every AI charge records which feature produced it and
-- which course (AI project) it belongs to, so the study page can show what a
-- course has cost and each practice step can show its own price.

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS feature VARCHAR(40) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS project_id UUID REFERENCES ai_projects(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_usage_logs_project
  ON usage_logs(user_id, project_id, created_at DESC)
  WHERE project_id IS NOT NULL;

-- What one skill-map generation actually cost, shown once the job is ready.
ALTER TABLE skill_map_jobs
  ADD COLUMN IF NOT EXISTS cost_usd NUMERIC(14,6) NOT NULL DEFAULT 0
    CHECK (cost_usd >= 0);
