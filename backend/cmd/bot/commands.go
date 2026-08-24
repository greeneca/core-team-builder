package main

import (
	"context"
	"errors"
	"fmt"
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
	teams        *models.TeamStore
	encounters   *models.EncounterStore
	groupings    *models.GroupingStore
	members      *models.MemberStore
	discord      *models.DiscordStore
	premade      *models.PremadeStore
	rosterImages *models.RosterImageStore
	// appBaseURL is the public base URL of the web app (APP_BASE_URL), used to
	// build sign-in links the bot sends to users. Empty when unconfigured.
	appBaseURL string
	// repoURL is the public source-repository URL (REPO_URL), shown by
	// /coreteam help for browsing the code and reporting bugs.
	repoURL string
	// botInviteURL is the Discord authorization link that installs this bot in
	// another server (BOT_INVITE_URL), shown by /coreteam help.
	botInviteURL string
	// supportDiscordURL invites users to the project's own Discord server
	// (SUPPORT_DISCORD_URL), shown by /coreteam help.
	supportDiscordURL string
	// nameCache memoizes resolved Discord display names (by guild+user) so
	// re-rendering a post on every RSVP/fill press doesn't re-hit the API.
	nameCache *handleNameCache
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

// postLocked reports whether a post's signups are closed because its locked run
// time has passed. runAtUnix is 0 for posts with no concrete schedule (or an
// untracked post), which are never locked — there's no run instant to pass.
func postLocked(runAtUnix int64) bool {
	return runAtUnix > 0 && time.Now().Unix() >= runAtUnix
}

// postComponents are the controls attached to a posted trial overview: a button
// row (the two RSVP buttons, the per-player details button, and the admin
// Manage button) and a signup dropdown so players can fill a slot or join the
// general fill list (the dropdown is always shown so people can volunteer as
// backups even when no slot is open). marks is the slot -> RSVP status map (so
// slots whose assigned player declined become fillable). Defined once so the
// initial post and every in-place update render the same controls.
//
// locked closes signups once the run time has passed (see postLocked): the RSVP
// buttons and the signup dropdown are disabled, but "Build Details" and
// "Manage" stay active — players can still pull their loadout for a run in
// progress, and Manage's own menu marks the actions signups have closed for.
func postComponents(team *models.Team, fills []models.PostFill, marks map[int]string, locked bool) []discordgo.MessageComponent {
	rows := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Coming",
				Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
				Style:    discordgo.SuccessButton,
				CustomID: "rsvp_yes",
				Disabled: locked,
			},
			discordgo.Button{
				Label:    "Not Coming",
				Emoji:    &discordgo.ComponentEmoji{Name: "❌"},
				Style:    discordgo.DangerButton,
				CustomID: "rsvp_no",
				Disabled: locked,
			},
			// The custom ID stays "get_my_details" from when the button only sent
			// your own build, so buttons on already-posted messages keep routing.
			discordgo.Button{
				Label:    "Build Details",
				Style:    discordgo.PrimaryButton,
				CustomID: "get_my_details",
			},
			// Shown to everyone because Discord can't hide a button per-user;
			// unauthorized pressers get an ephemeral rejection instead (the same
			// approach as the premade run's "Edit run" button).
			discordgo.Button{
				Label:    "Manage",
				Emoji:    &discordgo.ComponentEmoji{Name: "\U0001F6E1\uFE0F"}, // 🛡️
				Style:    discordgo.SecondaryButton,
				CustomID: postManageID,
			},
		}},
	}
	if row, ok := postFillSelectRow(team, fills, marks, locked); ok {
		rows = append(rows, row)
	}
	return rows
}

// postFillSelectRow builds the signup dropdown: one option per fillable roster
// slot that isn't already taken by a filler, plus "Join the fill list" and
// "Remove my signup". A slot is fillable when it has no Discord handle (open) or
// its assigned player marked themselves "not coming" (absent, per marks). The
// "Join the fill list" / "Remove my signup" options are always present, so the
// dropdown is shown even on a fully staffed post (people can still volunteer as
// backups). ok=false only when team is nil.
//
// locked disables the dropdown and swaps its placeholder to a "signups closed"
// note once the run time has passed (see postLocked).
func postFillSelectRow(team *models.Team, fills []models.PostFill, marks map[int]string, locked bool) (discordgo.MessageComponent, bool) {
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
	for _, p := range team.Players { // store returns players slot-ordered
		assigned := strings.TrimSpace(p.DiscordHandle) != ""
		absent := assigned && marks[p.Slot] == models.RSVPNo
		if assigned && !absent {
			continue // slot has an assigned player who hasn't declined
		}
		if filled[p.Slot] {
			continue // already claimed by a filler
		}
		// Leave room for the two trailing options (Discord caps a select at 25).
		if len(opts) >= 23 {
			continue
		}
		label := slotOptionLabel(team, p)
		if absent {
			label = "Fill for " + fillForName(p) + " · " + label
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label: truncate(label, 100),
			Value: strconv.Itoa(p.Slot),
			Emoji: &discordgo.ComponentEmoji{Name: team.RoleEmoji(p.Role)},
		})
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
	placeholder := "Sign up to fill a slot or join the fill list"
	if locked {
		placeholder = "Signups are closed — this run has started"
	}
	return discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.SelectMenu{
			CustomID:    postFillSelectID,
			Placeholder: placeholder,
			Options:     opts,
			Disabled:    locked,
		},
	}}, true
}

