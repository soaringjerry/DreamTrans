-- Enforce tenants.api_quota_monthly with a compact, transactional counter.
-- Provider-facing requests consume one unit before upstream work begins.

CREATE TABLE IF NOT EXISTS tenant_api_usage (
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  month_key VARCHAR(7) NOT NULL,
  request_count BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  PRIMARY KEY (tenant_id, month_key),
  CONSTRAINT tenant_api_usage_month_key_format
    CHECK (month_key ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
  CONSTRAINT tenant_api_usage_nonnegative_count
    CHECK (request_count >= 0),
  CONSTRAINT tenant_api_usage_finite_updated_at
    CHECK (isfinite(updated_at))
);
