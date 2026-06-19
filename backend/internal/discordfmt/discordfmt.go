// Package discordfmt renders a team's roster into Discord post text: a condensed
// channel "overview" (roster grouped by role with abbreviated gear) and a
// per-player "detail" block (full build + per-encounter loadout) sent as a DM.
//
// These formatters use the code-generated labels in internal/esoref (sourced
// from the frontend's single-source data). The web app no longer renders these
// posts itself — the Discord bot is the only consumer.
package discordfmt

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/core-team-builder/backend/internal/esoref"
	"github.com/core-team-builder/backend/internal/models"
)

// BuildPost renders a team's channel post into embed-ready parts the bot wraps
// in a single boxed embed:
//   - title:       the team name.
//   - description: a dynamic next-run timestamp (shown in each viewer's own
//     timezone), the roster grouped by role as Markdown lines, any groupings,
//     and the post footer.
//
// marks carries each responding player's RSVP status keyed by slot
// (models.RSVPYes / models.RSVPNo); a ✅ or ❌ icon is rendered beside that
// player's name in the roster. Players without an entry show a neutral ▫️.
//
// The self-required penetration/crit and missing self-buffs now live in the
// per-player build-details DM (see PlayerDetail), not on this post.
func BuildPost(team *models.Team, primary *models.Encounter, groupings []models.Grouping, marks map[int]string) (title, description string) {
	title = team.Name

	var L []string
	// Schedule as a single Discord timestamp: each viewer sees it in their own
	// timezone, plus a relative "in X" hint. Falls back to the day list when
	// there is no concrete next run to anchor a timestamp on.
	if unix, ok := nextRunUnix(team.ScheduleDays, team.ScheduleTime); ok {
		L = append(L, fmt.Sprintf("\U0001F5D3\uFE0F  Next run: <t:%d:F> (<t:%d:R>)", unix, unix))
	} else if s := scheduleFallback(team); s != "" {
		L = append(L, "\U0001F5D3\uFE0F  "+s)
	}

	// Roster grouped by role. Rendered as Markdown (not a code block) so each
	// player's RSVP status shows as a ✅/❌/▫️ icon beside their name.
	if rl := rosterLines(team, primary, marks); len(rl) > 0 {
		if len(L) > 0 {
			L = append(L, "")
		}
		L = append(L, rl...)
	}

	if gl := formatGroupings(team, groupings); len(gl) > 0 {
		L = append(L, "")
		L = append(L, gl...)
	}
	if footer := strings.TrimSpace(team.PostFooter); footer != "" {
		L = append(L, "")
		L = append(L, footer)
	}
	description = strings.TrimSpace(strings.Join(L, "\n"))
	return title, description
}

// BuildPremadePost renders a pre-made trial run announcement into embed-ready
// parts. title is the run's title (falling back to the team name); description
// holds the scheduled time as a per-viewer Discord timestamp, the team's
// free-form premade post body, and the per-slot roster showing each slot's
// role/class and either the claimant's name or "open".
//
// claimants maps a slot number to the claiming signup; an empty/absent entry
// renders the slot as open. Claimants are shown by their stored display name
// (plain text), not a <@id> mention, because Discord's mobile client does not
// reliably resolve mentions inside embeds (it shows them as raw numeric IDs).
//
// postOverride, when non-empty, replaces the team's default premade post body
// (team.PremadePost) for this run.
func BuildPremadePost(team *models.Team, title, postOverride string, scheduledUnix int64, primary *models.Encounter, claimants map[int]models.PremadeSignup, waitlist []models.PremadeWaitlistEntry) (string, string) {
	if strings.TrimSpace(title) == "" {
		title = team.Name
	}

	var L []string
	if scheduledUnix > 0 {
		L = append(L, fmt.Sprintf("\U0001F5D3\uFE0F  <t:%d:F> (<t:%d:R>)", scheduledUnix, scheduledUnix))
	}
	body := strings.TrimSpace(postOverride)
	if body == "" {
		body = strings.TrimSpace(team.PremadePost)
	}
	if body != "" {
		if len(L) > 0 {
			L = append(L, "")
		}
		L = append(L, body)
	}
	if rl := premadeRosterLines(team, primary, claimants); len(rl) > 0 {
		if len(L) > 0 {
			L = append(L, "")
		}
		L = append(L, rl...)
	}
	if wl := premadeWaitlistLines(team, waitlist); len(wl) > 0 {
		L = append(L, "")
		L = append(L, wl...)
	}
	// Simple-signup runs hide each slot's class/gear and the build-details menu,
	// so the footer doesn't mention build details for them.
	claimText := "claim a slot or get a slot's build details"
	if team.SimpleSignup {
		claimText = "claim a slot"
	}
	footer := fmt.Sprintf("_Use the menus below to %s. Claiming a new slot releases your previous one._", claimText)
	if team.WaitlistEnabled {
		footer = fmt.Sprintf("_Use the menus below to %s. Claiming a new slot releases your previous one. If a role is full you can join its waitlist — you'll be moved in automatically when a slot opens._", claimText)
	}
	L = append(L, "", footer)
	return title, strings.TrimSpace(strings.Join(L, "\n"))
}

