package config

import (
	"strings"
	"testing"
	"time"
)

// envKeys is every variable Load reads. Tests clear all of them first so a
// value that happens to be set in the developer's shell (DATABASE_URL is a
// likely one) can't change what a case is actually asserting.
var envKeys = []string{
	"HTTP_ADDR", "DATABASE_URL", "JWT_SECRET", "CORS_ORIGIN", "APP_BASE_URL",
	"REPO_URL", "BOT_INVITE_URL", "SUPPORT_DISCORD_URL",
	"SMTP_HOST", "SMTP_PORT", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_FROM",
	"DISCORD_BOT_TOKEN", "DISCORD_APP_ID", "DISCORD_GUILD_ID",
	"DISCORD_CLIENT_ID", "DISCORD_CLIENT_SECRET", "DISCORD_OAUTH_REDIRECT_URL",
	"JWT_TTL", "REFRESH_TTL", "PASSWORD_RESET_TTL",
}

// validSecret is long enough to clear MinJWTSecretLen.
const validSecret = "0123456789abcdef0123456789abcdef"

// cleanEnv clears every configuration variable, then sets the two that Load
// requires so a case only has to declare what it actually varies. t.Setenv
// restores the previous values when the test ends.
func cleanEnv(t *testing.T) {
	t.Helper()
	for _, k := range envKeys {
		t.Setenv(k, "")
	}
	t.Setenv("DATABASE_URL", "postgres://localhost/test")
	t.Setenv("JWT_SECRET", validSecret)
}

func TestLoadDefaults(t *testing.T) {
	cleanEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
	}
	if cfg.JWTTTL != 15*time.Minute {
		t.Errorf("JWTTTL = %v, want 15m", cfg.JWTTTL)
	}
	if cfg.RefreshTTL != 30*24*time.Hour {
		t.Errorf("RefreshTTL = %v, want 720h", cfg.RefreshTTL)
	}
	if cfg.PasswordResetTTL != time.Hour {
		t.Errorf("PasswordResetTTL = %v, want 1h", cfg.PasswordResetTTL)
	}
	if cfg.CORSOrigin != "http://localhost:8081" {
		t.Errorf("CORSOrigin = %q", cfg.CORSOrigin)
	}
	// AppBaseURL follows CORSOrigin unless set explicitly, since the reset link
	// points at the frontend.
	if cfg.AppBaseURL != cfg.CORSOrigin {
		t.Errorf("AppBaseURL = %q, want it to default to CORSOrigin %q", cfg.AppBaseURL, cfg.CORSOrigin)
	}
	if cfg.SMTP.Port != "587" {
		t.Errorf("SMTP.Port = %q, want 587", cfg.SMTP.Port)
	}
	if cfg.RepoURL == "" || cfg.BotInviteURL == "" || cfg.SupportDiscordURL == "" {
		t.Error("the /coreteam help links should all have defaults")
	}
}

