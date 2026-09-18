// Command seed initializes the database schema and inserts baseline data.
//
// It is idempotent: running it repeatedly applies the (idempotent) migrations
// and ensures the test user exists without creating duplicates.
//
// Configuration (environment variables). Credentials are never hardcoded; they
// must be supplied via the environment (see .env / .env.example):
//
//	DATABASE_URL    PostgreSQL connection string (required)
//	MIGRATIONS_DIR  Directory of *.sql files to apply (default: /migrations)
//	SEED_USERNAME   Test user username (required)
//	SEED_EMAIL      Test user email (required)
//	SEED_PASSWORD   Test user password (required)
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("seed failed: %v", err)
	}
	log.Println("seed completed successfully")
}

func run() error {
	ctx := context.Background()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := applyMigrations(ctx, pool); err != nil {
		return err
	}
	return ensureTestUser(ctx, pool)
}

func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	applied, err := db.Migrate(ctx, pool, getEnv("MIGRATIONS_DIR", "/migrations"))
	if err != nil {
		return err
	}
	log.Printf("applied %d migrations (%s … %s)", len(applied), first(applied), last(applied))
	return nil
}

func first(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return names[0]
}

func last(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return names[len(names)-1]
}

func ensureTestUser(ctx context.Context, pool *pgxpool.Pool) error {
	// Credentials come from the environment only; never hardcode them so they
	// stay isolated in .env and out of source control.
	username, err := requireEnv("SEED_USERNAME")
	if err != nil {
		return err
	}
	email, err := requireEnv("SEED_EMAIL")
	if err != nil {
		return err
	}
	password, err := requireEnv("SEED_PASSWORD")
	if err != nil {
		return err
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	// The seed/test user is always an admin. ON CONFLICT keeps the operation
	// idempotent and promotes an existing test user to admin on re-run.
	const q = `
		INSERT INTO users (username, email, password_hash, is_admin)
		VALUES ($1, $2, $3, true)
		ON CONFLICT (username) DO UPDATE SET is_admin = true`

	tag, err := pool.Exec(ctx, q, username, email, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		log.Printf("test user %q already exists, skipping", username)
	} else {
		// Never log the plaintext password; it is configured via the environment.
		log.Printf("ensured admin test user %q", username)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// requireEnv returns the value of a required environment variable, or an error
// when it is unset/empty. Used for credentials so they are never hardcoded.
func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}
