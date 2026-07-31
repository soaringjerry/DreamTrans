package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/lib/pq"
)

func (s *PostgresStore) CreateAIArtifact(ctx context.Context, artifact *models.AIArtifact) error {
	policy, err := json.Marshal(artifact.ContextPolicy)
	if err != nil {
		return err
	}
	return s.db.QueryRowContext(ctx, `
		INSERT INTO ai_artifacts (
			tenant_id, user_id, session_id, project_id, artifact_type, title,
			content, context_policy, context_tokens, model
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, created_at, updated_at
	`, artifact.TenantID, artifact.UserID, artifact.SessionID, artifact.ProjectID,
		artifact.ArtifactType, artifact.Title, artifact.Content, policy,
		artifact.ContextTokens, artifact.Model,
	).Scan(&artifact.ID, &artifact.CreatedAt, &artifact.UpdatedAt)
}

func (s *PostgresStore) ListAIArtifacts(
	ctx context.Context, userID, sessionID string, limit int,
) ([]models.AIArtifact, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, user_id, session_id, project_id, artifact_type,
		       title, content, context_policy, context_tokens, model,
		       created_at, updated_at
		FROM ai_artifacts
		WHERE user_id=$1 AND ($2='' OR session_id::text=$2)
		ORDER BY created_at DESC LIMIT $3
	`, userID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make([]models.AIArtifact, 0)
	for rows.Next() {
		var artifact models.AIArtifact
		var policy []byte
		if err := rows.Scan(
			&artifact.ID, &artifact.TenantID, &artifact.UserID,
			&artifact.SessionID, &artifact.ProjectID, &artifact.ArtifactType,
			&artifact.Title, &artifact.Content, &policy, &artifact.ContextTokens,
			&artifact.Model, &artifact.CreatedAt, &artifact.UpdatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(policy, &artifact.ContextPolicy)
		out = append(out, artifact)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateAIProject(ctx context.Context, project *models.AIProject) error {
	return s.db.QueryRowContext(ctx, `
		INSERT INTO ai_projects (
			tenant_id, user_id, name, description, context_mode, max_context_tokens
		) VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id, created_at, updated_at
	`, project.TenantID, project.UserID, project.Name, project.Description,
		project.ContextMode, project.MaxContextTokens,
	).Scan(&project.ID, &project.CreatedAt, &project.UpdatedAt)
}

func (s *PostgresStore) ListAIProjects(ctx context.Context, userID string) ([]models.AIProject, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, user_id, name, description, context_mode,
		       max_context_tokens, created_at, updated_at
		FROM ai_projects WHERE user_id=$1 ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	projects := make([]models.AIProject, 0)
	for rows.Next() {
		var project models.AIProject
		if err := rows.Scan(
			&project.ID, &project.TenantID, &project.UserID, &project.Name,
			&project.Description, &project.ContextMode, &project.MaxContextTokens,
			&project.CreatedAt, &project.UpdatedAt,
		); err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (s *PostgresStore) GetAIProject(
	ctx context.Context, projectID, userID string,
) (*models.AIProject, error) {
	var project models.AIProject
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, name, description, context_mode,
		       max_context_tokens, created_at, updated_at
		FROM ai_projects WHERE id=$1 AND user_id=$2
	`, projectID, userID).Scan(
		&project.ID, &project.TenantID, &project.UserID, &project.Name,
		&project.Description, &project.ContextMode, &project.MaxContextTokens,
		&project.CreatedAt, &project.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &project, err
}

func (s *PostgresStore) UpdateAIProject(ctx context.Context, project *models.AIProject) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE ai_projects SET name=$1, description=$2, context_mode=$3,
			max_context_tokens=$4 WHERE id=$5 AND user_id=$6
	`, project.Name, project.Description, project.ContextMode,
		project.MaxContextTokens, project.ID, project.UserID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) DeleteAIProject(ctx context.Context, projectID, userID string) error {
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM ai_projects WHERE id=$1 AND user_id=$2`, projectID, userID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *PostgresStore) LinkProjectSession(
	ctx context.Context, projectID, sessionID, userID string,
) error {
	var allowed bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM ai_projects p
			JOIN sessions s ON s.user_id=p.user_id
			WHERE p.id=$1 AND s.id=$2 AND p.user_id=$3
		)
	`, projectID, sessionID, userID).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return sql.ErrNoRows
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO project_sessions(project_id, session_id) VALUES($1,$2)
		ON CONFLICT(session_id) DO UPDATE SET project_id=excluded.project_id
	`, projectID, sessionID)
	return err
}

