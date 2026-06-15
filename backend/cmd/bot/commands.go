package main

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/discordfmt"
	"github.com/core-team-builder/backend/internal/models"
)

// bot bundles the data stores the interaction handlers need.
type bot struct {
	teams      *models.TeamStore
	encounters *models.EncounterStore
	groupings  *models.GroupingStore
	discord    *models.DiscordStore
}

// discordMessageLimit is Discord's hard cap on a message's content length.
const discordMessageLimit = 2000

// createTeamOption is the sentinel select value meaning "create a new team".
const createTeamOption = "__create__"

// coreTeamCommand is the /coreteam slash command and its subcommands.
var coreTeamCommand = &discordgo.ApplicationCommand{
	Name:        "coreteam",
	Description: "Manage and post a Core Team Builder trial for this channel",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "link",
			Description: "Link your Discord account to Core Team Builder using a code from the web app",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "code",
					Description: "The link code shown in the web app",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "setup",
			Description: "Bind this channel to one of your teams (or create a new team)",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "post",
			Description: "Post this channel's trial overview with a Get my details button",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "status",
			Description: "Show which team this channel is bound to",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "unset",
			Description: "Unbind this channel from its team",
		},
	},
}

// onInteraction dispatches every interaction to the right handler.
func (b *bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		b.onCommand(s, i)
	case discordgo.InteractionMessageComponent:
		b.onComponent(s, i)
	case discordgo.InteractionModalSubmit:
		b.onModalSubmit(s, i)
	}
}

func (b *bot) onCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]
	switch sub.Name {
	case "link":
		b.handleLink(s, i, sub)
	case "setup":
		b.handleSetup(s, i)
	case "post":
		b.handlePost(s, i)
	case "status":
		b.handleStatus(s, i)
	case "unset":
		b.handleUnset(s, i)
	}
}

func (b *bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	switch id {
	case "get_my_details":
		b.handleGetMyDetails(s, i)
	case "setup_select":
		b.handleSetupSelect(s, i)
	}
}

func (b *bot) onModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ModalSubmitData().CustomID == "setup_create_modal" {
		b.handleSetupCreate(s, i)
	}
}

// --- /coreteam link ---

func (b *bot) handleLink(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	code := normalizeCode(sub.Options[0].StringValue())
	if code == "" {
		ephemeral(s, i, "Please provide the link code from the web app.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	userID, err := b.discord.ConsumeLinkCode(ctx, auth.HashRefreshToken(code))
	if errors.Is(err, models.ErrLinkCodeInvalid) {
		ephemeral(s, i, "That code is invalid or expired. Generate a new one in the web app and try again.")
		return
	}
	if err != nil {
		log.Printf("link: consume code: %v", err)
		ephemeral(s, i, "Something went wrong linking your account. Please try again.")
		return
	}

	if err := b.discord.LinkUser(ctx, userID, user.ID, displayName(user)); err != nil {
		if errors.Is(err, models.ErrDiscordAlreadyLinked) {
			ephemeral(s, i, "This Discord account is already linked to another Core Team Builder user.")
			return
		}
		log.Printf("link: link user: %v", err)
		ephemeral(s, i, "Something went wrong linking your account. Please try again.")
		return
	}
	ephemeral(s, i, "Your Discord account is now linked to Core Team Builder. You can run /coreteam setup.")
}

// --- /coreteam setup ---

func (b *bot) handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !hasManageChannels(i) {
		ephemeral(s, i, "You need the Manage Channels permission to bind a channel.")
		return
	}
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first: open the web app, generate a code, then run /coreteam link code:<code>.")
		return
	}
	if err != nil {
		log.Printf("setup: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	teams, err := b.teams.ListForUser(ctx, appUserID)
	if err != nil {
		log.Printf("setup: list teams: %v", err)
		ephemeral(s, i, "Something went wrong loading your teams. Please try again.")
		return
	}

	options := make([]discordgo.SelectMenuOption, 0, len(teams)+1)
	// Discord allows at most 25 options; reserve one for "create new".
	for idx, t := range teams {
		if idx >= 24 {
			break
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(t.Name, 100),
			Value: strconv.FormatInt(t.ID, 10),
		})
	}
	options = append(options, discordgo.SelectMenuOption{
		Label:       "Create a new team…",
		Value:       createTeamOption,
		Description: "Make a fresh empty team and bind it to this channel",
	})

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Choose a team to bind to this channel:",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    "setup_select",
						Placeholder: "Select a team",
						Options:     options,
					},
				}},
			},
		},
	})
	if err != nil {
		log.Printf("setup: respond: %v", err)
	}
}

