package speechmatics

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/gorilla/websocket"
)

const (
	realtimeAPIURL = "wss://eu2.rt.speechmatics.com/v2"
	// Message types from Speechmatics
	msgRecognitionStarted   = "RecognitionStarted"
	msgAddTranscript        = "AddTranscript"
	msgAddPartialTranscript = "AddPartialTranscript"
	msgEndOfTranscript      = "EndOfTranscript"
	msgAudioAdded           = "AudioAdded"
	msgError                = "Error"
	msgWarning              = "Warning"
	msgInfo                 = "Info"
)

// Client handles real-time streaming transcription with Speechmatics
type Client struct {
	apiKey         string
	tokenGenerator *auth.TokenGenerator
}

// NewClient creates a new Speechmatics real-time client
func NewClient() (*Client, error) {
	apiKey := os.Getenv("SM_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("SM_API_KEY environment variable not set")
	}

	tokenGen, err := auth.NewTokenGenerator()
	if err != nil {
		return nil, fmt.Errorf("failed to create token generator: %w", err)
	}

	return &Client{
		apiKey:         apiKey,
		tokenGenerator: tokenGen,
	}, nil
}

// StreamingConfig contains configuration for the streaming transcription
type StreamingConfig struct {
	Language       string
	EnablePartials bool
	MaxDelay       float64
}

// StartStreamingTranscription starts a streaming transcription session
func (c *Client) StartStreamingTranscription(ctx context.Context, config StreamingConfig, audioInput <-chan []byte, textOutput chan<- string) error {
	// Generate temporary JWT token
	token, err := c.tokenGenerator.GenerateToken()
	if err != nil {
		return fmt.Errorf("failed to generate token: %w", err)
	}

	// Build WebSocket URL with JWT
	wsURL, err := url.Parse(realtimeAPIURL)
	if err != nil {
		return fmt.Errorf("failed to parse WebSocket URL: %w", err)
	}
	q := wsURL.Query()
	q.Set("jwt", token)
	wsURL.RawQuery = q.Encode()

	// Connect to WebSocket
	log.Printf("Connecting to Speechmatics WebSocket at %s", wsURL.String())
	conn, _, err := websocket.DefaultDialer.Dial(wsURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}
	defer conn.Close()

	// Send StartRecognition message
	startMsg := map[string]interface{}{
		"message": "StartRecognition",
		"audio_format": map[string]interface{}{
			"type":        "raw",
			"encoding":    "pcm_f32le",
			"sample_rate": 48000,
		},
		"transcription_config": map[string]interface{}{
			"language":                 config.Language,
			"enable_partials":          config.EnablePartials,
			"operating_point":          "enhanced",
			"enable_entities":          true,
			"speaker_diarization":      "speaker",
			"diarization_max_speakers": 10,
		},
	}

	if config.MaxDelay > 0 {
		startMsg["transcription_config"].(map[string]interface{})["max_delay"] = config.MaxDelay
	}

	if err := conn.WriteJSON(startMsg); err != nil {
		return fmt.Errorf("failed to send StartRecognition: %w", err)
	}

	// Create error channel for goroutines
	errChan := make(chan error, 2)

	// Start goroutine to read messages from WebSocket
	go c.readMessages(ctx, conn, textOutput, errChan)

	// Start goroutine to send audio data
	go c.sendAudio(ctx, conn, audioInput, errChan)

	// Wait for context cancellation or error
	select {
	case <-ctx.Done():
		log.Println("Streaming transcription context canceled")
		// Send EndOfStream message
		endMsg := map[string]interface{}{
			"message": "EndOfStream",
		}
		if err := conn.WriteJSON(endMsg); err != nil {
			log.Printf("Failed to send EndOfStream: %v", err)
		}
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}

// readMessages reads messages from the WebSocket and processes them
func (c *Client) readMessages(ctx context.Context, conn *websocket.Conn, textOutput chan<- string, errChan chan<- error) {
	defer close(textOutput)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Read message with timeout
			if err := conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
				log.Printf("Failed to set read deadline: %v", err)
			}
			_, message, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					errChan <- fmt.Errorf("WebSocket read error: %w", err)
				}
				return
			}

			// Parse message
			var msg map[string]interface{}
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("Failed to parse message: %v", err)
				continue
			}

			// Handle different message types
			msgType, ok := msg["message"].(string)
			if !ok {
				continue
			}

			switch msgType {
			case msgRecognitionStarted:
				log.Println("Recognition started")

			case msgAddTranscript:
				// Extract final transcript
				if metadata, ok := msg["metadata"].(map[string]interface{}); ok {
					if transcript, ok := metadata["transcript"].(string); ok && transcript != "" {
						select {
						case textOutput <- transcript:
						case <-ctx.Done():
							return
						}
					}
				}

			case msgAddPartialTranscript:
				// Extract partial transcript (optional - could be filtered out)
				if metadata, ok := msg["metadata"].(map[string]interface{}); ok {
					if transcript, ok := metadata["transcript"].(string); ok && transcript != "" {
						// Prefix with [PARTIAL] to distinguish from final transcripts
						select {
						case textOutput <- "[PARTIAL] " + transcript:
						case <-ctx.Done():
							return
						}
					}
				}

			case msgEndOfTranscript:
				log.Println("End of transcript received")
				return

			case msgError:
				errorMsg := fmt.Sprintf("Speechmatics error: %v", msg)
				log.Println(errorMsg)
				errChan <- fmt.Errorf("%s", errorMsg)
				return

			case msgWarning:
				log.Printf("Speechmatics warning: %v", msg)

			case msgInfo:
				log.Printf("Speechmatics info: %v", msg)

			case msgAudioAdded:
				// Audio successfully added, no action needed
			}
		}
	}
}

// sendAudio sends audio data to the WebSocket
func (c *Client) sendAudio(ctx context.Context, conn *websocket.Conn, audioInput <-chan []byte, errChan chan<- error) {
	seqNo := 0

	for {
		select {
		case <-ctx.Done():
			return
		case audioData, ok := <-audioInput:
			if !ok {
				// Audio input channel closed, send EndOfStream
				endMsg := map[string]interface{}{
					"message": "EndOfStream",
				}
				if err := conn.WriteJSON(endMsg); err != nil {
					log.Printf("Failed to send EndOfStream: %v", err)
				}
				return
			}

			// Send AddAudio message
			audioMsg := map[string]interface{}{
				"message": "AddAudio",
				"data":    audioData,
				"seq_no":  seqNo,
			}

			if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				log.Printf("Failed to set write deadline: %v", err)
			}
			if err := conn.WriteJSON(audioMsg); err != nil {
				errChan <- fmt.Errorf("failed to send audio: %w", err)
				return
			}

			seqNo++
		}
	}
}
