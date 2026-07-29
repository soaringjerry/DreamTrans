// Package main runs the authenticated DreamTrans PCAS gRPC provider.
package main

import (
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dreamtrans/backend/internal/pcas"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	log.Println("Starting DreamTrans PCAS gRPC Server...")

	// Default to loopback. Exposing the raw provider requires an explicit
	// shared key because the stream can consume paid Speechmatics capacity.
	port := envOrDefault("GRPC_PORT", "50052")
	bindAddress := envOrDefault("GRPC_BIND_ADDR", "127.0.0.1")
	apiKey := strings.TrimSpace(os.Getenv("PCAS_API_KEY"))
	loopbackOnly := isLoopbackBindAddress(bindAddress)
	if !loopbackOnly && apiKey == "" {
		log.Fatal("PCAS_API_KEY is required when GRPC_BIND_ADDR is not loopback")
	}
	if apiKey != "" && len(apiKey) < 16 {
		log.Fatal("PCAS_API_KEY must be at least 16 characters")
	}
	tlsCert := strings.TrimSpace(os.Getenv("PCAS_TLS_CERT"))
	tlsKey := strings.TrimSpace(os.Getenv("PCAS_TLS_KEY"))
	if (tlsCert == "") != (tlsKey == "") {
		log.Fatal("PCAS_TLS_CERT and PCAS_TLS_KEY must be configured together")
	}
	if !loopbackOnly && tlsCert == "" &&
		!strings.EqualFold(strings.TrimSpace(os.Getenv("PCAS_ALLOW_INSECURE_REMOTE")), "true") {
		log.Fatal("remote PCAS bind requires TLS; configure PCAS_TLS_CERT/PCAS_TLS_KEY or explicitly set PCAS_ALLOW_INSECURE_REMOTE=true")
	}

	// Create TCP listener
	lis, err := net.Listen("tcp", net.JoinHostPort(bindAddress, port))
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}
	log.Printf("Listening on %s", lis.Addr())

	// Create provider instance
	provider, err := pcas.NewProvider()
	if err != nil {
		log.Fatalf("Failed to create provider: %v", err)
	}

	// Create gRPC server
	serverOptions := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(4 << 20),
		grpc.MaxSendMsgSize(4 << 20),
		grpc.MaxConcurrentStreams(maxConcurrentPCASStreams()),
	}
	if apiKey != "" {
		serverOptions = append(serverOptions, grpc.StreamInterceptor(requirePCASKey(apiKey)))
	}
	if tlsCert != "" {
		serverCredentials, credentialErr := credentials.NewServerTLSFromFile(tlsCert, tlsKey)
		if credentialErr != nil {
			log.Fatalf("Failed to load PCAS TLS certificate: %v", credentialErr)
		}
		serverOptions = append(serverOptions, grpc.Creds(serverCredentials))
	}
	grpcServer := grpc.NewServer(serverOptions...)

	// Register the provider service
	provider.RegisterService(grpcServer)

	if strings.EqualFold(strings.TrimSpace(os.Getenv("PCAS_ENABLE_REFLECTION")), "true") {
		reflection.Register(grpcServer)
	}

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
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(15 * time.Second):
		log.Println("Graceful shutdown timed out; forcing active streams to close")
		grpcServer.Stop()
	}
	log.Println("DreamTrans PCAS gRPC Server stopped")
}

func maxConcurrentPCASStreams() uint32 {
	const fallback = 32
	value := strings.TrimSpace(os.Getenv("PCAS_MAX_CONCURRENT_STREAMS"))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil || parsed == 0 || parsed > 1024 {
		log.Printf("Invalid PCAS_MAX_CONCURRENT_STREAMS=%s; using %d", strconv.Quote(value), fallback)
		return fallback
	}
	return uint32(parsed)
}

func isLoopbackBindAddress(address string) bool {
	if strings.EqualFold(strings.TrimSpace(address), "localhost") {
		return true
	}
	ip := net.ParseIP(strings.TrimSpace(address))
	return ip != nil && ip.IsLoopback()
}

func requirePCASKey(expected string) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		values := metadata.ValueFromIncomingContext(stream.Context(), "authorization")
		if len(values) != 1 {
			return status.Error(codes.Unauthenticated, "authentication required")
		}
		provided := strings.TrimSpace(values[0])
		const prefix = "Bearer "
		if !strings.HasPrefix(provided, prefix) {
			return status.Error(codes.Unauthenticated, "invalid credentials")
		}
		provided = strings.TrimSpace(strings.TrimPrefix(provided, prefix))
		if len(provided) != len(expected) ||
			subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			return status.Error(codes.Unauthenticated, "invalid credentials")
		}
		return handler(srv, stream)
	}
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
