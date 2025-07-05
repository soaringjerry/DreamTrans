package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/dreamtrans/backend/internal/handlers"
	"github.com/joho/godotenv"
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

	http.HandleFunc("/api/token/rt", tokenHandler.HandleTokenRequest)
	http.HandleFunc("/ws/translate", handlers.HandleWebSocket)

	// Get port from environment variable or use default
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	
	addr := ":" + port
	fmt.Printf("Server starting on port %s\n", port)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}