// fillForName is the assigned player's name used in a "Fill for …" dropdown
// label, falling back to the slot number when the roster name is blank.
func fillForName(p models.Player) string {
	if n := strings.TrimSpace(p.Name); n != "" {
		return n
	}
	return "Slot " + strconv.Itoa(p.Slot)
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
		{
			Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
			Name:        "permissions",
			Description: "Manage which roles can use the Edit/Delete buttons on signup runs",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "add",
					Description: "Allow a role to use the Edit/Delete buttons on signup runs",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionRole,
							Name:        "role",
							Description: "The role to allow",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "remove",
					Description: "Stop a role from using the Edit/Delete buttons on signup runs",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionRole,
							Name:        "role",
							Description: "The role to remove",
							Required:    true,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "list",
					Description: "List the roles that can use the Edit/Delete buttons on signup runs",
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
			Name:        "actionlog",
			Description: "Log signup activity (signups, RSVPs, changes) to a channel",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "set",
					Description: "Designate a channel as this server's action log",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionChannel,
							Name:        "channel",
							Description: "The channel to log signup activity to",
							Required:    true,
							ChannelTypes: []discordgo.ChannelType{
								discordgo.ChannelTypeGuildText,
							},
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "off",
					Description: "Stop logging signup activity in this server",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "status",
					Description: "Show which channel signup activity is logged to",
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "help",
			Description: "DM you a command reference, web app link, and where to report bugs",
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
	case "permissions":
		b.handlePermissions(s, i, sub)
	case "actionlog":
		b.handleActionLog(s, i, sub)
	case "help":
		b.handleHelp(s, i)
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
	// The Manage flow runs from its own ephemeral, so its follow-up controls
	// encode the post's message id (and the chosen slot) in their custom IDs.
	if strings.HasPrefix(id, postManageID) {
		b.onPostManageComponent(s, i)
		return
	}
	// The build-details picker likewise runs from its own ephemeral, and encodes
	// the presser's own roster slot so re-renders keep marking it.
	if strings.HasPrefix(id, detailsPickID) {
		b.handleDetailsPick(s, i)
		return
	}
	switch id {
	case "get_my_details":
		b.handleGetMyDetails(s, i)
	case "setup_select":
		b.handleSetupSelect(s, i)
	case "recruit_select":
		b.handleRecruitSelect(s, i)
	case helpSelectID:
		b.handleHelpSelect(s, i)
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
		// Prefer the poster's server nickname (interactionDisplayName reads
		// i.Member.Nick) so the "Posted by" footer matches how they appear here.
		footer = postedByPrefix + interactionDisplayName(i)
	}
	// Lock the run date at post time: compute the next-run instant once, show it
	// on the embed, and hand the same value to startPostThread so the tracked
	// post and the embed agree. Re-renders reuse this locked value (loaded from
	// discord_posts) instead of recomputing, so the advertised date never drifts
	// and disappears once the run is past. 0 when the team has no concrete
	// schedule (BuildPost then falls back to the plain day/time text).
	var runAtUnix int64
	if unix, ok := discordfmt.NextRunUnix(team.ScheduleDays, team.ScheduleTime); ok {
		runAtUnix = unix
	}
	names := b.resolveRosterNames(s, i.GuildID, team)
	embed := buildPostEmbed(team, primary, gr, nil, nil, names, footer, runAtUnix)
	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: postComponents(team, nil, nil, postLocked(runAtUnix)),
		},
	})
	if err != nil {
		log.Printf("post: respond: %v", err)
		return
	}
	// Open a discussion thread off the post and track it so attendees get a
	// pre-run ping. Best effort — the post is already up.
	b.startPostThread(ctx, s, i, team, runAtUnix)
}

// postThreadIntro is posted in a /coreteam post discussion thread to invite the
// channel to chat about the upcoming run. The pre-run ping (see pingPost
// attendees) is sent here ~15 minutes before the start.
const postThreadIntro = "🗣️ Discuss this run here — strategy, builds, swaps, and questions. Anyone who RSVPs or signs up to fill will be pinged here about 15 minutes before it starts."

