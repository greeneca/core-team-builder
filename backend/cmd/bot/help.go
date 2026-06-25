package main

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// The /coreteam help subcommand opens a DM with the invoking user containing a
// command reference, a link to the web app, and a link to the source repository
// for browsing the code and reporting bugs. The DM carries a select menu so the
// user can drill into any command's details without leaving the conversation.
//
// helpSelectID is the menu's custom ID; the chosen option's value is the command
// name, which handleHelpSelect renders in place.
const helpSelectID = "help_select"

// helpCommand describes one /coreteam subcommand for the help guide: a one-line
// summary (shown in the overview list and as the menu option's description) and
// a longer detail paragraph (shown when the command is selected from the menu).
type helpCommand struct {
	Name    string
	Summary string
	Detail  string
}

// helpCommands is the documented command reference, in the order shown.
var helpCommands = []helpCommand{
	{
		Name:    "help",
		Summary: "Open this help guide in a DM.",
		Detail:  "Sends you this guide: a reference for every command, a link to the web app, and where to browse the code and report bugs. Pick a command from the menu below for details.",
	},
	{
		Name:    "link",
		Summary: "Link your Discord account to your web account.",
		Detail:  "Connects your Discord identity to your Core Team Builder web account so the bot knows which teams are yours. Generate a one-time code in the web app, then run `/coreteam link code:<code>`. Accounts created via \"Sign in with Discord\" are already linked and can skip this.",
	},
	{
		Name:    "setup",
		Summary: "Bind this channel to one of your teams.",
		Detail:  "Binds the current channel to a team (or creates a new one), so `/coreteam post`, `/coreteam recruit`, and `/coreteam status` know which team this channel is for. Requires the Manage Channels permission and a linked account.",
	},
	{
		Name:    "post",
		Summary: "Post this channel's trial overview.",
		Detail:  "Posts the bound team's trial overview: the roster grouped by role, the schedule as a live timestamp, and any groupings. Includes Coming / Not coming RSVP buttons, a Get My Build Details button, and a dropdown to fill open slots. It also opens a discussion thread and pings attendees there about 15 minutes before the run.",
	},
	{
		Name:    "recruit",
		Summary: "Post a recruitment message with DM intake.",
		Detail:  "Posts a recruitment message with an \"I'm Interested\" button. Pressing it starts a DM questionnaire that records availability, roles, and classes into the team's member pool. If the channel isn't bound to a team, you'll be asked which of your teams to recruit for.",
	},
	{
		Name:    "signup",
		Summary: "Post a scheduled run from a pre-made team.",
		Detail:  "Posts a one-off scheduled run from one of your pre-made signup-template teams. Players claim individual slots on the post (or pick a role, in simple-signup mode), with an optional waitlist.",
	},
	{
		Name:    "publish",
		Summary: "Share a signup template with the server.",
		Detail:  "Makes one of your signup templates available to everyone in this server, so other members can run `/coreteam signup` to post a run from it.",
	},
	{
		Name:    "timezone",
		Summary: "Set the timezone the bot remembers for you.",
		Detail:  "Sets or changes the timezone the bot remembers for you, used when scheduling signup runs so times are interpreted in your local zone.",
	},
	{
		Name:    "roll",
		Summary: "Pick a random ESO trial.",
		Detail:  "Posts a randomly chosen ESO trial (with its bosses) and a Re-roll button. The post is public, but only the person who rolled can re-roll it.",
	},
	{
		Name:    "login",
		Summary: "Get a link to the web app.",
		Detail:  "Replies with a link to the Core Team Builder web app, where you build rosters, edit teams, and manage your account.",
	},
	{
		Name:    "status",
		Summary: "Show this channel's bound team.",
		Detail:  "Shows which team the current channel is bound to (or tells you it isn't bound yet).",
	},
	{
		Name:    "unset",
		Summary: "Unbind this channel from its team.",
		Detail:  "Removes the binding between the current channel and its team. The team itself is not deleted; you can re-bind any time with `/coreteam setup`.",
	},
	{
		Name:    "permissions",
		Summary: "Choose which roles can edit/delete signup runs.",
		Detail:  "Manages which Discord roles may use the Edit run and Delete run buttons on signup runs in this server. Use `/coreteam permissions add role:<role>`, `remove role:<role>`, or `list`. Regardless of this list, each run's original poster and server admins (Manage Server / Administrator) can always edit or delete it. Changing the list requires the Manage Server permission.",
	},
}

