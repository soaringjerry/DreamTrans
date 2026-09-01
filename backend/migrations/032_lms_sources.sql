-- Moodle Sync: course materials synced by the browser extension arrive as
-- derived text (per-page text + OCR of figure pages), never as the original
-- file. They are a third source type so the UI can tell them apart from
-- uploads and memories, and carry where they came from (host, course,
-- section, cmid, timemodified) for incremental sync.

ALTER TABLE knowledge_sources
  DROP CONSTRAINT IF EXISTS knowledge_sources_source_type_check;
ALTER TABLE knowledge_sources
  ADD CONSTRAINT knowledge_sources_source_type_check
  CHECK (source_type IN ('file', 'memory', 'lms'));

ALTER TABLE knowledge_sources
  ADD COLUMN IF NOT EXISTS lms JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_knowledge_sources_lms_module
  ON knowledge_sources(project_id, (lms->>'host'), (lms->>'cmid'))
  WHERE source_type = 'lms';
