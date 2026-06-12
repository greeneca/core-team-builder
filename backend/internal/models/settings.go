package models

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// settingRegistrationEnabled is the app_settings key controlling whether public
// self-registration is allowed.
const settingRegistrationEnabled = "registration_enabled"

// SettingsStore provides access to the key/value app_settings table.
type SettingsStore struct {
	pool *pgxpool.Pool
}

// NewSettingsStore constructs a SettingsStore backed by the given pool.
func NewSettingsStore(pool *pgxpool.Pool) *SettingsStore {
	return &SettingsStore{pool: pool}
}

// Get returns the value for a key, or fallback when the key is absent.
func (s *SettingsStore) Get(ctx context.Context, key, fallback string) (string, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

// Set upserts a key/value pair.
func (s *SettingsStore) Set(ctx context.Context, key, value string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO app_settings (key, value) VALUES ($1, $2)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`,
		key, value,
	)
	return err
}

// RegistrationEnabled reports whether public self-registration is allowed
// (defaults to true when unset).
func (s *SettingsStore) RegistrationEnabled(ctx context.Context) (bool, error) {
	v, err := s.Get(ctx, settingRegistrationEnabled, "true")
	if err != nil {
		return false, err
	}
	return v == "true", nil
}

// SetRegistrationEnabled toggles public self-registration.
func (s *SettingsStore) SetRegistrationEnabled(ctx context.Context, enabled bool) error {
	v := "false"
	if enabled {
		v = "true"
	}
	return s.Set(ctx, settingRegistrationEnabled, v)
}
