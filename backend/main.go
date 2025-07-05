package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

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

	// Create a new mux to handle routes
	mux := http.NewServeMux()
	mux.HandleFunc("/api/token/rt", tokenHandler.HandleTokenRequest)
	mux.HandleFunc("/ws/translate", handlers.HandleWebSocket)

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
	fmt.Printf("Server starting on port %s with CORS enabled\n", port)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}