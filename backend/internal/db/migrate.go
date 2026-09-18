package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migrate applies every *.sql file in dir to the database, in filename order,
// and returns the names it applied. The migrations are written to be
// idempotent, so it is safe to run repeatedly against an existing database.
//
// Shared by the seed command and the handler integration tests so both build
// the same schema from the same files.
func Migrate(ctx context.Context, pool *pgxpool.Pool, dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		sqlBytes, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
			return nil, fmt.Errorf("apply migration %s: %w", name, err)
		}
	}
	return files, nil
}
