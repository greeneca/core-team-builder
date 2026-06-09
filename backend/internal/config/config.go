// Package config loads runtime configuration from environment variables.
//
// All configuration is sourced from the environment so the same binary runs
// unchanged across local, Docker, and production environments (12-factor).
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime configuration for the server.
type Config struct {
	// HTTPAddr is the address the HTTP server listens on, e.g. ":8080".
	HTTPAddr string

	// DatabaseURL is the full PostgreSQL connection string.
	DatabaseURL string

	// JWTSecret signs and verifies authentication tokens. Must be set to a
	// long, random value in any non-local environment.
	JWTSecret []byte

	// JWTTTL is how long an issued auth token remains valid.
	JWTTTL time.Duration

	// CORSOrigin is the allowed origin for browser requests (the frontend URL).
	CORSOrigin string
}

// Load reads configuration from the environment, applying sane defaults for
// local development. It returns an error when a required production value is
// missing.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:    getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL: getEnv("DATABASE_URL", ""),
		JWTSecret:   []byte(getEnv("JWT_SECRET", "")),
		JWTTTL:      24 * time.Hour,
		CORSOrigin:  getEnv("CORS_ORIGIN", "http://localhost:8081"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) == 0 {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}

	if ttl := os.Getenv("JWT_TTL"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return nil, fmt.Errorf("invalid JWT_TTL: %w", err)
		}
		cfg.JWTTTL = d
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
