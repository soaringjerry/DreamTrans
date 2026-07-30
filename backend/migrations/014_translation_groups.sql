-- An AI paragraph translation can cover several provider transcript
-- fragments. Persist one stable group id on every covered fragment so cloud
-- reloads can reconstruct the original translation range without duplicating
-- its visible text.
ALTER TABLE transcripts
  ADD COLUMN IF NOT EXISTS translation_group_id VARCHAR(128);

CREATE INDEX IF NOT EXISTS idx_transcripts_session_translation_group
  ON transcripts(session_id, translation_group_id)
  WHERE translation_group_id IS NOT NULL;