// startPostThread tracks the just-posted overview, opens a discussion thread off
// it, and seeds it with an intro. The post is recorded with its next-run time so
// the scheduler can ping attendees ~15 minutes before the start. It's best
// effort: failures are logged but don't fail the command, since the post itself
// already succeeded. The thread is created without an explicit auto-archive
// window (Discord applies the channel default); the pre-run ping keeps it active.
func (b *bot) startPostThread(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, team *models.Team, runAtUnix int64) {
	msg, err := s.InteractionResponse(i.Interaction)
	if err != nil {
		log.Printf("post: fetch response for thread: %v", err)
		return
	}

	// Record the post with the run date locked at post time (nil when there's no
	// concrete schedule) so the scheduler can ping attendees before the run and
	// re-renders can reuse the same fixed date. This is the value handlePost
	// already showed on the embed, so the two never diverge.
	var runAt *time.Time
	if runAtUnix > 0 {
		t := time.Unix(runAtUnix, 0).UTC()
		runAt = &t
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	if err := b.discord.RecordPost(c, msg.ID, i.ChannelID, runAt); err != nil {
		log.Printf("post: record post: %v", err)
	}
	cancel()

	name := strings.TrimSpace(team.Name)
	if name == "" {
		name = "Trial run"
	}
	thread, err := s.MessageThreadStartComplex(i.ChannelID, msg.ID, &discordgo.ThreadStart{
		Name: truncate(name, 100),
	})
	if err != nil {
		log.Printf("post: create thread: %v", err)
		return
	}
	c, cancel = context.WithTimeout(ctx, 10*time.Second)
	if err := b.discord.SetPostThread(c, msg.ID, thread.ID); err != nil {
		log.Printf("post: set thread id: %v", err)
	}
	cancel()
	if _, err := s.ChannelMessageSend(thread.ID, postThreadIntro); err != nil {
		log.Printf("post: thread intro: %v", err)
	}
	// Post the active roster's fight-positioning images so players can see where
	// to stand. Best-effort; failures are logged and skipped.
	b.postPositioningImages(ctx, s, thread.ID, team.ID, fmt.Sprintf("team %d post", team.ID))
}

// buildPostEmbed assembles the channel-post embed from team data, the current
// RSVPs, and the current fill signups. Each responding roster member gets a
// ✅/❌ icon beside their name; each filled open slot shows the filler's name
// with a `fill` tag and an automatic ✅, and fill-list backups get their own
// section. names maps a slot to the player's resolved Discord display name (shown
// instead of the raw handle). footerText, when non-empty, is shown as the embed
// footer (used to note who posted). runAtUnix is the run date locked at first
// post time (0 when unknown), shown while upcoming and hidden once past (see
// discordfmt.BuildPost). Pass nil rsvps/fills for the initial post.
func buildPostEmbed(team *models.Team, primary *models.Encounter, gr []models.Grouping, rsvps []models.RSVP, fills []models.PostFill, names map[int]string, footerText string, runAtUnix int64) *discordgo.MessageEmbed {
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
	title, desc := discordfmt.BuildPost(team, primary, gr, rsvpMarks(team, rsvps), fillBySlot, fillList, names, runAtUnix)
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

// handleSignupPost posts a team's recruitment signup: an embed with the team's
// signup body and an "I'm Interested" button that kicks off the DM intake flow.
// When this channel is bound to a team it recruits for that team; otherwise it
// asks the runner which of their teams to recruit for.
func (b *bot) handleSignupPost(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx, cancel := handlerContext()
	defer cancel()

	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if errors.Is(err, models.ErrChannelNotBound) {
		// No team bound here — let the runner pick which team to recruit for.
		b.promptRecruitTeam(ctx, s, i)
		return
	}
	if err != nil {
		log.Printf("recruit: get binding: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	team, err := b.teams.Get(ctx, teamID)
	if err != nil {
		ephemeral(s, i, "Could not load the team. It may have been deleted; re-run /coreteam setup.")
		return
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{recruitEmbed(team)},
			Components: signupComponents(team.ID),
		},
	})
	if err != nil {
		log.Printf("recruit: respond: %v", err)
	}
}

// promptRecruitTeam shows the runner an ephemeral picker of their (non-premade)
// teams so they can post a recruitment message in a channel that isn't bound to
// a team. The choice is handled by handleRecruitSelect.
func (b *bot) promptRecruitTeam(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	appUserID, err := b.discord.GetUserByDiscordID(ctx, user.ID)
	if errors.Is(err, models.ErrUserNotFound) {
		ephemeral(s, i, "This channel isn't bound to a team. Link your account first with /coreteam link, then run /coreteam recruit again to pick a team — or run /coreteam setup to bind this channel.")
		return
	}
	if err != nil {
		log.Printf("recruit: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	teams, err := b.teams.ListForUser(ctx, appUserID)
	if err != nil {
		log.Printf("recruit: list teams: %v", err)
		ephemeral(s, i, "Something went wrong loading your teams. Please try again.")
		return
	}
	options := make([]discordgo.SelectMenuOption, 0, len(teams))
	for _, t := range teams {
		// Signup templates aren't recruited for; skip them like /coreteam setup.
		if t.PreMade {
			continue
		}
		if len(options) >= 25 {
			break
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: truncate(t.Name, 100),
			Value: strconv.FormatInt(t.ID, 10),
		})
	}
	if len(options) == 0 {
		ephemeral(s, i, "You don't have any teams to recruit for. Create one in the web app, or run /coreteam setup to make one and bind it to this channel.")
		return
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "This channel isn't bound to a team. Choose which team to post a recruitment message for:",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    "recruit_select",
						Placeholder: "Select a team",
						Options:     options,
					},
				}},
			},
		},
	})
	if err != nil {
		log.Printf("recruit: prompt respond: %v", err)
	}
}