// premadeWaitlistLines renders the per-role waitlist block (FIFO, by display
// name per role), ordered by the team's own role set. Returns nil when nobody is
// waiting.
func premadeWaitlistLines(team *models.Team, waitlist []models.PremadeWaitlistEntry) []string {
	if len(waitlist) == 0 {
		return nil
	}
	byRole := map[string][]string{}
	roles := make([]string, 0, len(waitlist))
	for _, w := range waitlist {
		byRole[w.Role] = append(byRole[w.Role], claimantDisplay(w.DiscordUsername, w.DiscordUserID))
		roles = append(roles, w.Role)
	}
	L := []string{"__Waitlist__"}
	seen := map[string]bool{}
	emit := func(role string) {
		if seen[role] {
			return
		}
		seen[role] = true
		if names := byRole[role]; len(names) > 0 {
			L = append(L, team.RoleEmoji(role)+" "+team.RoleLabel(role)+": "+strings.Join(names, ", "))
		}
	}
	// The team's own roles first, then any other roles present (queue order).
	for _, r := range team.OrderedRoleKeys(roles...) {
		emit(r)
	}
	for _, w := range waitlist {
		emit(w.Role)
	}
	return L
}

// roleGroup is one labeled section when rendering a roster grouped by role; value
// is the role key ("" for the catch-all "Other" group).
type roleGroup struct{ label, value string }

// roleGroups returns the ordered sections to render a roster grouped by role: the
// team's own roles in their defined order (plus any extra roles present on the
// roster), followed by an "Other" group when any of the given roles is unset.
func roleGroups(team *models.Team, rolesPresent []string) []roleGroup {
	groups := make([]roleGroup, 0, len(rolesPresent)+1)
	for _, k := range team.OrderedRoleKeys(rolesPresent...) {
		groups = append(groups, roleGroup{label: team.RoleLabel(k), value: k})
	}
	for _, r := range rolesPresent {
		if r == "" {
			groups = append(groups, roleGroup{label: "Other", value: ""})
			break
		}
	}
	return groups
}

// claimantDisplay renders a signup for embed text. Discord does not reliably
// resolve <@id> mentions inside embeds on its mobile client (they show as a raw
// numeric ID), so use the stored display name as plain text; fall back to the
// raw ID only when no name was recorded.
func claimantDisplay(username, discordUserID string) string {
	if name := strings.TrimSpace(username); name != "" {
		return name
	}
	return discordUserID
}

