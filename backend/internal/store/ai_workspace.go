package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/dreamtrans/backend/internal/models"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

func (s *PostgresStore) CreateAIArtifact(ctx context.Context, artifact *models.AIArtifact) error {
	_, err := s.CreateAIArtifactIdempotent(ctx, artifact)
	return err
}

// CreateAIArtifactIdempotent inserts an artifact once per non-empty
// client_request_id. It returns true only when this call created the row.
//
//nolint:gocyclo // The transaction keeps replay, quota, and idempotency decisions atomic.
func (s *PostgresStore) CreateAIArtifactIdempotent(
	ctx context.Context, artifact *models.AIArtifact,
) (bool, error) {
	policy, err := json.Marshal(artifact.ContextPolicy)
	if err != nil {
		return false, err
	}
	artifact.ClientRequestID = strings.TrimSpace(artifact.ClientRequestID)
	artifact.RequestHash = strings.ToLower(strings.TrimSpace(artifact.RequestHash))
	if len(artifact.ReplayResponse) == 0 {
		artifact.ReplayResponse = json.RawMessage(`{}`)
	}
	if !json.Valid(artifact.ReplayResponse) {
		return false, fmt.Errorf("artifact replay response must be valid JSON")
	}
	if strings.TrimSpace(artifact.ID) == "" {
		artifact.ID = uuid.NewString()
	} else if uuid.Validate(artifact.ID) != nil {
		return false, fmt.Errorf("artifact id must be a UUID")
	}
	if len(artifact.ClientRequestID) > 128 {
		return false, fmt.Errorf("client_request_id must be at most 128 characters")
	}
	if artifact.RequestHash != "" {
		hash, hashErr := hex.DecodeString(artifact.RequestHash)
		if hashErr != nil || len(hash) != 32 ||
			hex.EncodeToString(hash) != artifact.RequestHash {
			return false, fmt.Errorf("request_hash must be a lowercase SHA-256 digest")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAIStorageOwnerFKGateTx(
		ctx,
		tx,
		artifact.TenantID,
		artifact.UserID,
	); err != nil {
		return false, err
	}
	var quotaGB int
	if err := tx.QueryRowContext(ctx, `
		SELECT storage_quota_gb FROM tenants WHERE id=$1 FOR UPDATE
	`, artifact.TenantID).Scan(&quotaGB); err != nil {
		return false, err
	}
	if artifact.ClientRequestID != "" {
		existing, loadErr := getAIArtifactByClientRequestIDFrom(
			ctx, tx, artifact.TenantID, artifact.UserID, artifact.ClientRequestID,
		)
		if loadErr != nil {
			return false, loadErr
		}
		if existing != nil {
			if existing.ArtifactType != artifact.ArtifactType ||
				!optionalStringEqual(existing.SessionID, artifact.SessionID) ||
				!optionalStringEqual(existing.ProjectID, artifact.ProjectID) ||
				existing.RequestHash != artifact.RequestHash {
				return false, ErrIdempotencyConflict
			}
			*artifact = *existing
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return false, nil
		}
	}
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((
		    SELECT COALESCE(SUM(a.storage_bytes), 0) FROM billing_accounts a JOIN users u ON u.billing_account_id = a.id WHERE u.tenant_id = $1
		  ), 0)
		  + COALESCE((
		      SELECT SUM(size_bytes + extracted_text_bytes + vector_bytes)
		      FROM knowledge_sources WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(content_bytes) FROM ai_artifacts WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(
		        octet_length(content)::bigint
		        + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		      )
		      FROM session_ai_chunks WHERE tenant_id=$1
		    ), 0)
	`, artifact.TenantID).Scan(&usedBytes); err != nil {
		return false, err
	}
	contentBytes := int64(len(artifact.Content))
	if contentBytes > math.MaxInt64-usedBytes ||
		exceedsStorageQuota(quotaGB, usedBytes+contentBytes) {
		return false, ErrStorageQuota
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO ai_artifacts (
			id, tenant_id, user_id, session_id, project_id, artifact_type, title,
			content, context_policy, context_tokens, model, client_request_id
			, request_hash, replay_response
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
		WHERE
		  EXISTS (
		    SELECT 1 FROM users
		    WHERE id=$3 AND tenant_id=$2 AND is_active=true
		  )
		  AND
		  ($4::uuid IS NULL OR EXISTS (
		    SELECT 1 FROM sessions
		    WHERE id=$4 AND tenant_id=$2 AND user_id=$3
		  ))
		  AND
		  ($5::uuid IS NULL OR EXISTS (
		    SELECT 1 FROM ai_projects
		    WHERE id=$5 AND tenant_id=$2 AND user_id=$3
		  ))
		ON CONFLICT (user_id, client_request_id)
		  WHERE client_request_id <> ''
		DO NOTHING
		RETURNING id, content_bytes, created_at, updated_at
	`, artifact.ID, artifact.TenantID, artifact.UserID,
		artifact.SessionID, artifact.ProjectID,
		artifact.ArtifactType, artifact.Title, artifact.Content, policy,
		artifact.ContextTokens, artifact.Model, artifact.ClientRequestID,
		artifact.RequestHash, artifact.ReplayResponse,
	).Scan(&artifact.ID, &artifact.ContentBytes, &artifact.CreatedAt, &artifact.UpdatedAt)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return false, err
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) || artifact.ClientRequestID == "" {
		return false, err
	}

	existing, loadErr := getAIArtifactByClientRequestIDFrom(
		ctx, tx, artifact.TenantID, artifact.UserID, artifact.ClientRequestID,
	)
	if loadErr != nil {
		return false, loadErr
	}
	if existing == nil {
		return false, sql.ErrNoRows
	}
	if existing.ArtifactType != artifact.ArtifactType ||
		!optionalStringEqual(existing.SessionID, artifact.SessionID) ||
		!optionalStringEqual(existing.ProjectID, artifact.ProjectID) ||
		existing.RequestHash != artifact.RequestHash {
		return false, ErrIdempotencyConflict
	}
	*artifact = *existing
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
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
		       client_request_id, request_hash, replay_response,
		       content_bytes, created_at, updated_at
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
			&artifact.Model, &artifact.ClientRequestID, &artifact.RequestHash,
			&artifact.ReplayResponse, &artifact.ContentBytes,
			&artifact.CreatedAt, &artifact.UpdatedAt,
		); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(policy, &artifact.ContextPolicy)
		out = append(out, artifact)
	}
	return out, rows.Err()
}

