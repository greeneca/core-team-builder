package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/core-team-builder/backend/internal/esoref"
	"github.com/core-team-builder/backend/internal/models"
)

// The /coreteam signup recruitment flow gathers a prospective member's
// availability through an interactive DM questionnaire built entirely from
// message components (select menus) — no privileged message intents are needed.
// Progress is persisted on a draft team_roster_members row after each answer;
// the component custom IDs carry the member row id (and the current day/role) so
// each stateless interaction can resume the flow.
//
// Custom ID grammar:
//
//	signup_join                 (on the recruitment post)
//	signup_days:<id>
//	signup_tz:<id>
//	signup_start:<id>:<day>
//	signup_end:<id>:<day>
//	signup_roles:<id>
//	signup_classes:<id>:<role>
const (
	signupPrefix = "signup_"
	signupJoinID = "signup_join"
)

// canonicalDays / canonicalRoles fix the order the flow walks days and roles in,
// regardless of the order the user picked them in the multi-selects.
var canonicalDays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
var canonicalRoles = []string{"tank", "healer", "dps"}

// signupTimezones is a curated, short list of representative IANA zones (Discord
// caps a select at 25 options). Mirrors the web app's POPULAR_TIMEZONES.
var signupTimezones = []string{
	"Pacific/Honolulu", "America/Anchorage", "America/Los_Angeles", "America/Denver",
	"America/Chicago", "America/New_York", "America/Halifax", "America/Sao_Paulo",
	"Atlantic/Azores", "Europe/London", "Europe/Paris", "Europe/Athens",
	"Europe/Moscow", "Asia/Dubai", "Asia/Karachi", "Asia/Dhaka", "Asia/Bangkok",
	"Asia/Shanghai", "Asia/Tokyo", "Australia/Sydney", "Pacific/Auckland",
}

// onSignupComponent dispatches every signup_* component interaction.
func (b *bot) onSignupComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	id := i.MessageComponentData().CustomID
	if id == signupJoinID {
		b.handleSignupJoin(s, i)
		return
	}

	parts := strings.SplitN(id, ":", 3)
	if len(parts) < 2 {
		return
	}
	memberID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return
	}
	ctx, cancel := handlerContext()
	defer cancel()

	member, err := b.members.GetByID(ctx, memberID)
	if err != nil {
		ephemeral(s, i, "I couldn't find your signup. Press \"I'm Interested\" again to restart.")
		return
	}
	values := i.MessageComponentData().Values

	switch parts[0] {
	case "signup_days":
		b.signupSaveDays(s, i, member, values)
	case "signup_tz":
		b.signupSaveTimezone(s, i, member, values)
	case "signup_start":
		if len(parts) == 3 {
			b.signupSaveHour(s, i, member, parts[2], values, true)
		}
	case "signup_end":
		if len(parts) == 3 {
			b.signupSaveHour(s, i, member, parts[2], values, false)
		}
	case "signup_span":
		// Quick-apply button: parts[2] is "<day>~<start>~<end>" (e.g. the
		// "All day" preset or a reused earlier window).
		if len(parts) == 3 {
			if day, start, end, ok := parseSpan(parts[2]); ok {
				b.signupApplySpan(s, i, member, day, start, end)
			}
		}
	case "signup_roles":
		b.signupSaveRoles(s, i, member, values)
	case "signup_classes":
		if len(parts) == 3 {
			b.signupSaveClasses(s, i, member, parts[2], values)
		}
	}
}

