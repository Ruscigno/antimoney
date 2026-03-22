package seed_test

import (
	"context"
	"testing"

	"github.com/user/antimoney/internal/seed"
	"github.com/user/antimoney/internal/testutil"
)

func TestSeed(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	// Test SeedDatabase which seeds commodities + old default book
	if err := seed.SeedDatabase(ctx, db.Pool); err != nil {
		t.Fatalf("Failed to seed database: %v", err)
	}

	// Just for coverage, seed a specific new book
	// create a dummy user and book to test SeedUserBook
	var userID, bookGUID string
	err = db.Pool.QueryRow(ctx, "INSERT INTO users (email, password_hash, name) VALUES ('seed@test.com', 'hash', 'Seed') RETURNING id").Scan(&userID)
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	err = db.Pool.QueryRow(ctx, "INSERT INTO books (user_id) VALUES ($1) RETURNING guid", userID).Scan(&bookGUID)
	if err != nil {
		t.Fatalf("Failed to create book: %v", err)
	}

	if err := seed.SeedUserBook(ctx, db.Pool, bookGUID); err != nil {
		t.Fatalf("Failed to seed user book: %v", err)
	}
}
