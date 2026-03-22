package auth

import (
	"context"
	"testing"

	"github.com/user/antimoney/internal/testutil"
)

func TestAuthService(t *testing.T) {
	ctx := context.Background()
	db, err := testutil.SetupDB(ctx, "../../migrations")
	if err != nil {
		t.Fatalf("Failed to setup DB: %v", err)
	}
	defer db.Teardown(ctx)

	SetJWTSecret("test_secret_for_tests")

	service := NewUserService(db.Pool)

	// Test Registration
	req := RegisterRequest{
		Email:    "test@example.com",
		Password: "password123",
		Name:     "Test User",
	}

	res, err := service.Register(ctx, req)
	if err != nil {
		t.Fatalf("Failed to register: %v", err)
	}
	if res.Token == "" || res.UserID == "" || res.BookGUID == "" {
		t.Fatalf("Invalid register response: %+v", res)
	}

	// Test Duplicate Registration
	_, err = service.Register(ctx, req)
	if err != ErrEmailTaken {
		t.Fatalf("Expected ErrEmailTaken, got %v", err)
	}

	// Test Login
	loginReq := LoginRequest{
		Email:    "test@example.com",
		Password: "password123",
	}
	loginRes, err := service.Login(ctx, loginReq)
	if err != nil {
		t.Fatalf("Failed to login: %v", err)
	}
	if loginRes.Token == "" {
		t.Fatalf("Expected token, got empty")
	}

	// Test Invalid Login
	_, err = service.Login(ctx, LoginRequest{Email: "test@example.com", Password: "wrong"})
	if err != ErrInvalidCreds {
		t.Fatalf("Expected ErrInvalidCreds, got %v", err)
	}

	_, err = service.Login(ctx, LoginRequest{Email: "notfound@example.com", Password: "password123"})
	if err != ErrInvalidCreds {
		t.Fatalf("Expected ErrInvalidCreds, got %v", err)
	}
}
