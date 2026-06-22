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
	premade    *models.PremadeStore
	// appBaseURL is the public base URL of the web app (APP_BASE_URL), used to
	// build sign-in links the bot sends to users. Empty when unconfigured.
	appBaseURL string
}

// Discord embed limits (and the post's accent color, Discord blurple).
const (
	embedTitleLimit       = 256
	embedDescriptionLimit = 4096
	embedFooterLimit      = 2048
	embedColor            = 0x5865F2
)

// postedByPrefix labels the embed footer noting who posted a signup (e.g.
// "Posted by Ada"). Shared by the /coreteam post overview and premade run posts.
const postedByPrefix = "Posted by "

// Custom IDs / sentinel values for the post's signup dropdown (fill open slots
// or join the general fill list).
const (
	postFillSelectID   = "post_fill_select"
	postFillListValue  = "filllist"
	postFillLeaveValue = "leave"
)

// postComponents are the controls attached to a posted trial overview: a button
// row (the two RSVP buttons + the per-player details button) and, when the
// roster has any open slots (no Discord handle), a signup dropdown so players
// can fill an open slot or join the general fill list. Defined once so the
// initial post and every in-place update render the same controls.
func postComponents(team *models.Team, fills []models.PostFill) []discordgo.MessageComponent {
	rows := []discordgo.MessageComponent{
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
	if row, ok := postFillSelectRow(team, fills); ok {
		rows = append(rows, row)
	}
	return rows
}

// postFillSelectRow builds the signup dropdown: one option per open roster slot
// (a slot with no Discord handle) that isn't already taken by a filler, plus
// "Join the fill list" and "Remove my signup". Returns ok=false when the roster
// has no open slots at all, so a fully-staffed post shows only the RSVP buttons.
func postFillSelectRow(team *models.Team, fills []models.PostFill) (discordgo.MessageComponent, bool) {
	if team == nil {
		return nil, false
	}
	filled := map[int]bool{}
	for _, f := range fills {
		if f.Slot > 0 {
			filled[f.Slot] = true
		}
	}
	opts := make([]discordgo.SelectMenuOption, 0, len(team.Players)+2)
	hasOpen := false
	for _, p := range team.Players { // store returns players slot-ordered
		if strings.TrimSpace(p.DiscordHandle) != "" {
			continue // slot already has an assigned player
		}
		hasOpen = true
		if filled[p.Slot] {
			continue // already claimed by a filler
		}
		// Leave room for the two trailing options (Discord caps a select at 25).
		if len(opts) >= 23 {
			continue
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label: truncate(slotOptionLabel(team, p), 100),
			Value: strconv.Itoa(p.Slot),
			Emoji: &discordgo.ComponentEmoji{Name: team.RoleEmoji(p.Role)},
		})
	}
	if !hasOpen {
		return nil, false
	}
	opts = append(opts,
		discordgo.SelectMenuOption{
			Label:       "Join the fill list",
			Value:       postFillListValue,
			Description: "Be a backup for any role",
			Emoji:       &discordgo.ComponentEmoji{Name: "\U0001F64B"}, // 🙋
		},
		discordgo.SelectMenuOption{
			Label:       "Remove my signup",
			Value:       postFillLeaveValue,
			Description: "Leave your slot or the fill list",
		},
	)
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{
			CustomID:    postFillSelectID,
			Placeholder: "Sign up to fill an open slot",
			Options:     opts,
		},
	}}, true
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
			Name:        "recruit",
			Description: "Post a recruitment post with an I'm Interested button (gathers availability via DM)",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "signup",
			Description: "Post a scheduled run from one of your pre-made teams (per-slot signups)",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "publish",
			Description: "Make one of your signup templates available to everyone in this server",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "timezone",
			Description: "Set or change your remembered timezone for signup scheduling",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "roll",
			Description: "Pick a random ESO trial (includes a re-roll button)",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "login",
			Description: "Post a link to the Core Team Builder web app",
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

// postCommand and signupCommand are top-level aliases that map to the same
// actions as /coreteam post and /coreteam signup, so users can run /post and
// /signup directly. They carry no options and are dispatched by name in
// onCommand.
var postCommand = &discordgo.ApplicationCommand{
	Name:        "post",
	Description: "Post this channel's trial overview with a Get my details button",
}

var signupCommand = &discordgo.ApplicationCommand{
	Name:        "signup",
	Description: "Post a scheduled run from one of your pre-made teams (per-slot signups)",
}