// premadeRosterLines renders the roster grouped by role for a pre-made run: each
// slot shows its number, the slot's roster name, class, and either the
// claimant's name or "open".
func premadeRosterLines(team *models.Team, primary *models.Encounter, claimants map[int]models.PremadeSignup) []string {
	bySlot := map[int]models.Loadout{}
	if primary != nil {
		for _, lo := range primary.Loadouts {
			bySlot[lo.Slot] = lo
		}
	}
	players := sortedPlayers(team)
	if len(players) == 0 {
		return nil
	}
	type row struct{ role, line string }
	rows := make([]row, 0, len(players))
	for _, p := range players {
		claim := "_open_"
		if sg, ok := claimants[p.Slot]; ok {
			if name := claimantDisplay(sg.DiscordUsername, sg.DiscordUserID); name != "" {
				claim = name
			}
		}
		// Unnamed slots simply omit the name segment (no "Slot N" fallback).
		name := strings.TrimSpace(p.Name)
		var parts []string
		if team.SimpleSignup {
			// Simple signup: roles only — hide class and gear.
			parts = []string{fmt.Sprintf("%d.", p.Slot)}
			if name != "" {
				parts = append(parts, name)
			}
			parts = append(parts, claim)
		} else {
			parts = []string{fmt.Sprintf("%d.", p.Slot)}
			if name != "" {
				parts = append(parts, name)
			}
			parts = append(parts, classOrDash(p.Class))
			if p.Werewolf {
				parts = append(parts, "WW")
			}
			parts = append(parts,
				gearAbbrevList(bySlot[p.Slot].Gear),
				claim,
			)
		}
		rows = append(rows, row{role: p.Role, line: strings.Join(parts, " · ")})
	}

	rolesPresent := make([]string, 0, len(rows))
	for _, r := range rows {
		rolesPresent = append(rolesPresent, r.role)
	}
	groups := roleGroups(team, rolesPresent)

	var L []string
	first := true
	for _, g := range groups {
		var grp []row
		for _, r := range rows {
			if g.value == "" {
				if r.role == "" {
					grp = append(grp, r)
				}
			} else if r.role == g.value {
				grp = append(grp, r)
			}
		}
		if len(grp) == 0 {
			continue
		}
		if !first {
			L = append(L, "")
		}
		first = false
		header := "__" + g.label + "__"
		if g.value != "" {
			header = team.RoleEmoji(g.value) + " " + header
		}
		L = append(L, header)
		for _, r := range grp {
			L = append(L, r.line)
		}
	}
	return L
}

// rosterLines renders the roster grouped by role (the team's own roles in their
// defined order, then any "Other" for unset roles) as Markdown lines. Each
// player is one line prefixed by an RSVP icon (✅ coming / ❌ not coming / ▫️ no
// response) followed by slot, name, class, and abbreviated gear from the primary
// encounter. Returns nil when there are no players.
func rosterLines(team *models.Team, primary *models.Encounter, marks map[int]string) []string {
	bySlot := map[int]models.Loadout{}
	if primary != nil {
		for _, lo := range primary.Loadouts {
			bySlot[lo.Slot] = lo
		}
	}

	players := sortedPlayers(team)
	if len(players) == 0 {
		return nil
	}
	type row struct {
		role, line string
	}
	rows := make([]row, 0, len(players))
	for _, p := range players {
		parts := []string{
			fmt.Sprintf("%d.", p.Slot),
			who(p),
			classOrDash(p.Class),
		}
		if p.Werewolf {
			parts = append(parts, "WW")
		}
		parts = append(parts, gearAbbrevList(bySlot[p.Slot].Gear))
		rows = append(rows, row{
			role: p.Role,
			line: rsvpIcon(marks[p.Slot]) + " " + strings.Join(parts, " · "),
		})
	}

	rolesPresent := make([]string, 0, len(rows))
	for _, r := range rows {
		rolesPresent = append(rolesPresent, r.role)
	}
	groups := roleGroups(team, rolesPresent)

	var L []string
	first := true
	for _, g := range groups {
		var grp []row
		for _, r := range rows {
			if g.value == "" {
				if r.role == "" {
					grp = append(grp, r)
				}
			} else if r.role == g.value {
				grp = append(grp, r)
			}
		}
		if len(grp) == 0 {
			continue
		}
		if !first {
			L = append(L, "")
		}
		first = false
		header := "__" + g.label + "__"
		if g.value != "" {
			header = team.RoleEmoji(g.value) + " " + header
		}
		L = append(L, header)
		for _, r := range grp {
			L = append(L, r.line)
		}
	}
	return L
}

