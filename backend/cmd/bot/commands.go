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
	members    *models.MemberStore
	discord    *models.DiscordStore
}

// Discord embed limits (and the post's accent color, Discord blurple).
const (
	embedTitleLimit       = 256
	embedDescriptionLimit = 4096
	embedColor            = 0x5865F2
)

// postComponents are the buttons attached to a posted trial overview: the two
// RSVP buttons (coming / not coming) and the per-player details button. Defined
// once so /coreteam post and the RSVP update both render the same row.
func postComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Coming",
				Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
				Style:    discordgo.SuccessButton,
				CustomID: "rsvp_yes",
			},
			discordgo.Button{
				Label:    "Not coming",
				Emoji:    &discordgo.ComponentEmoji{Name: "❌"},
				Style:    discordgo.DangerButton,
				CustomID: "rsvp_no",
			},
			discordgo.Button{
				Label:    "Get My Build Details",
				Style:    discordgo.PrimaryButton,
				CustomID: "get_my_details",
			},
		}},
	}
}

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
			Name:        "signup",
			Description: "Post a recruitment signup with an I'm Interested button (gathers availability via DM)",
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
	case "signup":
		b.handleSignupPost(s, i)
	case "status":
		b.handleStatus(s, i)
	case "unset":
		b.handleUnset(s, i)
	}
}

func (b *bot) onComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	// The signup intake flow encodes context (member id, day, role) in the custom
	// ID, so dispatch those by prefix before the exact-match cases.
	if strings.HasPrefix(id, signupPrefix) {
		b.onSignupComponent(s, i)
		return
	}
	switch id {
	case "get_my_details":
		b.handleGetMyDetails(s, i)
	case "setup_select":
		b.handleSetupSelect(s, i)
	case "rsvp_yes":
		b.handleRSVP(s, i, models.RSVPYes)
	case "rsvp_no":
		b.handleRSVP(s, i, models.RSVPNo)
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

	// Render as an embed so the post is wrapped in a tidy box (colored bar +
	// border) and the schedule renders as a per-viewer dynamic timestamp. The
	// self-required pen/crit moved to the per-player build-details DM. A fresh
	// post has no RSVPs yet, so no status marks are shown.
	embed := buildPostEmbed(team, primary, gr, nil)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: postComponents(),
		},
	})
	if err != nil {
		log.Printf("post: respond: %v", err)
	}
}

// buildPostEmbed assembles the channel-post embed from team data and the current
// RSVPs. Each responding roster member gets a ✅/❌ icon beside their name in the
// roster. Pass nil rsvps for the initial post.
func buildPostEmbed(team *models.Team, primary *models.Encounter, gr []models.Grouping, rsvps []models.RSVP) *discordgo.MessageEmbed {
	title, desc := discordfmt.BuildPost(team, primary, gr, rsvpMarks(team, rsvps))
	return &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
}

// --- /coreteam signup (recruitment post + DM intake) ---

// handleSignupPost posts the team's recruitment signup: an embed with the team's
// signup body and an "I'm Interested" button that kicks off the DM intake flow.
func (b *bot) handleSignupPost(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, cancel := handlerContext()
	defer cancel()

	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if errors.Is(err, models.ErrChannelNotBound) {
		ephemeral(s, i, "This channel isn't bound to a team yet. Run /coreteam setup first.")
		return
	}
	if err != nil {
		log.Printf("signup: get binding: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	team, err := b.teams.Get(ctx, teamID)
	if err != nil {
		ephemeral(s, i, "Could not load the team. It may have been deleted; re-run /coreteam setup.")
		return
	}

	body := strings.TrimSpace(team.SignupPost)
	if body == "" {
		body = "Interested in joining? Press the button below and I'll DM you a few questions about your availability, roles, and classes."
	}
	embed := &discordgo.MessageEmbed{
		Title:       truncate(team.Name+" — Signup", embedTitleLimit),
		Description: truncate(body, embedDescriptionLimit),
		Color:       embedColor,
	}
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: signupComponents(),
		},
	})
	if err != nil {
		log.Printf("signup: respond: %v", err)
	}
}