// botCommands is every slash command the bot registers on startup.
var botCommands = []*discordgo.ApplicationCommand{coreTeamCommand, postCommand, signupCommand}

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
	// Top-level aliases for the matching /coreteam subcommands.
	switch data.Name {
	case "post":
		b.handlePost(s, i)
		return
	case "signup":
		b.handlePremade(s, i)
		return
	}
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
	case "recruit":
		b.handleSignupPost(s, i)
	case "signup":
		b.handlePremade(s, i)
	case "publish":
		b.handlePublish(s, i)
	case "roll":
		b.handleRoll(s, i)
	case "timezone":
		b.handleTimezone(s, i)
	case "login":
		b.handleLogin(s, i)
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
	if strings.HasPrefix(id, premadePrefix) {
		b.onPremadeComponent(s, i)
		return
	}
	if strings.HasPrefix(id, rollRerollPrefix) {
		b.handleRollReroll(s, i)
		return
	}
	switch id {
	case "get_my_details":
		b.handleGetMyDetails(s, i)
	case "setup_select":
		b.handleSetupSelect(s, i)
	case "publish_select":
		b.handlePublishSelect(s, i)
	case "timezone_select":
		b.handleTimezoneSelect(s, i)
	case "rsvp_yes":
		b.handleRSVP(s, i, models.RSVPYes)
	case "rsvp_no":
		b.handleRSVP(s, i, models.RSVPNo)
	case postFillSelectID:
		b.handlePostFill(s, i)
	}
}

func (b *bot) onModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.ModalSubmitData().CustomID
	if id == "setup_create_modal" {
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
	// Now that this Discord identity is tied to an app account, grant viewer
	// access to any auto-share team whose member pool lists them. Idempotent;
	// failures are logged, not surfaced.
	if err := b.teams.ShareAutoTeamsForDiscord(ctx, user.ID, userID); err != nil {
		log.Printf("link: auto-share pool teams: %v", err)
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
	for _, t := range teams {
		// Skip signup-template teams: they aren't bound to a channel directly.
		if t.PreMade {
			continue
		}
		if len(options) >= 24 {
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
	footer := ""
	if user := invokingUser(i); user != nil {
		footer = postedByPrefix + displayName(user)
	}
	embed := buildPostEmbed(team, primary, gr, nil, nil, footer)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: postComponents(team, nil),
		},
	})
	if err != nil {
		log.Printf("post: respond: %v", err)
	}
}

// buildPostEmbed assembles the channel-post embed from team data, the current
// RSVPs, and the current fill signups. Each responding roster member gets a
// ✅/❌ icon beside their name; each filled open slot shows the filler's name
// with a `fill` tag and an automatic ✅, and fill-list backups get their own
// section. footerText, when non-empty, is shown as the embed footer (used to note
// who posted). Pass nil rsvps/fills for the initial post.
func buildPostEmbed(team *models.Team, primary *models.Encounter, gr []models.Grouping, rsvps []models.RSVP, fills []models.PostFill, footerText string) *discordgo.MessageEmbed {
	fillBySlot := map[int]string{}
	var fillList []string
	for _, f := range fills {
		name := strings.TrimSpace(f.DiscordUsername)
		if name == "" {
			name = f.DiscordUserID
		}
		if f.Slot == models.PostFillList {
			fillList = append(fillList, name)
		} else {
			fillBySlot[f.Slot] = name
		}
	}
	title, desc := discordfmt.BuildPost(team, primary, gr, rsvpMarks(team, rsvps), fillBySlot, fillList)
	embed := &discordgo.MessageEmbed{
		Title:       truncate(title, embedTitleLimit),
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
	if footerText = strings.TrimSpace(footerText); footerText != "" {
		embed.Footer = &discordgo.MessageEmbedFooter{Text: truncate(footerText, embedFooterLimit)}
	}
	return embed
}

// --- /coreteam recruit (recruitment post + DM intake) ---

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

	// Rebuild the post from current team data so each responder's ✅/❌ lands
	// beside their name in the roster (the RSVP is saved regardless).
	if err := b.renderPostUpdate(ctx, s, i); err != nil {
		log.Printf("rsvp: refresh post: %v", err)
		ephemeral(s, i, "Saved your RSVP, but couldn't refresh the post.")
	}
}

