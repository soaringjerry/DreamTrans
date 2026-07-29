-- Disable the legacy well-known administrator account without deleting any
-- sessions or audit records that reference it. Accounts whose password was
-- already changed are intentionally left untouched.
UPDATE users
SET is_active = false,
    password_hash = 'disabled-insecure-default-account',
    updated_at = NOW()
WHERE email = 'admin@dreamtrans.local'
  AND role = 'super_admin'
  AND password_hash = '$2a$10$DEoAtxRrvaAbHFrSSgw3uu.rhEuoc3UJr2ctVDEooZv96sRC.7Eie';
