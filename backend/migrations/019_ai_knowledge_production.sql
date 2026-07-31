-- Production AI knowledge indexing. Existing REAL[] vectors remain untouched
-- as the free/legacy retrieval path; semantic embeddings are never backfilled
-- by this migration.

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

ALTER TABLE knowledge_sources
  ADD COLUMN IF NOT EXISTS memory_content TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS ocr_languages TEXT[] NOT NULL
    DEFAULT ARRAY['eng', 'chi_sim']::TEXT[],
  ADD COLUMN IF NOT EXISTS extracted_text_bytes BIGINT NOT NULL DEFAULT 0
    CHECK (extracted_text_bytes >= 0),
  ADD COLUMN IF NOT EXISTS vector_bytes BIGINT NOT NULL DEFAULT 0
    CHECK (vector_bytes >= 0),
  ADD COLUMN IF NOT EXISTS index_status VARCHAR(20) NOT NULL DEFAULT 'unindexed'
    CHECK (index_status IN (
      'unindexed', 'queued', 'processing', 'ready', 'stale', 'error'
    )),
  ADD COLUMN IF NOT EXISTS embedding_model VARCHAR(200) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS embedding_dimensions INT NOT NULL DEFAULT 0
    CHECK (embedding_dimensions IN (0, 1536)),
  ADD COLUMN IF NOT EXISTS embedded_chunk_count INT NOT NULL DEFAULT 0
    CHECK (embedded_chunk_count >= 0),
  ADD COLUMN IF NOT EXISTS index_error_message TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS indexed_at TIMESTAMP WITH TIME ZONE,
  ADD COLUMN IF NOT EXISTS extract_lease_owner VARCHAR(200) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS extract_lease_expires_at TIMESTAMP WITH TIME ZONE,
  ADD COLUMN IF NOT EXISTS extract_attempts INT NOT NULL DEFAULT 0
    CHECK (extract_attempts >= 0),
  ADD COLUMN IF NOT EXISTS extract_max_attempts INT NOT NULL DEFAULT 3
    CHECK (extract_max_attempts BETWEEN 1 AND 20);

ALTER TABLE knowledge_sources
  ADD CONSTRAINT knowledge_sources_ocr_languages_valid
  CHECK (
    cardinality(ocr_languages) BETWEEN 1 AND 4
    AND ocr_languages <@ ARRAY['eng', 'chi_sim', 'jpn', 'kor']::TEXT[]
  ) NOT VALID;

ALTER TABLE knowledge_sources
  VALIDATE CONSTRAINT knowledge_sources_ocr_languages_valid;

ALTER TABLE knowledge_chunks
  ADD COLUMN IF NOT EXISTS embedding VECTOR(1536),
  ADD COLUMN IF NOT EXISTS embedding_model VARCHAR(200) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS embedding_status VARCHAR(20) NOT NULL DEFAULT 'unindexed'
    CHECK (embedding_status IN (
      'unindexed', 'queued', 'processing', 'ready', 'stale', 'error'
    )),
  ADD COLUMN IF NOT EXISTS embedding_error TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS token_count INT NOT NULL DEFAULT 0
    CHECK (token_count >= 0),
  ADD COLUMN IF NOT EXISTS embedded_at TIMESTAMP WITH TIME ZONE;

-- Match ai.EstimateTokens' tokenizer-independent upper bound. This is a free
-- metadata repair only: it does not create embeddings or invoke a provider.
UPDATE knowledge_chunks
SET token_count=GREATEST(token_count, octet_length(content))
WHERE token_count < octet_length(content);

ALTER TABLE sessions
  ADD COLUMN IF NOT EXISTS ai_chunks_content_hash CHAR(64) NOT NULL DEFAULT ''
    CHECK (
      ai_chunks_content_hash = ''
      OR ai_chunks_content_hash ~ '^[0-9a-f]{64}$'
    );

ALTER TABLE usage_logs
  ADD COLUMN IF NOT EXISTS provider_operation_fingerprint CHAR(64)
    NOT NULL DEFAULT ''
    CHECK (
      provider_operation_fingerprint = ''
      OR provider_operation_fingerprint ~ '^[0-9a-f]{64}$'
    ),
  ADD COLUMN IF NOT EXISTS settled_at TIMESTAMP WITH TIME ZONE;

-- Reconcile free, already-stored data for quota accounting and restore the
-- editable raw value for memories created before memory_content existed. This
-- does not create semantic vectors or provider work.
WITH chunk_storage AS (
  SELECT
    source_id,
    COALESCE(SUM(octet_length(content)::BIGINT), 0) AS text_bytes,
    COALESCE(SUM(COALESCE(cardinality(vector), 0)::BIGINT * 4), 0) AS vector_bytes,
    string_agg(content, E'\n' ORDER BY ordinal) AS reconstructed_content
  FROM knowledge_chunks
  GROUP BY source_id
)
UPDATE knowledge_sources source
SET extracted_text_bytes=storage.text_bytes,
    vector_bytes=storage.vector_bytes,
    memory_content=CASE
      WHEN source.source_type='memory' AND source.memory_content=''
      THEN COALESCE(storage.reconstructed_content, '')
      ELSE source.memory_content
    END
