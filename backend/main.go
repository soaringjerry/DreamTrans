package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/dreamtrans/backend/internal/handlers"
)

func main() {
	tokenHandler, err := handlers.NewTokenHandler()
	if err != nil {
		log.Fatalf("Failed to initialize token handler: %v", err)
	}

	http.HandleFunc("/api/token/rt", tokenHandler.HandleTokenRequest)
	http.HandleFunc("/ws/translate", handlers.HandleWebSocket)

	port := ":8080"
	fmt.Printf("Server starting on port %s\n", port)
	if err := http.ListenAndServe(port, nil); err != nil {
		log.Fatal(err)
	}
}