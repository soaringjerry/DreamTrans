-- Experimental project concept maps are stored as a new AI artifact type.
-- Content is a server-validated JSON document (see handlers/concept_map.go).

ALTER TABLE ai_artifacts
  DROP CONSTRAINT IF EXISTS ai_artifacts_artifact_type_check;
ALTER TABLE ai_artifacts
  ADD CONSTRAINT ai_artifacts_artifact_type_check
  CHECK (artifact_type IN ('summary', 'notes', 'action_items', 'concept_map'));
