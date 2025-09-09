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
	"github.com/dreamtrans/backend/internal/config"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

func main() {
    // Load .env file
    if err := godotenv.Load(); err != nil { log.Println("No .env file found") }
    // Load centralized config file (creates defaults if missing)
    if err := config.Load(); err != nil { log.Printf("config load error: %v", err) }
    // Build and run server
    handler := buildHandler()
    port := os.Getenv("PORT"); if port == "" { port = "8080" }
    addr := ":" + port
    fmt.Printf("Server starting on port %s\n", port)
    fmt.Printf("- API endpoint: http://localhost:%s/api/token/rt\n", port)
    fmt.Printf("- WebSocket endpoint: ws://localhost:%s/ws/translate\n", port)
    fmt.Printf("- Batch transcription: http://localhost:%s/api/transcribe/batch\n", port)
    fmt.Println("- CORS enabled for all origins")
    srv := &http.Server{ Addr: addr, Handler: handler, ReadTimeout: 5*time.Minute, WriteTimeout: 15*time.Minute, IdleTimeout: 60*time.Second }
    if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed { log.Printf("server error: %v", err) }
}

func buildHandler() http.Handler {
    tokenHandler, err := handlers.NewTokenHandler(); if err != nil { log.Fatalf("init token: %v", err) }
    batchHandler, err := handlers.NewBatchTranscribeHandler(); if err != nil { log.Fatalf("init batch: %v", err) }
    ragHandler, err := handlers.NewRAGHandler(); if err != nil { log.Fatalf("init rag: %v", err) }
    // Create mux
    mux := http.NewServeMux()
    mux.HandleFunc("/api/token/rt", tokenHandler.HandleTokenRequest)
    mux.HandleFunc("/ws/translate", handlers.HandleWebSocket)
    // RAG endpoints
    mux.HandleFunc("/api/rag/ask", ragHandler.HandleAsk)
    mux.HandleFunc("/api/rag/query", ragHandler.HandleQuery)
    mux.HandleFunc("/api/rag/stats", ragHandler.HandleStats)
    mux.HandleFunc("/api/rag/summary", ragHandler.HandleSummary)
    mux.HandleFunc("/api/rag/title", ragHandler.HandleTitle)
    // Metrics & prompts
    mux.HandleFunc("/api/metrics", handlers.HandleMetrics)
    mux.HandleFunc("/api/prompts/defaults", handlers.HandlePromptDefaults)
    // Batch transcription
    mux.HandleFunc("/api/transcribe/batch/submit", batchHandler.HandleSubmit)
    mux.HandleFunc("/api/transcribe/batch/status", batchHandler.HandleStatus)
    mux.HandleFunc("/api/transcribe/batch", batchHandler.HandleTranscribeAndWait)
    // Static
    publicDir := "./public"
    _ = os.MkdirAll(publicDir, 0o755)
    fs := http.FileServer(http.Dir(publicDir))
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/ws") { http.NotFound(w, r); return }
        filePath := filepath.Join(publicDir, r.URL.Path)
        if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() { fs.ServeHTTP(w, r); return }
        indexPath := filepath.Join(publicDir, "index.html")
        if _, err := os.Stat(indexPath); err == nil { http.ServeFile(w, r, indexPath); return }
        w.WriteHeader(http.StatusOK); fmt.Fprintf(w, "DreamTrans backend is running. Place your frontend build files in the './public' directory.")
    })
    // CORS
    c := cors.New(cors.Options{ AllowedOrigins: []string{"*"}, AllowCredentials: true, AllowedMethods: []string{"GET","POST","OPTIONS"}, AllowedHeaders: []string{"Accept","Content-Type","Content-Length","Accept-Encoding","Authorization"} })
    return c.Handler(mux)
}