// rsvpIcon maps an RSVP status to the emoji shown beside a player's name.
// Unknown/no response renders as a neutral marker.
func rsvpIcon(status string) string {
	switch status {
	case models.RSVPYes:
		return "\u2705" // ✅
	case models.RSVPNo:
		return "\u274C" // ❌
	default:
		return "\u25AB\uFE0F" // ▫️
	}
}

// PlayerDetail builds the detailed DM for a single player, split into embed-ready
// parts (title + description) so the bot can wrap it in a boxed embed. The
// description reads top-to-bottom: Player, Class & Race, Build, one section per
// encounter, then a Requirements section (self-required pen/crit and any self
// buffs). Every data type sits on its own line under an underlined header
// (Discord `__header__`). When there is only one encounter its name header is
// omitted, since it is redundant.
func PlayerDetail(team *models.Team, p models.Player, encounters []models.Encounter) (title, description string) {
	name := p.Name
	if name == "" {
		name = fmt.Sprintf("Slot %d", p.Slot)
	}
	title = fmt.Sprintf("%s — Your Build Details", team.Name)

	var L []string

	// Player.
	who := fmt.Sprintf("%d. %s — %s", p.Slot, name, team.RoleLabel(p.Role))
	if tag := discordTag(p.DiscordHandle); tag != "" {
		who += " · " + tag
	}
	L = append(L, "__Player__", who)

	// Class & Race.
	L = append(L, "", "__Class & Race__", fmt.Sprintf("%s · %s", esoref.ClassLabel(p.Class), esoref.RaceLabel(p.Race)))

	// Build (subclass lines or masteries).
	L = append(L, "", "__Build__", buildText(p))

	// One section per encounter, each data type on its own underlined line. A
	// single encounter needs no name header.
	singleEncounter := len(encounters) == 1
	for _, enc := range encounters {
		L = append(L, "")
		if !singleEncounter {
			L = append(L, fmt.Sprintf("**%s**", enc.Name))
		}
		lo, ok := loadoutForSlot(enc, p.Slot)
		var fields []labelledField
		if ok {
			fields = loadoutDetailFields(lo)
		}
		if len(fields) == 0 {
			L = append(L, "_(no loadout set)_")
			continue
		}
		for _, f := range fields {
			L = append(L, "__"+f.header+"__", f.value)
		}
	}

	// Requirements (bottom): team-wide targets after group buffs (what each
	// player must bring on their own) plus any self buffs the team does not cover
	// group-wide. Computed from the primary (first) encounter, matching the post.
	var primary *models.Encounter
	if len(encounters) > 0 {
		primary = &encounters[0]
	}
	pen, crit := selfRequired(team, primary)
	L = append(L, "", "**Requirements**")
	L = append(L, "__Personal Stats (before group buffs)__",
		fmt.Sprintf("Penetration %s", commaInt(pen)),
		fmt.Sprintf("Crit damage %d%%", crit))
	if missing := missingSelfBuffs(team, primary); len(missing) > 0 {
		L = append(L, "__Self Buffs__", strings.Join(missing, ", "))
	}

	// Optional team-wide footer (e.g. raid lead, voice channel, reminders).
	if footer := strings.TrimSpace(team.DMFooter); footer != "" {
		L = append(L, "", footer)
	}
	description = strings.TrimSpace(strings.Join(L, "\n"))
	return title, description
}

// --- helpers (ported from app.js) ---

func sortedPlayers(team *models.Team) []models.Player {
	ps := append([]models.Player{}, team.Players...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Slot < ps[j].Slot })
	return ps
}

func who(p models.Player) string {
	if tag := discordTag(p.DiscordHandle); tag != "" {
		return tag
	}
	if p.Name != "" {
		return p.Name
	}
	return fmt.Sprintf("Slot %d", p.Slot)
}

