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
		members:    models.NewMemberStore(pool),
		discord:    models.NewDiscordStore(pool),
		premade:    models.NewPremadeStore(pool),
		appBaseURL: cfg.AppBaseURL,
		repoURL:    cfg.RepoURL,
		nameCache:  newHandleNameCache(),
	}

	session, err := discordgo.New("Bot " + cfg.Discord.BotToken)
	if err != nil {
		return err
	}
	// Guild interactions are delivered as interactions (Guilds intent). The
	// /coreteam signup flow is a free-text DM conversation, so we also need the
	// DirectMessages intent (to receive DM message events) and the privileged
	// MessageContent intent (to read what the user types). MessageContent must be
	// enabled for the application in the Discord developer portal.
	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentDirectMessages | discordgo.IntentMessageContent
	session.AddHandler(b.onInteraction)
	session.AddHandler(b.onMessageCreate)

	if err := session.Open(); err != nil {
		return err
	}
	defer session.Close()

	appID := cfg.Discord.AppID
	if appID == "" {
		appID = session.State.User.ID
	}
	for _, cmd := range botCommands {
		if _, err := session.ApplicationCommandCreate(appID, cfg.Discord.GuildID, cmd); err != nil {
			return err
		}
	}
	scope := "globally"
	if cfg.Discord.GuildID != "" {
		scope = "to guild " + cfg.Discord.GuildID
	}
	log.Printf("bot ready as %s; /coreteam, /post, /signup registered %s", session.State.User.Username, scope)

	// Background loop for pre-made trial runs (thread 15 min before, cleanup 2 h
	// after). Stops when ctx is cancelled on shutdown.
	go b.runScheduler(ctx, session)

	<-ctx.Done()
	log.Println("shutting down")
	return nil
}
