-- Speechmatics model-training programme. Users choose whether their audio may
-- go through the training-enabled provider account (SM_API_KEY) in exchange
-- for a transcription discount; everyone else is routed through the
-- no-training account (SM_API_KEY_NO_TRAINING). NULL means not asked yet.
ALTER TABLE users ADD COLUMN IF NOT EXISTS training_opt_in BOOLEAN;

-- A provider job can only be polled with the key that created it. Every job
-- registered before this column existed went through SM_API_KEY.
ALTER TABLE batch_transcription_jobs
    ADD COLUMN IF NOT EXISTS training_route BOOLEAN NOT NULL DEFAULT TRUE;
