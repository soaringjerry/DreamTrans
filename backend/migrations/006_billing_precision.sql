-- Token-priced operations are often far below four decimal places. Preserve
-- enough precision so many small requests cannot round every debit to zero.
ALTER TABLE users
  ALTER COLUMN dreampoints TYPE DECIMAL(18,8),
  ALTER COLUMN dreampoints_used TYPE DECIMAL(18,8);

ALTER TABLE pricing_rules
  ALTER COLUMN price_per_unit TYPE DECIMAL(18,10),
  ALTER COLUMN model TYPE VARCHAR(200);

ALTER TABLE usage_logs
  ALTER COLUMN cost TYPE DECIMAL(18,10),
  ALTER COLUMN model TYPE VARCHAR(200);

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS refunded_at TIMESTAMP WITH TIME ZONE;

ALTER TABLE balance_transactions
  ALTER COLUMN amount TYPE DECIMAL(18,8),
  ALTER COLUMN balance_after TYPE DECIMAL(18,8);

-- Older admin endpoints accepted unsafe negative/NaN prices. Disable and
-- neutralize those rows before enforcing database-level invariants.
UPDATE pricing_rules
SET price_per_unit = 0, is_active = false
WHERE price_per_unit < 0 OR price_per_unit = 'NaN'::numeric;

UPDATE pricing_rules
SET priority = GREATEST(-1000, LEAST(1000, priority));

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'pricing_rules_nonnegative_price'
  ) THEN
    ALTER TABLE pricing_rules
      ADD CONSTRAINT pricing_rules_nonnegative_price
      CHECK (price_per_unit >= 0 AND price_per_unit <> 'NaN'::numeric);
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'pricing_rules_priority_range'
  ) THEN
    ALTER TABLE pricing_rules
      ADD CONSTRAINT pricing_rules_priority_range
      CHECK (priority BETWEEN -1000 AND 1000);
  END IF;
END
$$;

-- Default is configurable by administrators; this seeds a conservative
-- per-input-token rule for text-embedding workloads.
INSERT INTO pricing_rules
  (rule_type, model, price_per_unit, unit_type, description, priority)
SELECT
  'embedding', NULL, 0.0000000200, 'input_token',
  'Default embedding input token rate', 0
WHERE NOT EXISTS (
  SELECT 1 FROM pricing_rules
  WHERE rule_type = 'embedding' AND model IS NULL AND unit_type = 'input_token'
);
