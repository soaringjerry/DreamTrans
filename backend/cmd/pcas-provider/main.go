package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/dreamtrans/backend/internal/pcas"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	log.Println("Starting DreamTrans PCAS gRPC Server...")

	// Get port from environment or use default
	port := os.Getenv("GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	// Create TCP listener
	lis, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	log.Printf("Listening on port %s", port)

	// Create provider instance
	provider, err := pcas.NewProvider()
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Register the provider service
	provider.RegisterService(grpcServer)

	// Register reflection service for easier debugging
	reflection.Register(grpcServer)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		log.Println("Starting gRPC server...")
		if err := grpcServer.Serve(lis); err != nil {
			errChan <- fmt.Errorf("failed to serve: %v", err)
		}
	}()

	// Wait for interrupt signal or error
	select {
	case sig := <-sigChan:
		log.Printf("Received signal: %v, shutting down gracefully...", sig)
	case err := <-errChan:
		log.Printf("Server error: %v", err)
	}

	// Graceful shutdown
	log.Println("Stopping gRPC server...")
	grpcServer.GracefulStop()
	log.Println("DreamTrans PCAS gRPC Server stopped")
}