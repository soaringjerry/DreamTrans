-- Channel attribution is fixed at registration; rewards are claimed exactly once.
CREATE TABLE promotion_invites (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code VARCHAR(48) NOT NULL UNIQUE CHECK (code ~ '^[A-Z0-9][A-Z0-9_-]{5,47}$'),
    name VARCHAR(100) NOT NULL,
    channel VARCHAR(100) NOT NULL,
    tags JSONB NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(tags) = 'array'),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    expires_at TIMESTAMPTZ NOT NULL,
    max_registrations INT NOT NULL CHECK (max_registrations BETWEEN 1 AND 1000000),
    grant_usd DECIMAL(18,8) NOT NULL DEFAULT 0 CHECK (grant_usd BETWEEN 0 AND 10000),
    grant_days INT NOT NULL DEFAULT 30 CHECK (grant_days BETWEEN 1 AND 3650),
    plan_code VARCHAR(40) REFERENCES plans(code),
    plan_days INT NOT NULL DEFAULT 30 CHECK (plan_days BETWEEN 1 AND 3650),
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE promotion_registrations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invite_id UUID NOT NULL REFERENCES promotion_invites(id),
    user_id UUID UNIQUE REFERENCES users(id) ON DELETE SET NULL,
    canonical_email_hash VARCHAR(64) NOT NULL UNIQUE,
    registered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rewarded_at TIMESTAMPTZ,
    grant_id UUID REFERENCES grants(id) ON DELETE SET NULL,
    plan_until TIMESTAMPTZ
);
CREATE INDEX promotion_registrations_invite ON promotion_registrations(invite_id, registered_at DESC);
