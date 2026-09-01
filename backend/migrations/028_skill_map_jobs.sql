-- Background skill-map generation. The HTTP handler only enqueues; workers
-- read every linked lecture, chunk without truncation, and persist the artifact.

CREATE TABLE IF NOT EXISTS skill_map_jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  status VARCHAR(20) NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'processing', 'ready', 'error', 'cancelled')),
  model VARCHAR(200) NOT NULL DEFAULT '',
  reasoning_effort VARCHAR(16) NOT NULL DEFAULT 'low'
    CHECK (reasoning_effort IN ('low', 'medium', 'high')),
  request_hash CHAR(64) NOT NULL
    CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  client_request_id VARCHAR(128) NOT NULL DEFAULT '',
  chunk_count INT NOT NULL DEFAULT 0 CHECK (chunk_count >= 0),
  processed_chunks INT NOT NULL DEFAULT 0
    CHECK (processed_chunks >= 0),
  error_message TEXT NOT NULL DEFAULT '',
  lease_owner VARCHAR(200) NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMP WITH TIME ZONE,
  attempt_count INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  max_attempts INT NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 20),
  started_at TIMESTAMP WITH TIME ZONE,
  finished_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_map_jobs_client_request
  ON skill_map_jobs(tenant_id, user_id, client_request_id)
  WHERE client_request_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_map_jobs_one_active
  ON skill_map_jobs(project_id)
  WHERE status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_skill_map_jobs_claim
  ON skill_map_jobs(status, lease_expires_at, created_at);

DROP TRIGGER IF EXISTS update_skill_map_jobs_updated_at ON skill_map_jobs;
CREATE TRIGGER update_skill_map_jobs_updated_at BEFORE UPDATE ON skill_map_jobs
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