// handleRecruitSelect posts the recruitment message for the team chosen in the
// promptRecruitTeam picker, then confirms in the ephemeral picker. Used when the
// channel isn't bound to a team.
func (b *bot) handleRecruitSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	teamID, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		ephemeral(s, i, "That selection was invalid.")
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
		ephemeral(s, i, "Link your account first with /coreteam link.")
		return
	}
	if err != nil {
		log.Printf("recruit select: get user: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}

	// Confirm the runner can access the chosen team (the values come from their
	// own ephemeral picker, but re-check so a stale/forged choice can't post for
	// a team they don't have).
	teams, err := b.teams.ListForUser(ctx, appUserID)
	if err != nil {
		log.Printf("recruit select: list teams: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	var chosen *models.Team
	for idx := range teams {
		if teams[idx].ID == teamID && !teams[idx].PreMade {
			chosen = &teams[idx]
			break
		}
	}
	if chosen == nil {
		ephemeral(s, i, "You can only recruit for your own teams.")
		return
	}

	_, err = s.ChannelMessageSendComplex(i.ChannelID, &discordgo.MessageSend{
		Embeds:     []*discordgo.MessageEmbed{recruitEmbed(chosen)},
		Components: signupComponents(chosen.ID),
	})
	if err != nil {
		log.Printf("recruit select: post: %v", err)
		ephemeral(s, i, "Something went wrong posting the recruitment message. Please try again.")
		return
	}
	updateEphemeral(s, i, "Posted a recruitment message for **"+chosen.Name+"**.")
}

// recruitEmbed builds a team's recruitment signup embed (title + signup body).
func recruitEmbed(team *models.Team) *discordgo.MessageEmbed {
	body := strings.TrimSpace(team.SignupPost)
	if body == "" {
		body = "Interested in joining? Press the button below and I'll DM you a few questions about your availability, roles, and classes."
	}
	return &discordgo.MessageEmbed{
		Title:       truncate(team.Name+" — Signup", embedTitleLimit),
		Description: truncate(body, embedDescriptionLimit),
		Color:       embedColor,
	}
}

// signupComponents is the button row on a recruitment signup post. The team id
// is encoded in the button's custom ID so the "I'm Interested" intake knows the
// team even when the channel isn't bound to one.
func signupComponents(teamID int64) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "I'm Interested",
				Emoji:    &discordgo.ComponentEmoji{Name: "\U0001F64B"}, // 🙋
				Style:    discordgo.SuccessButton,
				CustomID: fmt.Sprintf("%s:%d", signupJoinID, teamID),
			},
		}},
	}
}

// --- RSVP buttons (✅ Coming / ❌ Not Coming) ---

// postSignupsClosed reports whether a tracked post's signups are locked because
// its run time has passed (see postLocked). A lookup failure is logged and
// treated as "not locked" so a transient DB error never blocks a legitimate
// RSVP/fill.
func (b *bot) postSignupsClosed(ctx context.Context, messageID string) bool {
	runAtUnix, err := b.discord.GetPostRunAt(ctx, messageID)
	if err != nil {
		log.Printf("post: get locked run_at (%s): %v", messageID, err)
		return false
	}
	return postLocked(runAtUnix)
}

// handleRSVP records the presser's attendance for the post they clicked, then
// edits the post in place so everyone sees the updated Coming / Not Coming
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

	// Signups close once the run has started. Guard here too (not just by
	// disabling the button) so a stale client that still shows an enabled button
	// can't record a late RSVP; re-render to lock the controls for everyone.
	if b.postSignupsClosed(ctx, i.Message.ID) {
		if err := b.renderPostUpdate(ctx, s, i); err != nil {
			log.Printf("rsvp: lock post: %v", err)
			ephemeral(s, i, "This run has already started — signups are closed.")
		}
		return
	}

	if err := b.discord.SetRSVP(ctx, i.Message.ID, i.ChannelID, user.ID, user.Username, user.GlobalName, status); err != nil {
		log.Printf("rsvp: set: %v", err)
		ephemeral(s, i, "Something went wrong saving your RSVP. Please try again.")
		return
	}

	// Reflect the presser's ✅/❌ on the post immediately (fast pass: cached names
	// only, no REST fan-out), so they get instant feedback within Discord's
	// 3-second window. This is the interaction acknowledgement; a build failure
	// means we haven't acknowledged yet, so fall back to an ephemeral notice.
	if err := b.renderPostUpdateFast(ctx, s, i); err != nil {
		log.Printf("rsvp: fast refresh: %v", err)
		ephemeral(s, i, "Saved your RSVP, but couldn't refresh the post.")
		return
	}

	// Best-effort side effects that may change the post's data. A roster player
	// marking themselves coming again reclaims their slot: if someone was filling
	// it while they were out, move that filler to the fill list and DM them. A
	// roster player marking themselves not coming opens their slot: let the
	// fill-list backups know so they can grab it. These run after the fast
	// acknowledgement (they can DM, which is slow), and the full pass below picks
	// up any resulting change.
	if status == models.RSVPYes {
		b.displaceFillerForReturningPlayer(ctx, s, i.GuildID, i.ChannelID, i.Message.ID, user)
	}
	if status == models.RSVPNo {
		b.notifyFillListOfOpening(ctx, s, i.GuildID, i.ChannelID, i.Message.ID, user)
	}

	// Full pass: re-read the roster/fills (reflecting any displacement above) and
	// re-render with display names fully resolved over REST, editing the post
	// again. Best effort — the fast pass already showed the presser's change.
	b.renderPostUpdateFull(ctx, s, i)

	b.logPostAction(ctx, s, i, "RSVP'd **"+rsvpLogLabel(status)+"**")
}

// rsvpLogLabel renders an RSVP status the way the buttons label it, for the
// action log.
func rsvpLogLabel(status string) string {
	if status == models.RSVPNo {
		return "Not Coming"
	}
	return "Coming"
}

