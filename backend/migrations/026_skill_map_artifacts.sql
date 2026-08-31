-- 学习模式 M1: the course Skill Map replaces the experimental concept map.
-- Content is one server-validated JSON skillMapDocument per project
-- (see handlers/skill_map.go). Stored concept maps are dropped with their
-- feature; they were regenerable snapshots, not source data.

DELETE FROM ai_artifacts WHERE artifact_type = 'concept_map';

ALTER TABLE ai_artifacts
  DROP CONSTRAINT IF EXISTS ai_artifacts_artifact_type_check;
ALTER TABLE ai_artifacts
  ADD CONSTRAINT ai_artifacts_artifact_type_check
  CHECK (artifact_type IN ('summary', 'notes', 'action_items', 'skill_map'));
