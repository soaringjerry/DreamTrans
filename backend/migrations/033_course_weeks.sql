-- 按周学习: a course knows when its week 1 starts, so sessions, synced
-- materials and skills can be grouped by teaching week and the study page
-- can say "这周该学什么" and "第 N 周还没补". NULL means "infer from the
-- earliest linked session".

ALTER TABLE ai_projects
  ADD COLUMN IF NOT EXISTS week_start DATE;
