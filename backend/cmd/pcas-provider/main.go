package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dreamtrans/backend/internal/pcas"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	log.Println("Starting DreamTrans PCAS Provider...")

	// Create provider instance
	provider, err := pcas.NewProvider()
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Create channels for streaming
	inputChan := make(chan []byte, 100)
	outputChan := make(chan []byte, 100)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start the provider's ExecuteStream in a goroutine
	errChan := make(chan error, 1)
	go func() {
		// Simulate attributes that would come from PCAS
		attributes := map[string]string{
			"language":        "en",
			"enable_partials": "true",
			"max_delay":       "2.0",
		}

		log.Println("Starting ExecuteStream...")
		err := provider.ExecuteStream(ctx, attributes, inputChan, outputChan)
		if err != nil {
			errChan <- fmt.Errorf("ExecuteStream error: %w", err)
		}
		close(errChan)
	}()

	// Start a goroutine to read and display output
	go func() {
		log.Println("Starting output reader...")
		for result := range outputChan {
			log.Printf("Transcription result: %s", string(result))
		}
		log.Println("Output reader stopped")
	}()

	// Simulate audio input
	go func() {
		log.Println("Starting audio simulator...")
		defer close(inputChan)

		// Simulate sending audio chunks
		// In a real scenario, this would be actual PCM audio data
		audioChunks := []string{
			"Hello, this is a test of the emergency broadcast system.",
			"The quick brown fox jumps over the lazy dog.",
			"DreamTrans is now integrated with PCAS for real-time transcription.",
			"This concludes our test broadcast. Thank you for listening.",
		}

		for i, chunk := range audioChunks {
			select {
			case <-ctx.Done():
				log.Println("Audio simulator stopped by context")
				return
			default:
				// Simulate PCM audio data (in reality, this would be actual audio bytes)
				// For now, we're sending text that will be "transcribed" by our mock logic
				audioData := []byte(fmt.Sprintf("Audio chunk %d: %s", i+1, chunk))
				
				log.Printf("Sending simulated audio chunk %d: %d bytes", i+1, len(audioData))
				inputChan <- audioData
				
				// Simulate delay between audio chunks
				time.Sleep(2 * time.Second)
			}
		}

		log.Println("All audio chunks sent, closing input channel")
	}()

	// Wait for interrupt signal or error
	select {
	case sig := <-sigChan:
		log.Printf("Received signal: %v, shutting down...", sig)
		cancel()
	case err := <-errChan:
		log.Printf("Provider error: %v", err)
		cancel()
	}

	// Give goroutines time to clean up
	time.Sleep(1 * time.Second)
	log.Println("DreamTrans PCAS Provider stopped")
}