// TestLoadRequiredValues covers the boot-time refusals. The JWT secret length
// check is the security-relevant one: a short HS256 secret is brute-forceable,
// so booting with one must be impossible rather than merely discouraged.
func TestLoadRequiredValues(t *testing.T) {
	cases := []struct {
		name        string
		databaseURL string
		jwtSecret   string
		wantErr     string
	}{
		{"missing database url", "", validSecret, "DATABASE_URL is required"},
		{"missing jwt secret", "postgres://localhost/test", "", "JWT_SECRET is required"},
		{"short jwt secret", "postgres://localhost/test", "tooshort", "at least 32 bytes"},
		{"secret one byte under the minimum", "postgres://localhost/test", strings.Repeat("a", MinJWTSecretLen-1), "at least 32 bytes"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanEnv(t)
			t.Setenv("DATABASE_URL", tc.databaseURL)
			t.Setenv("JWT_SECRET", tc.jwtSecret)

			_, err := Load()
			if err == nil {
				t.Fatal("got nil error, want a refusal to boot")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadSecretAtExactlyTheMinimumIsAccepted(t *testing.T) {
	cleanEnv(t)
	t.Setenv("JWT_SECRET", strings.Repeat("a", MinJWTSecretLen))

	if _, err := Load(); err != nil {
		t.Errorf("a %d-byte secret should be accepted, got %v", MinJWTSecretLen, err)
	}
}

func TestLoadOverrides(t *testing.T) {
	cleanEnv(t)
	t.Setenv("HTTP_ADDR", ":9999")
	t.Setenv("CORS_ORIGIN", "https://app.example.com")
	t.Setenv("APP_BASE_URL", "https://public.example.com")
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "465")
	t.Setenv("DISCORD_BOT_TOKEN", "bot-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.AppBaseURL != "https://public.example.com" {
		t.Errorf("an explicit APP_BASE_URL should win over CORS_ORIGIN, got %q", cfg.AppBaseURL)
	}
	if !cfg.SMTP.Configured() {
		t.Error("SMTP should report configured once a host is set")
	}
	if !cfg.Discord.Configured() {
		t.Error("Discord should report configured once a bot token is set")
	}
}

// TestLoadDiscordOAuthRedirectDefault checks the callback URL is derived from
// the app base URL, since a mismatch with the Discord portal breaks sign-in.
func TestLoadDiscordOAuthRedirectDefault(t *testing.T) {
	cleanEnv(t)
	t.Setenv("APP_BASE_URL", "https://public.example.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if want := "https://public.example.com/api/auth/discord/callback"; cfg.DiscordOAuth.RedirectURL != want {
		t.Errorf("RedirectURL = %q, want %q", cfg.DiscordOAuth.RedirectURL, want)
	}

	t.Setenv("DISCORD_OAUTH_REDIRECT_URL", "https://api.example.com/cb")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DiscordOAuth.RedirectURL != "https://api.example.com/cb" {
		t.Errorf("an explicit redirect should win, got %q", cfg.DiscordOAuth.RedirectURL)
	}
}

func TestLoadDurationOverrides(t *testing.T) {
	cleanEnv(t)
	t.Setenv("JWT_TTL", "5m")
	t.Setenv("REFRESH_TTL", "48h")
	t.Setenv("PASSWORD_RESET_TTL", "30m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.JWTTTL != 5*time.Minute {
		t.Errorf("JWTTTL = %v", cfg.JWTTTL)
	}
	if cfg.RefreshTTL != 48*time.Hour {
		t.Errorf("RefreshTTL = %v", cfg.RefreshTTL)
	}
	if cfg.PasswordResetTTL != 30*time.Minute {
		t.Errorf("PasswordResetTTL = %v", cfg.PasswordResetTTL)
	}
}

// TestLoadRejectsUnparseableDurations fails fast rather than silently running
// with a default the operator did not intend.
func TestLoadRejectsUnparseableDurations(t *testing.T) {
	for _, key := range []string{"JWT_TTL", "REFRESH_TTL", "PASSWORD_RESET_TTL"} {
		t.Run(key, func(t *testing.T) {
			cleanEnv(t)
			t.Setenv(key, "half an hour")

			_, err := Load()
			if err == nil {
				t.Fatal("got nil error, want a parse failure")
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error = %q, want it to name %s", err, key)
			}
		})
	}
}

func TestDiscordOAuthEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  DiscordOAuthConfig
		want bool
	}{
		{"fully configured", DiscordOAuthConfig{ClientID: "id", ClientSecret: "secret", RedirectURL: "url"}, true},
		{"no client id", DiscordOAuthConfig{ClientSecret: "secret", RedirectURL: "url"}, false},
		{"no secret", DiscordOAuthConfig{ClientID: "id", RedirectURL: "url"}, false},
		{"no redirect", DiscordOAuthConfig{ClientID: "id", ClientSecret: "secret"}, false},
		{"empty", DiscordOAuthConfig{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.Enabled(); got != tc.want {
				t.Errorf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestConfiguredPredicates(t *testing.T) {
	if (SMTPConfig{}).Configured() {
		t.Error("empty SMTP config should not report configured")
	}
	if !(SMTPConfig{Host: "smtp.example.com"}).Configured() {
		t.Error("a host alone is enough to attempt delivery")
	}
	if (DiscordConfig{AppID: "app"}).Configured() {
		t.Error("an app id without a token is not enough to start the bot")
	}
	if !(DiscordConfig{BotToken: "t"}).Configured() {
		t.Error("a bot token is the minimum to start the bot")
	}
}

func TestGetEnvTreatsEmptyAsUnset(t *testing.T) {
	t.Setenv("CTB_TEST_KEY", "")
	if got := getEnv("CTB_TEST_KEY", "fallback"); got != "fallback" {
		t.Errorf("got %q, want the fallback for an empty value", got)
	}
	t.Setenv("CTB_TEST_KEY", "set")
	if got := getEnv("CTB_TEST_KEY", "fallback"); got != "set" {
		t.Errorf("got %q, want the set value", got)
	}
}
