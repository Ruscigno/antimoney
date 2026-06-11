package config

import (
	"log"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL        string
	RedisURL           string
	Port               string
	Environment        string
	JWTSecret          string
	CORSAllowedOrigins string
	PlaidClientID      string
	PlaidSecret        string
	PlaidEnv           string
	PlaidTokenEncKey   string
	// PlaidLegacyTokenFallback temporarily accepts pre-AAD token ciphertexts
	// (sunset flag): enable, let the opportunistic re-seal migrate them, disable.
	PlaidLegacyTokenFallback bool
}

func Load() *Config {
	return &Config{
		DatabaseURL:              getEnv("DATABASE_URL", "postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable"),
		RedisURL:                 getEnv("REDIS_URL", "redis://localhost:6379/0"),
		Port:                     getEnv("PORT", "8000"),
		Environment:              getEnv("ENVIRONMENT", "development"),
		JWTSecret:                getEnv("JWT_SECRET", "antimoney-dev-secret-change-in-prod"),
		CORSAllowedOrigins:       getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:5173,http://localhost:8000,http://127.0.0.1:5173"),
		PlaidClientID:            getEnv("PLAID_CLIENT_ID", ""),
		PlaidSecret:              getEnv("PLAID_SECRET", ""),
		PlaidEnv:                 getEnv("PLAID_ENV", "sandbox"),
		PlaidTokenEncKey:         getEnv("PLAID_TOKEN_ENC_KEY", ""),
		PlaidLegacyTokenFallback: getBoolEnv("PLAID_LEGACY_TOKEN_FALLBACK"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// getBoolEnv parses a boolean env var accepting the usual spellings
// (true/TRUE/1/t, false/0/...) and WARNS on garbage instead of silently
// treating it as off — these flags exist to be flipped deliberately in
// operational emergencies, where a silent no-op is the worst outcome.
func getBoolEnv(key string) bool {
	v := os.Getenv(key)
	if v == "" {
		return false
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("config: invalid %s=%q (expected a boolean like true/false); treating as false", key, v)
		return false
	}
	return b
}
