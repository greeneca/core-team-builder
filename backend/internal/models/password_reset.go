package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPasswordResetInvalid is returned when a presented reset token is unknown,
// expired, or already used. The same error is used for all cases so callers
// cannot distinguish them.
var ErrPasswordResetInvalid = errors.New("password reset token invalid")

// PasswordResetStore provides data access for password reset tokens. Only token
// hashes are ever stored or compared; plaintext tokens never touch the database.
type PasswordResetStore struct {
	pool *pgxpool.Pool
}

// NewPasswordResetStore constructs a PasswordResetStore backed by the given pool.
func NewPasswordResetStore(pool *pgxpool.Pool) *PasswordResetStore {
	return &PasswordResetStore{pool: pool}
}

// Create persists a new reset token hash for a user with the given expiry.
func (s *PasswordResetStore) Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO password_resets (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`
	_, err := s.pool.Exec(ctx, q, userID, tokenHash, expiresAt)
	return err
}

// Consume atomically validates and marks a reset token as used (single use), so
// a token cannot be replayed. It returns the owning user ID on success and
// ErrPasswordResetInvalid if the token is unknown, expired, or already used.
func (s *PasswordResetStore) Consume(ctx context.Context, tokenHash string) (int64, error) {
	const q = `
		UPDATE password_resets
		SET used_at = now()
		WHERE token_hash = $1
		  AND used_at IS NULL
		  AND expires_at > now()
		RETURNING user_id`
	var userID int64
	err := s.pool.QueryRow(ctx, q, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrPasswordResetInvalid
	}
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// InvalidateForUser marks every outstanding (unused) reset token for a user as
// used, so requesting a new reset (or completing one) cancels older links.
func (s *PasswordResetStore) InvalidateForUser(ctx context.Context, userID int64) error {
	const q = `
		UPDATE password_resets
		SET used_at = now()
		WHERE user_id = $1 AND used_at IS NULL`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}

// DeleteExpired removes reset tokens that expired or were used before the
// cutoff. Intended for periodic housekeeping.
func (s *PasswordResetStore) DeleteExpired(ctx context.Context) error {
	const q = `
		DELETE FROM password_resets
		WHERE expires_at < now() OR used_at IS NOT NULL`
	_, err := s.pool.Exec(ctx, q)
	return err
}
