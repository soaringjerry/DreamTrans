-- Wallet + membership billing model.
--
-- DreamPoints are retired: balances are USD. Every user gets one billing
-- account with two buckets — expiring grants (trial credit, top-up bonus,
-- promotions) and a never-expiring wallet funded by top-ups. Plans define
-- membership: a usage discount, hard limits, and feature flags; they contain
-- no usage allowance. Retail prices are derived at charge time from provider
-- cost × markup × (1 − discount) and frozen into each usage row's snapshot,
-- so the generated pricing_rules table is no longer needed.

-- ---------------------------------------------------------------------------
-- Plans (membership definitions). 'free' is a row too so limits and features
-- have exactly one source.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS plans (
  code VARCHAR(40) PRIMARY KEY,
  name VARCHAR(100) NOT NULL,
  is_public BOOLEAN NOT NULL DEFAULT TRUE,
  active BOOLEAN NOT NULL DEFAULT TRUE,
  sort INT NOT NULL DEFAULT 0,
  price_usd_month DECIMAL(18,8) NOT NULL DEFAULT 0 CHECK (price_usd_month >= 0),
  price_usd_year DECIMAL(18,8) NOT NULL DEFAULT 0 CHECK (price_usd_year >= 0),
  stripe_price_id_month VARCHAR(120),
  stripe_price_id_year VARCHAR(120),
  usage_discount_percent DECIMAL(8,4) NOT NULL DEFAULT 0
    CHECK (usage_discount_percent >= 0 AND usage_discount_percent <= 100),
  -- Hard limits. -1 means unlimited.
  storage_gb INT NOT NULL DEFAULT 1 CHECK (storage_gb >= -1),
  retention_days INT NOT NULL DEFAULT 30 CHECK (retention_days >= -1),
  max_concurrent_sessions INT NOT NULL DEFAULT 1 CHECK (max_concurrent_sessions >= -1),
  seats INT NOT NULL DEFAULT 1 CHECK (seats >= 1),
  features JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

INSERT INTO plans
  (code, name, is_public, sort, price_usd_month, price_usd_year,
   usage_discount_percent, storage_gb, retention_days, max_concurrent_sessions,
   seats, features)
VALUES
  ('free', 'Free', TRUE, 0, 0, 0, 0, 1, 30, 1, 1,
   '{"premium_models": false, "byok": false, "batch": false,
     "custom_prompt": false, "auto_topup": false, "export_ledger": false,
     "api_access": false}'::jsonb),
  ('pro', 'Pro', TRUE, 10, 6, 60, 20, 20, 365, 2, 1,
   '{"premium_models": true, "byok": true, "batch": true,
     "custom_prompt": true, "auto_topup": true, "export_ledger": true,
     "api_access": false}'::jsonb)
ON CONFLICT (code) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Billing accounts. owner_type is polymorphic so team (tenant) accounts can be
-- added later without touching the ledger.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS billing_accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_type VARCHAR(10) NOT NULL CHECK (owner_type IN ('user', 'tenant')),
  owner_id UUID NOT NULL,
  plan_code VARCHAR(40) NOT NULL DEFAULT 'free' REFERENCES plans(code),
  wallet_usd DECIMAL(18,8) NOT NULL DEFAULT 0,
  lifetime_charged_usd DECIMAL(18,8) NOT NULL DEFAULT 0
    CHECK (lifetime_charged_usd >= 0),
  member_until TIMESTAMP WITH TIME ZONE,
  stripe_customer_id VARCHAR(120),
  status VARCHAR(20) NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'past_due', 'suspended')),
  auto_topup_threshold_usd DECIMAL(18,8)
    CHECK (auto_topup_threshold_usd IS NULL OR auto_topup_threshold_usd >= 0),
  auto_topup_amount_usd DECIMAL(18,8)
    CHECK (auto_topup_amount_usd IS NULL OR auto_topup_amount_usd > 0),
  custom_discount_percent DECIMAL(8,4)
    CHECK (custom_discount_percent IS NULL
           OR (custom_discount_percent >= 0 AND custom_discount_percent <= 100)),
  custom_markup_percent DECIMAL(12,6)
    CHECK (custom_markup_percent IS NULL
           OR (custom_markup_percent >= 0 AND custom_markup_percent <= 100000)),
  storage_bytes BIGINT NOT NULL DEFAULT 0 CHECK (storage_bytes >= 0),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (owner_type, owner_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_billing_accounts_stripe_customer
  ON billing_accounts(stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;

ALTER TABLE users
  ADD COLUMN IF NOT EXISTS billing_account_id UUID REFERENCES billing_accounts(id);

-- One account per existing user; DreamPoints were pegged 1:1 to USD.
INSERT INTO billing_accounts (owner_type, owner_id, wallet_usd, lifetime_charged_usd)
SELECT 'user', id, COALESCE(dreampoints, 0), GREATEST(0, COALESCE(dreampoints_used, 0))
FROM users
ON CONFLICT (owner_type, owner_id) DO NOTHING;

UPDATE users
SET billing_account_id = accounts.id
FROM billing_accounts AS accounts
WHERE accounts.owner_type = 'user'
  AND accounts.owner_id = users.id
  AND users.billing_account_id IS NULL;

CREATE OR REPLACE FUNCTION delete_user_billing_account()
RETURNS TRIGGER AS $$
BEGIN
  DELETE FROM billing_accounts
  WHERE owner_type = 'user' AND owner_id = OLD.id;
  RETURN OLD;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS delete_user_billing_account_after_delete ON users;
CREATE TRIGGER delete_user_billing_account_after_delete
AFTER DELETE ON users
FOR EACH ROW EXECUTE FUNCTION delete_user_billing_account();

-- ---------------------------------------------------------------------------
-- Grants: the expiring bucket. Consumed oldest-expiry first.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS grants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  kind VARCHAR(30) NOT NULL
    CHECK (kind IN ('trial', 'topup_bonus', 'promo', 'adjustment', 'settle_return')),
  amount_usd DECIMAL(18,8) NOT NULL CHECK (amount_usd > 0),
  remaining_usd DECIMAL(18,8) NOT NULL CHECK (remaining_usd >= 0),
  expires_at TIMESTAMP WITH TIME ZONE,
  source_payment_id UUID,
  note TEXT NOT NULL DEFAULT '',
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_grants_account_open
  ON grants(account_id, expires_at) WHERE remaining_usd > 0;

-- ---------------------------------------------------------------------------
-- Memberships, payments, Stripe webhook idempotency, top-up tiers.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS memberships (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  plan_code VARCHAR(40) NOT NULL REFERENCES plans(code),
  billing_interval VARCHAR(10) NOT NULL DEFAULT 'month'
    CHECK (billing_interval IN ('month', 'year')),
  stripe_subscription_id VARCHAR(120),
  status VARCHAR(30) NOT NULL DEFAULT 'active',
  current_period_start TIMESTAMP WITH TIME ZONE,
  current_period_end TIMESTAMP WITH TIME ZONE,
  cancel_at_period_end BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_memberships_stripe_subscription
  ON memberships(stripe_subscription_id) WHERE stripe_subscription_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_memberships_account
  ON memberships(account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES billing_accounts(id) ON DELETE CASCADE,
  kind VARCHAR(20) NOT NULL CHECK (kind IN ('topup', 'membership', 'refund')),
  amount_usd DECIMAL(18,8) NOT NULL,
  bonus_usd DECIMAL(18,8) NOT NULL DEFAULT 0 CHECK (bonus_usd >= 0),
  currency VARCHAR(3) NOT NULL DEFAULT 'usd',
  stripe_object_id VARCHAR(120),
  status VARCHAR(30) NOT NULL DEFAULT 'succeeded',
  description TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_stripe_object
  ON payments(stripe_object_id) WHERE stripe_object_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payments_account
  ON payments(account_id, created_at DESC);

CREATE TABLE IF NOT EXISTS stripe_events (
  event_id VARCHAR(120) PRIMARY KEY,
  event_type VARCHAR(80) NOT NULL,
  processed_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS topup_tiers (
  amount_usd DECIMAL(18,8) PRIMARY KEY CHECK (amount_usd > 0),
  bonus_percent DECIMAL(8,4) NOT NULL DEFAULT 0
    CHECK (bonus_percent >= 0 AND bonus_percent <= 100),
  bonus_expiry_days INT NOT NULL DEFAULT 365 CHECK (bonus_expiry_days >= 1),
  stripe_price_id VARCHAR(120),
  active BOOLEAN NOT NULL DEFAULT TRUE,
  sort INT NOT NULL DEFAULT 0,
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

INSERT INTO topup_tiers (amount_usd, bonus_percent, sort)
VALUES (10, 0, 0), (20, 10, 1), (50, 15, 2), (100, 15, 3)
ON CONFLICT (amount_usd) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Ledger columns: which bucket paid, in USD.
-- ---------------------------------------------------------------------------
ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS account_id UUID REFERENCES billing_accounts(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS grant_usd DECIMAL(18,8) NOT NULL DEFAULT 0 CHECK (grant_usd >= 0),
  ADD COLUMN IF NOT EXISTS wallet_usd DECIMAL(18,8) NOT NULL DEFAULT 0 CHECK (wallet_usd >= 0);

DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'usage_logs' AND column_name = 'cost'
  ) THEN
    ALTER TABLE usage_logs RENAME COLUMN cost TO charge_usd;
  END IF;
  IF EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'usage_logs' AND column_name = 'service_fee_dp'
  ) THEN
    ALTER TABLE usage_logs RENAME COLUMN service_fee_dp TO margin_usd;
  END IF;
END
$$;

UPDATE usage_logs
SET account_id = users.billing_account_id
FROM users
WHERE users.id = usage_logs.user_id
  AND usage_logs.account_id IS NULL;

-- Historical charges were wallet (DreamPoint) debits.
UPDATE usage_logs
SET wallet_usd = charge_usd
WHERE wallet_usd = 0 AND grant_usd = 0 AND charge_usd > 0;

CREATE INDEX IF NOT EXISTS idx_usage_logs_account_created
  ON usage_logs(account_id, created_at DESC);

ALTER TABLE usage_logs DROP CONSTRAINT IF EXISTS usage_logs_cost_attribution_check;
ALTER TABLE usage_logs
  ADD CONSTRAINT usage_logs_cost_attribution_check
  CHECK (cost_attribution IN ('provider_priced', 'byok', 'non_provider', 'legacy_unknown', 'unpriced'));

ALTER TABLE balance_transactions
  ADD COLUMN IF NOT EXISTS account_id UUID REFERENCES billing_accounts(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS bucket VARCHAR(10) NOT NULL DEFAULT 'wallet'
    CHECK (bucket IN ('wallet', 'grant')),
  ADD COLUMN IF NOT EXISTS grant_id UUID REFERENCES grants(id) ON DELETE SET NULL;

UPDATE balance_transactions
SET account_id = users.billing_account_id
FROM users
WHERE users.id = balance_transactions.user_id
  AND balance_transactions.account_id IS NULL;

CREATE INDEX IF NOT EXISTS idx_balance_transactions_account
  ON balance_transactions(account_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Retire DreamPoints, the generated retail table, the second pricing layer's
-- staging state, and the request-count quota.
-- ---------------------------------------------------------------------------
ALTER TABLE users
  DROP COLUMN IF EXISTS dreampoints,
  DROP COLUMN IF EXISTS dreampoints_used;

DROP TABLE IF EXISTS pricing_rules;
DROP TABLE IF EXISTS tenant_api_usage;

ALTER TABLE billing_config
  DROP COLUMN IF EXISTS dp_per_usd,
  DROP COLUMN IF EXISTS pending_config,
  DROP COLUMN IF EXISTS pricing_state;

-- Settings: rename the signup credit and add its expiry window.
UPDATE system_settings
SET key = 'trial_credit_usd',
    description = 'USD granted to newly created accounts as an expiring trial credit'
WHERE key = 'free_tier_dreampoints'
  AND NOT EXISTS (SELECT 1 FROM system_settings WHERE key = 'trial_credit_usd');
DELETE FROM system_settings WHERE key = 'free_tier_dreampoints';

INSERT INTO system_settings (key, value, description)
VALUES
  ('trial_credit_usd', '1'::jsonb, 'USD granted to newly created accounts as an expiring trial credit'),
  ('trial_credit_days', '30'::jsonb, 'Days before the signup trial credit expires'),
  ('billing_enabled', 'true'::jsonb, 'Enable or disable usage charging'),
  ('allow_negative_balance', 'false'::jsonb, 'Allow charges to continue below zero balance'),
  ('allow_user_api_key', 'false'::jsonb, 'Allow users to use their own provider API key')
ON CONFLICT (key) DO NOTHING;

-- ---------------------------------------------------------------------------
-- Transcript storage is counted per billing account and limited by the plan.
-- AI knowledge storage keeps its tenant-level accounting.
-- ---------------------------------------------------------------------------
UPDATE billing_accounts AS account
SET storage_bytes = usage.bytes
FROM (
  SELECT u.billing_account_id AS account_id,
         COALESCE(SUM(
           octet_length(t.speaker)::BIGINT
           + octet_length(t.text)::BIGINT
           + COALESCE(octet_length(t.translation), 0)::BIGINT
         ), 0) AS bytes
  FROM transcripts t
  JOIN sessions s ON s.id = t.session_id
  JOIN users u ON u.id = s.user_id
  WHERE u.billing_account_id IS NOT NULL
  GROUP BY u.billing_account_id
) AS usage
WHERE usage.account_id = account.id;

CREATE OR REPLACE FUNCTION update_account_transcript_storage()
RETURNS TRIGGER AS $$
DECLARE
  affected_session_id UUID;
  affected_account_id UUID;
  quota_gb INTEGER;
  old_bytes BIGINT := 0;
  new_bytes BIGINT := 0;
  current_bytes BIGINT;
  next_bytes BIGINT;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    old_bytes :=
      octet_length(OLD.speaker)::BIGINT
      + octet_length(OLD.text)::BIGINT
      + COALESCE(octet_length(OLD.translation), 0)::BIGINT;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_bytes :=
      octet_length(NEW.speaker)::BIGINT
      + octet_length(NEW.text)::BIGINT
      + COALESCE(octet_length(NEW.translation), 0)::BIGINT;
  END IF;
  IF TG_OP = 'DELETE' THEN
    affected_session_id := OLD.session_id;
  ELSE
    affected_session_id := NEW.session_id;
  END IF;

  SELECT u.billing_account_id, p.storage_gb
  INTO affected_account_id, quota_gb
  FROM sessions s
  JOIN users u ON u.id = s.user_id
  LEFT JOIN billing_accounts a ON a.id = u.billing_account_id
  LEFT JOIN plans p ON p.code = a.plan_code
  WHERE s.id = affected_session_id;

  -- Cascading deletes or accounts that are still being created: nothing to count.
  IF affected_account_id IS NULL THEN
    IF TG_OP = 'DELETE' THEN
      RETURN OLD;
    END IF;
    RETURN NEW;
  END IF;

  SELECT storage_bytes
  INTO current_bytes
  FROM billing_accounts
  WHERE id = affected_account_id
  FOR UPDATE;

  IF NOT FOUND THEN
    IF TG_OP = 'DELETE' THEN
      RETURN OLD;
    END IF;
    RETURN NEW;
  END IF;

  next_bytes := GREATEST(0, current_bytes + new_bytes - old_bytes);

  -- Only additional bytes fail: reductions and retries stay possible after
  -- a plan downgrade lowered the limit below current usage.
  IF new_bytes > old_bytes
     AND quota_gb IS NOT NULL
     AND quota_gb >= 0
     AND next_bytes > quota_gb::BIGINT * 1073741824 THEN
    RAISE EXCEPTION 'account transcript storage limit exceeded'
      USING ERRCODE = 'check_violation',
            CONSTRAINT = 'account_transcript_storage_limit';
  END IF;

  UPDATE billing_accounts
  SET storage_bytes = next_bytes,
      updated_at = NOW()
  WHERE id = affected_account_id;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS update_tenant_transcript_storage_after_write ON transcripts;
DROP FUNCTION IF EXISTS update_tenant_transcript_storage();
DROP TABLE IF EXISTS tenant_storage_usage;

DROP TRIGGER IF EXISTS update_account_transcript_storage_after_write ON transcripts;
CREATE TRIGGER update_account_transcript_storage_after_write
AFTER INSERT OR UPDATE OR DELETE ON transcripts
FOR EACH ROW EXECUTE FUNCTION update_account_transcript_storage();
