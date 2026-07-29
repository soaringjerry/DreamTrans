package rag

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ProviderUsage describes one logical provider operation. Reserved values are
// conservative upper bounds; settled values reflect authoritative provider
// usage. When a compatible provider omits usage metadata, callers keep the
// conservative reservation instead of undercharging unknown work.
type ProviderUsage struct {
	Action       string
	Model        string
	InputTokens  int
	OutputTokens int
	// CustomerFunded marks an operation executed with a request-scoped provider
	// credential. It still consumes tenant/API quota, but must not debit the
	// user's DreamPoint balance for provider tokens paid directly by the user.
	CustomerFunded bool
}

// ProviderUsageReservation controls the lifecycle of one provider operation.
// A successful provider response must be settled before its result is persisted
// or returned. Failed provider calls are refunded.
type ProviderUsageReservation interface {
	Settle(context.Context, ProviderUsage) error
	Refund(string) error
}

// ProviderUsageMeter reserves billing/quota before provider work starts.
type ProviderUsageMeter interface {
	ReserveProviderUsage(context.Context, ProviderUsage) (ProviderUsageReservation, error)
}

type providerUsageMeterContextKey struct{}

// WithProviderUsageMeter attaches request-scoped metering to RAG provider
// operations. Services used outside a billed HTTP request remain unchanged.
func WithProviderUsageMeter(ctx context.Context, meter ProviderUsageMeter) context.Context {
	if meter == nil {
		return ctx
	}
	return context.WithValue(ctx, providerUsageMeterContextKey{}, meter)
}

func providerUsageMeterFromContext(ctx context.Context) ProviderUsageMeter {
	if ctx == nil {
		return nil
	}
	meter, _ := ctx.Value(providerUsageMeterContextKey{}).(ProviderUsageMeter)
	return meter
}

func reserveProviderUsage(
	ctx context.Context,
	usage ProviderUsage,
) (ProviderUsageReservation, error) {
	meter := providerUsageMeterFromContext(ctx)
	if meter == nil {
		return nil, nil
	}
	reservation, err := meter.ReserveProviderUsage(ctx, usage)
	if err != nil {
		return nil, fmt.Errorf("reserve provider usage: %w", err)
	}
	if reservation == nil {
		return nil, errors.New("provider usage meter returned a nil reservation")
	}
	return reservation, nil
}

func refundProviderUsage(reservation ProviderUsageReservation, reason string, operationErr error) error {
	if reservation == nil {
		return operationErr
	}
	if refundErr := reservation.Refund(reason); refundErr != nil {
		return fmt.Errorf("%w; refund provider usage: %w", operationErr, refundErr)
	}
	return operationErr
}

func settleProviderUsage(
	ctx context.Context,
	reservation ProviderUsageReservation,
	actual ProviderUsage,
) error {
	if reservation == nil {
		return nil
	}
	if err := reservation.Settle(ctx, actual); err != nil {
		return fmt.Errorf("settle provider usage: %w", err)
	}
	return nil
}

func embeddingModelName() string {
	if model := strings.TrimSpace(os.Getenv("OPENAI_EMBEDDING_MODEL")); model != "" {
		return model
	}
	return "text-embedding-3-small"
}

// conservativeProviderTokens is an upper bound for byte-level tokenizers: one
// token cannot encode less than one input byte. The small fixed allowance
// covers message framing added by compatible chat APIs.
func conservativeProviderTokens(parts ...string) int {
	const framingAllowance = 256
	total := framingAllowance
	for _, part := range parts {
		total += len(part)
	}
	if total < 1 {
		return 1
	}
	return total
}

func (s *Service) embedWithMeter(ctx context.Context, text string) ([]float32, error) {
	model := embeddingModelName()
	reservation, err := reserveProviderUsage(ctx, ProviderUsage{
		Action:      "embedding",
		Model:       model,
		InputTokens: conservativeProviderTokens(text),
	})
	if err != nil {
		return nil, err
	}

	actualInputTokens := conservativeProviderTokens(text)
	var vector []float32
	if provider, ok := s.embedder.(embeddingUsageProvider); ok {
		vector, actualInputTokens, err = provider.EmbedWithUsage(ctx, text)
		if actualInputTokens <= 0 {
			// An otherwise compatible provider may omit usage metadata. Keeping
			// the conservative reservation avoids turning missing metadata into
			// free upstream work.
			actualInputTokens = conservativeProviderTokens(text)
		}
	} else {
		vector, err = s.embedder.Embed(ctx, text)
	}
	if err != nil {
		return nil, refundProviderUsage(
			reservation,
			"RAG embedding provider request failed",
			fmt.Errorf("embedding provider request: %w", err),
		)
	}
	if err := settleProviderUsage(ctx, reservation, ProviderUsage{
		Action:      "embedding",
		Model:       model,
		InputTokens: actualInputTokens,
	}); err != nil {
		return nil, err
	}
	return vector, nil
}
