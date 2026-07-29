-- UsageLog.MonthKey is a required aggregation key. Older schemas allowed NULL
-- and the legacy INSERT path did not populate it, which both broke Scan into a
-- Go string and silently excluded records from monthly quota totals.
UPDATE usage_logs
SET created_at = NOW(),
    month_key = TO_CHAR(NOW() AT TIME ZONE 'UTC', 'YYYY-MM')
WHERE created_at IS NULL
   OR NOT isfinite(created_at);

UPDATE usage_logs
SET month_key = TO_CHAR(created_at AT TIME ZONE 'UTC', 'YYYY-MM')
WHERE month_key IS NULL
   OR month_key !~ '^[0-9]{4}-(0[1-9]|1[0-2])$';

ALTER TABLE usage_logs
  ALTER COLUMN month_key
    SET DEFAULT TO_CHAR(CURRENT_TIMESTAMP AT TIME ZONE 'UTC', 'YYYY-MM'),
  ALTER COLUMN month_key SET NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'usage_logs_month_key_format'
      AND conrelid = 'usage_logs'::regclass
  ) THEN
    ALTER TABLE usage_logs
      ADD CONSTRAINT usage_logs_month_key_format
      CHECK (month_key ~ '^[0-9]{4}-(0[1-9]|1[0-2])$');
  END IF;
END
$$;