func (s *PostgresStore) CreateKnowledgeSource(
	ctx context.Context, source *models.KnowledgeSource,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var quotaGB int
	if err := tx.QueryRowContext(ctx, `
		SELECT storage_quota_gb FROM tenants WHERE id=$1 FOR UPDATE
	`, source.TenantID).Scan(&quotaGB); err != nil {
		return err
	}
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((SELECT transcript_bytes FROM tenant_storage_usage WHERE tenant_id=$1), 0)
		  + COALESCE((SELECT SUM(size_bytes) FROM knowledge_sources WHERE tenant_id=$1), 0)
	`, source.TenantID).Scan(&usedBytes); err != nil {
		return err
	}
	if source.SizeBytes < 0 || source.SizeBytes > math.MaxInt64-usedBytes ||
		exceedsStorageQuota(quotaGB, usedBytes+source.SizeBytes) {
		return ErrStorageQuota
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO knowledge_sources (
			project_id, tenant_id, user_id, source_type, name, media_type,
			size_bytes, sha256, blob_path, status, error_message, chunk_count
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING id, created_at, updated_at
	`, source.ProjectID, source.TenantID, source.UserID, source.SourceType,
		source.Name, source.MediaType, source.SizeBytes, source.SHA256,
		source.BlobPath, source.Status, source.ErrorMessage, source.ChunkCount,
	).Scan(&source.ID, &source.CreatedAt, &source.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ReplaceKnowledgeChunks(
	ctx context.Context, source *models.KnowledgeSource, chunks []models.KnowledgeChunk,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM knowledge_chunks WHERE source_id=$1`, source.ID); err != nil {
		return err
	}
	for _, chunk := range chunks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_chunks(source_id, project_id, ordinal, content, vector)
			VALUES($1,$2,$3,$4,$5)
		`, source.ID, source.ProjectID, chunk.Ordinal, chunk.Content, pq.Array(chunk.Vector)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE knowledge_sources SET status='ready', error_message='', chunk_count=$1
		WHERE id=$2 AND user_id=$3
	`, len(chunks), source.ID, source.UserID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ListProjectKnowledgeChunks(
	ctx context.Context, projectID, userID string, limit int,
) ([]models.KnowledgeChunk, error) {
	if limit <= 0 || limit > 20_000 {
		limit = 10_000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.source_id, c.project_id, c.ordinal, c.content, c.vector, s.name
		FROM knowledge_chunks c
		JOIN knowledge_sources s ON s.id=c.source_id
		WHERE c.project_id=$1 AND s.user_id=$2 AND s.status='ready'
		ORDER BY s.created_at DESC, c.ordinal ASC
		LIMIT $3
	`, projectID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	chunks := make([]models.KnowledgeChunk, 0)
	for rows.Next() {
		var chunk models.KnowledgeChunk
		if err := rows.Scan(
			&chunk.ID, &chunk.SourceID, &chunk.ProjectID, &chunk.Ordinal,
			&chunk.Content, pq.Array(&chunk.Vector), &chunk.SourceName,
		); err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (s *PostgresStore) UpdateKnowledgeSourceStatus(
	ctx context.Context, id, userID, status, errorMessage string, chunkCount int,
) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_sources SET status=$1, error_message=$2, chunk_count=$3
		WHERE id=$4 AND user_id=$5
	`, status, errorMessage, chunkCount, id, userID)
	return err
}

func (s *PostgresStore) ListKnowledgeSources(
	ctx context.Context, projectID, userID string,
) ([]models.KnowledgeSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, tenant_id, user_id, source_type, name, media_type,
		       size_bytes, sha256, blob_path, status, error_message, chunk_count,
		       created_at, updated_at
		FROM knowledge_sources WHERE project_id=$1 AND user_id=$2
		ORDER BY created_at DESC
	`, projectID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	sources := make([]models.KnowledgeSource, 0)
	for rows.Next() {
		var source models.KnowledgeSource
		if err := rows.Scan(
			&source.ID, &source.ProjectID, &source.TenantID, &source.UserID,
			&source.SourceType, &source.Name, &source.MediaType,
			&source.SizeBytes, &source.SHA256, &source.BlobPath, &source.Status,
			&source.ErrorMessage, &source.ChunkCount, &source.CreatedAt,
			&source.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *PostgresStore) ListPendingKnowledgeSources(
	ctx context.Context, limit int,
) ([]models.KnowledgeSource, error) {
	if limit <= 0 || limit > 1_000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, tenant_id, user_id, source_type, name, media_type,
		       size_bytes, sha256, blob_path, status, error_message, chunk_count,
		       created_at, updated_at
		FROM knowledge_sources
		WHERE source_type='file' AND status IN ('queued', 'processing')
		ORDER BY created_at ASC LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	sources := make([]models.KnowledgeSource, 0)
	for rows.Next() {
		var source models.KnowledgeSource
		if err := rows.Scan(
			&source.ID, &source.ProjectID, &source.TenantID, &source.UserID,
			&source.SourceType, &source.Name, &source.MediaType,
			&source.SizeBytes, &source.SHA256, &source.BlobPath, &source.Status,
			&source.ErrorMessage, &source.ChunkCount, &source.CreatedAt,
			&source.UpdatedAt,
		); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *PostgresStore) DeleteKnowledgeSource(
	ctx context.Context, sourceID, projectID, userID string,
) (string, error) {
	var blobPath string
	err := s.db.QueryRowContext(ctx, `
		DELETE FROM knowledge_sources
		WHERE id=$1 AND project_id=$2 AND user_id=$3
		RETURNING blob_path
	`, sourceID, projectID, userID).Scan(&blobPath)
	return blobPath, err
}

func ValidateAIProject(project *models.AIProject) error {
	project.Name = strings.TrimSpace(project.Name)
	project.Description = strings.TrimSpace(project.Description)
	project.ContextMode = strings.ToLower(strings.TrimSpace(project.ContextMode))
	if project.Name == "" || len([]rune(project.Name)) > 160 {
		return fmt.Errorf("project name is required and must be at most 160 characters")
	}
	if len([]rune(project.Description)) > 10_000 {
		return fmt.Errorf("project description is too long")
	}
	switch project.ContextMode {
	case "smart", "full", "retrieval":
	default:
		return fmt.Errorf("invalid context mode")
	}
	if project.MaxContextTokens < 1024 || project.MaxContextTokens > 256000 {
		return fmt.Errorf("max_context_tokens must be between 1024 and 256000")
	}
	return nil
}
