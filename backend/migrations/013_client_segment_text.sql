-- client_segment_id is a client-chosen idempotency key, not a database UUID.
-- Newer clients send bounded seg_<start>_<end>_<hash> strings; legacy UUID
-- keys keep working because their canonical text form is preserved verbatim.
ALTER TABLE transcripts
  ALTER COLUMN client_segment_id DROP DEFAULT;

ALTER TABLE transcripts
  ALTER COLUMN client_segment_id TYPE VARCHAR(128)
  USING client_segment_id::text;

ALTER TABLE transcripts
  ALTER COLUMN client_segment_id SET DEFAULT gen_random_uuid()::text;

-- The per-session uniqueness guarantee must survive the type change.
DO $client_segment_unique$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_indexes
    WHERE indexname = 'idx_transcripts_session_client_segment'
  ) THEN
    CREATE UNIQUE INDEX idx_transcripts_session_client_segment
      ON transcripts(session_id, client_segment_id);
  END IF;
END
$client_segment_unique$;
