package database_test

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/user/antimoney/internal/database"
)

func TestConnect(t *testing.T) {
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("test_db"),
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

	// Test RunMigrations
	err = database.RunMigrations(connStr, "../../migrations")
	if err != nil {
		t.Fatalf("RunMigrations failed: %v", err)
	}

	// Test Connect
	pool, err := database.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
}