// handleSignupJoin records interest and opens the DM questionnaire.
func (b *bot) handleSignupJoin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	user := invokingUser(i)
	if user == nil {
		ephemeral(s, i, "Could not identify your Discord account.")
		return
	}
	ctx, cancel := handlerContext()
	defer cancel()

	teamID, err := b.discord.GetChannelTeam(ctx, i.ChannelID)
	if err != nil {
		ephemeral(s, i, "This channel isn't bound to a team anymore.")
		return
	}
	member, err := b.members.UpsertDraft(ctx, teamID, user.ID, user.Username, displayName(user))
	if err != nil {
		log.Printf("signup join: upsert: %v", err)
		ephemeral(s, i, "Something went wrong recording your interest. Please try again.")
		return
	}

	content, components := signupDaysStep(member.ID)
	dm, err := s.UserChannelCreate(user.ID)
	if err == nil {
		_, err = s.ChannelMessageSendComplex(dm.ID, &discordgo.MessageSend{
			Content:    "Thanks for your interest! Your signup has been recorded.\n\n" + content,
			Components: components,
		})
	}
	if err != nil {
		log.Printf("signup join: dm: %v", err)
		ephemeral(s, i, "Your interest is recorded, but I couldn't DM you (your DMs may be closed). Enable DMs from server members and press the button again.")
		return
	}
	ephemeral(s, i, "Your interest is recorded! Check your DMs — I've sent you a few quick questions.")
}

func (b *bot) signupSaveDays(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, values []string) {
	m.Days = orderBy(values, canonicalDays)
	m.Step = models.MemberStepTimezone
	content, components := signupTimezoneStep(m.ID)
	b.signupAdvance(s, i, m, content, components)
}

func (b *bot) signupSaveTimezone(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, values []string) {
	if len(values) > 0 {
		m.Timezone = values[0]
	}
	// Begin the per-day hour questions on the first chosen day.
	if day := firstDay(m.Days); day != "" {
		m.Step = "times:" + day
		content, components := signupTimesStep(m, day)
		b.signupAdvance(s, i, m, content, components)
		return
	}
	m.Step = models.MemberStepRoles
	content, components := signupRolesStep(m.ID)
	b.signupAdvance(s, i, m, content, components)
}

func (b *bot) signupSaveHour(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, day string, values []string, isStart bool) {
	if len(values) == 0 {
		return
	}
	hour, err := strconv.Atoi(values[0])
	if err != nil {
		return
	}
	if m.Availability == nil {
		m.Availability = map[string]models.DayWindow{}
	}
	w := m.Availability[day]
	if isStart {
		w.Start = hour
		m.Availability[day] = w
		// Stay on the same day so the user can now pick an end hour.
		content, components := signupTimesStep(m, day)
		b.signupAdvance(s, i, m, content, components)
		return
	}
	w.End = hour
	m.Availability[day] = w
	// End chosen — advance to the next chosen day, or move on to roles.
	if next := nextDay(m.Days, day); next != "" {
		m.Step = "times:" + next
		content, components := signupTimesStep(m, next)
		b.signupAdvance(s, i, m, content, components)
		return
	}
	m.Step = models.MemberStepRoles
	content, components := signupRolesStep(m.ID)
	b.signupAdvance(s, i, m, content, components)
}

// signupApplySpan sets a whole window for a day in one click (the "All day"
// preset or a reused earlier span) and advances to the next day or to roles —
// the same continuation as choosing an end hour.
func (b *bot) signupApplySpan(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, day string, start, end int) {
	if m.Availability == nil {
		m.Availability = map[string]models.DayWindow{}
	}
	m.Availability[day] = models.DayWindow{Start: start, End: end}
	if next := nextDay(m.Days, day); next != "" {
		m.Step = "times:" + next
		content, components := signupTimesStep(m, next)
		b.signupAdvance(s, i, m, content, components)
		return
	}
	m.Step = models.MemberStepRoles
	content, components := signupRolesStep(m.ID)
	b.signupAdvance(s, i, m, content, components)
}

func (b *bot) signupSaveRoles(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, values []string) {
	m.Roles = orderBy(values, canonicalRoles)
	if role := firstRole(m.Roles); role != "" {
		m.Step = "classes:" + role
		content, components := signupClassesStep(m.ID, role)
		b.signupAdvance(s, i, m, content, components)
		return
	}
	b.signupFinish(s, i, m)
}

func (b *bot) signupSaveClasses(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, role string, values []string) {
	if m.ClassesByRole == nil {
		m.ClassesByRole = map[string][]string{}
	}
	m.ClassesByRole[role] = values
	if next := nextRole(m.Roles, role); next != "" {
		m.Step = "classes:" + next
		content, components := signupClassesStep(m.ID, next)
		b.signupAdvance(s, i, m, content, components)
		return
	}
	b.signupFinish(s, i, m)
}

