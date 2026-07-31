package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/dreamtrans/backend/internal/billing"
	"github.com/dreamtrans/backend/internal/rag"
)

// websocketRAGUsageMeter bridges RAG's provider-lifecycle hook to the
// connection's API quota and DreamPoint reservation. RAG invokes it only after
// duplicate/filter/feature checks and immediately before the provider call.
type websocketRAGUsageMeter struct {
	apiQuota providerAPIQuotaStore
	billing  websocketBillingService

	tenantID  string
	userID    string
	sessionID *string

	onBillingError func(error)
}

type websocketRAGUsageReservation struct {
	inner          *realtimeUsageReservation
	userID         string
	tenantID       string
	sessionID      *string
	customerFunded bool
	reservedAction string
	reservedModel  string
	onBillingError func(error)
}

func (m *websocketRAGUsageMeter) ReserveProviderUsage(
	ctx context.Context,
	usage *rag.ProviderUsage,
) (rag.ProviderUsageReservation, error) {
	if usage == nil {
		return nil, fmt.Errorf("provider usage is required")
	}
	if err := consumeProviderAPIRequest(ctx, m.apiQuota, m.tenantID, m.userID); err != nil {
		return nil, err
	}

	var reservation *realtimeUsageReservation
	var err error
	if m.billing != nil {
		action := strings.TrimSpace(usage.Action)
		if action == "" {
			action = "embedding"
		}
		reservation, err = reserveRealtimeUsage(
			ctx,
			m.billing,
			"ws-rag-"+action+":",
			&billing.UsageRecord{
				UserID:         m.userID,
				TenantID:       m.tenantID,
				SessionID:      m.sessionID,
				Action:         action,
				Model:          usage.Model,
				InputTokens:    usage.InputTokens,
				OutputTokens:   usage.OutputTokens,
				CustomerFunded: usage.CustomerFunded,
			},
		)
		if err != nil {
			return nil, err
		}
	}

	return &websocketRAGUsageReservation{
		inner:          reservation,
		userID:         m.userID,
		tenantID:       m.tenantID,
		sessionID:      m.sessionID,
		customerFunded: usage.CustomerFunded,
		reservedAction: usage.Action,
		reservedModel:  usage.Model,
		onBillingError: m.onBillingError,
	}, nil
}

func (r *websocketRAGUsageReservation) Settle(
	_ context.Context,
	actual *rag.ProviderUsage,
) error {
	if r == nil || r.inner == nil {
		return nil
	}
	if actual == nil {
		return fmt.Errorf("actual provider usage is required")
	}
	action := strings.TrimSpace(actual.Action)
	if action == "" {
		action = r.reservedAction
	}
	model := strings.TrimSpace(actual.Model)
	if model == "" {
		model = r.reservedModel
	}
	_, err := r.inner.settle(&billing.UsageRecord{
		UserID:            r.userID,
		TenantID:          r.tenantID,
		SessionID:         r.sessionID,
		Action:            action,
		Model:             model,
		InputTokens:       actual.InputTokens,
		CachedInputTokens: actual.CachedInputTokens,
		CacheWriteTokens:  actual.CacheWriteTokens,
		OutputTokens:      actual.OutputTokens,
		CustomerFunded:    r.customerFunded || actual.CustomerFunded,
	})
	if err != nil {
		if r.onBillingError != nil {
			r.onBillingError(err)
		}
		return fmt.Errorf("settle WebSocket RAG usage: %w", err)
	}
	return nil
}

func (r *websocketRAGUsageReservation) Refund(reason string) error {
	if r == nil || r.inner == nil {
		return nil
	}
	if err := r.inner.refund(reason); err != nil {
		if r.onBillingError != nil {
			r.onBillingError(err)
		}
		return fmt.Errorf("refund WebSocket RAG usage: %w", err)
	}
	return nil
}