// displaceFillerForReturningPlayer handles a roster player being marked "coming"
// again. If a filler signed up to cover their slot while they were out, the slot
// is theirs again, so the filler is moved to the general fill list (as a backup)
// and DM'd about the change. user is the returning player, who is not
// necessarily whoever pressed the button — an admin can RSVP on their behalf.
// Best-effort: any failure is logged only, since the RSVP and post refresh
// should still succeed.
func (b *bot) displaceFillerForReturningPlayer(ctx context.Context, s *discordgo.Session, guildID, channelID, messageID string, user *discordgo.User) {
	teamID, err := b.discord.GetChannelTeam(ctx, channelID)
	if err != nil {
		return
	}
	team, err := b.teams.Get(ctx, teamID)
	if err != nil {
		log.Printf("rsvp: load team for displace: %v", err)
		return
	}
	p, ok := matchPlayer(team, user)
	if !ok {
		return // not a roster player, so no slot to reclaim
	}
	moved, found, err := b.discord.MoveFillToList(ctx, messageID, p.Slot)
	if err != nil {
		log.Printf("rsvp: move filler to list: %v", err)
		return
	}
	if !found {
		return
	}
	b.dmFillerDisplaced(s, moved.DiscordUserID, team.Name, displayName(user), messageURL(guildID, channelID, messageID))
}

// dmFillerDisplaced notifies a filler (by Discord user ID) that the slot they
// signed up to fill has been reclaimed by its returning player, and that they've
// been moved to the fill list as a backup. postURL (when non-empty) links back
// to the post. Failures are logged, not surfaced.
func (b *bot) dmFillerDisplaced(s *discordgo.Session, fillerUserID, teamName, returningName, postURL string) {
	dm, err := s.UserChannelCreate(fillerUserID)
	if err != nil {
		log.Printf("rsvp: dm filler (create channel): %v", err)
		return
	}
	msg := fmt.Sprintf("Heads up: **%s** is now coming to **%s**, so the slot you signed up to fill is theirs again. I've moved you to the fill list as a backup — thanks for being ready to step in!", returningName, teamName)
	msg += postLinkSuffix(postURL)
	if _, err := s.ChannelMessageSend(dm.ID, msg); err != nil {
		log.Printf("rsvp: dm filler (send): %v", err)
	}
}

// notifyFillListOfOpening DMs everyone currently on the general fill list that a
// roster slot just opened — its assigned player was marked not coming — so
// backups can grab it from the post. user is the declining player, who is not
// necessarily whoever pressed the button (an admin can RSVP on their behalf). It
// does nothing when that user isn't a roster player (a non-roster decline opens
// nothing) or when the slot already has a filler. Best-effort: failures are
// logged only.
func (b *bot) notifyFillListOfOpening(ctx context.Context, s *discordgo.Session, guildID, channelID, messageID string, user *discordgo.User) {
	teamID, err := b.discord.GetChannelTeam(ctx, channelID)
	if err != nil {
		return
	}
	team, err := b.teams.Get(ctx, teamID)
	if err != nil {
		log.Printf("rsvp: load team for fill-list notify: %v", err)
		return
	}
	p, ok := matchPlayer(team, user)
	if !ok {
		return // a non-roster decline doesn't open a slot
	}
	fills, err := b.discord.ListFills(ctx, messageID)
	if err != nil {
		log.Printf("rsvp: list fills for notify: %v", err)
		return
	}
	var backups []models.PostFill
	for _, f := range fills {
		if f.Slot == p.Slot {
			return // slot already has a filler, so nothing newly opened
		}
		if f.Slot == models.PostFillList {
			backups = append(backups, f)
		}
	}
	role := team.RoleLabel(p.Role)
	postURL := messageURL(guildID, channelID, messageID)
	for _, f := range backups {
		b.dmFillListOpening(s, f.DiscordUserID, team.Name, displayName(user), role, postURL)
	}
}

