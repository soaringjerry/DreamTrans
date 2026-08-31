-- Per-session cost display reads usage rows by session. The ledger only had
-- (user_id, created_at) and (account_id, created_at) indexes, so a session
-- lookup scanned the user's whole history.

CREATE INDEX IF NOT EXISTS idx_usage_logs_session_id
    ON usage_logs (session_id)
    WHERE session_id IS NOT NULL;
