package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	os.Setenv("PORT", "1234")
	os.Setenv("DATABASE_URL", "test_db")
	os.Setenv("ENVIRONMENT", "test")
	os.Setenv("JWT_SECRET", "super")

	cfg := Load()

	if cfg.Port != "1234" {
		t.Errorf("expected port 1234, got %s", cfg.Port)
	}

	// Unset to test defaults
	os.Unsetenv("PORT")
	os.Unsetenv("DATABASE_URL")
	cfg2 := Load()

	if cfg2.Port != "8000" {
		t.Errorf("expected port 8000, got %s", cfg2.Port)
	}
}
