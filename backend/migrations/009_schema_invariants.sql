-- Older releases relied on application defaults while leaving many columns
-- nullable. A single legacy/manual NULL then caused database/sql Scan failures,
-- and separate user/tenant foreign keys allowed cross-tenant associations.

INSERT INTO tenants
  (id, name, slug, plan, api_quota_monthly, storage_quota_gb, max_sessions)
VALUES
  ('00000000-0000-0000-0000-000000000001',
   'Default Organization', 'default', 'enterprise', -1, 100, -1)
ON CONFLICT DO NOTHING;

UPDATE tenants
SET plan = CASE
      WHEN plan IN ('free', 'pro', 'enterprise') THEN plan
      ELSE 'free'
    END,
    api_quota_monthly = GREATEST(-1, LEAST(1000000000, COALESCE(api_quota_monthly, 1000))),
    storage_quota_gb = GREATEST(-1, LEAST(1000000, COALESCE(storage_quota_gb, 1))),
    max_sessions = GREATEST(-1, LEAST(1000000, COALESCE(max_sessions, 10))),
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    updated_at = CASE
      WHEN updated_at IS NULL OR NOT isfinite(updated_at) THEN NOW()
      ELSE updated_at
    END;

WITH fallback_tenant AS MATERIALIZED (
  SELECT id
  FROM tenants
  ORDER BY
    CASE
      WHEN id = '00000000-0000-0000-0000-000000000001'::UUID THEN 0
      WHEN slug = 'default' THEN 1
      ELSE 2
    END,
    created_at ASC NULLS LAST,
    id
  LIMIT 1
)
UPDATE users
SET tenant_id = COALESCE(
      tenant_id,
      (SELECT id FROM fallback_tenant)
    ),
    name = CASE
      WHEN NULLIF(BTRIM(name), '') IS NULL
        THEN LEFT(COALESCE(NULLIF(SPLIT_PART(email, '@', 1), ''), 'User'), 100)
      ELSE name
    END,
    role = CASE
      WHEN role IN ('user', 'admin', 'super_admin') THEN role
      ELSE 'user'
    END,
    is_active = COALESCE(is_active, false),
    email_verified = COALESCE(email_verified, false),
    dreampoints = CASE
      WHEN dreampoints IS NULL
        OR dreampoints IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        THEN 0
      ELSE dreampoints
    END,
    dreampoints_used = CASE
      WHEN dreampoints_used IS NULL
        OR dreampoints_used IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        OR dreampoints_used < 0
        THEN 0
      ELSE dreampoints_used
    END,
    last_login_at = CASE
      WHEN last_login_at IS NOT NULL AND NOT isfinite(last_login_at) THEN NULL
      ELSE last_login_at
    END,
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    updated_at = CASE
      WHEN updated_at IS NULL OR NOT isfinite(updated_at) THEN NOW()
      ELSE updated_at
    END;

-- The authenticated user's tenant is authoritative for all owned data.
UPDATE sessions AS session
SET tenant_id = app_user.tenant_id
FROM users AS app_user
WHERE session.user_id = app_user.id
  AND session.tenant_id IS DISTINCT FROM app_user.tenant_id;

UPDATE usage_logs AS usage
SET tenant_id = app_user.tenant_id
FROM users AS app_user
WHERE usage.user_id = app_user.id
  AND usage.tenant_id IS DISTINCT FROM app_user.tenant_id;

-- A client-provided session UUID must never associate one user's charge with
-- another user's session. Preserve the usage ledger but drop invalid context.
UPDATE usage_logs AS usage
SET session_id = NULL
WHERE usage.session_id IS NOT NULL
  AND NOT EXISTS (
    SELECT 1
    FROM sessions AS session
    WHERE session.id = usage.session_id
      AND session.user_id = usage.user_id
      AND session.tenant_id = usage.tenant_id
  );

UPDATE batch_transcription_jobs AS job
SET tenant_id = app_user.tenant_id
FROM users AS app_user
WHERE job.user_id = app_user.id
  AND job.tenant_id IS DISTINCT FROM app_user.tenant_id;