// signupAdvance saves progress then edits the DM message to the next question.
func (b *bot) signupAdvance(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember, content string, components []discordgo.MessageComponent) {
	ctx, cancel := handlerContext()
	defer cancel()
	if err := b.members.SaveProgress(ctx, m); err != nil {
		log.Printf("signup: save progress: %v", err)
		ephemeral(s, i, "Something went wrong saving your answer. Please try again.")
		return
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: components,
		},
	})
	if err != nil {
		log.Printf("signup: advance respond: %v", err)
	}
}

// signupFinish marks the draft complete and shows a summary.
func (b *bot) signupFinish(s *discordgo.Session, i *discordgo.InteractionCreate, m *models.RosterMember) {
	m.Status = models.MemberStatusComplete
	m.Step = models.MemberStepDone
	ctx, cancel := handlerContext()
	defer cancel()
	if err := b.members.SaveProgress(ctx, m); err != nil {
		log.Printf("signup: finish: %v", err)
	}
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    signupSummary(m),
			Components: []discordgo.MessageComponent{},
		},
	})
	if err != nil {
		log.Printf("signup: finish respond: %v", err)
	}
}

// --- step renderers (content + components) ---

func signupDaysStep(memberID int64) (string, []discordgo.MessageComponent) {
	opts := make([]discordgo.SelectMenuOption, 0, len(canonicalDays))
	for _, d := range canonicalDays {
		opts = append(opts, discordgo.SelectMenuOption{Label: esoref.DayLabel(d), Value: d})
	}
	return "**Step 1 of 5 — Availability**\nWhich days of the week are you usually available?",
		selectRow("signup_days:"+strconv.FormatInt(memberID, 10), "Pick your days", 1, len(opts), opts)
}

func signupTimezoneStep(memberID int64) (string, []discordgo.MessageComponent) {
	opts := make([]discordgo.SelectMenuOption, 0, len(signupTimezones))
	for _, tz := range signupTimezones {
		opts = append(opts, discordgo.SelectMenuOption{Label: tzOffsetLabel(tz), Value: tz})
	}
	return "**Step 2 of 5 — Timezone**\nWhich timezone should I record your hours in?",
		selectRow("signup_tz:"+strconv.FormatInt(memberID, 10), "Pick your timezone", 1, 1, opts)
}

// tzOffsetLabel renders an IANA zone with its current UTC offset, e.g.
// "America/New_York (UTC-5)" or "Asia/Kolkata (UTC+5:30)", so the picker shows
// the +/- offset at a glance. Falls back to the bare name if the zone can't be
// loaded (the bot embeds time/tzdata, so this is unexpected).
func tzOffsetLabel(tz string) string {
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return tz
	}
	_, offset := time.Now().In(loc).Zone() // seconds east of UTC
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	hours := offset / 3600
	mins := (offset % 3600) / 60
	if mins != 0 {
		return fmt.Sprintf("%s (UTC%s%d:%02d)", tz, sign, hours, mins)
	}
	return fmt.Sprintf("%s (UTC%s%d)", tz, sign, hours)
}

func signupTimesStep(m *models.RosterMember, day string) (string, []discordgo.MessageComponent) {
	id := strconv.FormatInt(m.ID, 10)
	w := m.Availability[day]
	startOpts := hourOptions(w.Start)
	endOpts := endHourOptions(w.End)
	rows := []discordgo.MessageComponent{}
	rows = append(rows, selectRow("signup_start:"+id+":"+day, "Start hour", 1, 1, startOpts)...)
	rows = append(rows, selectRow("signup_end:"+id+":"+day, "End hour", 1, 1, endOpts)...)
	// Quick-apply buttons: "All day" plus one per unique window already entered
	// on an earlier day, so common spans can be reused in a single click.
	rows = append(rows, quickSpanRows(id, day, m)...)
	tz := m.Timezone
	if tz == "" {
		tz = "your local time"
	}
	content := fmt.Sprintf("**Step 3 of 5 — Hours (%s)**\nWhat time are you available on **%s**? Pick a start and end hour, or use a quick button below. (Times are in %s; pick **24:00** to run to midnight.)",
		tz, esoref.DayLabel(day), tz)
	return content, rows
}

