package store

import (
	"context"
	"database/sql"
)

// lockAIStorageOwnerFKGateTx holds the same referenced-row lock that a later
// user-owned INSERT would acquire. It must be taken before the tenant quota
// row: administrative user deletion locks users first and transcript cascade
// accounting locks the tenant afterwards.
func lockAIStorageOwnerFKGateTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, userID string,
) error {
	var lockedUserID string
	return tx.QueryRowContext(ctx, `
		SELECT id
		FROM users
		WHERE id=$1 AND tenant_id=$2
		FOR KEY SHARE
	`, userID, tenantID).Scan(&lockedUserID)
}

// lockAIStorageOwnerMutationGateTx is the exclusive variant used by scope
// mutations. Taking it after the scope advisory lock but before index-job rows
// keeps both account deletion and concurrent indexing on user -> job -> tenant.
func lockAIStorageOwnerMutationGateTx(
	ctx context.Context,
	tx *sql.Tx,
	tenantID, userID string,
) error {
	var lockedUserID string
	return tx.QueryRowContext(ctx, `
		SELECT id
		FROM users
		WHERE id=$1 AND tenant_id=$2
		FOR UPDATE
	`, userID, tenantID).Scan(&lockedUserID)
}