func classOrDash(class string) string {
	if class == "" {
		return "—"
	}
	return esoref.ClassLabel(class)
}

func gearAbbrevList(gear []string) string {
	if len(gear) == 0 {
		return "—"
	}
	out := make([]string, 0, len(gear))
	for _, g := range gear {
		out = append(out, esoref.GearAbbrev(g))
	}
	return strings.Join(out, "/")
}

func discordTag(handle string) string {
	h := strings.TrimSpace(handle)
	if h == "" {
		return ""
	}
	if strings.HasPrefix(h, "@") {
		return h
	}
	return "@" + h
}

func buildText(p models.Player) string {
	if p.Subclassed {
		var lines []string
		for _, v := range []string{p.SkillLine1, p.SkillLine2, p.SkillLine3} {
			if v != "" {
				lines = append(lines, esoref.SkillLineLabel(v))
			}
		}
		if len(lines) == 0 {
			return "Subclass: —"
		}
		return "Subclass: " + strings.Join(lines, " / ")
	}
	var m []string
	for _, v := range []string{p.Mastery1, p.Mastery2} {
		if v != "" {
			m = append(m, esoref.MasteryLabel(v))
		}
	}
	if len(m) == 0 {
		return "Masteries: —"
	}
	return "Masteries: " + strings.Join(m, ", ")
}

func loadoutForSlot(enc models.Encounter, slot int) (models.Loadout, bool) {
	for _, lo := range enc.Loadouts {
		if lo.Slot == slot {
			return lo, true
		}
	}
	return models.Loadout{}, false
}

// labelledField is one loadout data type rendered as an underlined header
// (Header) plus its value (Value) in the player-detail DM.
type labelledField struct {
	header string
	value  string
}

// loadoutDetailFields mirrors the JS helper: one labelled field per data type
// set on a loadout (Gear, Skills, Crit Damage, Mundus, Potions, CP, Armor,
// Penetration, Scribed Buffs). Empty data types are omitted.
func loadoutDetailFields(lo models.Loadout) []labelledField {
	var fields []labelledField
	if s := mapLabels(lo.Gear, esoref.GearLabel); s != "" {
		fields = append(fields, labelledField{"Gear", s})
	}
	if s := mapLabels(lo.Skills, esoref.SkillLabel); s != "" {
		fields = append(fields, labelledField{"Skills", s})
	}
	if s := mapLabels(lo.CritDmg, esoref.CritDmgLabel); s != "" {
		fields = append(fields, labelledField{"Crit Damage", s})
	}
	if lo.Mundus != "" {
		fields = append(fields, labelledField{"Mundus", esoref.MundusLabel(lo.Mundus)})
	}
	if s := mapLabels(lo.Potions, esoref.PotionLabel); s != "" {
		fields = append(fields, labelledField{"Potions", s})
	}
	if s := mapLabels(lo.CPBlue, esoref.CPBlueLabel); s != "" {
		fields = append(fields, labelledField{"CP", s})
	}
	var armor []string
	if lo.ArmorHeavy > 0 {
		armor = append(armor, fmt.Sprintf("%dH", lo.ArmorHeavy))
	}
	if lo.ArmorMedium > 0 {
		armor = append(armor, fmt.Sprintf("%dM", lo.ArmorMedium))
	}
	if lo.ArmorLight > 0 {
		armor = append(armor, fmt.Sprintf("%dL", lo.ArmorLight))
	}
	if len(armor) > 0 {
		fields = append(fields, labelledField{"Armor", strings.Join(armor, "/")})
	}
	// pen_extra may repeat keys for stackable sources; collapse into "Label ×N".
	if pen := penExtraParts(lo.PenExtra); pen != "" {
		fields = append(fields, labelledField{"Penetration", pen})
	}
	if s := mapLabels(lo.ScribedBuffs, esoref.ScribedBuffLabel); s != "" {
		fields = append(fields, labelledField{"Scribed Buffs", s})
	}
	// Banner Bearer's Focus Script only makes sense when the grimoire is slotted;
	// mirror the web UI gate so a stale selection isn't shown after it's removed.
	if lo.BannerBearerFocus != "" && hasBannerBearer(lo) {
		fields = append(fields, labelledField{"Banner Focus", esoref.BannerBearerFocusLabel(lo.BannerBearerFocus)})
	}
	return fields
}

