package config

import "os"

type Config struct {
	DatabaseURL         string
	RedisURL            string
	Port                string
	Environment         string
	JWTSecret           string
	CORSAllowedOrigins  string
}

func Load() *Config {
	return &Config{
		DatabaseURL:        getEnv("DATABASE_URL", "postgres://antimoney:antimoney_dev@localhost:5432/antimoney?sslmode=disable"),
		RedisURL:           getEnv("REDIS_URL", "redis://localhost:6379/0"),
		Port:               getEnv("PORT", "8000"),
		Environment:        getEnv("ENVIRONMENT", "development"),
		JWTSecret:          getEnv("JWT_SECRET", "antimoney-dev-secret-change-in-prod"),
		CORSAllowedOrigins: getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:5173,http://localhost:8000,http://127.0.0.1:5173"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
