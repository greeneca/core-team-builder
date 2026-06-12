package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRefreshTokenInvalid is returned when a presented refresh token is unknown,
// expired, or already revoked. The same error is used for all cases so callers
// cannot distinguish them.
var ErrRefreshTokenInvalid = errors.New("refresh token invalid")

// RefreshTokenStore provides data access for refresh tokens. Only token hashes
// are ever stored or compared; plaintext tokens never touch the database.
type RefreshTokenStore struct {
	pool *pgxpool.Pool
}

// NewRefreshTokenStore constructs a RefreshTokenStore backed by the given pool.
func NewRefreshTokenStore(pool *pgxpool.Pool) *RefreshTokenStore {
	return &RefreshTokenStore{pool: pool}
}

// Create persists a new refresh token hash for a user with the given expiry.
func (s *RefreshTokenStore) Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)`
	_, err := s.pool.Exec(ctx, q, userID, tokenHash, expiresAt)
	return err
}

// Consume atomically validates and revokes a refresh token (single use), so a
// token cannot be replayed. It returns the owning user ID on success and
// ErrRefreshTokenInvalid if the token is unknown, expired, or already revoked.
func (s *RefreshTokenStore) Consume(ctx context.Context, tokenHash string) (int64, error) {
	const q = `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()
		RETURNING user_id`
	var userID int64
	err := s.pool.QueryRow(ctx, q, tokenHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRefreshTokenInvalid
	}
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// Revoke marks a single refresh token as revoked. It is a no-op if the token is
// unknown or already revoked, so logout is idempotent.
func (s *RefreshTokenStore) Revoke(ctx context.Context, tokenHash string) error {
	const q = `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE token_hash = $1 AND revoked_at IS NULL`
	_, err := s.pool.Exec(ctx, q, tokenHash)
	return err
}

// RevokeAllForUser revokes every active refresh token for a user ("sign out
// everywhere").
func (s *RefreshTokenStore) RevokeAllForUser(ctx context.Context, userID int64) error {
	const q = `
		UPDATE refresh_tokens
		SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}

// DeleteExpired removes refresh tokens that expired or were revoked before the
// cutoff. Intended for periodic housekeeping.
func (s *RefreshTokenStore) DeleteExpired(ctx context.Context) error {
	const q = `
		DELETE FROM refresh_tokens
		WHERE expires_at < now() OR revoked_at IS NOT NULL`
	_, err := s.pool.Exec(ctx, q)
	return err
}
