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
	"github.com/core-team-builder/backend/internal/handlers"
	"github.com/core-team-builder/backend/internal/models"
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
	tokens := auth.NewTokenManager(cfg.JWTSecret, cfg.JWTTTL)
	srv := handlers.New(users, teams, tokens, cfg.CORSOrigin)

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
