package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dreamtrans/backend/internal/auth"
	"github.com/dreamtrans/backend/internal/config"
	"github.com/dreamtrans/backend/internal/handlers"
	"github.com/dreamtrans/backend/internal/store"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

var (
	pgStore    *store.PostgresStore
	jwtManager *auth.JWTManager
	authMw     *auth.AuthMiddleware
)

func main() {
	// Load .env file
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}
	// Load centralized config file (creates defaults if missing)
	if err := config.Load(); err != nil {
		log.Printf("config load error: %v", err)
	}

	// Initialize PostgreSQL store (optional - only if DATABASE_URL is set)
	if dbURL := os.Getenv("DATABASE_URL"); dbURL != "" {
		var err error
		pgStore, err = store.NewPostgresStore()
		if err != nil {
			log.Printf("PostgreSQL not available: %v (auth features disabled)", err)
		} else {
			log.Println("PostgreSQL connected successfully")
			defer pgStore.Close()
		}
	}

	// Initialize JWT manager
	var err error
	jwtManager, err = auth.NewJWTManager()
	if err != nil {
		log.Printf("JWT manager init error: %v", err)
	} else {
		authMw = auth.NewAuthMiddleware(jwtManager)
	}

	// Build and run server
	handler := buildHandler()
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	fmt.Printf("Server starting on port %s\n", port)
	fmt.Printf("- API endpoint: http://localhost:%s/api/token/rt\n", port)
	fmt.Printf("- WebSocket endpoint: ws://localhost:%s/ws/translate\n", port)
	fmt.Printf("- Batch transcription: http://localhost:%s/api/transcribe/batch\n", port)
	if pgStore != nil {
		fmt.Printf("- Auth endpoints: http://localhost:%s/api/auth/*\n", port)
		fmt.Printf("- Session endpoints: http://localhost:%s/api/sessions/*\n", port)
	}
	fmt.Println("- CORS enabled for all origins")
	srv := &http.Server{Addr: addr, Handler: handler, ReadTimeout: 5 * time.Minute, WriteTimeout: 15 * time.Minute, IdleTimeout: 60 * time.Second}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Printf("server error: %v", err)
	}
}