func (b *bot) handleSetupSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !hasManageChannels(i) {
		ephemeral(s, i, "You need the Manage Channels permission to bind a channel.")
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	choice := values[0]
	if choice == createTeamOption {
		// Open a modal to capture the new team's name.
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseModal,
			Data: &discordgo.InteractionResponseData{
				CustomID: "setup_create_modal",
				Title:    "Create a new team",
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "team_name",
							Label:       "Team name",
							Style:       discordgo.TextInputShort,
							Required:    true,
							MaxLength:   100,
							Placeholder: "e.g. Tuesday Core",
						},
					}},
				},
			},
		})
		if err != nil {
			log.Printf("setup select: modal: %v", err)
		}
		return
	}

	teamID, err := strconv.ParseInt(choice, 10, 64)
	if err != nil {
		ephemeral(s, i, "That selection was invalid.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, ok := b.requireLinkedOwner(ctx, s, i, teamID)
	if !ok {
		return
	}
	if err := b.discord.BindChannel(ctx, i.GuildID, i.ChannelID, teamID, appUserID); err != nil {
		log.Printf("setup select: bind: %v", err)
		ephemeral(s, i, "Something went wrong binding the channel. Please try again.")
		return
	}
	team, _ := b.teams.Get(ctx, teamID)
	name := "the team"
	if team != nil {
		name = team.Name
	}
	updateEphemeral(s, i, "Bound this channel to **"+name+"**. Run /coreteam post to share the trial.")
}

func (b *bot) handleSetupCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	name := strings.TrimSpace(modalValue(i, "team_name"))
	if name == "" {
		ephemeral(s, i, "Please provide a team name.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link.")
		return
	}
	if err != nil {
		log.Printf("setup create: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	team, err := b.teams.Create(ctx, appUserID, name, 0)
	if err != nil {
		log.Printf("setup create: create team: %v", err)
		ephemeral(s, i, "Could not create the team. Please try again.")
		return
	}
	if err := b.discord.BindChannel(ctx, i.GuildID, i.ChannelID, team.ID, appUserID); err != nil {
		log.Printf("setup create: bind: %v", err)
		ephemeral(s, i, "Created the team but could not bind the channel. Try /coreteam setup again.")
		return
	}
	ephemeral(s, i, "Created **"+team.Name+"** and bound it to this channel. Fill it out in the web app, then run /coreteam post.")
}

// --- /coreteam post ---

func (b *bot) handlePost(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, cancel := handlerContext()
	defer cancel()

	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if errors.Is(err, models.ErrChannelNotBound) {
		ephemeral(s, i, "This channel isn't bound to a team yet. Run /coreteam setup first.")
		return
	}
	if err != nil {
		log.Printf("post: get binding: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	team, _, primary, gr, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		log.Printf("post: load team: %v", err)
		ephemeral(s, i, "Could not load the team. It may have been deleted; re-run /coreteam setup.")
		return
	}

	content := truncate(discordfmt.Overview(team, primary, gr), discordMessageLimit)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{
						Label:    "Get my details",
						Style:    discordgo.PrimaryButton,
						CustomID: "get_my_details",
					},
				}},
			},
		},
	})
	if err != nil {
		log.Printf("post: respond: %v", err)
	}
}

// --- Get my details button ---

