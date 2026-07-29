-- Bind asynchronous Speechmatics jobs to their submitting DreamTrans user so
-- job IDs cannot be used to read another tenant's transcript.
CREATE TABLE IF NOT EXISTS batch_transcription_jobs (
  job_id VARCHAR(200) PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_batch_jobs_user
  ON batch_transcription_jobs(user_id, created_at DESC);
