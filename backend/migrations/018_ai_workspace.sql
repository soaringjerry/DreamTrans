-- User-controlled AI artifacts, projects, and explicit knowledge sources.

CREATE TABLE IF NOT EXISTS ai_projects (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name VARCHAR(160) NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  context_mode VARCHAR(20) NOT NULL DEFAULT 'smart'
    CHECK (context_mode IN ('smart', 'full', 'retrieval')),
  max_context_tokens INT NOT NULL DEFAULT 64000
    CHECK (max_context_tokens BETWEEN 1024 AND 256000),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS project_sessions (
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  PRIMARY KEY (project_id, session_id),
  UNIQUE (session_id)
);

CREATE TABLE IF NOT EXISTS knowledge_sources (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_type VARCHAR(20) NOT NULL
    CHECK (source_type IN ('file', 'memory')),
  name VARCHAR(255) NOT NULL,
  media_type VARCHAR(120) NOT NULL DEFAULT 'text/plain',
  size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  sha256 VARCHAR(64) NOT NULL DEFAULT '',
  blob_path TEXT NOT NULL DEFAULT '',
  status VARCHAR(20) NOT NULL DEFAULT 'queued'
    CHECK (status IN ('queued', 'processing', 'ready', 'error')),
  error_message TEXT NOT NULL DEFAULT '',
  chunk_count INT NOT NULL DEFAULT 0 CHECK (chunk_count >= 0),
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS ai_artifacts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id UUID REFERENCES sessions(id) ON DELETE CASCADE,
  project_id UUID REFERENCES ai_projects(id) ON DELETE SET NULL,
  artifact_type VARCHAR(24) NOT NULL
    CHECK (artifact_type IN ('summary', 'notes', 'action_items')),
  title VARCHAR(255) NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  context_policy JSONB NOT NULL DEFAULT '{}'::jsonb,
  context_tokens INT NOT NULL DEFAULT 0,
  model VARCHAR(200) NOT NULL DEFAULT '',
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS knowledge_chunks (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  source_id UUID NOT NULL REFERENCES knowledge_sources(id) ON DELETE CASCADE,
  project_id UUID NOT NULL REFERENCES ai_projects(id) ON DELETE CASCADE,
  ordinal INT NOT NULL CHECK (ordinal >= 0),
  content TEXT NOT NULL,
  vector REAL[] NOT NULL,
  created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  UNIQUE (source_id, ordinal)
);

CREATE INDEX IF NOT EXISTS idx_ai_projects_user_updated
  ON ai_projects(user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_project_sessions_project
  ON project_sessions(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_sources_project
  ON knowledge_sources(project_id, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_knowledge_sources_project_sha
  ON knowledge_sources(project_id, sha256) WHERE sha256 <> '';
CREATE INDEX IF NOT EXISTS idx_ai_artifacts_session
  ON ai_artifacts(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_ai_artifacts_project
  ON ai_artifacts(project_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_knowledge_chunks_project
  ON knowledge_chunks(project_id, source_id, ordinal);

DROP TRIGGER IF EXISTS update_ai_projects_updated_at ON ai_projects;
CREATE TRIGGER update_ai_projects_updated_at BEFORE UPDATE ON ai_projects
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_knowledge_sources_updated_at ON knowledge_sources;
CREATE TRIGGER update_knowledge_sources_updated_at BEFORE UPDATE ON knowledge_sources
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();

DROP TRIGGER IF EXISTS update_ai_artifacts_updated_at ON ai_artifacts;
CREATE TRIGGER update_ai_artifacts_updated_at BEFORE UPDATE ON ai_artifacts
  FOR EACH ROW EXECUTE FUNCTION update_updated_at_column();
