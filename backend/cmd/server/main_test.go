package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestMainContext(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("antimoney"),
		postgres.WithUsername("user"),
		postgres.WithPassword("pass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(15*time.Second)),
	)
	if err != nil {
		t.Fatalf("failed to start postgres: %v", err)
	}
	defer postgresContainer.Terminate(ctx)

	connStr, _ := postgresContainer.ConnectionString(ctx, "sslmode=disable")

	// Set environmental variables used by main()
	os.Setenv("PORT", "52489")
	os.Setenv("DATABASE_URL", connStr)
	os.Setenv("ENVIRONMENT", "test")
	os.Setenv("JWT_SECRET", "supersec")

	go main()

	// Give it a moment to boot
	time.Sleep(2 * time.Second)

	resp, err := http.Get("http://127.0.0.1:52489/health")
	if err != nil {
		t.Fatalf("Failed to call health check: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Health check failed")
	}

	// Just for coverage's sake, kill the server
	p, _ := os.FindProcess(os.Getpid())
	p.Signal(os.Interrupt)
}
