-- Pro membership now includes three concurrent live transcriptions. Only
-- installs still on the seeded value move; an operator who tuned the plan in
-- the admin console keeps their number.
UPDATE plans SET max_concurrent_sessions = 3
WHERE code = 'pro' AND max_concurrent_sessions = 2;