func (s *PostgresStore) getAIArtifactByClientRequestID(
	ctx context.Context, tenantID, userID, clientRequestID string,
) (*models.AIArtifact, error) {
	return getAIArtifactByClientRequestIDFrom(
		ctx, s.db, tenantID, userID, clientRequestID,
	)
}

func getAIArtifactByClientRequestIDFrom(
	ctx context.Context,
	db queryRower,
	tenantID, userID, clientRequestID string,
) (*models.AIArtifact, error) {
	var artifact models.AIArtifact
	var policy []byte
	err := db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, session_id, project_id, artifact_type,
		       title, content, context_policy, context_tokens, model,
		       client_request_id, request_hash, replay_response,
		       content_bytes, created_at, updated_at
		FROM ai_artifacts
		WHERE tenant_id=$1 AND user_id=$2 AND client_request_id=$3
	`, tenantID, userID, clientRequestID).Scan(
		&artifact.ID, &artifact.TenantID, &artifact.UserID,
		&artifact.SessionID, &artifact.ProjectID, &artifact.ArtifactType,
		&artifact.Title, &artifact.Content, &policy, &artifact.ContextTokens,
		&artifact.Model, &artifact.ClientRequestID, &artifact.RequestHash,
		&artifact.ReplayResponse, &artifact.ContentBytes,
		&artifact.CreatedAt, &artifact.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(policy, &artifact.ContextPolicy)
	return &artifact, nil
}

func (s *PostgresStore) GetAIArtifactByClientRequestID(
	ctx context.Context, tenantID, userID, clientRequestID string,
) (*models.AIArtifact, error) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return nil, nil
	}
	if len(clientRequestID) > 128 {
		return nil, fmt.Errorf("client_request_id must be at most 128 characters")
	}
	return s.getAIArtifactByClientRequestID(ctx, tenantID, userID, clientRequestID)
}

func (s *PostgresStore) DeleteAIArtifact(
	ctx context.Context, artifactID, tenantID, userID string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var clientRequestID string
	if err := tx.QueryRowContext(ctx, `
		SELECT client_request_id
		FROM ai_artifacts
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		FOR UPDATE
	`, artifactID, tenantID, userID).Scan(&clientRequestID); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM ai_artifacts
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
	`, artifactID, tenantID, userID)
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
	if strings.TrimSpace(clientRequestID) != "" {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM ai_generation_requests
			WHERE tenant_id=$1 AND user_id=$2 AND client_request_id=$3
			  AND request_kind='artifact'
		`, tenantID, userID, clientRequestID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PostgresStore) CreateAIProject(ctx context.Context, project *models.AIProject) error {
	return s.db.QueryRowContext(ctx, `
		INSERT INTO ai_projects (
			tenant_id, user_id, name, description, context_mode, max_context_tokens
		)
		SELECT $1,$2,$3,$4,$5,$6
		FROM users
		WHERE id=$2 AND tenant_id=$1 AND is_active=true
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

func (s *PostgresStore) ListAIProjectsWithLinked(
	ctx context.Context, tenantID, userID, sessionID string,
) (*models.AIProjectList, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, tenant_id, user_id, name, description, context_mode,
		       max_context_tokens, created_at, updated_at
		FROM ai_projects
		WHERE tenant_id=$1 AND user_id=$2
		ORDER BY updated_at DESC
	`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := &models.AIProjectList{Projects: make([]models.AIProject, 0)}
	for rows.Next() {
		var project models.AIProject
		if err := rows.Scan(
			&project.ID, &project.TenantID, &project.UserID, &project.Name,
			&project.Description, &project.ContextMode, &project.MaxContextTokens,
			&project.CreatedAt, &project.UpdatedAt,
		); err != nil {
			return nil, err
		}
		result.Projects = append(result.Projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return result, nil
	}
	var linkedID string
	err = s.db.QueryRowContext(ctx, `
		SELECT ps.project_id
		FROM project_sessions ps
		JOIN ai_projects p ON p.id=ps.project_id
		JOIN sessions se ON se.id=ps.session_id
		WHERE ps.session_id=$1
		  AND p.tenant_id=$2 AND p.user_id=$3
		  AND se.tenant_id=$2 AND se.user_id=$3
	`, sessionID, tenantID, userID).Scan(&linkedID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	result.LinkedProjectID = &linkedID
	return result, nil
}

func (s *PostgresStore) GetLinkedAIProject(
	ctx context.Context, tenantID, userID, sessionID string,
) (*models.AIProject, error) {
	var project models.AIProject
	err := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.tenant_id, p.user_id, p.name, p.description,
		       p.context_mode, p.max_context_tokens, p.created_at, p.updated_at
		FROM project_sessions ps
		JOIN ai_projects p ON p.id=ps.project_id
		JOIN sessions se ON se.id=ps.session_id
		WHERE ps.session_id=$1
		  AND p.tenant_id=$2 AND p.user_id=$3
		  AND se.tenant_id=$2 AND se.user_id=$3
	`, sessionID, tenantID, userID).Scan(
		&project.ID, &project.TenantID, &project.UserID, &project.Name,
		&project.Description, &project.ContextMode, &project.MaxContextTokens,
		&project.CreatedAt, &project.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &project, err
}

// GetProjectSessionSourceLanguage returns the source language only when the
// session is owned by the same tenant/user and is currently linked to the
// requested project. This keeps upload-time OCR defaults from becoming a
// session-enumeration or cross-project data access path.
func (s *PostgresStore) GetProjectSessionSourceLanguage(
	ctx context.Context,
	projectID, sessionID, tenantID, userID string,
) (string, error) {
	var sourceLanguage string
	err := s.db.QueryRowContext(ctx, `
		SELECT se.source_language
		FROM project_sessions ps
		JOIN ai_projects p ON p.id=ps.project_id
		JOIN sessions se ON se.id=ps.session_id
		WHERE ps.project_id=$1 AND ps.session_id=$2
		  AND p.tenant_id=$3 AND p.user_id=$4
		  AND se.tenant_id=$3 AND se.user_id=$4
	`, projectID, sessionID, tenantID, userID).Scan(&sourceLanguage)
	return sourceLanguage, err
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
	_, err := s.DeleteAIProjectAndCancelIndexJobs(ctx, projectID, userID)
	return err
}

func (s *PostgresStore) DeleteAIProjectAndCancelIndexJobs(
	ctx context.Context,
	projectID, userID string,
) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var tenantID string
	if err := tx.QueryRowContext(ctx, `
		SELECT tenant_id
		FROM ai_projects
		WHERE id=$1 AND user_id=$2
	`, projectID, userID).Scan(&tenantID); err != nil {
		return nil, err
	}
	cancelledJobIDs, err := coordinateAIIndexScopeMutationTx(
		ctx,
		tx,
		tenantID,
		userID,
		"project",
		projectID,
		aiIndexMutationWhole,
		"",
	)
	if err != nil {
		return nil, err
	}
	var lockedTenantID string
	if err := tx.QueryRowContext(ctx, `
		SELECT tenant_id
		FROM ai_projects
		WHERE id=$1 AND user_id=$2 AND tenant_id=$3
		FOR UPDATE
	`, projectID, userID, tenantID).Scan(&lockedTenantID); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT user_id, blob_path
		FROM knowledge_sources
		WHERE project_id=$1 AND user_id=$2 AND blob_path<>''
		ORDER BY id
		FOR UPDATE
	`, projectID, userID)
	if err != nil {
		return nil, err
	}
	type blobOwner struct {
		userID   string
		blobPath string
	}
	blobs := make([]blobOwner, 0)
	for rows.Next() {
		var sourceUserID, blobPath string
		if err := rows.Scan(&sourceUserID, &blobPath); err != nil {
			_ = rows.Close()
			return nil, err
		}
		blobs = append(blobs, blobOwner{
			userID: sourceUserID, blobPath: blobPath,
		})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, blob := range blobs {
		if err := enqueueKnowledgeBlobDeletionTx(
			ctx, tx, tenantID, blob.userID, blob.blobPath,
		); err != nil {
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx,
		`DELETE FROM ai_projects WHERE id=$1 AND user_id=$2`, projectID, userID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cancelledJobIDs, nil
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

func (s *PostgresStore) UnlinkProjectSession(
	ctx context.Context, projectID, sessionID, tenantID, userID string,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM project_sessions ps
		USING ai_projects p, sessions se
		WHERE ps.project_id=p.id
		  AND ps.session_id=se.id
		  AND ps.project_id=$1 AND ps.session_id=$2
		  AND p.tenant_id=$3 AND p.user_id=$4
		  AND se.tenant_id=$3 AND se.user_id=$4
	`, projectID, sessionID, tenantID, userID)
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

