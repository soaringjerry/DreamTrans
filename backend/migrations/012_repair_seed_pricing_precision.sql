-- Migration 002 originally inserted sub-micro token prices while the column
-- was DECIMAL(12,6). PostgreSQL rounded those seed values before migration 006
-- widened the column, so simply changing the type could not recover them.
-- Match the canonical seed descriptions to avoid overwriting administrator
-- rules that deliberately use a different price.

UPDATE pricing_rules
SET price_per_unit = CASE unit_type
  WHEN 'input_token' THEN 0.0000001500
  WHEN 'output_token' THEN 0.0000006000
END
WHERE rule_type = 'translation'
  AND model = 'gpt-4o-mini'
  AND priority = 10
  AND (
    (unit_type = 'input_token' AND description = 'GPT-4o-mini translation input')
    OR
    (unit_type = 'output_token' AND description = 'GPT-4o-mini translation output')
  );

UPDATE pricing_rules
SET price_per_unit = CASE unit_type
  WHEN 'input_token' THEN 0.0000001500
  WHEN 'output_token' THEN 0.0000006000
END
WHERE rule_type = 'chat'
  AND model = 'gpt-4o-mini'
  AND priority = 10
  AND (
    (unit_type = 'input_token' AND description = 'GPT-4o-mini chat input')
    OR
    (unit_type = 'output_token' AND description = 'GPT-4o-mini chat output')
  );
