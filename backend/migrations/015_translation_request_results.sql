-- Durable replay for paid real-time translations.
--
-- Billing idempotency alone can prevent a second debit, but it cannot recreate
-- a provider result after a WebSocket disconnect or application restart. Keep
-- the short-lived result outside usage_logs so deleting a cloud session also
-- deletes its translated content while the accounting row remains intact.
CREATE TABLE IF NOT EXISTS translation_request_results (
  request_key VARCHAR(255) PRIMARY KEY,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
  request_fingerprint CHAR(64) NOT NULL,
  state VARCHAR(20) NOT NULL DEFAULT 'reserved'
    CHECK (state IN ('reserved', 'completed', 'failed', 'expired')),
  attempt INTEGER NOT NULL DEFAULT 1 CHECK (attempt > 0),
  usage_idempotency_key VARCHAR(255) NOT NULL,
  content TEXT,
  model VARCHAR(200),
  latency_ms BIGINT,
  started_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMP WITH TIME ZONE,
  expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT translation_request_completed_payload
    CHECK (
      state <> 'completed'
      OR (
        content IS NOT NULL
        AND completed_at IS NOT NULL
      )
    )
);

CREATE INDEX IF NOT EXISTS idx_translation_request_results_expiry
  ON translation_request_results(expires_at)
  WHERE state IN ('completed', 'failed', 'expired');

CREATE INDEX IF NOT EXISTS idx_translation_request_results_owner
  ON translation_request_results(tenant_id, user_id, started_at);

-- Claim recovery can reconstruct the next monotonic attempt from the durable
-- accounting keys after a short-lived replay/tombstone row is cleaned up.
CREATE INDEX IF NOT EXISTS idx_usage_logs_translation_attempt_prefix
  ON usage_logs(idempotency_key text_pattern_ops)
  WHERE idempotency_key LIKE 'ws-translation:%:attempt:%';
