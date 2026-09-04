-- Email verification for self-service registration.
--
-- Accounts that already exist were either created by an administrator or
-- registered before verification existed; locking them out now would strand
-- paying customers, so they are grandfathered in as verified.
UPDATE users SET email_verified = true WHERE email_verified = false;

-- Canonical form used to stop one inbox from farming trial credit through
-- aliases: lower-cased, "+tag" removed, and dots removed for Gmail. Kept as a
-- non-unique index so legacy collisions never block the migration; the
-- registration handler enforces uniqueness for new sign-ups.
ALTER TABLE users ADD COLUMN IF NOT EXISTS email_canonical VARCHAR(255);

UPDATE users
SET email_canonical = (
  CASE
    WHEN lower(split_part(email, '@', 2)) IN ('gmail.com', 'googlemail.com')
      THEN replace(split_part(lower(split_part(email, '@', 1)), '+', 1), '.', '') || '@gmail.com'
    ELSE split_part(lower(split_part(email, '@', 1)), '+', 1) || '@' || lower(split_part(email, '@', 2))
  END
)
WHERE email_canonical IS NULL;

CREATE INDEX IF NOT EXISTS idx_users_email_canonical ON users(email_canonical);

CREATE TABLE IF NOT EXISTS email_verification_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash CHAR(64) NOT NULL UNIQUE,
  expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
  used_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_email_verification_tokens_user
  ON email_verification_tokens(user_id, created_at DESC);
