package store

import "context"

// ListSessionIDsByOwner returns the complete set of PostgreSQL sessions whose
// legacy SQLite RAG documents must be removed before the owning user is
// cascade-deleted. Both tenant and user are required so cleanup never crosses
// a tenant boundary even if an unexpected ownership inconsistency exists.
func (s *PostgresStore) ListSessionIDsByOwner(
	ctx context.Context,
	tenantID, userID string,
) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id
		FROM sessions
		WHERE tenant_id = $1 AND user_id = $2
		ORDER BY id
	`, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	sessionIDs := make([]string, 0)
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		sessionIDs = append(sessionIDs, sessionID)
	}
	return sessionIDs, rows.Err()
}
