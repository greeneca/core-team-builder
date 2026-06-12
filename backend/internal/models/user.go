// Package models defines the domain types persisted in the database.
package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUserNotFound is returned when a lookup matches no user.
var ErrUserNotFound = errors.New("user not found")

// User represents an application account. The password hash is never exposed
// in JSON responses.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	IsAdmin      bool      `json:"is_admin"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// UserStore provides data access for users.
type UserStore struct {
	pool *pgxpool.Pool
}

// NewUserStore constructs a UserStore backed by the given pool.
func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// Create inserts a new user and returns the populated record.
func (s *UserStore) Create(ctx context.Context, username, email, passwordHash string, isAdmin bool) (*User, error) {
	const q = `
		INSERT INTO users (username, email, password_hash, is_admin)
		VALUES ($1, $2, $3, $4)
		RETURNING id, username, email, password_hash, is_admin, created_at, updated_at`

	u := &User{}
	err := s.pool.QueryRow(ctx, q, username, email, passwordHash, isAdmin).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetByUsername looks up a user by their unique username.
func (s *UserStore) GetByUsername(ctx context.Context, username string) (*User, error) {
	const q = `
		SELECT id, username, email, password_hash, is_admin, created_at, updated_at
		FROM users
		WHERE username = $1`

	u := &User{}
	err := s.pool.QueryRow(ctx, q, username).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// GetByID looks up a user by their primary key.
func (s *UserStore) GetByID(ctx context.Context, id int64) (*User, error) {
	const q = `
		SELECT id, username, email, password_hash, is_admin, created_at, updated_at
		FROM users
		WHERE id = $1`

	u := &User{}
	err := s.pool.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Count returns the total number of registered users.
func (s *UserStore) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// CountAdmins returns the number of users with the admin flag set.
func (s *UserStore) CountAdmins(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE is_admin`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// List returns all users ordered by username (password hashes omitted in JSON).
func (s *UserStore) List(ctx context.Context) ([]User, error) {
	const q = `
		SELECT id, username, email, password_hash, is_admin, created_at, updated_at
		FROM users
		ORDER BY username`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(
			&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt,
		); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// SetAdmin updates a user's admin flag.
func (s *UserStore) SetAdmin(ctx context.Context, id int64, isAdmin bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET is_admin = $1 WHERE id = $2`, isAdmin, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}

// Delete removes a user (and, via ON DELETE CASCADE, their owned teams).
func (s *UserStore) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}
	return nil
}