FROM chunk_storage storage
WHERE source.id=storage.source_id;

CREATE TABLE IF NOT EXISTS session_ai_chunks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  ordinal INT NOT NULL CHECK (ordinal >= 0),
  content TEXT NOT NULL,
  token_count INT NOT NULL DEFAULT 0 CHECK (token_count >= 0),
  embedding VECTOR(1536),
  embedding_model VARCHAR(200) NOT NULL DEFAULT '',
  embedding_status VARCHAR(20) NOT NULL DEFAULT 'unindexed'
    CHECK (embedding_status IN (
      'unindexed', 'queued', 'processing', 'ready', 'stale', 'error'
    )),
  embedding_error TEXT NOT NULL DEFAULT '',
  embedded_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (session_id, ordinal)
);

CREATE TABLE IF NOT EXISTS ai_index_jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  target_type VARCHAR(20) NOT NULL
    CHECK (target_type IN ('project', 'source', 'session')),
  project_id UUID REFERENCES ai_projects(id) ON DELETE CASCADE,
  source_id UUID REFERENCES knowledge_sources(id) ON DELETE CASCADE,
  session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
  model VARCHAR(200) NOT NULL,
  dimensions INT NOT NULL DEFAULT 1536 CHECK (dimensions = 1536),
  status VARCHAR(20) NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'processing', 'ready', 'error', 'cancelled')),
  chunk_count INT NOT NULL DEFAULT 0 CHECK (chunk_count >= 0),
  processed_chunks INT NOT NULL DEFAULT 0
    CHECK (processed_chunks >= 0 AND processed_chunks <= chunk_count),
  estimated_tokens BIGINT NOT NULL DEFAULT 0 CHECK (estimated_tokens >= 0),
  actual_tokens BIGINT NOT NULL DEFAULT 0 CHECK (actual_tokens >= 0),
  estimated_dp NUMERIC(20, 8) NOT NULL DEFAULT 0 CHECK (estimated_dp >= 0),
  content_digest CHAR(64) NOT NULL
    CHECK (content_digest ~ '^[0-9a-f]{64}$'),
  client_request_id VARCHAR(128) NOT NULL DEFAULT '',
  error_message TEXT NOT NULL DEFAULT '',
  lease_owner VARCHAR(200) NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMP WITH TIME ZONE,
  attempt_count INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  max_attempts INT NOT NULL DEFAULT 3 CHECK (max_attempts BETWEEN 1 AND 20),
  cancel_requested_at TIMESTAMP WITH TIME ZONE,
  started_at TIMESTAMP WITH TIME ZONE,
  finished_at TIMESTAMP WITH TIME ZONE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CONSTRAINT ai_index_jobs_target_matches_type CHECK (
    (target_type = 'project' AND project_id IS NOT NULL
      AND source_id IS NULL AND session_id IS NULL)
    OR
    (target_type = 'source' AND source_id IS NOT NULL
      AND project_id IS NULL AND session_id IS NULL)
    OR
    (target_type = 'session' AND session_id IS NOT NULL
      AND project_id IS NULL AND source_id IS NULL)
  )
);

-- Immutable paid-work snapshot. Workers only embed these confirmed chunk
-- identities and hashes, so target edits cannot silently add newly billable
-- content to an already-confirmed job.
CREATE TABLE IF NOT EXISTS ai_index_job_chunks (
  job_id UUID NOT NULL REFERENCES ai_index_jobs(id) ON DELETE CASCADE,
  chunk_id UUID NOT NULL,
  chunk_order INT NOT NULL CHECK (chunk_order >= 0),
  content_hash CHAR(64) NOT NULL
    CHECK (content_hash ~ '^[0-9a-f]{64}$'),
  PRIMARY KEY (job_id, chunk_id),
  UNIQUE (job_id, chunk_order)
);

CREATE TABLE IF NOT EXISTS knowledge_blob_deletions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL,
  user_id UUID NOT NULL,
  blob_path TEXT NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'processing', 'error')),
  lease_owner VARCHAR(200) NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMP WITH TIME ZONE,
  attempt_count INT NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
  max_attempts INT NOT NULL DEFAULT 20 CHECK (max_attempts BETWEEN 1 AND 100),
  error_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (blob_path)
);