// span is a start/end hour pair (end is 1..24; 24 = midnight/end of day).
type span struct{ start, end int }

// quickSpanRows builds the quick-apply button rows for the hours step: an
// "All day" preset followed by each distinct window the user already entered on
// other days. Buttons carry the window in their custom ID so the stateless
// handler can apply it. Chunked into rows of 5 (Discord's per-row button cap)
// and capped overall to stay within the message's action-row budget.
func quickSpanRows(id, day string, m *models.RosterMember) []discordgo.MessageComponent {
	allDay := span{0, 24}
	spans := []span{allDay}
	seen := map[span]bool{allDay: true}
	// Reuse earlier days' completed windows, in the order days were chosen.
	for _, d := range m.Days {
		if d == day {
			continue
		}
		w, ok := m.Availability[d]
		if !ok || w.End == 0 || w.End <= w.Start {
			continue
		}
		sp := span{w.Start, w.End}
		if seen[sp] {
			continue
		}
		seen[sp] = true
		spans = append(spans, sp)
	}

	// Keep within the action-row budget: 2 select rows are already used, leaving
	// at most 3 button rows of 5 → 15 buttons.
	if len(spans) > 15 {
		spans = spans[:15]
	}

	rows := []discordgo.MessageComponent{}
	for i := 0; i < len(spans); i += 5 {
		end := i + 5
		if end > len(spans) {
			end = len(spans)
		}
		btns := make([]discordgo.MessageComponent, 0, end-i)
		for _, sp := range spans[i:end] {
			label := spanLabel(sp)
			style := discordgo.SecondaryButton
			if sp == allDay {
				style = discordgo.PrimaryButton
			}
			btns = append(btns, discordgo.Button{
				Label:    label,
				Style:    style,
				CustomID: fmt.Sprintf("signup_span:%s:%s~%d~%d", id, day, sp.start, sp.end),
			})
		}
		rows = append(rows, discordgo.ActionsRow{Components: btns})
	}
	return rows
}

func spanLabel(sp span) string {
	if sp.start == 0 && sp.end == 24 {
		return "All day (00:00–24:00)"
	}
	return fmt.Sprintf("%02d:00–%02d:00", sp.start, sp.end)
}

// parseSpan decodes a quick-span button payload "<day>~<start>~<end>".
func parseSpan(payload string) (day string, start, end int, ok bool) {
	p := strings.Split(payload, "~")
	if len(p) != 3 {
		return "", 0, 0, false
	}
	s, err1 := strconv.Atoi(p[1])
	e, err2 := strconv.Atoi(p[2])
	if err1 != nil || err2 != nil {
		return "", 0, 0, false
	}
	return p[0], s, e, true
}

func signupRolesStep(memberID int64) (string, []discordgo.MessageComponent) {
	opts := make([]discordgo.SelectMenuOption, 0, len(canonicalRoles))
	for _, r := range canonicalRoles {
		opts = append(opts, discordgo.SelectMenuOption{Label: esoref.RoleLabel(r), Value: r})
	}
	return "**Step 4 of 5 — Roles**\nWhich roles are you comfortable playing?",
		selectRow("signup_roles:"+strconv.FormatInt(memberID, 10), "Pick your roles", 1, len(opts), opts)
}

func signupClassesStep(memberID int64, role string) (string, []discordgo.MessageComponent) {
	opts := make([]discordgo.SelectMenuOption, 0, len(signupClasses))
	for _, c := range signupClasses {
		opts = append(opts, discordgo.SelectMenuOption{Label: esoref.ClassLabel(c), Value: c})
	}
	content := fmt.Sprintf("**Step 5 of 5 — Classes**\nWhich classes can you play as **%s**?", esoref.RoleLabel(role))
	return content, selectRow("signup_classes:"+strconv.FormatInt(memberID, 10)+":"+role, "Pick your classes", 1, len(opts), opts)
}

// signupClasses lists the playable class keys (mirrors the frontend CLASSES).
var signupClasses = []string{"arcanist", "dragonknight", "necromancer", "nightblade", "sorcerer", "templar", "warden"}

