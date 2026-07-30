-- Cost-plus billing, governed model catalog, cloud preferences, and audit data.
-- Existing pricing_rules remain active so an upgrade never changes retail
-- prices until a super administrator explicitly applies/reset the new catalog.

CREATE TABLE IF NOT EXISTS billing_config (
  singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
  dp_per_usd DECIMAL(18,8) NOT NULL DEFAULT 1 CHECK (dp_per_usd > 0),
  default_markup_percent DECIMAL(12,6) NOT NULL DEFAULT 100
    CHECK (default_markup_percent >= 0 AND default_markup_percent <= 100000),
  catalog_version VARCHAR(40) NOT NULL DEFAULT 'legacy-compatible',
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_by UUID REFERENCES users(id)
);

INSERT INTO billing_config (singleton)
VALUES (TRUE)
ON CONFLICT (singleton) DO NOTHING;

CREATE TABLE IF NOT EXISTS provider_cost_rates (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider VARCHAR(60) NOT NULL,
  sku VARCHAR(200) NOT NULL,
  service VARCHAR(50) NOT NULL,
  unit_type VARCHAR(30) NOT NULL,
  cost_per_unit_usd DECIMAL(24,12) NOT NULL
    CHECK (cost_per_unit_usd >= 0),
  catalog_version VARCHAR(40) NOT NULL,
  source_url TEXT NOT NULL DEFAULT '',
  effective_at TIMESTAMP WITH TIME ZONE,
  is_builtin BOOLEAN NOT NULL DEFAULT TRUE,
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (provider, sku, service, unit_type)
);

CREATE TABLE IF NOT EXISTS billing_markup_overrides (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope_type VARCHAR(20) NOT NULL
    CHECK (scope_type IN ('provider', 'category', 'sku')),
  scope_key VARCHAR(260) NOT NULL,
  markup_percent DECIMAL(12,6) NOT NULL
    CHECK (markup_percent >= 0 AND markup_percent <= 100000),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_by UUID REFERENCES users(id),
  UNIQUE (scope_type, scope_key)
);

ALTER TABLE pricing_rules
  ADD COLUMN IF NOT EXISTS managed_key VARCHAR(320),
  ADD COLUMN IF NOT EXISTS source VARCHAR(30) NOT NULL DEFAULT 'legacy',
  ADD COLUMN IF NOT EXISTS catalog_version VARCHAR(40);

CREATE UNIQUE INDEX IF NOT EXISTS idx_pricing_rules_managed_key
  ON pricing_rules(managed_key) WHERE managed_key IS NOT NULL;

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS cached_input_tokens INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS cache_write_tokens INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS upstream_cost_usd DECIMAL(24,12) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS service_fee_dp DECIMAL(24,12) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS pricing_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE TABLE IF NOT EXISTS provider_models (
  provider VARCHAR(60) NOT NULL,
  model_id VARCHAR(200) NOT NULL,
  source VARCHAR(30) NOT NULL DEFAULT 'provider',
  provider_available BOOLEAN NOT NULL DEFAULT TRUE,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  first_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  PRIMARY KEY (provider, model_id)
);

CREATE TABLE IF NOT EXISTS model_policies (
  purpose VARCHAR(30) NOT NULL
    CHECK (purpose IN ('translation', 'summary', 'chat', 'embedding')),
  model_id VARCHAR(200) NOT NULL,
  is_approved BOOLEAN NOT NULL DEFAULT FALSE,
  is_default BOOLEAN NOT NULL DEFAULT FALSE,
  cost_confirmed BOOLEAN NOT NULL DEFAULT FALSE,
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_by UUID REFERENCES users(id),
  PRIMARY KEY (purpose, model_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_model_policy_one_default
  ON model_policies(purpose) WHERE is_default = TRUE AND is_approved = TRUE;

CREATE TABLE IF NOT EXISTS user_model_preferences (
  user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  translation_model VARCHAR(200),
  summary_model VARCHAR(200),
  chat_model VARCHAR(200),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS admin_audit_logs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  actor_user_id UUID REFERENCES users(id),
  action VARCHAR(80) NOT NULL,
  target_type VARCHAR(80) NOT NULL,
  target_id VARCHAR(320),
  details JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_admin_audit_logs_created
  ON admin_audit_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_usage_logs_user_created
  ON usage_logs(user_id, created_at DESC);

-- Make the shipped defaults visible even before the first successful provider
-- refresh. Availability will be updated by /models synchronization.
INSERT INTO provider_models (provider, model_id, source)
VALUES
  ('openai-compatible', 'gpt-5.6-luna', 'builtin'),
  ('openai-compatible', 'gpt-5.6-terra', 'builtin'),
  ('openai-compatible', 'gpt-5.6-sol', 'builtin'),
  ('openai-compatible', 'gpt-5', 'builtin'),
  ('openai-compatible', 'gpt-5-mini', 'builtin'),
  ('openai-compatible', 'gpt-5-nano', 'builtin'),
  ('openai-compatible', 'gpt-4o', 'builtin'),
  ('openai-compatible', 'gpt-4o-mini', 'builtin'),
  ('openai-compatible', 'text-embedding-3-small', 'builtin'),
  ('openai-compatible', 'text-embedding-3-large', 'builtin')
ON CONFLICT (provider, model_id) DO NOTHING;

INSERT INTO model_policies
  (purpose, model_id, is_approved, is_default, cost_confirmed)
VALUES
  ('translation', 'gpt-5.6-luna', TRUE, TRUE, TRUE),
  ('summary', 'gpt-5.6-sol', TRUE, TRUE, TRUE),
  ('chat', 'gpt-5.6-sol', TRUE, TRUE, TRUE),
  ('embedding', 'text-embedding-3-small', TRUE, TRUE, TRUE)
ON CONFLICT (purpose, model_id) DO NOTHING;
