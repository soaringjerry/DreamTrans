-- Keep earlier lessons, rubrics and questions for historical attempts while
-- selecting new teaching content by the explicitly generated skill-map version.
ALTER TABLE study_lessons ADD COLUMN IF NOT EXISTS content_version TEXT NOT NULL DEFAULT '';
ALTER TABLE study_rubrics ADD COLUMN IF NOT EXISTS content_version TEXT NOT NULL DEFAULT '';
ALTER TABLE study_scenarios ADD COLUMN IF NOT EXISTS content_version TEXT NOT NULL DEFAULT '';
ALTER TABLE study_lessons DROP CONSTRAINT IF EXISTS study_lessons_project_id_skill_key_key;
ALTER TABLE study_rubrics DROP CONSTRAINT IF EXISTS study_rubrics_project_id_skill_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS study_lessons_version_key ON study_lessons(project_id, skill_key, content_version);
CREATE UNIQUE INDEX IF NOT EXISTS study_rubrics_version_key ON study_rubrics(project_id, skill_key, content_version);
CREATE INDEX IF NOT EXISTS study_scenarios_version_idx ON study_scenarios(user_id, project_id, skill_key, content_version, difficulty) WHERE status='active';