UPDATE sessions
SET title = COALESCE(title, 'Untitled Session'),
    source_language = COALESCE(source_language, 'en'),
    target_language = COALESCE(target_language, 'zh'),
    duration_seconds = GREATEST(COALESCE(duration_seconds, 0), 0),
    status = CASE
      WHEN status IN ('active', 'paused', 'completed', 'archived') THEN status
      ELSE 'archived'
    END,
    started_at = CASE
      WHEN started_at IS NOT NULL AND isfinite(started_at) THEN started_at
      WHEN created_at IS NOT NULL AND isfinite(created_at) THEN created_at
      ELSE NOW()
    END,
    ended_at = CASE
      WHEN ended_at IS NOT NULL AND NOT isfinite(ended_at) THEN NULL
      ELSE ended_at
    END,
    created_at = CASE
      WHEN created_at IS NOT NULL AND isfinite(created_at) THEN created_at
      WHEN started_at IS NOT NULL AND isfinite(started_at) THEN started_at
      ELSE NOW()
    END,
    updated_at = CASE
      WHEN updated_at IS NOT NULL AND isfinite(updated_at) THEN updated_at
      WHEN created_at IS NOT NULL AND isfinite(created_at) THEN created_at
      WHEN started_at IS NOT NULL AND isfinite(started_at) THEN started_at
      ELSE NOW()
    END;

UPDATE transcripts
SET speaker = COALESCE(speaker, 'Speaker'),
    status = CASE
      WHEN status IN ('partial', 'confirmed', 'translated') THEN status
      ELSE 'confirmed'
    END,
    is_partial = COALESCE(is_partial, COALESCE(status, 'confirmed') = 'partial'),
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    updated_at = CASE
      WHEN updated_at IS NOT NULL AND isfinite(updated_at) THEN updated_at
      WHEN created_at IS NOT NULL AND isfinite(created_at) THEN created_at
      ELSE NOW()
    END;

UPDATE transcripts
SET start_time = 0
WHERE start_time IS NULL
   OR start_time < 0
   OR start_time IN (
     'NaN'::DOUBLE PRECISION,
     'Infinity'::DOUBLE PRECISION,
     '-Infinity'::DOUBLE PRECISION
   );

UPDATE transcripts
SET end_time = NULL
WHERE end_time IS NOT NULL
  AND (
    end_time < start_time
    OR end_time IN (
      'NaN'::DOUBLE PRECISION,
      'Infinity'::DOUBLE PRECISION,
      '-Infinity'::DOUBLE PRECISION
    )
  );

UPDATE usage_logs
SET quantity = 0
WHERE quantity IS NULL
   OR quantity < 0
   OR quantity > 1000000000
   OR quantity IN (
     'NaN'::DOUBLE PRECISION,
     'Infinity'::DOUBLE PRECISION,
     '-Infinity'::DOUBLE PRECISION
   );

UPDATE usage_logs
SET input_tokens = GREATEST(COALESCE(input_tokens, 0), 0),
    output_tokens = GREATEST(COALESCE(output_tokens, 0), 0),
    cost = CASE
      WHEN cost IS NULL
        OR cost < 0
        OR cost >= 100000000
        OR cost IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        THEN 0
      ELSE cost
    END,
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    refunded_at = CASE
      -- A non-NULL marker means the refund was already observed. Preserve
      -- that state while replacing an unscannable legacy timestamp.
      WHEN refunded_at IS NOT NULL AND NOT isfinite(refunded_at) THEN NOW()
      ELSE refunded_at
    END;

UPDATE pricing_rules
SET is_active = COALESCE(is_active, false),
    priority = COALESCE(priority, 0),
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    updated_at = CASE
      WHEN updated_at IS NOT NULL AND isfinite(updated_at) THEN updated_at
      WHEN created_at IS NOT NULL AND isfinite(created_at) THEN created_at
      ELSE NOW()
    END;

UPDATE pricing_rules
SET price_per_unit = 0,
    is_active = false
WHERE price_per_unit IS NULL
   OR price_per_unit < 0
   OR price_per_unit >= 100000000
   OR price_per_unit IN (
     'NaN'::NUMERIC,
     'Infinity'::NUMERIC,
     '-Infinity'::NUMERIC
   );

