-- Dreampoint Balance System Migration
-- Run this after 001_init.sql

-- Add balance field to users
ALTER TABLE users ADD COLUMN IF NOT EXISTS dreampoints DECIMAL(12,4) DEFAULT 0;
ALTER TABLE users ADD COLUMN IF NOT EXISTS dreampoints_used DECIMAL(12,4) DEFAULT 0;

-- Pricing rules table
CREATE TABLE IF NOT EXISTS pricing_rules (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  -- Rule identification
  rule_type VARCHAR(50) NOT NULL, -- 'transcription', 'translation', 'chat', 'summarize'
  model VARCHAR(100), -- NULL means default, otherwise specific model like 'gpt-4o', 'gpt-4o-mini'
  -- Pricing (in Dreampoints)
  price_per_unit DECIMAL(12,6) NOT NULL, -- per minute for transcription, per 1M tokens for LLM
  unit_type VARCHAR(20) NOT NULL, -- 'minute', 'hour', 'input_token', 'output_token'
  -- Metadata
  description TEXT,
  is_active BOOLEAN DEFAULT true,
  priority INT DEFAULT 0, -- higher priority rules override lower ones
  -- Timestamps
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Add cost tracking to usage_logs
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS cost DECIMAL(12,6) DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS model VARCHAR(100);
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS input_tokens INT;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS output_tokens INT;

-- Balance transactions log
CREATE TABLE IF NOT EXISTS balance_transactions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  -- Transaction details
  amount DECIMAL(12,4) NOT NULL, -- positive for credit, negative for debit
  balance_after DECIMAL(12,4) NOT NULL,
  transaction_type VARCHAR(30) NOT NULL, -- 'credit', 'debit', 'refund', 'adjustment'
  -- Reference
  reference_type VARCHAR(30), -- 'usage', 'topup', 'admin_adjustment'
  reference_id UUID, -- usage_log id or other reference
  -- Metadata
  description TEXT,
  created_by UUID REFERENCES users(id),
  created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- System settings table for global configuration
CREATE TABLE IF NOT EXISTS system_settings (
  key VARCHAR(100) PRIMARY KEY,
  value JSONB NOT NULL,
  description TEXT,
  updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
  updated_by UUID REFERENCES users(id)
);

-- Indexes
CREATE INDEX IF NOT EXISTS idx_pricing_rules_type ON pricing_rules(rule_type);
CREATE INDEX IF NOT EXISTS idx_pricing_rules_model ON pricing_rules(model);
CREATE INDEX IF NOT EXISTS idx_balance_transactions_user ON balance_transactions(user_id);
CREATE INDEX IF NOT EXISTS idx_balance_transactions_created ON balance_transactions(created_at);

-- Trigger for pricing_rules updated_at
DROP TRIGGER IF EXISTS update_pricing_rules_updated_at ON pricing_rules;
CREATE TRIGGER update_pricing_rules_updated_at BEFORE UPDATE ON pricing_rules
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

-- Insert default pricing rules
-- Transcription: 1.05/hr = 0.0175/min
INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT 'transcription', NULL, 0.0175, 'minute', 'Base transcription rate (Speechmatics)', 0
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE rule_type = 'transcription' AND model IS NULL AND unit_type = 'minute' AND priority = 0
);

-- Translation (default model): input 3/1M, output 12/1M
INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT seed.*
FROM (
  VALUES
    ('translation', NULL::VARCHAR, 0.000003, 'input_token', 'Default translation input tokens', 0),
    ('translation', NULL::VARCHAR, 0.000012, 'output_token', 'Default translation output tokens', 0)
) AS seed(rule_type, model, price_per_unit, unit_type, description, priority)
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules existing
  WHERE existing.rule_type = seed.rule_type
    AND existing.model IS NOT DISTINCT FROM seed.model
    AND existing.unit_type = seed.unit_type
    AND existing.priority = seed.priority
);

-- Chat (default model): same as translation
INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT seed.*
FROM (
  VALUES
    ('chat', NULL::VARCHAR, 0.000003, 'input_token', 'Default chat input tokens', 0),
    ('chat', NULL::VARCHAR, 0.000012, 'output_token', 'Default chat output tokens', 0)
) AS seed(rule_type, model, price_per_unit, unit_type, description, priority)
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules existing
  WHERE existing.rule_type = seed.rule_type
    AND existing.model IS NOT DISTINCT FROM seed.model
    AND existing.unit_type = seed.unit_type
    AND existing.priority = seed.priority
);

-- Summarize (default model)
INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT seed.*
FROM (
  VALUES
    ('summarize', NULL::VARCHAR, 0.000003, 'input_token', 'Default summarize input tokens', 0),
    ('summarize', NULL::VARCHAR, 0.000012, 'output_token', 'Default summarize output tokens', 0)
) AS seed(rule_type, model, price_per_unit, unit_type, description, priority)
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules existing
  WHERE existing.rule_type = seed.rule_type
    AND existing.model IS NOT DISTINCT FROM seed.model
    AND existing.unit_type = seed.unit_type
    AND existing.priority = seed.priority
);

-- GPT-4o specific pricing (higher)
INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT seed.*
FROM (
  VALUES
    ('translation', 'gpt-4o', 0.000005, 'input_token', 'GPT-4o translation input', 10),
    ('translation', 'gpt-4o', 0.000015, 'output_token', 'GPT-4o translation output', 10),
    ('chat', 'gpt-4o', 0.000005, 'input_token', 'GPT-4o chat input', 10),
    ('chat', 'gpt-4o', 0.000015, 'output_token', 'GPT-4o chat output', 10)
) AS seed(rule_type, model, price_per_unit, unit_type, description, priority)
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules existing
  WHERE existing.rule_type = seed.rule_type
    AND existing.model IS NOT DISTINCT FROM seed.model
    AND existing.unit_type = seed.unit_type
    AND existing.priority = seed.priority
);

-- GPT-4o-mini specific pricing (cheaper)
INSERT INTO pricing_rules (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT seed.*
FROM (
  VALUES
    ('translation', 'gpt-4o-mini', 0.00000015, 'input_token', 'GPT-4o-mini translation input', 10),
    ('translation', 'gpt-4o-mini', 0.0000006, 'output_token', 'GPT-4o-mini translation output', 10),
    ('chat', 'gpt-4o-mini', 0.00000015, 'input_token', 'GPT-4o-mini chat input', 10),
    ('chat', 'gpt-4o-mini', 0.0000006, 'output_token', 'GPT-4o-mini chat output', 10)
) AS seed(rule_type, model, price_per_unit, unit_type, description, priority)
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules existing
  WHERE existing.rule_type = seed.rule_type
    AND existing.model IS NOT DISTINCT FROM seed.model
    AND existing.unit_type = seed.unit_type
    AND existing.priority = seed.priority
);

-- Insert default system settings
INSERT INTO system_settings (key, value, description)
VALUES
  ('billing_enabled', 'true', 'Enable/disable Dreampoint billing'),
  ('free_tier_dreampoints', '100', 'Initial Dreampoints for new users'),
  ('allow_negative_balance', 'false', 'Allow users to go negative on balance')
ON CONFLICT DO NOTHING;
