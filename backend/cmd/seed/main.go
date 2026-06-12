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
	"path/filepath"
	"sort"

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
	dir := getEnv("MIGRATIONS_DIR", "/migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		path := filepath.Join(dir, name)
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		log.Printf("applying migration %s", name)
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			return err
		}
	}
	return nil
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