UPDATE refresh_tokens
SET expires_at = CASE
      -- Invalid expiry timestamps fail closed instead of creating an
      -- effectively immortal refresh token.
      WHEN expires_at IS NULL OR NOT isfinite(expires_at) THEN NOW()
      ELSE expires_at
    END,
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    revoked_at = CASE
      -- Preserve the fact that this token was revoked.
      WHEN revoked_at IS NOT NULL AND NOT isfinite(revoked_at) THEN NOW()
      ELSE revoked_at
    END;

UPDATE balance_transactions
SET amount = CASE
      WHEN amount IS NULL
        OR amount IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        THEN 0
      ELSE amount
    END,
    balance_after = CASE
      WHEN balance_after IS NULL
        OR balance_after IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        THEN 0
      ELSE balance_after
    END,
    created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END;

UPDATE batch_transcription_jobs
SET created_at = CASE
      WHEN created_at IS NULL OR NOT isfinite(created_at) THEN NOW()
      ELSE created_at
    END,
    completed_at = CASE
      -- Preserve the completed marker used to suppress failure refunds.
      WHEN completed_at IS NOT NULL AND NOT isfinite(completed_at) THEN NOW()
      ELSE completed_at
    END;

UPDATE system_settings
SET updated_at = CASE
      WHEN updated_at IS NULL OR NOT isfinite(updated_at) THEN NOW()
      ELSE updated_at
    END;

ALTER TABLE tenants
  ALTER COLUMN plan SET NOT NULL,
  ALTER COLUMN api_quota_monthly SET NOT NULL,
  ALTER COLUMN storage_quota_gb SET NOT NULL,
  ALTER COLUMN max_sessions SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE users
  ALTER COLUMN tenant_id SET NOT NULL,
  ALTER COLUMN name SET NOT NULL,
  ALTER COLUMN role SET NOT NULL,
  ALTER COLUMN is_active SET NOT NULL,
  ALTER COLUMN email_verified SET NOT NULL,
  ALTER COLUMN dreampoints SET NOT NULL,
  ALTER COLUMN dreampoints_used SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE sessions
  ALTER COLUMN title SET NOT NULL,
  ALTER COLUMN source_language SET NOT NULL,
  ALTER COLUMN target_language SET NOT NULL,
  ALTER COLUMN duration_seconds SET NOT NULL,
  ALTER COLUMN status SET NOT NULL,
  ALTER COLUMN started_at SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE transcripts
  ALTER COLUMN speaker SET NOT NULL,
  ALTER COLUMN start_time SET NOT NULL,
  ALTER COLUMN status SET NOT NULL,
  ALTER COLUMN is_partial SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE usage_logs
  ALTER COLUMN quantity SET NOT NULL,
  ALTER COLUMN input_tokens SET DEFAULT 0,
  ALTER COLUMN input_tokens SET NOT NULL,
  ALTER COLUMN output_tokens SET DEFAULT 0,
  ALTER COLUMN output_tokens SET NOT NULL,
  ALTER COLUMN cost SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL;

ALTER TABLE pricing_rules
  ALTER COLUMN price_per_unit SET NOT NULL,
  ALTER COLUMN is_active SET NOT NULL,
  ALTER COLUMN priority SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL,
  ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE refresh_tokens
  ALTER COLUMN expires_at SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL;

ALTER TABLE balance_transactions
  ALTER COLUMN amount SET NOT NULL,
  ALTER COLUMN balance_after SET NOT NULL,
  ALTER COLUMN created_at SET NOT NULL;

ALTER TABLE system_settings
  ALTER COLUMN updated_at SET NOT NULL;

ALTER TABLE batch_transcription_jobs
  ALTER COLUMN created_at SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_id_tenant
  ON users(id, tenant_id);