// dmFillListOpening notifies a fill-list backup (by Discord user ID) that a slot
// opened up because its assigned player declined, so they can sign up from the
// post. postURL (when non-empty) links back to the post. Failures are logged,
// not surfaced.
func (b *bot) dmFillListOpening(s *discordgo.Session, backupUserID, teamName, droppedName, role, postURL string) {
	dm, err := s.UserChannelCreate(backupUserID)
	if err != nil {
		log.Printf("rsvp: dm fill list (create channel): %v", err)
		return
	}
	slot := "A slot"
	if r := strings.TrimSpace(role); r != "" {
		slot = "A " + r + " slot"
	}
	msg := fmt.Sprintf("%s just opened on **%s** — **%s** marked themselves not coming. You're on the fill list, so head to the post and sign up to fill it if you can make it!", slot, teamName, droppedName)
	msg += postLinkSuffix(postURL)
	if _, err := s.ChannelMessageSend(dm.ID, msg); err != nil {
		log.Printf("rsvp: dm fill list (send): %v", err)
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

	// Signups close once the run has started. Guard here too (not just by
	// disabling the dropdown) so a stale client can't sign up late; re-render to
	// lock the controls for everyone.
	if b.postSignupsClosed(ctx, i.Message.ID) {
		if err := b.renderPostUpdate(ctx, s, i); err != nil {
			log.Printf("post fill: lock post: %v", err)
			ephemeral(s, i, "This run has already started — signups are closed.")
		}
		return
	}

	// What to record in the server's action log, set per branch below and
	// written once the post has been refreshed.
	action := ""

	switch choice := values[0]; choice {
	case postFillLeaveValue:
		if err := b.discord.LeaveFill(ctx, i.Message.ID, user.ID); err != nil {
			log.Printf("post fill: leave: %v", err)
			ephemeral(s, i, "Something went wrong. Please try again.")
			return
		}
		action = "removed their signup"
	default:
		// Both joining the fill list and filling an open slot are validated
		// against the live roster, so load the team once for both.
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
		// A user already assigned to a roster slot doesn't need (and shouldn't)
		// sign up as a fill — neither for an open slot nor the fill list.
		if _, ok := matchPlayer(team, user); ok {
			ephemeral(s, i, "You're already on this team's roster, so you don't need to sign up to fill an open slot or the fill list.")
			return
		}

		if choice == postFillListValue {
			if err := b.discord.ClaimFill(ctx, i.Message.ID, i.ChannelID, models.PostFillList, user.ID, interactionDisplayName(i)); err != nil {
				log.Printf("post fill: join list: %v", err)
				ephemeral(s, i, "Something went wrong. Please try again.")
				return
			}
			action = "joined the fill list"
			break
		}

		slot, err := strconv.Atoi(choice)
		if err != nil || slot <= 0 {
			return
		}
		// Validate against the live roster + current RSVPs so a stale dropdown
		// can't claim a slot that has since been assigned a present player. A slot
		// is fillable when it's open or its assigned player declined (RSVP ❌).
		rsvps, err := b.discord.ListRSVPs(ctx, i.Message.ID)
		if err != nil {
			log.Printf("post fill: list rsvps: %v", err)
			ephemeral(s, i, "Something went wrong. Please try again.")
			return
		}
		if !isFillableSlot(team, rsvpMarks(team, rsvps), slot) {
			ephemeral(s, i, "That slot isn't open to fill anymore. Pick another slot or the fill list.")
			return
		}
		err = b.discord.ClaimFill(ctx, i.Message.ID, i.ChannelID, slot, user.ID, interactionDisplayName(i))
		if errors.Is(err, models.ErrSlotTaken) {
			ephemeral(s, i, "Someone just signed up to fill that slot. Pick another slot or the fill list.")
			return
		}
		if err != nil {
			log.Printf("post fill: claim: %v", err)
			ephemeral(s, i, "Something went wrong signing you up. Please try again.")
			return
		}
		action = "signed up to fill " + slotLogLabel(team, slot)
	}

	// Re-render in place: a fast pass (cached names) acknowledges the interaction
	// immediately with the presser's change, then a full pass resolves names over
	// REST. A fast-pass build failure means we haven't acknowledged yet, so fall
	// back to an ephemeral notice.
	if err := b.renderPostUpdate(ctx, s, i); err != nil {
		log.Printf("post fill: refresh post: %v", err)
		ephemeral(s, i, "Saved your signup, but couldn't refresh the post.")
	}

	b.logPostAction(ctx, s, i, action)
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

// existingEmbedTitle returns the title of a message's first embed (the post's
// rendered heading), or "" when there is none. Used so an action log entry names
// a post the same way the post itself does.
func existingEmbedTitle(msg *discordgo.Message) string {
	if msg == nil || len(msg.Embeds) == 0 {
		return ""
	}
	return msg.Embeds[0].Title
}

// isFillableSlot reports whether a roster slot can be signed up for via the
// dropdown: it exists and is either open (no Discord handle) or its assigned
// player marked themselves "not coming" (RSVP ❌, per marks).
func isFillableSlot(team *models.Team, marks map[int]string, slot int) bool {
	for _, p := range team.Players {
		if p.Slot == slot {
			if strings.TrimSpace(p.DiscordHandle) == "" {
				return true // open slot
			}
			return marks[p.Slot] == models.RSVPNo // assigned but declined
		}
	}
	return false
}

// postRenderData is the current DB state needed to re-render a posted trial
// overview: the team + its active-roster composition, this message's RSVPs and
// fills, and the run date locked at post time.
type postRenderData struct {
	team      *models.Team
	primary   *models.Encounter
	groupings []models.Grouping
	rsvps     []models.RSVP
	fills     []models.PostFill
	runAtUnix int64
}

// loadPostRenderData gathers the DB state for a post re-render. These are all
// local queries (no Discord REST), so it's safe on the interaction hot path. A
// missing/NULL locked run date yields runAtUnix 0 (logged, not fatal), matching
// the pre-lock fallback in buildPostEmbed.
func (b *bot) loadPostRenderData(ctx context.Context, channelID, messageID string) (*postRenderData, error) {
	teamID, err := b.discord.GetChannelTeam(ctx, channelID)
	if err != nil {
		return nil, err
	}
	team, _, primary, gr, err := b.loadTeamData(ctx, teamID)
	if err != nil {
		return nil, err
	}
	rsvps, err := b.discord.ListRSVPs(ctx, messageID)
	if err != nil {
		return nil, err
	}
	fills, err := b.discord.ListFills(ctx, messageID)
	if err != nil {
		return nil, err
	}
	runAtUnix, err := b.discord.GetPostRunAt(ctx, messageID)
	if err != nil {
		log.Printf("post: get locked run_at (%s): %v", messageID, err)
	}
	return &postRenderData{team: team, primary: primary, groupings: gr, rsvps: rsvps, fills: fills, runAtUnix: runAtUnix}, nil
}

// renderPostUpdate re-renders a posted trial overview in place in two passes so
// the presser sees their change instantly without risking Discord's 3-second
// deadline: a fast pass using only cached display names (delivered as the
// interaction's in-place message update — its acknowledgement), then a full pass
// that resolves names over REST and edits the post again. It returns an error
// only when the fast pass can't be built (so callers can still surface an
// ephemeral notice, since the interaction isn't acknowledged yet); full-pass and
// Discord delivery failures are logged and swallowed.
func (b *bot) renderPostUpdate(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	if err := b.renderPostUpdateFast(ctx, s, i); err != nil {
		return err
	}
	b.renderPostUpdateFull(ctx, s, i)
	return nil
}

// renderPostUpdateFast re-renders the post using only cached display names (no
// Discord REST lookups) and delivers it as the interaction's in-place message
// update — the acknowledgement. Because it skips the name fan-out it stays well
// within the 3-second deadline, so the presser's ✅/❌ (or fill change) shows
// immediately even if a few roster names are momentarily stale; renderPostUpdateFull
// corrects them right after. Returns an error only when the post can't be built
// from DB state (interaction not yet acknowledged, so the caller can fall back to
// an ephemeral notice); the Discord update call's own error is logged.
func (b *bot) renderPostUpdateFast(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) error {
	d, err := b.loadPostRenderData(ctx, i.ChannelID, i.Message.ID)
	if err != nil {
		return err
	}
	names := b.rosterNamesFromCache(i.GuildID, d.team)
	// Preserve the "Posted by" footer set on the original post (re-renders build
	// the embed from scratch, which would otherwise drop it).
	embed := buildPostEmbed(d.team, d.primary, d.groupings, d.rsvps, d.fills, names, existingFooterText(i.Message), d.runAtUnix)
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: postComponents(d.team, d.fills, rsvpMarks(d.team, d.rsvps), postLocked(d.runAtUnix)),
		},
	}); err != nil {
		log.Printf("post: fast update respond: %v", err)
	}
	return nil
}