func (b *bot) handleGetMyDetails(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if errors.Is(err, models.ErrChannelNotBound) {
		ephemeral(s, i, "This channel isn't bound to a team anymore.")
		return
	}
	if err != nil {
		log.Printf("details: get binding: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	team, encs, _, _, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		log.Printf("details: load team: %v", err)
		ephemeral(s, i, "Could not load the team. Please try again.")
		return
	}

	player, ok := matchPlayer(team, user)
	if !ok {
		ephemeral(s, i, "You're not on this trial — no roster slot matches your Discord handle. Ask your raid lead to set your handle to `"+displayName(user)+"`.")
		return
	}

	detail := truncate(discordfmt.PlayerDetail(team, player, encs), discordMessageLimit)
	if dm, err := s.UserChannelCreate(user.ID); err == nil {
		if _, err := s.ChannelMessageSend(dm.ID, detail); err == nil {
			ephemeral(s, i, "Sent your trial details via DM.")
			return
		}
	}
	// DMs likely closed — fall back to an ephemeral reply only the user sees.
	ephemeral(s, i, "I couldn't DM you (your DMs may be closed). Here are your details:\n\n"+detail)
}

// --- /coreteam status & unset ---

func (b *bot) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, cancel := handlerContext()
	defer cancel()

	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if errors.Is(err, models.ErrChannelNotBound) {
		ephemeral(s, i, "This channel isn't bound to a team. Run /coreteam setup.")
		return
	}
	if err != nil {
		log.Printf("status: get binding: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	team, err := b.teams.Get(ctx, teamID)
	if err != nil {
		ephemeral(s, i, "This channel is bound to a team that no longer exists. Re-run /coreteam setup.")
		return
	}
	ephemeral(s, i, "This channel is bound to **"+team.Name+"**.")
}

func (b *bot) handleUnset(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !hasManageChannels(i) {
		ephemeral(s, i, "You need the Manage Channels permission to unbind a channel.")
		return
	}
	ctx, cancel := handlerContext()
	defer cancel()
	if err := b.discord.UnbindChannel(ctx, i.ChannelID); err != nil {
		log.Printf("unset: unbind: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	ephemeral(s, i, "Unbound this channel.")
}

// --- shared helpers ---

// loadTeamData fetches the team, its encounters (with loadouts), the primary
// encounter, and groupings. When the team has encounters disabled, only the
// first encounter is loaded (mirroring the web app's export behavior).
func (b *bot) loadTeamData(ctx context.Context, teamID int64) (*models.Team, []models.Encounter, *models.Encounter, []models.Grouping, error) {
	team, err := b.teams.Get(ctx, teamID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	list, err := b.encounters.ListForTeam(ctx, teamID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var full []models.Encounter
	if team.EncountersEnabled {
		for _, e := range list {
			fe, err := b.encounters.Get(ctx, e.ID)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			full = append(full, *fe)
		}
	} else if len(list) > 0 {
		fe, err := b.encounters.Get(ctx, list[0].ID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		full = append(full, *fe)
	}
	var primary *models.Encounter
	if len(full) > 0 {
		primary = &full[0]
	}
	gr, err := b.groupings.ListForTeam(ctx, teamID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return team, full, primary, gr, nil
}

// requireLinkedOwner resolves the invoking Discord user to an app user and
// confirms they can access the team. Responds ephemerally and returns ok=false
// on any failure.
func (b *bot) requireLinkedOwner(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, teamID int64) (int64, bool) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return 0, false
	}
	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "Link your account first with /coreteam link.")
		return 0, false
	}
	if err != nil {
		log.Printf("require linked: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return 0, false
	}
	found, _, err := b.teams.Access(ctx, teamID, appUserID)
	if err != nil {
		log.Printf("require linked: access: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return 0, false
	}
	if !found {
		ephemeral(s, i, "You don't have access to that team.")
		return 0, false
	}
	return appUserID, true
}

// matchPlayer finds the roster slot belonging to a Discord user. It prefers an
// exact Discord ID/mention stored in discord_handle, then falls back to a
// case-insensitive match against the user's username or global (display) name.
func matchPlayer(team *models.Team, user *discordgo.User) (models.Player, bool) {
	id := user.ID
	uname := strings.ToLower(user.Username)
	gname := strings.ToLower(user.GlobalName)
	for _, p := range team.Players {
		h := strings.TrimSpace(p.DiscordHandle)
		if h == "" {
			continue
		}
		// Mention or raw ID forms.
		if h == "<@"+id+">" || h == "<@!"+id+">" || h == id {
			return p, true
		}
		hl := strings.ToLower(strings.TrimPrefix(h, "@"))
		if hl == uname || (gname != "" && hl == gname) {
			return p, true
		}
	}
	return models.Player{}, false
}

// invokingUser returns the user who triggered an interaction (Member in guilds,
// User in DMs).
func invokingUser(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

// displayName returns the user's global (display) name when set, else username.
func displayName(u *discordgo.User) string {
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// hasManageChannels reports whether the invoking member has the Manage Channels
// or Administrator permission in the guild.
func hasManageChannels(i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	perms := i.Member.Permissions
	return perms&discordgo.PermissionManageChannels != 0 || perms&discordgo.PermissionAdministrator != 0
}

// modalValue extracts a text-input value from a submitted modal by custom ID.
func modalValue(i *discordgo.InteractionCreate, customID string) string {
	for _, row := range i.ModalSubmitData().Components {
		ar, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, c := range ar.Components {
			if ti, ok := c.(*discordgo.TextInput); ok && ti.CustomID == customID {
				return ti.Value
			}
		}
	}
	return ""
}

// normalizeCode upper-cases and strips spaces/dashes from a typed link code.
func normalizeCode(code string) string {
	r := strings.NewReplacer(" ", "", "-", "", "\t", "")
	return strings.ToUpper(r.Replace(strings.TrimSpace(code)))
}

// ephemeral sends a private interaction reply visible only to the invoker.
func ephemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: msg,
		},
	})
	if err != nil {
		log.Printf("respond ephemeral: %v", err)
	}
}

// updateEphemeral replaces the original ephemeral message (used after a select
// menu, which must update rather than create a new response).
func updateEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    msg,
			Components: []discordgo.MessageComponent{},
		},
	})
	if err != nil {
		log.Printf("update ephemeral: %v", err)
	}
}

// truncate caps s to at most max characters (rune-aware so multibyte runes are
// never split), appending an ellipsis when it had to cut.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func handlerContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}