func signupSummary(m *models.RosterMember) string {
	var b strings.Builder
	b.WriteString("All done — thank you! Here's what I recorded:\n\n")
	if len(m.Days) > 0 {
		days := make([]string, 0, len(m.Days))
		for _, d := range m.Days {
			label := esoref.DayLabel(d)
			if w, ok := m.Availability[d]; ok {
				label += fmt.Sprintf(" %02d:00–%02d:00", w.Start, w.End)
			}
			days = append(days, label)
		}
		tz := m.Timezone
		if tz != "" {
			b.WriteString("__Availability__ (" + tz + ")\n")
		} else {
			b.WriteString("__Availability__\n")
		}
		b.WriteString(strings.Join(days, "\n") + "\n\n")
	}
	if len(m.Roles) > 0 {
		b.WriteString("__Roles & Classes__\n")
		for _, r := range m.Roles {
			classes := m.ClassesByRole[r]
			labels := make([]string, 0, len(classes))
			for _, c := range classes {
				labels = append(labels, esoref.ClassLabel(c))
			}
			line := esoref.RoleLabel(r)
			if len(labels) > 0 {
				line += ": " + strings.Join(labels, ", ")
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// --- small helpers ---

// selectRow builds an action row holding a single string select menu. min/max
// bound how many options the user may choose.
func selectRow(customID, placeholder string, min, max int, opts []discordgo.SelectMenuOption) []discordgo.MessageComponent {
	minV := min
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				CustomID:    customID,
				Placeholder: placeholder,
				MinValues:   &minV,
				MaxValues:   max,
				Options:     opts,
			},
		}},
	}
}

// hourOptions returns 00:00–23:00 start-hour options, marking `selected` as the
// default. Midnight as a start is 00:00.
func hourOptions(selected int) []discordgo.SelectMenuOption {
	opts := make([]discordgo.SelectMenuOption, 0, 24)
	for h := 0; h < 24; h++ {
		opts = append(opts, discordgo.SelectMenuOption{
			Label:   fmt.Sprintf("%02d:00", h),
			Value:   strconv.Itoa(h),
			Default: h == selected,
		})
	}
	return opts
}

// endHourOptions returns 01:00–24:00 end-hour options, marking `selected` as the
// default. 24:00 represents midnight (end of day): a plain 00:00–23:00 list has
// no way to say "available until midnight", which this fixes.
func endHourOptions(selected int) []discordgo.SelectMenuOption {
	opts := make([]discordgo.SelectMenuOption, 0, 24)
	for h := 1; h <= 24; h++ {
		label := fmt.Sprintf("%02d:00", h)
		if h == 24 {
			label = "24:00 (midnight)"
		}
		opts = append(opts, discordgo.SelectMenuOption{
			Label:   label,
			Value:   strconv.Itoa(h),
			Default: h == selected,
		})
	}
	return opts
}

// orderBy returns the members of `values` in the order they appear in `order`.
func orderBy(values, order []string) []string {
	have := map[string]bool{}
	for _, v := range values {
		have[v] = true
	}
	out := []string{}
	for _, o := range order {
		if have[o] {
			out = append(out, o)
		}
	}
	return out
}

func firstDay(days []string) string   { return firstIn(days, canonicalDays) }
func firstRole(roles []string) string { return firstIn(roles, canonicalRoles) }

func firstIn(values, order []string) string {
	have := map[string]bool{}
	for _, v := range values {
		have[v] = true
	}
	for _, o := range order {
		if have[o] {
			return o
		}
	}
	return ""
}

func nextDay(days []string, current string) string   { return nextIn(days, current, canonicalDays) }
func nextRole(roles []string, current string) string { return nextIn(roles, current, canonicalRoles) }

// nextIn returns the value after `current` among `values`, walking in `order`.
func nextIn(values []string, current string, order []string) string {
	have := map[string]bool{}
	for _, v := range values {
		have[v] = true
	}
	seen := false
	for _, o := range order {
		if o == current {
			seen = true
			continue
		}
		if seen && have[o] {
			return o
		}
	}
	return ""
}