// renderPostUpdateFull re-reads the post's DB state (picking up any side effects
// since the fast pass, e.g. a filler moved to the fill list) and re-renders with
// display names fully resolved over REST, editing the already-acknowledged
// response via the webhook edit. Best effort: build and delivery failures are
// logged and swallowed, since the fast pass already updated the post.
func (b *bot) renderPostUpdateFull(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	d, err := b.loadPostRenderData(ctx, i.ChannelID, i.Message.ID)
	if err != nil {
		log.Printf("post: full update load: %v", err)
		return
	}
	names := b.resolveRosterNames(s, i.GuildID, d.team)
	embed := buildPostEmbed(d.team, d.primary, d.groupings, d.rsvps, d.fills, names, existingFooterText(i.Message), d.runAtUnix)
	embeds := []*discordgo.MessageEmbed{embed}
	components := postComponents(d.team, d.fills, rsvpMarks(d.team, d.rsvps), postLocked(d.runAtUnix))
	if _, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds:     &embeds,
		Components: &components,
	}); err != nil {
		log.Printf("post: full update edit: %v", err)
	}
}

// refreshPostMessage re-renders a posted trial overview by editing the message
// directly, for changes made from somewhere other than the post itself (the
// admin RSVP flow acts from its own ephemeral, so the interaction's message is
// that ephemeral, not the post). Names are resolved over REST — there's no
// 3-second deadline here since the interaction was acknowledged separately — and
// the original "Posted by …" footer is carried over from the live message.
func (b *bot) refreshPostMessage(ctx context.Context, s *discordgo.Session, guildID, channelID, messageID string) error {
	d, err := b.loadPostRenderData(ctx, channelID, messageID)
	if err != nil {
		return err
	}
	footer := ""
	if msg, merr := s.ChannelMessage(channelID, messageID); merr == nil {
		footer = existingFooterText(msg)
	} else {
		log.Printf("post: fetch message for refresh (%s): %v", messageID, merr)
	}
	names := b.resolveRosterNames(s, guildID, d.team)
	embeds := []*discordgo.MessageEmbed{buildPostEmbed(d.team, d.primary, d.groupings, d.rsvps, d.fills, names, footer, d.runAtUnix)}
	components := postComponents(d.team, d.fills, rsvpMarks(d.team, d.rsvps), postLocked(d.runAtUnix))
	_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Embeds:     &embeds,
		Components: &components,
	})
	return err
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
		u := &discordgo.User{ID: r.DiscordUserID, Username: r.DiscordUsername, GlobalName: r.DiscordGlobalName}
		if p, ok := matchPlayer(team, u); ok {
			marks[p.Slot] = r.Status
		}
	}
	return marks
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

// --- /coreteam permissions (manage run-edit roles) ---

// handlePermissions routes the /coreteam permissions subcommand group (add /
// remove / list). It manages the per-guild set of Discord roles whose holders
// may use a signup run's restricted buttons (Edit run / Delete run). Changing
// the list is gated to server admins (Manage Server or Administrator).
func (b *bot) handlePermissions(s *discordgo.Session, i *discordgo.InteractionCreate, group *discordgo.ApplicationCommandInteractionDataOption) {
	if i.GuildID == "" {
		ephemeral(s, i, "Run this in a server — edit permissions are managed per server.")
		return
	}
	if !hasManageGuild(i) {
		ephemeral(s, i, "You need the Manage Server (or Administrator) permission to change run-edit permissions.")
		return
	}
	if len(group.Options) == 0 {
		return
	}
	sub := group.Options[0]

	ctx, cancel := handlerContext()
	defer cancel()

	switch sub.Name {
	case "add":
		b.handlePermissionsAdd(ctx, s, i, sub)
	case "remove":
		b.handlePermissionsRemove(ctx, s, i, sub)
	case "list":
		b.handlePermissionsList(ctx, s, i)
	}
}