// ListProjectSessions returns full session rows linked to a project, oldest
// first (course order), verifying project and sessions belong to the caller.
func (s *PostgresStore) ListProjectSessions(
	ctx context.Context, tenantID, userID, projectID string,
) ([]models.Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT se.id, se.user_id, se.tenant_id, se.title, se.source_language, se.target_language,
		       se.duration_seconds, se.status, se.started_at, se.ended_at, se.created_at, se.updated_at
		FROM project_sessions ps
		JOIN ai_projects p ON p.id = ps.project_id
		JOIN sessions se ON se.id = ps.session_id
		WHERE ps.project_id = $1
		  AND p.tenant_id = $2 AND p.user_id = $3
		  AND se.tenant_id = $2 AND se.user_id = $3
		ORDER BY se.started_at ASC, se.id ASC
	`, projectID, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	sessions := make([]models.Session, 0)
	for rows.Next() {
		var session models.Session
		if err := rows.Scan(
			&session.ID, &session.UserID, &session.TenantID, &session.Title,
			&session.SourceLanguage, &session.TargetLanguage, &session.DurationSeconds,
			&session.Status, &session.StartedAt, &session.EndedAt,
			&session.CreatedAt, &session.UpdatedAt,
		); err != nil {
			return nil, err
		}
		session.ProjectID = &projectID
		sessions = append(sessions, session)
	}
	return sessions, rows.Err()
}

func (s *PostgresStore) CreateKnowledgeSource(
	ctx context.Context, source *models.KnowledgeSource,
) error {
	if len(source.OCRLanguages) == 0 {
		source.OCRLanguages = []string{"eng", "chi_sim"}
	}
	if err := validateOCRLanguages(source.OCRLanguages); err != nil {
		return err
	}
	if source.SourceType != "memory" {
		source.Content = ""
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockAIStorageOwnerFKGateTx(
		ctx,
		tx,
		source.TenantID,
		source.UserID,
	); err != nil {
		return err
	}
	var quotaGB int
	if err := tx.QueryRowContext(ctx, `
		SELECT storage_quota_gb FROM tenants WHERE id=$1 FOR UPDATE
	`, source.TenantID).Scan(&quotaGB); err != nil {
		return err
	}
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((SELECT COALESCE(SUM(a.storage_bytes), 0) FROM billing_accounts a JOIN users u ON u.billing_account_id = a.id WHERE u.tenant_id = $1), 0)
		  + COALESCE((
		      SELECT SUM(size_bytes + extracted_text_bytes + vector_bytes)
		      FROM knowledge_sources WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(content_bytes) FROM ai_artifacts WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(
		        octet_length(content)::bigint
		        + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		      )
		      FROM session_ai_chunks WHERE tenant_id=$1
		    ), 0)
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
			size_bytes, sha256, blob_path, memory_content, ocr_languages,
			status, error_message, chunk_count
		)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14
		FROM ai_projects
		WHERE id=$1 AND tenant_id=$2 AND user_id=$3
		RETURNING id, index_status, created_at, updated_at
	`, source.ProjectID, source.TenantID, source.UserID, source.SourceType,
		source.Name, source.MediaType, source.SizeBytes, source.SHA256,
		source.BlobPath, source.Content, pq.Array(source.OCRLanguages),
		source.Status, source.ErrorMessage, source.ChunkCount,
	).Scan(&source.ID, &source.IndexStatus, &source.CreatedAt, &source.UpdatedAt); err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) &&
			pqErr.Constraint == "idx_knowledge_sources_project_sha" {
			return ErrDuplicateKnowledgeSource
		}
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) ReplaceKnowledgeChunks(
	ctx context.Context, source *models.KnowledgeSource, chunks []models.KnowledgeChunk,
) error {
	return s.replaceKnowledgeChunks(ctx, source, chunks, "", nil)
}

