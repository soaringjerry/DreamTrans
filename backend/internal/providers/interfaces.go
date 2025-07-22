package providers

import "context"

// StreamingComputeProvider is the interface that PCAS providers must implement
// This is a mock interface to replace the missing github.com/soaringjerry/pcas/internal/providers
type StreamingComputeProvider interface {
	// Execute handles non-streaming compute requests
	Execute(ctx context.Context, requestData map[string]interface{}) (string, error)

	// ExecuteStream handles streaming compute requests
	ExecuteStream(ctx context.Context, attributes map[string]string, input <-chan []byte, output chan<- []byte) error
}