// hasBannerBearer reports whether the loadout has the Banner Bearer grimoire
// skill slotted.
func hasBannerBearer(lo models.Loadout) bool {
	for _, k := range lo.Skills {
		if k == "banner_bearer" {
			return true
		}
	}
	return false
}

func penExtraParts(keys []string) string {
	if len(keys) == 0 {
		return ""
	}
	counts := map[string]int{}
	for _, k := range keys {
		counts[k]++
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		label := esoref.PenExtraLabel(k)
		if n := counts[k]; n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", label, n))
		} else {
			out = append(out, label)
		}
	}
	return strings.Join(out, ", ")
}

func mapLabels(keys []string, label func(string) string) string {
	if len(keys) == 0 {
		return ""
	}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, label(k))
	}
	return strings.Join(out, ", ")
}

// exportSlotName returns a slot's roster name (or "Slot N") from a team snapshot.
func exportSlotName(team *models.Team, slot int) string {
	for _, p := range team.Players {
		if p.Slot == slot {
			if n := strings.TrimSpace(p.Name); n != "" {
				return n
			}
		}
	}
	return fmt.Sprintf("Slot %d", slot)
}

// formatGroupings renders a section listing each grouping's numbered groups and
// assigned players. Returns nil when there are none.
func formatGroupings(team *models.Team, groupings []models.Grouping) []string {
	if len(groupings) == 0 {
		return nil
	}
	L := []string{"**Groupings**"}
	for _, g := range groupings {
		name := strings.TrimSpace(g.Name)
		if name == "" {
			name = "Grouping"
		}
		L = append(L, "")
		L = append(L, fmt.Sprintf("__%s__", name))
		for _, grp := range g.Groups {
			label := strings.TrimSpace(grp.Name)
			if label == "" {
				label = fmt.Sprintf("Group %d", grp.GroupNumber)
			}
			slots := append([]int{}, grp.Slots...)
			sort.Ints(slots)
			members := make([]string, 0, len(slots))
			for _, s := range slots {
				members = append(members, fmt.Sprintf("%d. %s", s, exportSlotName(team, s)))
			}
			line := "—"
			if len(members) > 0 {
				line = strings.Join(members, ", ")
			}
			L = append(L, fmt.Sprintf("• %s: %s", label, line))
		}
	}
	return L
}

// scheduleFallback renders a plain days/time line, used when there is no
// concrete next run to anchor a dynamic timestamp on.
func scheduleFallback(team *models.Team) string {
	days := dayLabels(team.ScheduleDays)
	if days == "" {
		return "Schedule: TBD"
	}
	if team.ScheduleTime == "" {
		return "Schedule: " + days
	}
	return fmt.Sprintf("Schedule: %s · %s UTC", days, team.ScheduleTime)
}

func dayLabels(days []string) string {
	if len(days) == 0 {
		return ""
	}
	out := make([]string, 0, len(days))
	for _, d := range days {
		out = append(out, esoref.DayLabel(d))
	}
	return strings.Join(out, ", ")
}

var weekdayByDay = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// nextRunUnix returns the Unix time (seconds) of the next occurrence of the
// recurring weekly schedule (one of days, at the UTC HH:MM time), or ok=false
// when either is unset/invalid. The schedule has no date, so the nearest future
// match within the coming week is used. The emitted Discord timestamp then
// renders in each viewer's own timezone.
func nextRunUnix(days []string, hhmm string) (int64, bool) {
	if len(days) == 0 || hhmm == "" {
		return 0, false
	}
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return 0, false
	}
	want := map[time.Weekday]bool{}
	for _, d := range days {
		if wd, ok := weekdayByDay[strings.ToLower(strings.TrimSpace(d))]; ok {
			want[wd] = true
		}
	}
	if len(want) == 0 {
		return 0, false
	}
	now := time.Now().UTC()
	base := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	for offset := 0; offset < 8; offset++ {
		cand := base.AddDate(0, 0, offset)
		if want[cand.Weekday()] && cand.After(now) {
			return cand.Unix(), true
		}
	}
	return 0, false
}

