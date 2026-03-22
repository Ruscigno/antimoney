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

	// Seed database (commodities + default chart of accounts for legacy book)
	if err := seed.SeedDatabase(ctx, pool); err != nil {
		log.Printf("Warning: seed: %v", err)
	}

	// Create services
	txSvc := services.NewTransactionService(pool)
	acctSvc := services.NewAccountService(pool)
	commoditySvc := services.NewCommodityService(pool)
	userSvc := auth.NewUserService(pool)

	// Create handlers
	txHandler := handlers.NewTransactionHandler(txSvc)
	acctHandler := handlers.NewAccountHandler(acctSvc, txSvc)
	commodityHandler := handlers.NewCommodityHandler(commoditySvc)

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:*", "http://127.0.0.1:*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health check (public)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})

	// Auth routes (public)
	r.Route("/auth", func(r chi.Router) {
		r.Post("/register", func(w http.ResponseWriter, r *http.Request) {
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
				handlers.WriteErrorPublic(w, http.StatusInternalServerError, err.Error())
				return
			}

			// Seed default chart of accounts for the new user's book
			if err := seed.SeedUserBook(r.Context(), pool, resp.BookGUID); err != nil {
				log.Printf("Warning: seed user book: %v", err)
			}

			handlers.WriteJSONPublic(w, http.StatusCreated, resp)
		})

		r.Post("/login", func(w http.ResponseWriter, r *http.Request) {
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
				handlers.WriteErrorPublic(w, http.StatusInternalServerError, err.Error())
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
		r.Mount("/commodities", commodityHandler.Routes())

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

	log.Printf("🏦 Antimoney API server starting on %s", addr)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
