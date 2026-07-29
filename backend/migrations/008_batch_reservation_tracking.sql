-- Keep the initial billing reservation attached to its asynchronous provider
-- job. This makes terminal-failure refunds possible after a process restart.
ALTER TABLE batch_transcription_jobs
  ADD COLUMN IF NOT EXISTS reservation_key VARCHAR(255),
  ADD COLUMN IF NOT EXISTS completed_at TIMESTAMP WITH TIME ZONE;

CREATE UNIQUE INDEX IF NOT EXISTS idx_batch_jobs_reservation_key
  ON batch_transcription_jobs(reservation_key)
  WHERE reservation_key IS NOT NULL;