// --- Self-required penetration / crit damage ---
//
// Ports the GROUP-source half of computePenCoverage / computeCritCoverage in
// frontend/js/data.js: detect which sources the team provides to everyone, then
// the "self required" figure is the target/cap minus that group total — what
// each player must still bring from their own gear/CP/skills.

// pcContext is one player's inputs for crit/pen group-source detection.
type pcContext struct {
	slot             int
	gear             map[string]bool
	skills           map[string]bool
	potions          map[string]bool
	masteries        map[string]bool
	skillLines       map[string]bool
	cpBlue           map[string]bool
	penExtra         map[string]bool
	scribed          map[string]bool
	mundus           string
	race             string
	class            string
	subclassed       bool
	catalystElements int
	weaponDamage     int
}

func newPCContext(p models.Player, lo models.Loadout) pcContext {
	c := pcContext{
		slot:             p.Slot,
		gear:             toSet(lo.Gear),
		skills:           toSet(lo.Skills),
		potions:          toSet(lo.Potions),
		cpBlue:           toSet(lo.CPBlue),
		penExtra:         toSet(lo.PenExtra),
		scribed:          scribedSet(lo),
		mundus:           lo.Mundus,
		race:             p.Race,
		class:            p.Class,
		subclassed:       p.Subclassed,
		masteries:        map[string]bool{},
		skillLines:       map[string]bool{},
		catalystElements: clampCatalystElements(lo.CatalystElements),
		weaponDamage:     lo.WeaponDamage,
	}
	if c.weaponDamage < 0 {
		c.weaponDamage = 0
	}
	if p.Subclassed {
		for _, v := range []string{p.SkillLine1, p.SkillLine2, p.SkillLine3} {
			if v != "" {
				c.skillLines[v] = true
			}
		}
	} else {
		for _, v := range []string{p.Mastery1, p.Mastery2} {
			if v != "" {
				c.masteries[v] = true
			}
		}
	}
	return c
}

