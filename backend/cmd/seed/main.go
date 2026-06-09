// Command seed initializes the database schema and inserts baseline data.
//
// It is idempotent: running it repeatedly applies the (idempotent) migrations
// and ensures the test user exists without creating duplicates.
//
// Configuration (environment variables):
//
//	DATABASE_URL    PostgreSQL connection string (required)
//	MIGRATIONS_DIR  Directory of *.sql files to apply (default: /migrations)
//	SEED_USERNAME   Test user username (default: testuser)
//	SEED_EMAIL      Test user email (default: test@example.com)
//	SEED_PASSWORD   Test user password (default: changeme123)
package main

import (
	"context"
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
		log.Fatal("DATABASE_URL is required")
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
	username := getEnv("SEED_USERNAME", "testuser")
	email := getEnv("SEED_EMAIL", "test@example.com")
	password := getEnv("SEED_PASSWORD", "changeme123")

	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}

	// ON CONFLICT keeps the operation idempotent across repeated runs.
	const q = `
		INSERT INTO users (username, email, password_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (username) DO NOTHING`

	tag, err := pool.Exec(ctx, q, username, email, hash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		log.Printf("test user %q already exists, skipping", username)
	} else {
		log.Printf("created test user %q (password: %q)", username, password)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