// findHelpCommand returns the documented command with the given name, or nil.
func findHelpCommand(name string) *helpCommand {
	for idx := range helpCommands {
		if helpCommands[idx].Name == name {
			return &helpCommands[idx]
		}
	}
	return nil
}

// --- /coreteam help ---

// handleHelp opens a DM with the invoking user and posts the help guide there:
// an overview embed (intro, web app + repo links, command summaries) followed by
// a message with a select menu for drilling into each command. The slash command
// itself gets an ephemeral acknowledgement (or the guide inline if DMs fail).
func (b *bot) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}

	overview := b.helpOverviewEmbed()
	dm, err := s.UserChannelCreate(user.ID)
	if err == nil {
		_, err = s.ChannelMessageSendEmbed(dm.ID, overview)
	}
	if err == nil {
		_, err = s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{
			Content:    "Want details on a specific command? Pick one below:",
			Components: helpMenuComponents(),
		})
	}
	if err != nil {
		// DMs are likely closed — fall back to showing the guide inline (only the
		// runner sees it). The menu works the same in an ephemeral reply.
		log.Printf("help: dm: %v", err)
		respErr := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Flags:      discordgo.MessageFlagsEphemeral,
				Content:    "I couldn't DM you (your DMs may be closed), so here's the guide. Pick a command below for details:",
				Embeds:     []*discordgo.MessageEmbed{overview},
				Components: helpMenuComponents(),
			},
		})
		if respErr != nil {
			log.Printf("help: ephemeral fallback: %v", respErr)
		}
		return
	}
	ephemeral(s, i, "I've sent you a DM with the command guide. Check your DMs!")
}

// handleHelpSelect renders the chosen command's detail in place, keeping the menu
// so the user can browse other commands. Works the same whether the menu lives
// in the DM or in the ephemeral fallback.
func (b *bot) handleHelpSelect(s *discordgo.Session, i *discordgo.InteractionCreate) {
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		return
	}
	cmd := findHelpCommand(values[0])
	if cmd == nil {
		ephemeral(s, i, "That command is no longer available.")
		return
	}
	embed := &discordgo.MessageEmbed{
		Title:       truncate("/coreteam "+cmd.Name, embedTitleLimit),
		Description: truncate(cmd.Detail, embedDescriptionLimit),
		Color:       embedColor,
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "Want details on another command? Pick one below:",
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: helpMenuComponents(),
		},
	})
	if err != nil {
		log.Printf("help select: respond: %v", err)
	}
}

// helpOverviewEmbed builds the guide's overview embed: an intro, the web app and
// source-repository links, and a one-line summary of every command.
func (b *bot) helpOverviewEmbed() *discordgo.MessageEmbed {
	var sb strings.Builder
	sb.WriteString("Here's everything I can do. Run any command as `/coreteam <name>`, or use `/post` and `/signup` directly.\n")

	if url := strings.TrimSpace(b.appBaseURL); url != "" {
		sb.WriteString("\n\U0001F310 **Web app:** " + url) // 🌐
	}
	if repo := strings.TrimSpace(b.repoURL); repo != "" {
		repo = strings.TrimRight(repo, "/")
		sb.WriteString("\n\U0001F41B **Report a bug:** " + repo + "/issues/new") // 🐛
		sb.WriteString("\n\U0001F4BB **Source code:** " + repo)                  // 💻
	}

	sb.WriteString("\n\n**Commands**")
	for _, c := range helpCommands {
		sb.WriteString("\n• **/coreteam " + c.Name + "** — " + c.Summary)
	}

	return &discordgo.MessageEmbed{
		Title:       "Core Team Builder — Help",
		Description: truncate(sb.String(), embedDescriptionLimit),
		Color:       embedColor,
	}
}

// helpMenuComponents is the select menu for picking a command to read about.
func helpMenuComponents() []discordgo.MessageComponent {
	options := make([]discordgo.SelectMenuOption, 0, len(helpCommands))
	for _, c := range helpCommands {
		options = append(options, discordgo.SelectMenuOption{
			Label:       truncate("/coreteam "+c.Name, 100),
			Value:       c.Name,
			Description: truncate(c.Summary, 100),
		})
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    helpSelectID,
				Placeholder: "Select a command for details",
				Options:     options,
			},
		}},
	}
}
