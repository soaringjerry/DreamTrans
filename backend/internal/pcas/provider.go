package pcas

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/dreamtrans/backend/internal/speechmatics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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
func (p *Provider) TranscribeStream(stream grpc.ServerStream) error {
	log.Println("DreamTrans Provider: TranscribeStream started.")
	defer log.Println("DreamTrans Provider: TranscribeStream finished.")

	ctx := stream.Context()
	
	// Create channels for audio data
	audioChan := make(chan []byte, 100)
	defer close(audioChan)
	
	// Channel for configuration
	configChan := make(chan map[string]string, 1)
	
	// Error channel for goroutines
	errChan := make(chan error, 2)
	
	// Start goroutine to receive data from client
	go func() {
		firstMessage := true
		for {
			// Receive Any message
			var anyMsg anypb.Any
			if err := stream.RecvMsg(&anyMsg); err == io.EOF {
				close(audioChan)
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
			}
			
			// All other messages are audio data
			if len(anyMsg.Value) > 0 {
				select {
				case audioChan <- anyMsg.Value:
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
		log.Printf("Received config: %v", config)
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
	
	enablePartials := config["enable_partials"] == "true"
	maxDelay := 0.0
	if delayStr := config["max_delay"]; delayStr != "" {
		fmt.Sscanf(delayStr, "%f", &maxDelay)
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
			log.Printf("Sent transcription: %s", text)
			
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
				StreamName:    "TranscribeStream",
				Handler:       func(srv interface{}, stream grpc.ServerStream) error {
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

func parseKeyValue(s string) (string, string, bool) {
	for i, ch := range s {
		if ch == '=' {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}