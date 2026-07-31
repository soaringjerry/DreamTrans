-- Make cost-plus billing observable and safely configurable without changing
-- immutable historical charges.

ALTER TABLE billing_config
  ADD COLUMN IF NOT EXISTS pricing_state VARCHAR(30) NOT NULL DEFAULT 'legacy_active';

-- Draft commercial settings must never affect actual charges before the
-- administrator explicitly applies the current catalog.
ALTER TABLE billing_config
  ADD COLUMN IF NOT EXISTS pending_config JSONB;

-- Managed retail prices are provider-specific. Keep this nullable so pricing
-- rules created before cost-plus billing remain a compatible fallback.
ALTER TABLE pricing_rules
  ADD COLUMN IF NOT EXISTS provider VARCHAR(60);

UPDATE pricing_rules
SET provider = CASE
  WHEN managed_key LIKE 'alias:%'
    AND LOWER(COALESCE(model, '')) LIKE 'speechmatics%'
    THEN 'speechmatics'
  WHEN managed_key NOT LIKE 'alias:%'
    THEN LOWER(SPLIT_PART(managed_key, ':', 1))
  ELSE NULL
END
WHERE source = 'managed'
  AND provider IS NULL
  AND managed_key IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_pricing_rules_active_provider_model
  ON pricing_rules(rule_type, provider, model, unit_type)
  WHERE is_active = TRUE;

ALTER TABLE billing_config
  DROP CONSTRAINT IF EXISTS billing_config_pricing_state_check;
ALTER TABLE billing_config
  ADD CONSTRAINT billing_config_pricing_state_check
  CHECK (pricing_state IN ('legacy_active', 'managed_active'));

-- Preserve an explicit administrator choice. Migration 017's untouched seed is
-- identifiable by its legacy catalog marker and NULL actor.
UPDATE billing_config
SET default_markup_percent = 50,
    updated_at = NOW()
WHERE catalog_version = 'legacy-compatible'
  AND default_markup_percent = 100
  AND updated_by IS NULL;

-- Likewise, only replace the original signup-credit seed. Custom values and
-- values saved through the admin API have a non-NULL actor and are retained.
UPDATE system_settings
SET value = '1'::jsonb,
    description = 'Initial Dreampoints for new users',
    updated_at = NOW()
WHERE key = 'free_tier_dreampoints'
  AND value = '100'::jsonb
  AND updated_by IS NULL
  AND description = 'Initial Dreampoints for new users';

INSERT INTO system_settings (key, value, description)
VALUES (
  'allow_user_api_key',
  'false'::jsonb,
  'Allow users to use their own provider API key'
)
ON CONFLICT (key) DO NOTHING;

UPDATE billing_config
SET pricing_state = CASE
  WHEN EXISTS (
    SELECT 1
    FROM pricing_rules
    WHERE source = 'managed' AND is_active = TRUE
  ) THEN 'managed_active'
  ELSE 'legacy_active'
END
WHERE singleton = TRUE;

CREATE TABLE IF NOT EXISTS provider_cost_overrides (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider VARCHAR(60) NOT NULL,
  sku VARCHAR(200) NOT NULL,
  service VARCHAR(50) NOT NULL,
  unit_type VARCHAR(30) NOT NULL,
  cost_per_unit_usd DECIMAL(24,12) NOT NULL
    CHECK (cost_per_unit_usd >= 0),
  source_label VARCHAR(120) NOT NULL DEFAULT 'contract',
  effective_at TIMESTAMP WITH TIME ZONE,
  updated_by UUID REFERENCES users(id),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (provider, sku, service, unit_type)
);

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS cost_attribution VARCHAR(30) NOT NULL DEFAULT 'legacy_unknown';

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS settled_at TIMESTAMP WITH TIME ZONE;

-- Earlier v2 snapshots already carried an explicit settlement marker. Preserve
-- their one-shot semantics when upgrading without guessing about older rows.
UPDATE usage_logs
SET settled_at = created_at
WHERE settled_at IS NULL
  AND pricing_snapshot->>'settled_from_reservation_snapshot' = 'true';

ALTER TABLE usage_logs
  DROP CONSTRAINT IF EXISTS usage_logs_cost_attribution_check;
ALTER TABLE usage_logs
  ADD CONSTRAINT usage_logs_cost_attribution_check
  CHECK (
    cost_attribution IN (
      'provider_priced',
      'byok',
      'non_provider',
      'legacy_unknown',
      'unpriced'
    )
  );

-- Classify what migration 017 already recorded, but never rewrite cost,
-- upstream_cost_usd, service_fee_dp, or pricing_snapshot.
UPDATE usage_logs
SET cost_attribution = CASE
  WHEN action = 'rag_query'
    AND COALESCE(input_tokens, 0) = 0
    AND COALESCE(output_tokens, 0) = 0
    THEN 'non_provider'
  WHEN pricing_snapshot->>'attribution' = 'byok'
    THEN 'byok'
  WHEN pricing_snapshot->>'attribution' = 'provider_priced'
    THEN 'provider_priced'
  WHEN upstream_cost_usd <> 0
    OR (
      pricing_snapshot ? 'rates_usd'
      AND jsonb_typeof(pricing_snapshot->'rates_usd') = 'object'
      AND pricing_snapshot->'rates_usd' <> '{}'::jsonb
    )
    THEN 'provider_priced'
  ELSE 'legacy_unknown'
END
WHERE cost_attribution = 'legacy_unknown';

CREATE INDEX IF NOT EXISTS idx_usage_logs_cost_attribution
  ON usage_logs(cost_attribution)
  WHERE refunded_at IS NULL;

-- Provider model synchronization status must survive process restarts.
CREATE TABLE IF NOT EXISTS provider_model_sync_status (
  provider VARCHAR(60) PRIMARY KEY,
  status VARCHAR(40) NOT NULL DEFAULT 'builtin_unverified'
    CHECK (
      status IN (
        'provider_confirmed',
        'builtin_unverified',
        'temporarily_unavailable'
      )
    ),
  last_attempt_at TIMESTAMP WITH TIME ZONE,
  last_success_at TIMESTAMP WITH TIME ZONE,
  last_error TEXT NOT NULL DEFAULT '',
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

INSERT INTO provider_model_sync_status (
  provider,
  status,
  last_attempt_at,
  last_success_at,
  updated_at
)
SELECT
  'openai-compatible',
  CASE
    WHEN EXISTS (
      SELECT 1
      FROM provider_models
      WHERE provider = 'openai-compatible'
        AND source IN ('provider', 'builtin+provider')
    ) THEN 'provider_confirmed'
    ELSE 'builtin_unverified'
  END,
  MAX(last_seen_at) FILTER (
    WHERE source IN ('provider', 'builtin+provider')
  ),
  MAX(last_seen_at) FILTER (
    WHERE source IN ('provider', 'builtin+provider')
  ),
  COALESCE(
    MAX(last_seen_at) FILTER (
      WHERE source IN ('provider', 'builtin+provider')
    ),
    NOW()
  )
FROM provider_models
WHERE provider = 'openai-compatible'
ON CONFLICT (provider) DO NOTHING;