CREATE TABLE IF NOT EXISTS ai_generation_requests (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
  client_request_id VARCHAR(128) NOT NULL,
  request_kind VARCHAR(32) NOT NULL,
  request_hash CHAR(64) NOT NULL
    CHECK (request_hash ~ '^[0-9a-f]{64}$'),
  status VARCHAR(20) NOT NULL DEFAULT 'processing'
    CHECK (status IN ('processing', 'ready', 'error')),
  response_json JSONB,
  lease_owner VARCHAR(200) NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMP WITH TIME ZONE,
  attempt_count INT NOT NULL DEFAULT 1 CHECK (attempt_count >= 1),
  error_message TEXT NOT NULL DEFAULT '',
  completed_at TIMESTAMP WITH TIME ZONE,
  expires_at TIMESTAMP WITH TIME ZONE NOT NULL
    DEFAULT (NOW() + INTERVAL '24 hours'),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  CHECK (client_request_id <> ''),
  CHECK (request_kind <> ''),
  CONSTRAINT ai_generation_requests_state_valid CHECK (
    (
      status='processing'
      AND response_json IS NULL
      AND lease_owner<>''
      AND lease_expires_at IS NOT NULL
      AND completed_at IS NULL
    )
    OR
    (
      status='ready'
      AND response_json IS NOT NULL
      AND lease_owner=''
      AND lease_expires_at IS NULL
      AND completed_at IS NOT NULL
    )
    OR
    (
      status='error'
      AND response_json IS NULL
      AND lease_owner=''
      AND lease_expires_at IS NULL
      AND completed_at IS NOT NULL
    )
  ),
  UNIQUE (tenant_id, user_id, client_request_id)
);

ALTER TABLE ai_artifacts
  ADD COLUMN IF NOT EXISTS client_request_id VARCHAR(128) NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS request_hash CHAR(64) NOT NULL DEFAULT ''
    CHECK (request_hash = '' OR request_hash ~ '^[0-9a-f]{64}$'),
  ADD COLUMN IF NOT EXISTS replay_response JSONB NOT NULL DEFAULT '{}'::JSONB,
  ADD COLUMN IF NOT EXISTS content_bytes BIGINT GENERATED ALWAYS AS (
    octet_length(content)::BIGINT
  ) STORED;

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_artifacts_user_request
  ON ai_artifacts(user_id, client_request_id)
  WHERE client_request_id <> '';

CREATE INDEX IF NOT EXISTS idx_knowledge_sources_extract_claim
  ON knowledge_sources(status, extract_lease_expires_at, created_at)
  WHERE source_type = 'file' AND status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_knowledge_sources_project_index_status
  ON knowledge_sources(project_id, index_status, created_at);

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_embedding_hnsw
  ON knowledge_chunks USING hnsw (embedding vector_cosine_ops)
  WHERE embedding IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_content_trgm
  ON knowledge_chunks USING gin (content gin_trgm_ops);

CREATE INDEX IF NOT EXISTS idx_session_ai_chunks_owner
  ON session_ai_chunks(tenant_id, user_id, session_id, ordinal);

CREATE INDEX IF NOT EXISTS idx_session_ai_chunks_embedding_hnsw
  ON session_ai_chunks USING hnsw (embedding vector_cosine_ops)
  WHERE embedding IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_session_ai_chunks_content_trgm
  ON session_ai_chunks USING gin (content gin_trgm_ops);

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_index_jobs_user_request
  ON ai_index_jobs(user_id, client_request_id)
  WHERE client_request_id <> '';

CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_index_jobs_active_target
  ON ai_index_jobs(
    user_id,
    target_type,
    COALESCE(project_id, source_id, session_id)
  )
  WHERE status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_ai_index_jobs_claim
  ON ai_index_jobs(status, lease_expires_at, created_at)
  WHERE status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_knowledge_blob_deletions_claim
  ON knowledge_blob_deletions(status, lease_expires_at, created_at)
  WHERE status IN ('queued', 'processing');

CREATE INDEX IF NOT EXISTS idx_ai_index_jobs_owner_created
  ON ai_index_jobs(tenant_id, user_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_ai_generation_requests_lease
  ON ai_generation_requests(status, lease_expires_at)
  WHERE status='processing';

CREATE INDEX IF NOT EXISTS idx_ai_generation_requests_expiry
  ON ai_generation_requests(expires_at);

DROP TRIGGER IF EXISTS update_session_ai_chunks_updated_at ON session_ai_chunks;
CREATE TRIGGER update_session_ai_chunks_updated_at
  BEFORE UPDATE ON session_ai_chunks
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_ai_index_jobs_updated_at ON ai_index_jobs;
CREATE TRIGGER update_ai_index_jobs_updated_at
  BEFORE UPDATE ON ai_index_jobs
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_knowledge_blob_deletions_updated_at
  ON knowledge_blob_deletions;
CREATE TRIGGER update_knowledge_blob_deletions_updated_at
  BEFORE UPDATE ON knowledge_blob_deletions
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_ai_generation_requests_updated_at
  ON ai_generation_requests;
CREATE TRIGGER update_ai_generation_requests_updated_at
  BEFORE UPDATE ON ai_generation_requests
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