func toSet(keys []string) map[string]bool {
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// scribedSet returns the loadout's scribed-buff selection, but only when a
// grimoire skill is slotted (mirrors the JS gate so a stale selection kept after
// removing the grimoire doesn't contribute, e.g. to penetration).
func scribedSet(lo models.Loadout) map[string]bool {
	hasGrimoire := false
	for _, k := range lo.Skills {
		if esoref.IsGrimoireSkill(k) {
			hasGrimoire = true
			break
		}
	}
	if !hasGrimoire {
		return map[string]bool{}
	}
	return toSet(lo.ScribedBuffs)
}

// clampCatalystElements mirrors the JS clamp: anything < 1 falls back to 3
// (the full Elemental Catalyst bonus), capped at 3.
func clampCatalystElements(v int) int {
	if v < 1 || v > 3 {
		return 3
	}
	return v
}

func anySet(have map[string]bool, keys []string) bool {
	for _, k := range keys {
		if have[k] {
			return true
		}
	}
	return false
}

// detectHit reports whether a player satisfies a detection map any way it can be
// applied (any category, any candidate key).
func detectHit(c pcContext, d esoref.DetectMap) bool {
	if anySet(c.gear, d.Gear) || anySet(c.skills, d.Skills) || anySet(c.potions, d.Potions) ||
		anySet(c.masteries, d.Masteries) || anySet(c.skillLines, d.SkillLines) || anySet(c.cpBlue, d.CP) ||
		anySet(c.scribed, d.Scribed) {
		return true
	}
	for _, k := range d.Classes {
		if c.class != "" && c.class == k {
			return true
		}
	}
	for _, k := range d.Race {
		if c.race != "" && c.race == k {
			return true
		}
	}
	for _, k := range d.Mundus {
		if c.mundus != "" && c.mundus == k {
			return true
		}
	}
	for _, cp := range d.ClassPassive {
		if c.subclassed {
			if c.skillLines[cp.Line] {
				return true
			}
		} else if c.class == cp.Class {
			return true
		}
	}
	return false
}

// selfRequired returns the penetration and crit damage (%) each player must
// supply from their own sources, after subtracting what the team provides
// group-wide. Loadout inputs come from the primary encounter.
func selfRequired(team *models.Team, primary *models.Encounter) (penReq, critReq int) {
	bySlot := map[int]models.Loadout{}
	if primary != nil {
		for _, lo := range primary.Loadouts {
			bySlot[lo.Slot] = lo
		}
	}
	contexts := make([]pcContext, 0, len(team.Players))
	for _, p := range team.Players {
		contexts = append(contexts, newPCContext(p, bySlot[p.Slot]))
	}

	// Crit group total = base (everyone) + detected group sources.
	critGroup := esoref.CritBase
	for _, s := range esoref.CritGroupSources {
		var providers []pcContext
		for _, c := range contexts {
			if detectHit(c, s.Detect) {
				providers = append(providers, c)
			}
		}
		if len(providers) == 0 {
			continue
		}
		pct := s.Pct
		if s.PerElement {
			// Elemental Catalyst scales with the highest element count among wearers.
			elements := 0
			for _, c := range providers {
				if c.catalystElements > elements {
					elements = c.catalystElements
				}
			}
			pct = elements * esoref.ElementalCatalystPerElement
		}
		critGroup += pct
	}
	critReq = esoref.CritCap - critGroup
	if critReq < 0 {
		critReq = 0
	}

	// Pen group total = detected group sources + group-bucket pen_extra (Crusher).
	penGroup := 0
	for _, s := range esoref.PenGroupSources {
		var providers []pcContext
		for _, c := range contexts {
			if detectHit(c, s.Detect) {
				providers = append(providers, c)
			}
		}
		if len(providers) == 0 {
			continue
		}
		pen := s.Pen
		if s.PerWeaponDamage {
			// Anthelmir's Construct scales off the highest weapon damage among wearers.
			wd := 0
			for _, c := range providers {
				if c.weaponDamage > wd {
					wd = c.weaponDamage
				}
			}
			pen = int(math.Round(float64(wd) * esoref.AnthelmirPenPerWD))
		}
		if pen > 0 {
			penGroup += pen
		}
	}
	for _, s := range esoref.PenExtraSources {
		if s.Bucket != "group" {
			continue
		}
		for _, c := range contexts {
			if c.penExtra[s.Value] {
				penGroup += s.Pen
				break
			}
		}
	}
	penReq = esoref.PenTarget - penGroup
	if penReq < 0 {
		penReq = 0
	}
	return penReq, critReq
}

// missingSelfBuffs returns the labels of self-providable buffs (selfBuff=true)
// that no team member supplies group-wide, so each player must bring them on
// their own. Group coverage is checked against the primary encounter's loadouts.
func missingSelfBuffs(team *models.Team, primary *models.Encounter) []string {
	bySlot := map[int]models.Loadout{}
	if primary != nil {
		for _, lo := range primary.Loadouts {
			bySlot[lo.Slot] = lo
		}
	}
	contexts := make([]pcContext, 0, len(team.Players))
	for _, p := range team.Players {
		contexts = append(contexts, newPCContext(p, bySlot[p.Slot]))
	}
	var missing []string
	for _, b := range esoref.Buffs {
		if !b.SelfBuff {
			continue
		}
		covered := false
		for _, c := range contexts {
			if detectHit(c, b.Detect) {
				covered = true
				break
			}
		}
		if !covered {
			missing = append(missing, b.Label)
		}
	}
	return missing
}

// commaInt formats an integer with thousands separators (e.g. 18200 -> 18,200).
func commaInt(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