// signupComponents is the button row on a recruitment signup post.
func signupComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "I'm Interested",
				Emoji:    &discordgo.ComponentEmoji{Name: "\U0001F64B"}, // 🙋
				Style:    discordgo.SuccessButton,
				CustomID: signupJoinID,
			},
		}},
	}
}

// --- RSVP buttons (✅ Coming / ❌ Not coming) ---

// handleRSVP records the presser's attendance for the post they clicked, then
// edits the post in place so everyone sees the updated Coming / Not coming
// tally. RSVPs are keyed to this specific message, so a fresh /coreteam post
// starts a new tally.
func (b *bot) handleRSVP(s *discordgo.Session, i *discordgo.InteractionCreate, status string) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	if i.Message == nil {
		ephemeral(s, i, "Could not find the post to update.")
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	if err := b.discord.SetRSVP(ctx, i.Message.ID, i.ChannelID, user.ID, displayName(user), status); err != nil {
		log.Printf("rsvp: set: %v", err)
		ephemeral(s, i, "Something went wrong saving your RSVP. Please try again.")
		return
	}
	rsvps, err := b.discord.ListRSVPs(ctx, i.Message.ID)
	if err != nil {
		log.Printf("rsvp: list: %v", err)
		ephemeral(s, i, "Saved your RSVP, but couldn't refresh the post.")
		return
	}

	// Rebuild the post from current team data so each responder's ✅/❌ lands
	// beside their name in the roster. Fall back to keeping the existing embed if
	// the team can no longer be loaded (the RSVP is still saved).
	embed, err := b.rsvpEmbed(ctx, i, rsvps)
	if err != nil {
		log.Printf("rsvp: rebuild post: %v", err)
		if len(i.Message.Embeds) > 0 {
			embed = i.Message.Embeds[0]
		} else {
			ephemeral(s, i, "Saved your RSVP, but couldn't refresh the post.")
			return
		}
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: postComponents(),
		},
	})
	if err != nil {
		log.Printf("rsvp: respond: %v", err)
	}
}

// rsvpEmbed reloads the team bound to the post's channel and re-renders the full
// post embed with the current RSVPs (so marks appear beside names).
func (b *bot) rsvpEmbed(ctx context.Context, i *discordgo.InteractionCreate, rsvps []models.RSVP) (*discordgo.MessageEmbed, error) {
	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if err != nil {
		return nil, err
	}
	team, _, primary, gr, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		return nil, err
	}
	return buildPostEmbed(team, primary, gr, rsvps), nil
}

// rsvpMarks matches each RSVP to a roster slot (by Discord ID/handle) and
// returns a slot -> status map for rendering the inline ✅/❌ icons. Responders
// who can't be matched to a slot are simply omitted (no separate list is shown).
func rsvpMarks(team *models.Team, rsvps []models.RSVP) map[int]string {
	marks := map[int]string{}
	if team == nil {
		return marks
	}
	for _, r := range rsvps {
		if r.Status != models.RSVPYes && r.Status != models.RSVPNo {
			continue
		}
		u := &discordgo.User{ID: r.DiscordUserID, Username: r.DiscordUsername, GlobalName: r.DiscordUsername}
		if p, ok := matchPlayer(team, u); ok {
			marks[p.Slot] = r.Status
		}
	}
	return marks
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

	title, desc := discordfmt.PlayerDetail(team, player, encs)
	embed := &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
	if dm, err := s.UserChannelCreate(user.ID); err == nil {
		if _, err := s.ChannelMessageSendEmbed(dm.ID, embed); err == nil {
			ephemeral(s, i, "Sent your trial details via DM.")
			return
		}
	}
	// DMs likely closed — fall back to an ephemeral reply (boxed embed) only the
	// user sees.
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "I couldn't DM you (your DMs may be closed). Here are your details:",
			Embeds:  []*discordgo.MessageEmbed{embed},
		},
	})
	if err != nil {
		log.Printf("details: ephemeral fallback: %v", err)
	}
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
