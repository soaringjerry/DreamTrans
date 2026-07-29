-- Keep transcript storage accounting incremental and enforce it at the
-- database boundary. This covers every writer, including future maintenance
-- jobs that do not go through the Go store.

ALTER TABLE transcripts
  ADD COLUMN IF NOT EXISTS tenant_id UUID;

UPDATE transcripts AS transcript
SET tenant_id = session.tenant_id
FROM sessions AS session
WHERE session.id = transcript.session_id
  AND transcript.tenant_id IS NULL;

ALTER TABLE transcripts
  ALTER COLUMN tenant_id SET NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1
    FROM pg_constraint
    WHERE conname = 'transcripts_tenant_id_fkey'
      AND conrelid = 'transcripts'::regclass
  ) THEN
    ALTER TABLE transcripts
      ADD CONSTRAINT transcripts_tenant_id_fkey
      FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE;
  END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_transcripts_tenant
  ON transcripts(tenant_id);

CREATE TABLE IF NOT EXISTS tenant_storage_usage (
  tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  transcript_bytes BIGINT NOT NULL DEFAULT 0,
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT tenant_storage_usage_nonnegative
    CHECK (transcript_bytes >= 0),
  CONSTRAINT tenant_storage_usage_finite_updated_at
    CHECK (isfinite(updated_at))
);

INSERT INTO tenant_storage_usage (tenant_id, transcript_bytes, updated_at)
SELECT
  tenant.id,
  COALESCE(SUM(
    octet_length(transcript.speaker)::BIGINT
    + octet_length(transcript.text)::BIGINT
    + COALESCE(octet_length(transcript.translation), 0)::BIGINT
  ), 0),
  NOW()
FROM tenants AS tenant
LEFT JOIN transcripts AS transcript ON transcript.tenant_id = tenant.id
GROUP BY tenant.id
ON CONFLICT (tenant_id) DO UPDATE SET
  transcript_bytes = EXCLUDED.transcript_bytes,
  updated_at = EXCLUDED.updated_at;

CREATE OR REPLACE FUNCTION set_transcript_tenant()
RETURNS TRIGGER AS $$
DECLARE
  session_tenant_id UUID;
BEGIN
  IF TG_OP = 'UPDATE'
     AND (NEW.session_id <> OLD.session_id OR NEW.tenant_id <> OLD.tenant_id) THEN
    RAISE EXCEPTION 'a transcript cannot be moved between sessions or tenants'
      USING ERRCODE = 'check_violation',
            CONSTRAINT = 'transcripts_immutable_owner';
  END IF;

  SELECT tenant_id
  INTO session_tenant_id
  FROM sessions
  WHERE id = NEW.session_id;

  IF session_tenant_id IS NULL THEN
    RAISE EXCEPTION 'transcript session does not exist'
      USING ERRCODE = 'foreign_key_violation',
            CONSTRAINT = 'transcripts_session_id_fkey';
  END IF;

  IF NEW.tenant_id IS NULL THEN
    NEW.tenant_id := session_tenant_id;
  ELSIF NEW.tenant_id <> session_tenant_id THEN
    RAISE EXCEPTION 'transcript tenant does not match its session'
      USING ERRCODE = 'check_violation',
            CONSTRAINT = 'transcripts_tenant_matches_session';
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS set_transcript_tenant_before_write ON transcripts;
CREATE TRIGGER set_transcript_tenant_before_write
BEFORE INSERT OR UPDATE OF session_id, tenant_id ON transcripts
FOR EACH ROW EXECUTE FUNCTION set_transcript_tenant();

CREATE OR REPLACE FUNCTION update_tenant_transcript_storage()
RETURNS TRIGGER AS $$
DECLARE
  affected_tenant_id UUID;
  quota_gb INTEGER;
  old_bytes BIGINT := 0;
  new_bytes BIGINT := 0;
  current_bytes BIGINT;
  next_bytes BIGINT;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    old_bytes :=
      octet_length(OLD.speaker)::BIGINT
      + octet_length(OLD.text)::BIGINT
      + COALESCE(octet_length(OLD.translation), 0)::BIGINT;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    new_bytes :=
      octet_length(NEW.speaker)::BIGINT
      + octet_length(NEW.text)::BIGINT
      + COALESCE(octet_length(NEW.translation), 0)::BIGINT;
  END IF;

  IF TG_OP = 'DELETE' THEN
    affected_tenant_id := OLD.tenant_id;
  ELSE
    affected_tenant_id := NEW.tenant_id;
  END IF;
  SELECT storage_quota_gb
  INTO quota_gb
  FROM tenants
  WHERE id = affected_tenant_id
  FOR UPDATE;

  -- A tenant cascade is deleting this row and the counter alongside it.
  IF NOT FOUND THEN
    IF TG_OP = 'DELETE' THEN
      RETURN OLD;
    END IF;
    RETURN NEW;
  END IF;

  INSERT INTO tenant_storage_usage (tenant_id)
  VALUES (affected_tenant_id)
  ON CONFLICT (tenant_id) DO NOTHING;

  SELECT transcript_bytes
  INTO current_bytes
  FROM tenant_storage_usage
  WHERE tenant_id = affected_tenant_id
  FOR UPDATE;

  next_bytes := current_bytes + new_bytes - old_bytes;
  IF next_bytes < 0 THEN
    RAISE EXCEPTION 'transcript storage counter would become negative'
      USING ERRCODE = 'data_exception',
            CONSTRAINT = 'tenant_storage_usage_nonnegative';
  END IF;

  -- If an administrator lowered a quota below current usage, idempotent
  -- retries and net reductions remain possible. Only additional bytes fail.
  IF new_bytes > old_bytes
     AND quota_gb >= 0
     AND next_bytes > quota_gb::BIGINT * 1073741824 THEN
    RAISE EXCEPTION 'tenant transcript storage quota exceeded'
      USING ERRCODE = 'check_violation',
            CONSTRAINT = 'tenant_transcript_storage_quota';
  END IF;

  UPDATE tenant_storage_usage
  SET transcript_bytes = next_bytes,
      updated_at = NOW()
  WHERE tenant_id = affected_tenant_id;

  IF TG_OP = 'DELETE' THEN
    RETURN OLD;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS update_tenant_transcript_storage_after_write ON transcripts;
CREATE TRIGGER update_tenant_transcript_storage_after_write
AFTER INSERT OR UPDATE OR DELETE ON transcripts
FOR EACH ROW EXECUTE FUNCTION update_tenant_transcript_storage();
