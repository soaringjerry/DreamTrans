-- Reward eligibility, not an account ban. Existing users keep their eligibility.
CREATE TABLE signup_risk_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    device_limit INT NOT NULL DEFAULT 1 CHECK (device_limit BETWEEN 1 AND 100),
    network_daily_limit INT NOT NULL DEFAULT 5 CHECK (network_daily_limit BETWEEN 1 AND 10000),
    automatic_daily_limit INT NOT NULL DEFAULT 100 CHECK (automatic_daily_limit BETWEEN 1 AND 100000),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);
INSERT INTO signup_risk_settings(singleton) VALUES(TRUE);
CREATE TABLE signup_risk_profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID UNIQUE REFERENCES users(id) ON DELETE SET NULL,
    email_hash VARCHAR(64) NOT NULL,
    device_hash VARCHAR(64),
    network_hash VARCHAR(64),
    decision VARCHAR(20) NOT NULL CHECK (decision IN ('allowed','review','approved','denied')),
    reasons JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(reasons)='array'),
    device_count INT NOT NULL DEFAULT 0,
    network_count INT NOT NULL DEFAULT 0,
    daily_count INT NOT NULL DEFAULT 0,
    rules JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    reviewed_at TIMESTAMPTZ,
    reviewed_by UUID REFERENCES users(id) ON DELETE SET NULL,
    review_note VARCHAR(500) NOT NULL DEFAULT ''
);
CREATE INDEX signup_risk_email ON signup_risk_profiles(email_hash);
CREATE INDEX signup_risk_device ON signup_risk_profiles(device_hash,created_at DESC) WHERE device_hash IS NOT NULL;
CREATE INDEX signup_risk_network ON signup_risk_profiles(network_hash,created_at DESC) WHERE network_hash IS NOT NULL;
CREATE INDEX signup_risk_created ON signup_risk_profiles(created_at DESC);
CREATE INDEX signup_risk_queue ON signup_risk_profiles(decision,created_at DESC);
CREATE TABLE signup_risk_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id UUID REFERENCES signup_risk_profiles(id) ON DELETE CASCADE,
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(40) NOT NULL,
    details JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
