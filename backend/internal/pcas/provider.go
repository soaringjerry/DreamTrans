package pcas

import (
	"context"
	"fmt"
	"log"

	"github.com/dreamtrans/backend/internal/providers"
	"github.com/dreamtrans/backend/internal/speechmatics"
)

// Provider implements the providers.StreamingComputeProvider interface for DreamTrans
type Provider struct {
	speechmaticsClient *speechmatics.Client
}

// Ensure Provider implements providers.StreamingComputeProvider
var _ providers.StreamingComputeProvider = (*Provider)(nil)

// NewProvider creates a new instance of the DreamTrans provider
func NewProvider() (*Provider, error) {
	client, err := speechmatics.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create Speechmatics client: %w", err)
	}

	return &Provider{
		speechmaticsClient: client,
	}, nil
}

// Execute implements the ComputeProvider interface for compatibility
// This method is not supported for streaming operations
func (p *Provider) Execute(ctx context.Context, requestData map[string]interface{}) (string, error) {
	return "", fmt.Errorf("Execute is not supported, please use ExecuteStream")
}

// ExecuteStream is the core streaming processor for real-time transcription
// It receives audio chunks from the input channel and sends transcription results to the output channel
func (p *Provider) ExecuteStream(ctx context.Context, attributes map[string]string, input <-chan []byte, output chan<- []byte) error {
	log.Println("DreamTrans Provider: ExecuteStream started.")
	defer log.Println("DreamTrans Provider: ExecuteStream finished.")
	defer close(output) // Ensure output channel is closed when function exits

	// Extract configuration from attributes
	language := attributes["language"]
	if language == "" {
		language = "en" // Default to English
	}

	enablePartials := attributes["enable_partials"] == "true"
	
	maxDelay := 0.0
	// Parse max_delay if provided
	if delayStr := attributes["max_delay"]; delayStr != "" {
		var err error
		maxDelay, err = parseFloat(delayStr)
		if err != nil {
			log.Printf("Invalid max_delay value: %s, using default", delayStr)
		}
	}

	// Configure streaming transcription
	config := speechmatics.StreamingConfig{
		Language:       language,
		EnablePartials: enablePartials,
		MaxDelay:       maxDelay,
	}

	// Create text channel to receive transcription results
	textChan := make(chan string)

	// Start Speechmatics streaming transcription in a goroutine
	errChan := make(chan error, 1)
	go func() {
		err := p.speechmaticsClient.StartStreamingTranscription(ctx, config, input, textChan)
		if err != nil {
			errChan <- fmt.Errorf("speechmatics streaming error: %w", err)
		}
		close(errChan)
	}()

	// Forward transcription results to output channel
	for {
		select {
		case text, ok := <-textChan:
			if !ok {
				// Text channel closed, transcription complete
				return nil
			}
			
			// Send text result as bytes
			select {
			case output <- []byte(text):
				log.Printf("DreamTrans: Sent transcription result: %s", text)
			case <-ctx.Done():
				return ctx.Err()
			}

		case err := <-errChan:
			if err != nil {
				return err
			}

		case <-ctx.Done():
			log.Println("DreamTrans Provider: Context cancelled, stopping stream.")
			return ctx.Err()
		}
	}
}

// parseFloat is a helper function to parse float from string
func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}