func buildHandler() http.Handler {
	tokenHandler, err := handlers.NewTokenHandler()
	if err != nil {
		log.Fatalf("init token: %v", err)
	}
	batchHandler, err := handlers.NewBatchTranscribeHandler()
	if err != nil {
		log.Fatalf("init batch: %v", err)
	}
	ragHandler, err := handlers.NewRAGHandler()
	if err != nil {
		log.Fatalf("init rag: %v", err)
	}

	// Create mux
	mux := http.NewServeMux()

	// Speechmatics token endpoint
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
	mux.HandleFunc("/api/metrics/reset", handlers.HandleMetricsReset)
	mux.HandleFunc("/api/prompts/defaults", handlers.HandlePromptDefaults)
	mux.HandleFunc("/api/models/defaults", handlers.HandleModelDefaults)

	// Batch transcription
	mux.HandleFunc("/api/transcribe/batch/submit", batchHandler.HandleSubmit)
	mux.HandleFunc("/api/transcribe/batch/status", batchHandler.HandleStatus)
	mux.HandleFunc("/api/transcribe/batch", batchHandler.HandleTranscribeAndWait)

	// Auth and Session endpoints (only if PostgreSQL is available)
	if pgStore != nil && jwtManager != nil {
		authHandler := handlers.NewAuthHandler(pgStore, jwtManager)
		sessionHandler := handlers.NewSessionHandler(pgStore)

		// Public auth endpoints
		mux.HandleFunc("/api/auth/register", authHandler.HandleRegister)
		mux.HandleFunc("/api/auth/login", authHandler.HandleLogin)
		mux.HandleFunc("/api/auth/refresh", authHandler.HandleRefresh)
		mux.HandleFunc("/api/auth/logout", authHandler.HandleLogout)

		// Protected user endpoints
		mux.Handle("/api/user/profile", authMw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				authHandler.HandleProfile(w, r)
			} else if r.Method == http.MethodPut {
				authHandler.HandleUpdateProfile(w, r)
			} else {
				http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			}
		})))
		mux.Handle("/api/user/password", authMw.RequireAuth(http.HandlerFunc(authHandler.HandleUpdatePassword)))

		// Session detail routes: /api/sessions/{id}
		mux.Handle("/api/sessions/", authMw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.Path
			// Handle /api/sessions/{id}/transcripts
			if strings.HasSuffix(path, "/transcripts") {
				if r.Method == http.MethodPost {
					sessionHandler.HandleSaveTranscript(w, r)
				} else {
					http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
				}
				return
			}
			// Handle /api/sessions/{id}/transcripts/batch
			if strings.HasSuffix(path, "/transcripts/batch") {
				if r.Method == http.MethodPost {
					sessionHandler.HandleBatchSaveTranscripts(w, r)
				} else {
					http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
				}
				return
			}
			// Handle /api/sessions/{id}/export
			if strings.HasSuffix(path, "/export") {
				sessionHandler.HandleExportSession(w, r)
				return
			}
			// Handle /api/sessions/{id}
			switch r.Method {
			case http.MethodGet:
				sessionHandler.HandleGetSession(w, r)
			case http.MethodPut, http.MethodPatch:
				sessionHandler.HandleUpdateSession(w, r)
			case http.MethodDelete:
				sessionHandler.HandleDeleteSession(w, r)
			default:
				http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			}
		})))

		// Quota middleware for session creation
		quotaMw := auth.NewQuotaMiddleware(pgStore)
		mux.Handle("/api/sessions", quotaMw.CheckSessions(authMw.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet {
				sessionHandler.HandleListSessions(w, r)
			} else if r.Method == http.MethodPost {
				sessionHandler.HandleCreateSession(w, r)
			} else {
				http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			}
		}))))

		// Admin endpoints (admin/super_admin only)
		adminHandler := handlers.NewAdminHandler(pgStore)
		adminRequired := func(next http.Handler) http.Handler {
			return authMw.RequireAuth(authMw.RequireRole("admin", "super_admin")(next))
		}

		// Admin users
		mux.Handle("/api/admin/users", adminRequired(http.HandlerFunc(adminHandler.HandleListUsers)))
		mux.Handle("/api/admin/users/", adminRequired(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				adminHandler.HandleGetUser(w, r)
			case http.MethodPut, http.MethodPatch:
				adminHandler.HandleUpdateUser(w, r)
			case http.MethodDelete:
				adminHandler.HandleDeleteUser(w, r)
			default:
				http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
			}
		})))

		// Admin tenants
		mux.Handle("/api/admin/tenants", adminRequired(http.HandlerFunc(adminHandler.HandleListTenants)))
		mux.Handle("/api/admin/tenants/", adminRequired(http.HandlerFunc(adminHandler.HandleUpdateTenant)))

		// Admin stats
		mux.Handle("/api/admin/stats", adminRequired(http.HandlerFunc(adminHandler.HandleGetStats)))
		mux.Handle("/api/admin/usage", adminRequired(http.HandlerFunc(adminHandler.HandleGetUsage)))
	}

	// Static file serving
	publicDir := "./public"
	_ = os.MkdirAll(publicDir, 0o755)
	fs := http.FileServer(http.Dir(publicDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") || strings.HasPrefix(r.URL.Path, "/ws") {
			http.NotFound(w, r)
			return
		}
		filePath := filepath.Join(publicDir, r.URL.Path)
		if fi, err := os.Stat(filePath); err == nil && !fi.IsDir() {
			fs.ServeHTTP(w, r)
			return
		}
		indexPath := filepath.Join(publicDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "DreamTrans backend is running. Place your frontend build files in the './public' directory.")
	})

	// CORS
	c := cors.New(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Content-Length", "Accept-Encoding", "Authorization"},
	})
	return c.Handler(mux)
}
