package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/user/antimoney/internal/auth"
	"github.com/user/antimoney/internal/config"
	"github.com/user/antimoney/internal/database"
	"github.com/user/antimoney/internal/handlers"
	"github.com/user/antimoney/internal/ratelimit"
	"github.com/user/antimoney/internal/scheduler"
	"github.com/user/antimoney/internal/seed"
	"github.com/user/antimoney/internal/services"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	// Initialize JWT secret
	auth.SetJWTSecret(cfg.JWTSecret)

	// Run database migrations
	log.Println("Running database migrations...")
	migrateURL := cfg.DatabaseURL
	if strings.HasPrefix(migrateURL, "postgresql://") {
		migrateURL = "postgres://" + strings.TrimPrefix(migrateURL, "postgresql://")
	}
	if err := database.RunMigrations(migrateURL, "migrations"); err != nil {
		log.Printf("Warning: migrations: %v", err)
	}

	// Connect to database
	log.Println("Connecting to database...")
	pool, err := database.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer pool.Close()

	// Initialize Redis rate limiter (fail-open if Redis is unavailable)
	limiter, err := ratelimit.New(cfg.RedisURL)
	if err != nil {
		log.Printf("Warning: rate limiter disabled (invalid Redis URL): %v", err)
	}

	// Seed database (commodities + default chart of accounts for legacy book)
	if err := seed.SeedDatabase(ctx, pool); err != nil {
		log.Printf("Warning: seed: %v", err)
	}

	// Create services
	txSvc := services.NewTransactionService(pool)
	acctSvc := services.NewAccountService(pool)
	userSvc := auth.NewUserService(pool)
	snapshotSvc := services.NewSnapshotService(pool)

	// Create handlers
	txHandler := handlers.NewTransactionHandler(txSvc)
	acctHandler := handlers.NewAccountHandler(acctSvc, txSvc)
	importExportHandler := handlers.NewImportExportHandler(pool, txSvc, snapshotSvc)
	snapshotHandler := handlers.NewSnapshotHandler(snapshotSvc, importExportHandler)

	// Parse CORS allowed origins from config
	corsOrigins := strings.Split(cfg.CORSAllowedOrigins, ",")
	for i, o := range corsOrigins {
		corsOrigins[i] = strings.TrimSpace(o)
	}

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   corsOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS", "PATCH"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
	r.Use(securityHeadersMiddleware)
	r.Use(bodySizeLimitMiddleware(10 << 20)) // 10 MB

	// Health check (public)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Auth routes (public)
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", func(w http.ResponseWriter, r *http.Request) {
			if limiter != nil && !limiter.AllowRegistration(r.Context()) {
				handlers.WriteErrorPublic(w, http.StatusTooManyRequests, "registration limit reached, try again tomorrow")
				return
			}

			var req auth.RegisterRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				handlers.WriteErrorPublic(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if req.Email == "" || req.Password == "" {
				handlers.WriteErrorPublic(w, http.StatusBadRequest, "email and password are required")
				return
			}
			if len(req.Password) < 6 {
				handlers.WriteErrorPublic(w, http.StatusBadRequest, "password must be at least 6 characters")
				return
			}

			resp, err := userSvc.Register(r.Context(), req)
			if err != nil {
				if err == auth.ErrEmailTaken {
					handlers.WriteErrorPublic(w, http.StatusConflict, "email already in use")
					return
				}
				log.Printf("register error: %v", err)
				handlers.WriteErrorPublic(w, http.StatusInternalServerError, "registration failed")
				return
			}

			handlers.WriteJSONPublic(w, http.StatusCreated, resp)
		})

		r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
			if limiter != nil && !limiter.AllowLogin(r.Context(), r.RemoteAddr) {
				handlers.WriteErrorPublic(w, http.StatusTooManyRequests, "too many login attempts, try again later")
				return
			}

			var req auth.LoginRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				handlers.WriteErrorPublic(w, http.StatusBadRequest, "invalid request body")
				return
			}

			resp, err := userSvc.Login(r.Context(), req)
			if err != nil {
				if err == auth.ErrInvalidCreds {
					handlers.WriteErrorPublic(w, http.StatusUnauthorized, "invalid email or password")
					return
				}
				log.Printf("login error: %v", err)
				handlers.WriteErrorPublic(w, http.StatusInternalServerError, "login failed")
				return
			}
			handlers.WriteJSONPublic(w, http.StatusOK, resp)
		})

		r.Get("/me", func(w http.ResponseWriter, r *http.Request) {
			// Quick token validation endpoint
			tokenStr := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			claims, err := auth.ParseToken(tokenStr)
			if err != nil {
				handlers.WriteErrorPublic(w, http.StatusUnauthorized, "invalid token")
				return
			}
			handlers.WriteJSONPublic(w, http.StatusOK, map[string]string{
				"user_id":   claims.UserID,
				"book_guid": claims.BookGUID,
				"email":     claims.Email,
			})
		})
	})

	// Protected API routes — require valid JWT
	r.Route("/api", func(r chi.Router) {
		r.Use(auth.RequireAuth)

		r.Mount("/transactions", txHandler.Routes())
		r.Mount("/accounts", acctHandler.Routes())
		r.Mount("/data", importExportHandler.Routes())
		r.Mount("/snapshots", snapshotHandler.Routes())

		// Books endpoint (user's book)
		r.Get("/books", func(w http.ResponseWriter, r *http.Request) {
			bookGUID := auth.BookGUIDFromCtx(r.Context())
			var guid string
			var rootGUID *string
			err := pool.QueryRow(r.Context(),
				"SELECT guid, root_account_guid FROM books WHERE guid = $1", bookGUID,
			).Scan(&guid, &rootGUID)
			if err != nil {
				handlers.WriteErrorPublic(w, http.StatusInternalServerError, "no book found")
				return
			}
			handlers.WriteJSONPublic(w, http.StatusOK, map[string]interface{}{
				"guid":              guid,
				"root_account_guid": rootGUID,
			})
		})
	})

	// Start server
	addr := fmt.Sprintf(":%s", cfg.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	// Start background snapshot scheduler.
	go scheduler.StartSnapshotScheduler(ctx, snapshotSvc)

	log.Printf("🏦 Antimoney API server starting on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

// securityHeadersMiddleware sets defensive HTTP headers on every response.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		// Tight CSP: this service only serves JSON (API) and the health check.
		// The React SPA is served separately (CDN/nginx) and needs its own CSP.
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		next.ServeHTTP(w, r)
	})
}

// bodySizeLimitMiddleware rejects request bodies larger than maxBytes.
func bodySizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
