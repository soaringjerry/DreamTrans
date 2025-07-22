package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/handlers"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	tokenHandler, err := handlers.NewTokenHandler()
	if err != nil {
		log.Fatalf("Failed to initialize token handler: %v", err)
	}

	batchHandler, err := handlers.NewBatchTranscribeHandler()
	if err != nil {
		log.Fatalf("Failed to initialize batch transcribe handler: %v", err)
	}

	// Create a new mux to handle routes
	mux := http.NewServeMux()

	// API and WebSocket handlers
	mux.HandleFunc("/api/token/rt", tokenHandler.HandleTokenRequest)
	mux.HandleFunc("/ws/translate", handlers.HandleWebSocket)

	// Batch transcription endpoints
	mux.HandleFunc("/api/transcribe/batch/submit", batchHandler.HandleSubmit)
	mux.HandleFunc("/api/transcribe/batch/status", batchHandler.HandleStatus)
	mux.HandleFunc("/api/transcribe/batch", batchHandler.HandleTranscribeAndWait)

	// Static file server for SPA
	publicDir := "./public"

	// Check if public directory exists, if not, create it
	if _, err := os.Stat(publicDir); os.IsNotExist(err) {
		log.Printf("Public directory does not exist, creating %s", publicDir)
		if err := os.MkdirAll(publicDir, 0o755); err != nil {
			log.Fatalf("Failed to create public directory: %v", err)
		}
	}

	// File server for static assets
	fs := http.FileServer(http.Dir(publicDir))

	// SPA handler - serves static files and falls back to index.html for client-side routing
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Don't serve static files for API or WebSocket routes
		if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/ws") {
			http.NotFound(w, r)
			return
		}

		// Construct the file path
		filePath := filepath.Join(publicDir, r.URL.Path)

		// Check if the file exists
		fileInfo, err := os.Stat(filePath)
		if err != nil || fileInfo.IsDir() {
			// If file doesn't exist or is a directory, serve index.html
			// This enables client-side routing in the SPA
			indexPath := filepath.Join(publicDir, "index.html")
			if _, err := os.Stat(indexPath); err == nil {
				http.ServeFile(w, r, indexPath)
			} else {
				// If even index.html doesn't exist, show a friendly message
				w.WriteHeader(http.StatusOK)
				fmt.Fprintf(w, "DreamTrans backend is running. Place your frontend build files in the './public' directory.")
			}
			return
		}

		// File exists, serve it
		fs.ServeHTTP(w, r)
	})

	// Setup CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"}, // Allow all origins for development
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Content-Length", "Accept-Encoding", "Authorization"},
	})

	// Apply CORS middleware
	handler := c.Handler(mux)

	// Get port from environment variable or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	addr := ":" + port
	fmt.Printf("Server starting on port %s\n", port)
	fmt.Printf("- API endpoint: http://localhost:%s/api/token/rt\n", port)
	fmt.Printf("- WebSocket endpoint: ws://localhost:%s/ws/translate\n", port)
	fmt.Printf("- Batch transcription: http://localhost:%s/api/transcribe/batch\n", port)
	fmt.Printf("- Static files served from: %s\n", publicDir)
	fmt.Println("- CORS enabled for all origins")

	// Create server with timeouts (increased for batch processing)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  5 * time.Minute,  // Increased for file uploads
		WriteTimeout: 15 * time.Minute, // Increased for batch processing
		IdleTimeout:  60 * time.Second,
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
