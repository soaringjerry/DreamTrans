// Package pcas implements the DreamTrans PCAS streaming provider.
package pcas

import (
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
	"strings"

	"github.com/dreamtrans/backend/internal/speechmatics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
)

const (
	maxPCASConfigBytes = 4096
	maxPCASAudioChunk  = 1 << 20
	pcasAudioQueueSize = 16
)

// Provider implements the gRPC streaming service for DreamTrans
type Provider struct {
	speechmaticsClient *speechmatics.Client
}

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

// TranscribeStream handles bidirectional streaming for real-time transcription
// This is a raw gRPC stream handler that processes bytes directly
// nolint:gocyclo
func (p *Provider) TranscribeStream(stream grpc.ServerStream) error {
	log.Println("DreamTrans Provider: TranscribeStream started.")
	defer log.Println("DreamTrans Provider: TranscribeStream finished.")

	ctx := stream.Context()

	// Create channels for audio data
	audioChan := make(chan []byte, pcasAudioQueueSize)

	// Channel for configuration
	configChan := make(chan map[string]string, 1)

	// Error channel for goroutines
	errChan := make(chan error, 2)

	// Start goroutine to receive data from client
	go func() {
		// The receiver is the sole producer, so it is also the sole owner of
		// closing audioChan. Closing it from TranscribeStream as well can panic
		// when RecvMsg returns EOF while the handler is unwinding.
		defer close(audioChan)

		firstMessage := true
		for {
			// Receive Any message
			var anyMsg anypb.Any
			if err := stream.RecvMsg(&anyMsg); err == io.EOF {
				return
			} else if err != nil {
				errChan <- status.Errorf(codes.Internal, "failed to receive: %v", err)
				return
			}

			// First message should contain configuration
			if firstMessage {
				// Extract configuration from first message
				// For simplicity, we'll use the type URL as a signal
				if anyMsg.TypeUrl == "config" {
					if len(anyMsg.Value) > maxPCASConfigBytes {
						errChan <- status.Error(codes.InvalidArgument, "configuration is too large")
						return
					}
					// Parse configuration from value
					config := make(map[string]string)
					config["language"] = "en" // Default
					config["enable_partials"] = "false"
					config["max_delay"] = "0"

					// Simple parsing: assume value contains "key=value,key=value"
					configStr := string(anyMsg.Value)
					if configStr != "" {
						// Basic parsing logic
						for _, pair := range splitConfig(configStr) {
							if k, v, ok := parseKeyValue(pair); ok {
								config[k] = v
							}
						}
					}

					select {
					case configChan <- config:
					default:
					}
					firstMessage = false
					continue
				}

				errChan <- status.Error(codes.InvalidArgument, "first message must contain configuration")
				return
			}

			// All other messages are audio data
			if len(anyMsg.Value) > 0 {
				if len(anyMsg.Value) > maxPCASAudioChunk {
					errChan <- status.Error(codes.ResourceExhausted, "audio chunk is too large")
					return
				}
				chunk := append([]byte(nil), anyMsg.Value...)
				select {
				case audioChan <- chunk:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	// Wait for configuration
	var config map[string]string
	select {
	case config = <-configChan:
		log.Printf("Received PCAS transcription configuration")
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		return err
	}

	// Extract configuration
	language := config["language"]
	if language == "" {
		language = "en"
	}
	language = strings.TrimSpace(language)
	if language == "" {
		language = "en"
	}
	if len(language) > 10 {
		return status.Error(codes.InvalidArgument, "invalid language")
	}
	for _, character := range language {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return status.Error(codes.InvalidArgument, "invalid language")
	}

	enablePartials, err := strconv.ParseBool(config["enable_partials"])
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid enable_partials")
	}
	maxDelay := 0.0
	if delayStr := config["max_delay"]; delayStr != "" {
		parsedDelay, parseErr := strconv.ParseFloat(strings.TrimSpace(delayStr), 64)
		maxDelay = parsedDelay
		if parseErr != nil ||
			math.IsNaN(maxDelay) || math.IsInf(maxDelay, 0) || maxDelay < 0 || maxDelay > 30 {
			return status.Error(codes.InvalidArgument, "invalid max_delay")
		}
	}

	// Configure streaming transcription
	streamConfig := speechmatics.StreamingConfig{
		Language:       language,
		EnablePartials: enablePartials,
		MaxDelay:       maxDelay,
	}

	// Create text channel to receive transcription results
	textChan := make(chan string)

	// Start Speechmatics streaming transcription
	go func() {
		err := p.speechmaticsClient.StartStreamingTranscription(ctx, streamConfig, audioChan, textChan)
		if err != nil {
			errChan <- fmt.Errorf("speechmatics error: %w", err)
		}
	}()

	// Forward transcription results to client
	for {
		select {
		case text, ok := <-textChan:
			if !ok {
				return nil
			}

			// Send text as Any message
			anyResp := &anypb.Any{
				TypeUrl: "transcription",
				Value:   []byte(text),
			}

			if err := stream.SendMsg(anyResp); err != nil {
				return status.Errorf(codes.Internal, "failed to send: %v", err)
			}
			log.Printf("Sent PCAS transcription result (%d bytes)", len(text))

		case err := <-errChan:
			if err != nil {
				return err
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// RegisterService registers the Provider with a gRPC server using raw registration
func (p *Provider) RegisterService(s *grpc.Server) {
	// Register as a generic bidirectional streaming service
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "DreamTransTranscription",
		HandlerType: (*interface{})(nil),
		Methods:     []grpc.MethodDesc{},
		Streams: []grpc.StreamDesc{
			{
				StreamName: "TranscribeStream",
				Handler: func(srv interface{}, stream grpc.ServerStream) error {
					return p.TranscribeStream(stream)
				},
				ServerStreams: true,
				ClientStreams: true,
			},
		},
	}, p)
}

// Helper functions
func splitConfig(s string) []string {
	var result []string
	var current string
	for _, ch := range s {
		if ch == ',' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func parseKeyValue(s string) (key, value string, ok bool) {
	for i, ch := range s {
		if ch == '=' {
			key = strings.TrimSpace(s[:i])
			value = strings.TrimSpace(s[i+1:])
			ok = key != ""
			return
		}
	}
	return
}