func (s *PostgresStore) ReplaceKnowledgeChunksForExtraction(
	ctx context.Context,
	source *models.KnowledgeSource,
	chunks []models.KnowledgeChunk,
	workerID string,
) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("worker id is required")
	}
	err := s.replaceKnowledgeChunks(ctx, source, chunks, workerID, nil)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

func (s *PostgresStore) ReplaceKnowledgeChunksForExtractionAndCancelIndexJobs(
	ctx context.Context,
	source *models.KnowledgeSource,
	chunks []models.KnowledgeChunk,
	workerID string,
) ([]string, error) {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return nil, fmt.Errorf("worker id is required")
	}
	var cancelledJobIDs []string
	err := s.replaceKnowledgeChunks(
		ctx,
		source,
		chunks,
		workerID,
		&cancelledJobIDs,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrLeaseLost
	}
	return cancelledJobIDs, err
}

func (s *PostgresStore) replaceKnowledgeChunks(
	ctx context.Context,
	source *models.KnowledgeSource,
	chunks []models.KnowledgeChunk,
	workerID string,
	cancelledJobIDsResult *[]string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var tenantID string
	if err := tx.QueryRowContext(ctx, `
		SELECT ks.tenant_id
		FROM knowledge_sources ks
		WHERE ks.id=$1 AND ks.project_id=$2 AND ks.user_id=$3
		  AND (
		    $4=''
		    OR (
		      ks.status='processing'
		      AND ks.extract_lease_owner=$4
		      AND ks.extract_lease_expires_at > NOW()
		    )
		  )
	`, source.ID, source.ProjectID, source.UserID, workerID).Scan(&tenantID); err != nil {
		return err
	}
	if source.TenantID != "" && tenantID != source.TenantID {
		return sql.ErrNoRows
	}
	cancelledJobIDs, err := coordinateAIIndexScopeMutationTx(
		ctx,
		tx,
		tenantID,
		source.UserID,
		"project",
		source.ProjectID,
		aiIndexMutationSource,
		source.ID,
	)
	if err != nil {
		return err
	}
	quotaGB, err := lockTenantStorageQuota(ctx, tx, tenantID)
	if err != nil {
		return err
	}
	if err := resetCancelledAIIndexTargetsTx(
		ctx,
		tx,
		tenantID,
		source.UserID,
		cancelledJobIDs,
	); err != nil {
		return err
	}
	var lockedTenantID string
	if err := tx.QueryRowContext(ctx, `
		SELECT tenant_id
		FROM knowledge_sources
		WHERE id=$1 AND project_id=$2 AND tenant_id=$3 AND user_id=$4
		  AND (
		    $5=''
		    OR (
		      status='processing'
		      AND extract_lease_owner=$5
		      AND extract_lease_expires_at > NOW()
		    )
		  )
		FOR UPDATE
	`, source.ID, source.ProjectID, tenantID, source.UserID, workerID).Scan(
		&lockedTenantID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM knowledge_chunks WHERE source_id=$1
	`, source.ID); err != nil {
		return err
	}
	var extractedTextBytes int64
	var vectorBytes int64
	reconstructedContent := make([]string, 0, len(chunks))
	for index := range chunks {
		chunk := &chunks[index]
		if chunk.Ordinal < 0 || chunk.TokenCount < 0 {
			return fmt.Errorf("invalid knowledge chunk metadata")
		}
		chunk.TokenCount = conservativeTokenCount(chunk.Content, chunk.TokenCount)
		if int64(len(chunk.Content)) > math.MaxInt64-extractedTextBytes {
			return ErrStorageQuota
		}
		extractedTextBytes += int64(len(chunk.Content))
		reconstructedContent = append(reconstructedContent, chunk.Content)
		if int64(len(chunk.Vector))*4 > math.MaxInt64-vectorBytes {
			return ErrStorageQuota
		}
		vectorBytes += int64(len(chunk.Vector)) * 4
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO knowledge_chunks(
				source_id, project_id, ordinal, content, vector, token_count
			) VALUES(
				$1,$2,$3,$4,
				COALESCE($5::REAL[], ARRAY[]::REAL[]),
				$6
			)
		`, source.ID, source.ProjectID, chunk.Ordinal, chunk.Content,
			pq.Array(chunk.Vector), chunk.TokenCount); err != nil {
			return err
		}
	}
	var usedBytes int64
	if err := tx.QueryRowContext(ctx, `
		SELECT
		  COALESCE((SELECT COALESCE(SUM(a.storage_bytes), 0) FROM billing_accounts a JOIN users u ON u.billing_account_id = a.id WHERE u.tenant_id = $1), 0)
		  + COALESCE((
		      SELECT SUM(
		        size_bytes
		        + CASE WHEN id=$2 THEN $3 ELSE extracted_text_bytes END
		        + CASE WHEN id=$2 THEN $4 ELSE vector_bytes END
		      )
		      FROM knowledge_sources WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(content_bytes) FROM ai_artifacts WHERE tenant_id=$1
		    ), 0)
		  + COALESCE((
		      SELECT SUM(
		        octet_length(content)::bigint
		        + CASE WHEN embedding IS NULL THEN 0 ELSE 1536 * 4 END
		      )
		      FROM session_ai_chunks WHERE tenant_id=$1
		    ), 0)
	`, tenantID, source.ID, extractedTextBytes, vectorBytes).Scan(&usedBytes); err != nil {
		return err
	}
	if exceedsStorageQuota(quotaGB, usedBytes) {
		return ErrStorageQuota
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE knowledge_sources
		SET status='ready',
		    error_message='',
		    chunk_count=$1,
		    extracted_text_bytes=$4,
		    vector_bytes=$5,
		    memory_content=CASE
		      WHEN source_type='memory' AND memory_content=''
		      THEN $7
		      ELSE memory_content
		    END,
		    index_status=CASE
		      WHEN index_status IN ('ready', 'stale') THEN 'stale'
		      ELSE 'unindexed'
		    END,
		    embedded_chunk_count=0,
		    index_error_message='',
		    extract_lease_owner='',
		    extract_lease_expires_at=NULL
		WHERE id=$2 AND user_id=$3
		  AND (
		    $6=''
		    OR (
		      status='processing'
		      AND extract_lease_owner=$6
		      AND extract_lease_expires_at > NOW()
		    )
		  )
	`, len(chunks), source.ID, source.UserID, extractedTextBytes, vectorBytes,
		workerID, strings.Join(reconstructedContent, "\n"))
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	source.Status = "ready"
	source.ErrorMessage = ""
	source.ChunkCount = len(chunks)
	source.ExtractedTextBytes = extractedTextBytes
	source.VectorBytes = vectorBytes
	if source.SourceType == "memory" && source.Content == "" {
		source.Content = strings.Join(reconstructedContent, "\n")
	}
	if source.IndexStatus == models.AIIndexStatusReady ||
		source.IndexStatus == models.AIIndexStatusStale {
		source.IndexStatus = models.AIIndexStatusStale
	} else {
		source.IndexStatus = models.AIIndexStatusUnindexed
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if cancelledJobIDsResult != nil {
		*cancelledJobIDsResult = cancelledJobIDs
	}
	return nil
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
	result, err := s.db.ExecContext(ctx, `
		UPDATE knowledge_sources
		SET status=$1,
		    error_message=$2,
		    chunk_count=$3,
		    extract_lease_owner=CASE
		      WHEN $1 IN ('ready', 'error') THEN '' ELSE extract_lease_owner
		    END,
		    extract_lease_expires_at=CASE
		      WHEN $1 IN ('ready', 'error') THEN NULL
		      ELSE extract_lease_expires_at
		    END
		WHERE id=$4 AND user_id=$5
	`, status, errorMessage, chunkCount, id, userID)
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

func (s *PostgresStore) ListKnowledgeSources(
	ctx context.Context, projectID, userID string,
) ([]models.KnowledgeSource, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, project_id, tenant_id, user_id, source_type, name, media_type,
		       size_bytes, sha256, blob_path, memory_content, ocr_languages,
		       status, error_message, chunk_count, extracted_text_bytes,
		       vector_bytes, index_status, embedding_model, embedding_dimensions,
		       embedded_chunk_count, index_error_message, indexed_at,
		       extract_lease_owner, extract_lease_expires_at, extract_attempts,
		       extract_max_attempts, created_at, updated_at
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
		if err := scanKnowledgeSource(rows, &source); err != nil {
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
		       size_bytes, sha256, blob_path, memory_content, ocr_languages,
		       status, error_message, chunk_count, extracted_text_bytes,
		       vector_bytes, index_status, embedding_model, embedding_dimensions,
		       embedded_chunk_count, index_error_message, indexed_at,
		       extract_lease_owner, extract_lease_expires_at, extract_attempts,
		       extract_max_attempts, created_at, updated_at
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
		if err := scanKnowledgeSource(rows, &source); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

func (s *PostgresStore) DeleteKnowledgeSource(
	ctx context.Context, sourceID, projectID, userID string,
) (string, error) {
	blobPath, _, err := s.DeleteKnowledgeSourceAndCancelIndexJobs(
		ctx,
		sourceID,
		projectID,
		userID,
	)
	return blobPath, err
}

func (s *PostgresStore) DeleteKnowledgeSourceAndCancelIndexJobs(
	ctx context.Context,
	sourceID, projectID, userID string,
) (string, []string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var blobPath, tenantID string
	err = tx.QueryRowContext(ctx, `
		SELECT blob_path, tenant_id
		FROM knowledge_sources
		WHERE id=$1 AND project_id=$2 AND user_id=$3
	`, sourceID, projectID, userID).Scan(&blobPath, &tenantID)
	if err != nil {
		return "", nil, err
	}
	cancelledJobIDs, err := coordinateAIIndexScopeMutationTx(
		ctx,
		tx,
		tenantID,
		userID,
		"project",
		projectID,
		aiIndexMutationSource,
		sourceID,
	)
	if err != nil {
		return "", nil, err
	}
	if err := resetCancelledAIIndexTargetsTx(
		ctx,
		tx,
		tenantID,
		userID,
		cancelledJobIDs,
	); err != nil {
		return "", nil, err
	}
	var lockedBlobPath string
	if err := tx.QueryRowContext(ctx, `
		SELECT blob_path
		FROM knowledge_sources
		WHERE id=$1 AND project_id=$2 AND tenant_id=$3 AND user_id=$4
		FOR UPDATE
	`, sourceID, projectID, tenantID, userID).Scan(&lockedBlobPath); err != nil {
		return "", nil, err
	}
	blobPath = lockedBlobPath
	if err := enqueueKnowledgeBlobDeletionTx(
		ctx, tx, tenantID, userID, blobPath,
	); err != nil {
		return "", nil, err
	}
	result, err := tx.ExecContext(ctx, `
		DELETE FROM knowledge_sources
		WHERE id=$1 AND project_id=$2 AND user_id=$3
	`, sourceID, projectID, userID)
	if err != nil {
		return "", nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", nil, err
	}
	if affected != 1 {
		return "", nil, sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return "", nil, err
	}
	return blobPath, cancelledJobIDs, nil
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

func optionalStringEqual(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