// handlePostFill records the presser's signup from the post's dropdown — filling
// a specific open slot, joining the general fill list, or removing their signup
// — then re-renders the post in place so the roster shows the change. A user
// holds at most one signup per post, so each choice replaces the prior one.
func (b *bot) handlePostFill(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil || i.Message == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}

	ctx, cancel := handlerContext()
	defer cancel()

	switch choice := values[0]; choice {
	case postFillLeaveValue:
		if err := b.discord.LeaveFill(ctx, i.Message.ID, user.ID); err != nil {
			log.Printf("post fill: leave: %v", err)
			ephemeral(s, i, "Something went wrong. Please try again.")
			return
		}
	case postFillListValue:
		if err := b.discord.ClaimFill(ctx, i.Message.ID, i.ChannelID, models.PostFillList, user.ID, displayName(user)); err != nil {
			log.Printf("post fill: join list: %v", err)
			ephemeral(s, i, "Something went wrong. Please try again.")
			return
		}
	default:
		slot, err := strconv.Atoi(choice)
		if err != nil || slot <= 0 {
			return
		}
		// Validate against the live roster so a stale dropdown can't claim a slot
		// that has since been assigned a real player.
		teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
		if err != nil {
			log.Printf("post fill: get binding: %v", err)
			ephemeral(s, i, "Something went wrong. Please try again.")
			return
		}
		team, err := b.teams.Get(ctx, teamID)
		if err != nil {
			log.Printf("post fill: load team: %v", err)
			ephemeral(s, i, "Something went wrong. Please try again.")
			return
		}
		if !isOpenSlot(team, slot) {
			ephemeral(s, i, "That slot already has a player assigned. Pick an open slot or the fill list.")
			return
		}
		err = b.discord.ClaimFill(ctx, i.Message.ID, i.ChannelID, slot, user.ID, displayName(user))
		if errors.Is(err, models.ErrSlotTaken) {
			ephemeral(s, i, "Someone just signed up to fill that slot. Pick another open slot or the fill list.")
			return
		}
		if err != nil {
			log.Printf("post fill: claim: %v", err)
			ephemeral(s, i, "Something went wrong signing you up. Please try again.")
			return
		}
	}

	if err := b.renderPostUpdate(ctx, s, i); err != nil {
		log.Printf("post fill: refresh post: %v", err)
		ephemeral(s, i, "Saved your signup, but couldn't refresh the post.")
	}
}

// existingFooterText returns the footer text on a message's first embed (the
// post's "Posted by …" note), or "" when there is none.
func existingFooterText(msg *discordgo.Message) string {
	if msg == nil || len(msg.Embeds) == 0 {
		return ""
	}
	if f := msg.Embeds[0].Footer; f != nil {
		return f.Text
	}
	return ""
}

// isOpenSlot reports whether a roster slot exists and has no Discord handle set,
// so it's eligible to be filled via the signup dropdown.
func isOpenSlot(team *models.Team, slot int) bool {
	for _, p := range team.Players {
		if p.Slot == slot {
			return strings.TrimSpace(p.DiscordHandle) == ""
		}
	}
	return false
}

// renderPostUpdate re-renders a posted trial overview in place (embed + controls)
// from current team data, RSVPs, and fill signups, in response to a button or
// dropdown interaction on the post. It returns an error only when the post can't
// be rebuilt (so callers can surface a "saved, but couldn't refresh" notice);
// failures from the Discord update call itself are logged and swallowed.
func (b *bot) renderPostUpdate(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if err != nil {
		return err
	}
	team, _, primary, gr, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		return err
	}
	rsvps, err := b.discord.ListRSVPs(ctx, i.Message.ID)
	if err != nil {
		return err
	}
	fills, err := b.discord.ListFills(ctx, i.Message.ID)
	if err != nil {
		return err
	}
	// Preserve the "Posted by" footer set on the original post (RSVP/fill updates
	// re-render the embed from scratch, which would otherwise drop it).
	embed := buildPostEmbed(team, primary, gr, rsvps, fills, existingFooterText(i.Message))
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: postComponents(team, fills),
		},
	}); err != nil {
		log.Printf("post: update respond: %v", err)
	}
	return nil
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

// --- /coreteam login ---

// handleLogin posts a public message with a link to the web app (APP_BASE_URL)
// so members can open Core Team Builder from Discord. Replies ephemerally if the
// base URL isn't configured.
func (b *bot) handleLogin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	url := strings.TrimSpace(b.appBaseURL)
	if url == "" {
		ephemeral(s, i, "The web app URL isn't configured. Ask an admin to set APP_BASE_URL.")
		return
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Log in to Core Team Builder: " + url,
		},
	})
	if err != nil {
		log.Printf("login: respond: %v", err)
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