func (b *bot) handlePermissionsAdd(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	roleID := roleOptionID(sub, "role")
	if roleID == "" {
		ephemeral(s, i, "Please pick a role.")
		return
	}
	if err := b.discord.AddEditRole(ctx, i.GuildID, roleID); err != nil {
		log.Printf("permissions add: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	ephemeralNoMentions(s, i, fmt.Sprintf("%s can now use the **Edit run** and **Delete run** buttons on signup runs.", roleMention(roleID)))
}

func (b *bot) handlePermissionsRemove(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	roleID := roleOptionID(sub, "role")
	if roleID == "" {
		ephemeral(s, i, "Please pick a role.")
		return
	}
	if err := b.discord.RemoveEditRole(ctx, i.GuildID, roleID); err != nil {
		log.Printf("permissions remove: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	ephemeralNoMentions(s, i, fmt.Sprintf("%s can no longer use the **Edit run** and **Delete run** buttons on signup runs.", roleMention(roleID)))
}

func (b *bot) handlePermissionsList(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	roles, err := b.discord.ListEditRoles(ctx, i.GuildID)
	if err != nil {
		log.Printf("permissions list: %v", err)
		ephemeral(s, i, "Something went wrong. Please try again.")
		return
	}
	if len(roles) == 0 {
		ephemeral(s, i, "No roles are designated yet. Only the run's poster and server admins can use the **Edit run** and **Delete run** buttons. Add one with `/coreteam permissions add`.")
		return
	}
	mentions := make([]string, 0, len(roles))
	for _, r := range roles {
		mentions = append(mentions, roleMention(r))
	}
	ephemeralNoMentions(s, i, "Roles that can use the **Edit run** and **Delete run** buttons on signup runs (alongside each run's poster and server admins):\n• "+strings.Join(mentions, "\n• "))
}

// roleOptionID returns the role ID picked for a named role option on a
// subcommand, or "" when absent.
func roleOptionID(sub *discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range sub.Options {
		if o.Name == name {
			if id, ok := o.Value.(string); ok {
				return id
			}
		}
	}
	return ""
}

// channelOptionID returns the channel ID picked for a named channel option on a
// subcommand, or "" when absent.
func channelOptionID(sub *discordgo.ApplicationCommandInteractionDataOption, name string) string {
	for _, o := range sub.Options {
		if o.Name == name {
			if id, ok := o.Value.(string); ok {
				return id
			}
		}
	}
	return ""
}

// roleMention renders a Discord role mention for a role ID.
func roleMention(roleID string) string {
	return "<@&" + roleID + ">"
}

// ephemeralNoMentions sends a private reply that renders role/user mentions as
// text without pinging anyone (used so listing/changing edit roles doesn't ping
// the role).
func ephemeralNoMentions(s *discordgo.Session, i *discordgo.InteractionCreate, msg string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:           discordgo.MessageFlagsEphemeral,
			Content:         msg,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	})
	if err != nil {
		log.Printf("respond ephemeral (no mentions): %v", err)
	}
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
	// All composition (players, encounters, groupings) hangs off the active
	// roster. team.Players is already the active roster's lineup (TeamStore.Get);
	// load its encounters and groupings by the active roster id.
	list, err := b.encounters.ListForRoster(ctx, team.ActiveRosterID)
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
	gr, err := b.groupings.ListForRoster(ctx, team.ActiveRosterID)
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
		// Mention or raw ID forms (including an "@"-prefixed id like @<id>).
		if h == "<@"+id+">" || h == "<@!"+id+">" || h == id || h == "@"+id {
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

// messageURL builds a clickable Discord jump link to a specific message, used so
// DMs can point recipients straight back to the post. Returns "" when the
// channel or message is unknown (the caller should then omit the link).
func messageURL(guildID, channelID, messageID string) string {
	if channelID == "" || messageID == "" {
		return ""
	}
	g := guildID
	if g == "" {
		g = "@me"
	}
	return "https://discord.com/channels/" + g + "/" + channelID + "/" + messageID
}

// postLinkSuffix returns a trailing "jump to the post" line to append to a DM,
// or "" when no link is available so the message reads naturally either way.
func postLinkSuffix(postURL string) string {
	if postURL == "" {
		return ""
	}
	return "\n\n" + postURL
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

// hasManageGuild reports whether the invoking member is a server admin: they
// hold the Manage Server (Manage Guild) or Administrator permission. This is the
// bar for managing run-edit roles and the always-allowed admin override on the
// restricted run buttons.
func hasManageGuild(i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}
	perms := i.Member.Permissions
	return perms&discordgo.PermissionManageGuild != 0 || perms&discordgo.PermissionAdministrator != 0
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

// dismissEphemeral deletes the ephemeral message a component interaction was
// attached to (e.g. a one-shot signup picker that's done its job). It first
// acknowledges the interaction with a deferred message update — the only way to
// respond without changing the message — then deletes that response, which for a
// component interaction is the ephemeral message itself. Best effort: failures
// are logged.
func dismissEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}); err != nil {
		log.Printf("dismiss ephemeral ack: %v", err)
		return
	}
	if err := s.InteractionResponseDelete(i.Interaction); err != nil {
		log.Printf("dismiss ephemeral delete: %v", err)
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
