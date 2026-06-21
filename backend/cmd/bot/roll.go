package main

import (
	"log"
	"math/rand/v2"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/models"
)

// The /coreteam roll subcommand picks a random ESO trial and posts it publicly
// with a Re-roll button. The trial pool is every encounter group in
// models.EncounterNameGroups except the General (Default/Trash) bucket — the
// same canonical list the app validates encounters against.
//
// Only the user who posted may re-roll: the poster's Discord ID is encoded in
// the button's custom ID (rollRerollPrefix + id) and checked on press.
const rollRerollPrefix = "roll_reroll:"

// trialGroups returns the rollable trials: every encounter group except the
// General Default/Trash bucket.
func trialGroups() []models.EncounterNameGroup {
	groups := make([]models.EncounterNameGroup, 0, len(models.EncounterNameGroups))
	for _, g := range models.EncounterNameGroups {
		if g.Group == models.GeneralEncounterGroup {
			continue
		}
		groups = append(groups, g)
	}
	return groups
}

// randomTrialEmbed builds the announcement embed for a randomly chosen trial
// (its name plus the bosses it contains). Returns nil when no trials exist.
func randomTrialEmbed() *discordgo.MessageEmbed {
	groups := trialGroups()
	if len(groups) == 0 {
		return nil
	}
	g := groups[rand.IntN(len(groups))]
	desc := "**" + g.Group + "**"
	if len(g.Names) > 0 {
		desc += "\n" + strings.Join(g.Names, " • ")
	}
	return &discordgo.MessageEmbed{
		Title:       "\U0001F3B2 Random Trial", // 🎲
		Description: truncate(desc, embedDescriptionLimit),
		Color:       embedColor,
	}
}

// rollComponents is the button row on a random-trial post. ownerID is the
// poster's Discord ID, encoded into the button so only they can re-roll.
func rollComponents(ownerID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Re-roll",
				Emoji:    &discordgo.ComponentEmoji{Name: "\U0001F3B2"}, // 🎲
				Style:    discordgo.PrimaryButton,
				CustomID: rollRerollPrefix + ownerID,
			},
		}},
	}
}

// --- /coreteam roll ---

// handleRoll posts a randomly chosen trial publicly, with a Re-roll button that
// only the invoking user can use.
func (b *bot) handleRoll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	embed := randomTrialEmbed()
	if embed == nil {
		ephemeral(s, i, "There are no trials configured to roll from.")
		return
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: rollComponents(user.ID),
		},
	})
	if err != nil {
		log.Printf("roll: respond: %v", err)
	}
}

// handleRollReroll re-rolls the trial in place, editing the post so everyone
// sees the new pick. Only the original poster (whose ID is encoded in the
// button's custom ID) may re-roll; anyone else gets an ephemeral notice.
func (b *bot) handleRollReroll(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	ownerID := strings.TrimPrefix(i.MessageComponentData().CustomID, rollRerollPrefix)
	if ownerID != user.ID {
		ephemeral(s, i, "Only the person who rolled can re-roll. Run /coreteam roll to get your own.")
		return
	}
	embed := randomTrialEmbed()
	if embed == nil {
		ephemeral(s, i, "There are no trials configured to roll from.")
		return
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: rollComponents(ownerID),
		},
	})
	if err != nil {
		log.Printf("roll reroll: respond: %v", err)
	}
}
