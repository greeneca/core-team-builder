package main

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

// The trial overview's two multi-step buttons — Manage and Build Details — run
// in the presser's DMs rather than in an ephemeral reply, so the conversation
// persists and browsing a roster doesn't leave a trail of throwaway messages in
// the trial channel.
//
// Discord still requires the button press itself to be answered within three
// seconds, and a DM is not an interaction response. So the press is
// acknowledged with a deferred message update — which shows nothing and changes
// nothing, since the flow never edits the post through it — and the DM goes out
// after. A user who blocks direct messages from server members can't be reached
// at all, so for them the same step is delivered as an ephemeral follow-up and
// the flow continues there instead; from the second step on both paths are
// identical, because every later step simply updates whichever message its
// control is attached to.
//
// One thing does not survive the hop into DMs: a DM interaction carries no
// guild, and its channel is the DM rather than the post's. postOrigin is the
// context that therefore has to ride along in the follow-up controls' custom
// IDs.

// postOrigin locates the post a DM-hosted flow is acting on: the guild it was
// posted in, the channel bound to the team, and the overview message itself.
type postOrigin struct {
	guildID   string
	channelID string
	messageID string
}

// encode renders an origin as custom-ID segments. Three snowflakes cost about
// 60 characters, which leaves room for the longest prefix and trailing
// arguments any of these flows adds within Discord's 100-character limit.
func (o postOrigin) encode() string {
	return o.guildID + ":" + o.channelID + ":" + o.messageID
}

// parsePostOrigin reads an origin back out of a custom ID already split on ":",
// starting at parts[from]. ok is false for a malformed ID — a control left over
// from an older build — which callers treat as a press to ignore.
func parsePostOrigin(parts []string, from int) (postOrigin, bool) {
	if len(parts) < from+3 {
		return postOrigin{}, false
	}
	o := postOrigin{guildID: parts[from], channelID: parts[from+1], messageID: parts[from+2]}
	if o.channelID == "" || o.messageID == "" {
		return postOrigin{}, false
	}
	return o, true
}

// dmClosedNote prefixes the ephemeral fallback so the user knows why the flow
// showed up in the channel instead of their DMs.
const dmClosedNote = "_I couldn't DM you — check that direct messages from server members are enabled. Only you can see this._\n\n"

// openFlowInDM delivers a flow's opening step to the user's DMs, acknowledging
// the button press invisibly first so it neither times out nor puts anything in
// the channel. When the DM bounces, the step is sent as an ephemeral follow-up
// so the flow still works for people with DMs closed.
func openFlowInDM(s *discordgo.Session, i *discordgo.InteractionCreate, userID, content string, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
	if err != nil {
		log.Printf("post flow: acknowledge press: %v", err)
		return
	}

	var embeds []*discordgo.MessageEmbed
	if embed != nil {
		embeds = []*discordgo.MessageEmbed{embed}
	}
	if sendFlowDM(s, userID, content, embeds, components) {
		return
	}
	_, err = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Flags:      discordgo.MessageFlagsEphemeral,
		Content:    dmClosedNote + content,
		Embeds:     embeds,
		Components: components,
	})
	if err != nil {
		log.Printf("post flow: ephemeral fallback: %v", err)
	}
}

// sendFlowDM sends one flow step as a DM, reporting whether it landed. A false
// result is an ordinary outcome (DMs closed), not an error worth logging.
func sendFlowDM(s *discordgo.Session, userID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) bool {
	dm, err := s.UserChannelCreate(userID)
	if err != nil {
		return false
	}
	_, err = s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{
		Content:    content,
		Embeds:     embeds,
		Components: components,
	})
	return err == nil
}

// updateFlowStep replaces the flow message a control sits on — the DM, or the
// ephemeral fallback — with the next step, and doubles as the way a flow ends
// (pass no embed and no components to leave a bare closing line). Embeds and
// components are always sent explicitly, since anything omitted from an update
// is left as it was.
func updateFlowStep(s *discordgo.Session, i *discordgo.InteractionCreate, content string, embed *discordgo.MessageEmbed, components []discordgo.MessageComponent) {
	embeds := []*discordgo.MessageEmbed{}
	if embed != nil {
		embeds = append(embeds, embed)
	}
	if components == nil {
		components = []discordgo.MessageComponent{}
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Embeds:     embeds,
			Components: components,
		},
	})
	if err != nil {
		log.Printf("post flow: update step: %v", err)
	}
}

// endFlowStep closes a flow with a final line, clearing its controls.
func endFlowStep(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	updateFlowStep(s, i, content, nil, nil)
}
