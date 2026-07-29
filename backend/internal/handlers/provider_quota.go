package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/dreamtrans/backend/internal/store"
)

// providerAPIQuotaStore is deliberately small so provider-backed WebSocket
// handlers can meter each upstream operation without depending on HTTP
// middleware that only runs once during the upgrade handshake.
type providerAPIQuotaStore interface {
	ConsumeAPIRequest(context.Context, string, string) (store.APIQuotaStatus, error)
}

func consumeProviderAPIRequest(
	ctx context.Context,
	quotaStore providerAPIQuotaStore,
	tenantID, userID string,
) error {
	if quotaStore == nil || tenantID == "" || userID == "" {
		return nil
	}
	if _, err := quotaStore.ConsumeAPIRequest(ctx, tenantID, userID); err != nil {
		if errors.Is(err, store.ErrAPIQuota) {
			return fmt.Errorf("monthly API quota exceeded: %w", err)
		}
		return fmt.Errorf("API quota service unavailable: %w", err)
	}
	return nil
}
