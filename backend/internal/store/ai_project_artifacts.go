package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/dreamtrans/backend/internal/models"
)

// ProjectSessionRef is the minimal session identity needed to assemble
// project-wide AI context in chronological order.
type ProjectSessionRef struct {
	ID        string
	Title     string
	StartedAt time.Time
}

// ListProjectSessionRefs returns the sessions linked to a project, oldest
// first, verifying both the project and every session belong to the caller.
// A non-positive limit returns every linked session.
func (s *PostgresStore) ListProjectSessionRefs(
	ctx context.Context, tenantID, userID, projectID string, limit int,
) ([]ProjectSessionRef, error) {
	query := `
		SELECT sess.id, sess.title, sess.started_at
		FROM project_sessions ps
		JOIN ai_projects p ON p.id = ps.project_id
		JOIN sessions sess ON sess.id = ps.session_id
		WHERE ps.project_id = $1
		  AND p.tenant_id = $2 AND p.user_id = $3
		  AND sess.tenant_id = $2 AND sess.user_id = $3
		ORDER BY sess.started_at ASC, sess.id ASC
	`
	args := []any{projectID, tenantID, userID}
	if limit > 0 {
		query += ` LIMIT $4`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	refs := make([]ProjectSessionRef, 0)
	for rows.Next() {
		var ref ProjectSessionRef
		if err := rows.Scan(&ref.ID, &ref.Title, &ref.StartedAt); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// GetLatestAIArtifactByProject returns the newest artifact of one type in a
// project, or nil when the project has none.
func (s *PostgresStore) GetLatestAIArtifactByProject(
	ctx context.Context, userID, projectID, artifactType string,
) (*models.AIArtifact, error) {
	var artifact models.AIArtifact
	var policy []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT id, tenant_id, user_id, session_id, project_id, artifact_type,
		       title, content, context_policy, context_tokens, model,
		       client_request_id, request_hash, replay_response,
		       content_bytes, created_at, updated_at
		FROM ai_artifacts
		WHERE user_id=$1 AND project_id=$2 AND artifact_type=$3
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, userID, projectID, artifactType).Scan(
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

// DeleteAIArtifactsByProjectAndTypeExcept removes superseded artifacts of one
// type in a project, keeping only keepID. Used so regenerating a concept map
// does not accumulate stale versions against the tenant storage quota.
func (s *PostgresStore) DeleteAIArtifactsByProjectAndTypeExcept(
	ctx context.Context, userID, projectID, artifactType, keepID string,
) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM ai_artifacts
		WHERE user_id=$1 AND project_id=$2 AND artifact_type=$3 AND id <> $4
	`, userID, projectID, artifactType, keepID)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
