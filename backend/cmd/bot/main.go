// Command bot runs the Core Team Builder Discord bot.
//
// It connects to the same PostgreSQL database as the API server (via
// internal/models) and to the Discord gateway (via discordgo). It registers the
// /coreteam slash command and handles the related component/modal interactions.
// No inbound network ports are used: the bot opens an outbound WebSocket to
// Discord and reaches the database over the internal network.
package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	// Embed the IANA timezone database so schedule conversions work in the
	// minimal runtime image (mirrors cmd/server).
	_ "time/tzdata"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/config"
	"github.com/core-team-builder/backend/internal/db"
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
	if !cfg.Discord.Configured() {
		log.Fatal("DISCORD_BOT_TOKEN is required to run the bot")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	b := &bot{
		teams:      models.NewTeamStore(pool),
		encounters: models.NewEncounterStore(pool),
		groupings:  models.NewGroupingStore(pool),
		discord:    models.NewDiscordStore(pool),
	}

	session, err := discordgo.New("Bot " + cfg.Discord.BotToken)
	if err != nil {
		return err
	}
	// Guild interactions only need the Guilds intent (slash commands, buttons,
	// and modals are delivered as interactions, not gateway messages).
	session.Identify.Intents = discordgo.IntentsGuilds
	session.AddHandler(b.onInteraction)

	if err := session.Open(); err != nil {
		return err
	}
	defer session.Close()

	appID := cfg.Discord.AppID
	if appID == "" {
		appID = session.State.User.ID
	}
	if _, err := session.ApplicationCommandCreate(appID, cfg.Discord.GuildID, coreTeamCommand); err != nil {
		return err
	}
	scope := "globally"
	if cfg.Discord.GuildID != "" {
		scope = "to guild " + cfg.Discord.GuildID
	}
	log.Printf("bot ready as %s; /coreteam registered %s", session.State.User.Username, scope)

	<-ctx.Done()
	log.Println("shutting down")
	return nil
}
