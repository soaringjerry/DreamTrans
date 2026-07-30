CREATE INDEX IF NOT EXISTS idx_transcripts_session_start_id
ON transcripts (session_id, start_time, id);
