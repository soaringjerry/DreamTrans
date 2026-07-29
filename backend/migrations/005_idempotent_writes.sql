-- Stable client IDs make transcript batch retries safe after a lost response.
ALTER TABLE transcripts
  ADD COLUMN IF NOT EXISTS client_segment_id UUID;

UPDATE transcripts
SET client_segment_id = gen_random_uuid()
WHERE client_segment_id IS NULL;

ALTER TABLE transcripts
  ALTER COLUMN client_segment_id SET DEFAULT gen_random_uuid(),
  ALTER COLUMN client_segment_id SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_transcripts_session_client_segment
  ON transcripts(session_id, client_segment_id);

-- Provider job identifiers give billing operations a durable idempotency key.
ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS idempotency_key VARCHAR(255);

CREATE UNIQUE INDEX IF NOT EXISTS idx_usage_logs_idempotency_key
  ON usage_logs(idempotency_key)
  WHERE idempotency_key IS NOT NULL;
