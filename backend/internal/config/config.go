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

	// JWTTTL is how long an issued access token remains valid. Access tokens are
	// deliberately short-lived; clients refresh them with a refresh token.
	JWTTTL time.Duration

	// RefreshTTL is how long an issued refresh token remains valid.
	RefreshTTL time.Duration

	// CORSOrigin is the allowed origin for browser requests (the frontend URL).
	CORSOrigin string

	// AppBaseURL is the public base URL of the frontend, used to build links in
	// outgoing emails (e.g. the password-reset link). Defaults to CORSOrigin.
	AppBaseURL string

	// RepoURL is the public source-repository URL, shown by the bot's
	// /coreteam help so users can browse the code and report bugs.
	RepoURL string

	// PasswordResetTTL is how long an issued password-reset token remains valid.
	PasswordResetTTL time.Duration

	// SMTP holds the outbound email configuration. When SMTP.Host is empty,
	// emails are logged instead of sent (suitable for local development).
	SMTP SMTPConfig

	// Discord holds the Discord bot configuration. Only the bot binary requires
	// these; the API server ignores them.
	Discord DiscordConfig

	// DiscordOAuth holds the "Sign in with Discord" OAuth2 settings. Only the API
	// server uses these; the bot ignores them. When ClientID/ClientSecret are
	// empty the API simply omits the Discord sign-in option.
	DiscordOAuth DiscordOAuthConfig
}

// DiscordOAuthConfig holds the settings for browser "Sign in with Discord"
// (OAuth2 authorization-code flow). The redirect URL must exactly match a
// redirect registered for the application in the Discord developer portal.
type DiscordOAuthConfig struct {
	// ClientID is the Discord application's OAuth2 client ID (same value as the
	// application/AppID). Required to enable Discord sign-in.
	ClientID string
	// ClientSecret is the application's OAuth2 client secret. Required to enable
	// Discord sign-in. Never logged.
	ClientSecret string
	// RedirectURL is the absolute, publicly reachable URL Discord redirects back
	// to after authorization (this API's /api/auth/discord/callback). It must be
	// registered verbatim in the Discord portal.
	RedirectURL string
}

// Enabled reports whether enough is configured to offer Discord sign-in.
func (c DiscordOAuthConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

// DiscordConfig holds the Discord bot settings.
type DiscordConfig struct {
	// BotToken authenticates the bot to the Discord gateway. Required by the bot.
	BotToken string
	// AppID is the Discord application (client) ID, used for command registration.
	AppID string
	// GuildID, when set, registers slash commands to that single guild for
	// instant availability (dev). Empty registers commands globally (can take up
	// to ~1h to propagate the first time).
	GuildID string
}

// Configured reports whether the bot has the minimum settings to start.
func (c DiscordConfig) Configured() bool {
	return c.BotToken != ""
}

// SMTPConfig holds outbound email (SMTP) settings.
type SMTPConfig struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Configured reports whether enough SMTP settings are present to send mail.
func (c SMTPConfig) Configured() bool {
	return c.Host != ""
}

// MinJWTSecretLen is the minimum acceptable length (in bytes) for JWT_SECRET. A
// short secret makes HS256 tokens forgeable by brute force, so we refuse to boot
// with anything weaker than 32 bytes (256 bits).
const MinJWTSecretLen = 32

// defaultAccessTTL is the access-token lifetime when JWT_TTL is unset. Kept
// short so a leaked access token has a small window of validity.
const defaultAccessTTL = 15 * time.Minute

// defaultRefreshTTL is the refresh-token lifetime when REFRESH_TTL is unset.
const defaultRefreshTTL = 30 * 24 * time.Hour

// defaultPasswordResetTTL is the reset-token lifetime when PASSWORD_RESET_TTL is
// unset. Kept short so a leaked reset link has a small window of validity.
const defaultPasswordResetTTL = time.Hour

// defaultRepoURL is the public source repository, used by /coreteam help for the
// "browse the code" and "report a bug" links when REPO_URL is unset.
const defaultRepoURL = "https://github.com/greeneca/core-team-builder"

// Load reads configuration from the environment, applying sane defaults for
// local development. It returns an error when a required production value is
// missing.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:         getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:      getEnv("DATABASE_URL", ""),
		JWTSecret:        []byte(getEnv("JWT_SECRET", "")),
		JWTTTL:           defaultAccessTTL,
		RefreshTTL:       defaultRefreshTTL,
		PasswordResetTTL: defaultPasswordResetTTL,
		CORSOrigin:       getEnv("CORS_ORIGIN", "http://localhost:8081"),
		SMTP: SMTPConfig{
			Host:     getEnv("SMTP_HOST", ""),
			Port:     getEnv("SMTP_PORT", "587"),
			Username: getEnv("SMTP_USERNAME", ""),
			Password: getEnv("SMTP_PASSWORD", ""),
			From:     getEnv("SMTP_FROM", ""),
		},
		Discord: DiscordConfig{
			BotToken: getEnv("DISCORD_BOT_TOKEN", ""),
			AppID:    getEnv("DISCORD_APP_ID", ""),
			GuildID:  getEnv("DISCORD_GUILD_ID", ""),
		},
	}

	// The reset link points at the frontend; fall back to the CORS origin when
	// APP_BASE_URL is not set explicitly.
	cfg.AppBaseURL = getEnv("APP_BASE_URL", cfg.CORSOrigin)

	// Public source repo for /coreteam help links.
	cfg.RepoURL = getEnv("REPO_URL", defaultRepoURL)

	// Discord sign-in (OAuth2). The redirect URL defaults to the API callback
	// under the public app base URL, but can be overridden when the API is hosted
	// on a different host/path than the frontend.
	cfg.DiscordOAuth = DiscordOAuthConfig{
		ClientID:     getEnv("DISCORD_CLIENT_ID", ""),
		ClientSecret: getEnv("DISCORD_CLIENT_SECRET", ""),
		RedirectURL:  getEnv("DISCORD_OAUTH_REDIRECT_URL", cfg.AppBaseURL+"/api/auth/discord/callback"),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if len(cfg.JWTSecret) == 0 {
		return nil, fmt.Errorf("JWT_SECRET is required")
	}
	if len(cfg.JWTSecret) < MinJWTSecretLen {
		return nil, fmt.Errorf("JWT_SECRET must be at least %d bytes; generate one with: openssl rand -base64 48", MinJWTSecretLen)
	}

	if ttl := os.Getenv("JWT_TTL"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return nil, fmt.Errorf("invalid JWT_TTL: %w", err)
		}
		cfg.JWTTTL = d
	}

	if ttl := os.Getenv("REFRESH_TTL"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return nil, fmt.Errorf("invalid REFRESH_TTL: %w", err)
		}
		cfg.RefreshTTL = d
	}

	if ttl := os.Getenv("PASSWORD_RESET_TTL"); ttl != "" {
		d, err := time.ParseDuration(ttl)
		if err != nil {
			return nil, fmt.Errorf("invalid PASSWORD_RESET_TTL: %w", err)
		}
		cfg.PasswordResetTTL = d
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
