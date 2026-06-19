// Command server runs the Core Team Builder HTTP API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	// Embed the IANA timezone database in the binary so time.LoadLocation works
	// in the minimal Alpine runtime image (which has no system tzdata).
	_ "time/tzdata"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/config"
	"github.com/core-team-builder/backend/internal/db"
	"github.com/core-team-builder/backend/internal/email"
	"github.com/core-team-builder/backend/internal/handlers"
	"github.com/core-team-builder/backend/internal/models"
	"github.com/core-team-builder/backend/internal/realtime"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	users := models.NewUserStore(pool)
	teams := models.NewTeamStore(pool)
	encounters := models.NewEncounterStore(pool)
	groupings := models.NewGroupingStore(pool)
	members := models.NewMemberStore(pool)
	settings := models.NewSettingsStore(pool)
	refreshTokens := models.NewRefreshTokenStore(pool)
	passwordResets := models.NewPasswordResetStore(pool)
	discord := models.NewDiscordStore(pool)
	startTokenCleanup(ctx, refreshTokens, passwordResets, discord, time.Hour)
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL, cfg.RefreshTTL)

	// Live-collaboration hub: LISTENs for team-change notifications (emitted by
	// DB triggers on any write, including the bot's) and fans them out to the
	// per-team SSE connections. Uses a dedicated connection, not the pool.
	hub := realtime.NewHub()
	go hub.Listen(ctx, cfg.DatabaseURL)

	var mailer email.Mailer
	if cfg.SMTP.Configured() {
		mailer = email.NewSMTPMailer(email.SMTPConfig{
			Host:     cfg.SMTP.Host,
			Port:     cfg.SMTP.Port,
			Username: cfg.SMTP.Username,
			Password: cfg.SMTP.Password,
			From:     cfg.SMTP.From,
		})
		log.Printf("email: SMTP delivery via %s:%s", cfg.SMTP.Host, cfg.SMTP.Port)
	} else {
		mailer = email.LogMailer{}
		log.Print("email: SMTP not configured; reset emails will be logged (dev mode)")
	}

	srv := handlers.New(handlers.Config{
		Users:            users,
		Teams:            teams,
		Encounters:       encounters,
		Groupings:        groupings,
		Members:          members,
		Settings:         settings,
		RefreshTokens:    refreshTokens,
		PasswordResets:   passwordResets,
		Discord:          discord,
		Tokens:           tokens,
		Mailer:           mailer,
		Realtime:         hub,
		CORSOrigin:       cfg.CORSOrigin,
		AppBaseURL:       cfg.AppBaseURL,
		PasswordResetTTL: cfg.PasswordResetTTL,
		DiscordOAuth: handlers.DiscordOAuthConfig{
			ClientID:     cfg.DiscordOAuth.ClientID,
			ClientSecret: cfg.DiscordOAuth.ClientSecret,
			RedirectURL:  cfg.DiscordOAuth.RedirectURL,
		},
	})

	if cfg.DiscordOAuth.Enabled() {
		log.Printf("auth: Discord sign-in enabled (redirect %s)", cfg.DiscordOAuth.RedirectURL)
	} else {
		log.Print("auth: Discord sign-in disabled (set DISCORD_CLIENT_ID/DISCORD_CLIENT_SECRET to enable)")
	}

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("listening on %s", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server error: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Println("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}

// startTokenCleanup runs a periodic sweep that deletes expired/revoked refresh
// tokens and expired/used password-reset tokens until ctx is cancelled. It runs
// an initial sweep on startup so a long-down deployment doesn't wait a full
// interval to catch up. The deletes are idempotent, so running it on multiple
// replicas is harmless.
func startTokenCleanup(ctx context.Context, refreshTokens *models.RefreshTokenStore, passwordResets *models.PasswordResetStore, discord *models.DiscordStore, every time.Duration) {
	sweep := func() {
		// Bound each sweep so a slow query can't hang on shutdown.
		sweepCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := refreshTokens.DeleteExpired(sweepCtx); err != nil {
			log.Printf("refresh token cleanup: %v", err)
		}
		if err := passwordResets.DeleteExpired(sweepCtx); err != nil {
			log.Printf("password reset cleanup: %v", err)
		}
		if err := discord.DeleteExpiredLinkCodes(sweepCtx); err != nil {
			log.Printf("discord link code cleanup: %v", err)
		}
	}

	go func() {
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		sweep()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sweep()
			}
		}
	}()
}