CREATE UNIQUE INDEX IF NOT EXISTS idx_sessions_id_user_tenant
  ON sessions(id, user_id, tenant_id);

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenants_valid_plan'
      AND conrelid = 'tenants'::regclass
  ) THEN
    ALTER TABLE tenants
      ADD CONSTRAINT tenants_valid_plan
      CHECK (plan IN ('free', 'pro', 'enterprise'));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenants_valid_quotas'
      AND conrelid = 'tenants'::regclass
  ) THEN
    ALTER TABLE tenants
      ADD CONSTRAINT tenants_valid_quotas
      CHECK (
        api_quota_monthly BETWEEN -1 AND 1000000000
        AND storage_quota_gb BETWEEN -1 AND 1000000
        AND max_sessions BETWEEN -1 AND 1000000
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'users_valid_role'
      AND conrelid = 'users'::regclass
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_valid_role
      CHECK (role IN ('user', 'admin', 'super_admin'));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sessions_user_tenant_fk'
      AND conrelid = 'sessions'::regclass
  ) THEN
    ALTER TABLE sessions
      ADD CONSTRAINT sessions_user_tenant_fk
      FOREIGN KEY (user_id, tenant_id)
      REFERENCES users(id, tenant_id)
      ON DELETE CASCADE;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'usage_logs_nonnegative_cost'
      AND conrelid = 'usage_logs'::regclass
  ) THEN
    ALTER TABLE usage_logs
      ADD CONSTRAINT usage_logs_nonnegative_cost
      CHECK (
        cost >= 0
        AND cost < 100000000
        AND cost <> 'NaN'::NUMERIC
        AND input_tokens >= 0
        AND output_tokens >= 0
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'pricing_rules_storable_price'
      AND conrelid = 'pricing_rules'::regclass
  ) THEN
    ALTER TABLE pricing_rules
      ADD CONSTRAINT pricing_rules_storable_price
      CHECK (price_per_unit >= 0 AND price_per_unit < 100000000);
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'usage_logs_user_tenant_fk'
      AND conrelid = 'usage_logs'::regclass
  ) THEN
    ALTER TABLE usage_logs
      ADD CONSTRAINT usage_logs_user_tenant_fk
      FOREIGN KEY (user_id, tenant_id)
      REFERENCES users(id, tenant_id)
      ON DELETE CASCADE;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'usage_logs_session_owner_fk'
      AND conrelid = 'usage_logs'::regclass
  ) THEN
    ALTER TABLE usage_logs
      ADD CONSTRAINT usage_logs_session_owner_fk
      FOREIGN KEY (session_id, user_id, tenant_id)
      REFERENCES sessions(id, user_id, tenant_id);
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'batch_jobs_user_tenant_fk'
      AND conrelid = 'batch_transcription_jobs'::regclass
  ) THEN
    ALTER TABLE batch_transcription_jobs
      ADD CONSTRAINT batch_jobs_user_tenant_fk
      FOREIGN KEY (user_id, tenant_id)
      REFERENCES users(id, tenant_id)
      ON DELETE CASCADE;
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sessions_nonnegative_duration'
      AND conrelid = 'sessions'::regclass
  ) THEN
    ALTER TABLE sessions
      ADD CONSTRAINT sessions_nonnegative_duration
      CHECK (duration_seconds >= 0);
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sessions_valid_status'
      AND conrelid = 'sessions'::regclass
  ) THEN
    ALTER TABLE sessions
      ADD CONSTRAINT sessions_valid_status
      CHECK (status IN ('active', 'paused', 'completed', 'archived'));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'transcripts_valid_timing'
      AND conrelid = 'transcripts'::regclass
  ) THEN
    ALTER TABLE transcripts
      ADD CONSTRAINT transcripts_valid_timing
      CHECK (
        start_time >= 0
        AND start_time NOT IN (
          'NaN'::DOUBLE PRECISION,
          'Infinity'::DOUBLE PRECISION,
          '-Infinity'::DOUBLE PRECISION
        )
        AND (
          end_time IS NULL
          OR (
            end_time >= start_time
            AND end_time NOT IN (
              'NaN'::DOUBLE PRECISION,
              'Infinity'::DOUBLE PRECISION,
              '-Infinity'::DOUBLE PRECISION
            )
          )
        )
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'transcripts_valid_status'
      AND conrelid = 'transcripts'::regclass
  ) THEN
    ALTER TABLE transcripts
      ADD CONSTRAINT transcripts_valid_status
      CHECK (status IN ('partial', 'confirmed', 'translated'));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'usage_logs_valid_quantity'
      AND conrelid = 'usage_logs'::regclass
  ) THEN
    ALTER TABLE usage_logs
      ADD CONSTRAINT usage_logs_valid_quantity
      CHECK (
        quantity >= 0
        AND quantity <= 1000000000
        AND quantity NOT IN (
          'NaN'::DOUBLE PRECISION,
          'Infinity'::DOUBLE PRECISION,
          '-Infinity'::DOUBLE PRECISION
        )
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'users_finite_balances'
      AND conrelid = 'users'::regclass
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_finite_balances
      CHECK (
        dreampoints NOT IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        AND dreampoints_used >= 0
        AND dreampoints_used NOT IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'balance_transactions_finite_amounts'
      AND conrelid = 'balance_transactions'::regclass
  ) THEN
    ALTER TABLE balance_transactions
      ADD CONSTRAINT balance_transactions_finite_amounts
      CHECK (
        amount NOT IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
        AND balance_after NOT IN (
          'NaN'::NUMERIC,
          'Infinity'::NUMERIC,
          '-Infinity'::NUMERIC
        )
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'tenants_finite_timestamps'
      AND conrelid = 'tenants'::regclass
  ) THEN
    ALTER TABLE tenants
      ADD CONSTRAINT tenants_finite_timestamps
      CHECK (isfinite(created_at) AND isfinite(updated_at));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'users_finite_timestamps'
      AND conrelid = 'users'::regclass
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_finite_timestamps
      CHECK (
        (last_login_at IS NULL OR isfinite(last_login_at))
        AND isfinite(created_at)
        AND isfinite(updated_at)
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'sessions_finite_timestamps'
      AND conrelid = 'sessions'::regclass
  ) THEN
    ALTER TABLE sessions
      ADD CONSTRAINT sessions_finite_timestamps
      CHECK (
        isfinite(started_at)
        AND (ended_at IS NULL OR isfinite(ended_at))
        AND isfinite(created_at)
        AND isfinite(updated_at)
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'transcripts_finite_timestamps'
      AND conrelid = 'transcripts'::regclass
  ) THEN
    ALTER TABLE transcripts
      ADD CONSTRAINT transcripts_finite_timestamps
      CHECK (isfinite(created_at) AND isfinite(updated_at));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'usage_logs_finite_timestamps'
      AND conrelid = 'usage_logs'::regclass
  ) THEN
    ALTER TABLE usage_logs
      ADD CONSTRAINT usage_logs_finite_timestamps
      CHECK (
        isfinite(created_at)
        AND (refunded_at IS NULL OR isfinite(refunded_at))
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'pricing_rules_finite_timestamps'
      AND conrelid = 'pricing_rules'::regclass
  ) THEN
    ALTER TABLE pricing_rules
      ADD CONSTRAINT pricing_rules_finite_timestamps
      CHECK (isfinite(created_at) AND isfinite(updated_at));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'refresh_tokens_finite_timestamps'
      AND conrelid = 'refresh_tokens'::regclass
  ) THEN
    ALTER TABLE refresh_tokens
      ADD CONSTRAINT refresh_tokens_finite_timestamps
      CHECK (
        isfinite(expires_at)
        AND isfinite(created_at)
        AND (revoked_at IS NULL OR isfinite(revoked_at))
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'balance_transactions_finite_timestamps'
      AND conrelid = 'balance_transactions'::regclass
  ) THEN
    ALTER TABLE balance_transactions
      ADD CONSTRAINT balance_transactions_finite_timestamps
      CHECK (isfinite(created_at));
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'batch_jobs_finite_timestamps'
      AND conrelid = 'batch_transcription_jobs'::regclass
  ) THEN
    ALTER TABLE batch_transcription_jobs
      ADD CONSTRAINT batch_jobs_finite_timestamps
      CHECK (
        isfinite(created_at)
        AND (completed_at IS NULL OR isfinite(completed_at))
      );
  END IF;

  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'system_settings_finite_timestamps'
      AND conrelid = 'system_settings'::regclass
  ) THEN
    ALTER TABLE system_settings
      ADD CONSTRAINT system_settings_finite_timestamps
      CHECK (isfinite(updated_at));
  END IF;
END
$$;
