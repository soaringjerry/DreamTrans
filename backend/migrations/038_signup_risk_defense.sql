-- Strict mode requires human approval for every new self-registration reward.
ALTER TABLE signup_risk_settings
    ADD COLUMN strict_mode BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN network_burst_limit INT NOT NULL DEFAULT 3 CHECK (network_burst_limit BETWEEN 1 AND 10000),
    ADD COLUMN prefix_hourly_limit INT NOT NULL DEFAULT 10 CHECK (prefix_hourly_limit BETWEEN 1 AND 100000),
    ADD COLUMN daily_reward_budget_cents BIGINT NOT NULL DEFAULT 10000 CHECK (daily_reward_budget_cents BETWEEN 0 AND 100000000);
ALTER TABLE signup_risk_profiles
    ADD COLUMN fingerprint_hash VARCHAR(64),
    ADD COLUMN prefix_hash VARCHAR(64),
    ADD COLUMN score INT NOT NULL DEFAULT 0 CHECK (score BETWEEN 0 AND 100),
    ADD COLUMN evidence JSONB NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(evidence)='object'),
    ADD COLUMN budget_holds JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(budget_holds)='array'),
    ADD COLUMN budget_blocked BOOLEAN NOT NULL DEFAULT FALSE;
CREATE INDEX signup_risk_prefix ON signup_risk_profiles(prefix_hash,created_at DESC) WHERE prefix_hash IS NOT NULL;
-- Independent of user deletion and grant expiry. Entries commit with actual grants.
CREATE TABLE signup_risk_reward_spend (
    receipt_key TEXT PRIMARY KEY,
    amount_usd NUMERIC(20,6) NOT NULL CHECK (amount_usd >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX signup_risk_reward_spend_created ON signup_risk_reward_spend(created_at);

CREATE INDEX signup_risk_fingerprint ON signup_risk_profiles(fingerprint_hash,created_at DESC) WHERE fingerprint_hash IS NOT NULL;
