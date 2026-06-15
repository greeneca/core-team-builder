// Package discordfmt renders a team's roster into Discord post text: a condensed
// channel "overview" (roster grouped by role with abbreviated gear) and a
// per-player "detail" block (full build + per-encounter loadout) sent as a DM.
//
// These are Go ports of the frontend formatters in frontend/js/app.js
// (formatCondensed / formatDetailed / loadoutDetailParts / buildText) and use
// the code-generated labels in internal/esoref. Keep the two in rough sync.
package discordfmt

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/core-team-builder/backend/internal/esoref"
	"github.com/core-team-builder/backend/internal/models"
)

// Overview builds the condensed channel post for a team: a schedule header, then
// players grouped by role (Tank, Healer, Support DPS, DPS) with abbreviated gear
// from the primary encounter. groupings are appended when present.
func Overview(team *models.Team, primary *models.Encounter, groupings []models.Grouping) string {
	var L []string
	L = append(L, fmt.Sprintf("**%s**", team.Name))
	L = append(L, scheduleHeader(team))
	L = append(L, "")

	bySlot := map[int]models.Loadout{}
	if primary != nil {
		for _, lo := range primary.Loadouts {
			bySlot[lo.Slot] = lo
		}
	}

	players := sortedPlayers(team)
	type row struct{ role, slot, who, cls, gear string }
	rows := make([]row, 0, len(players))
	for _, p := range players {
		rows = append(rows, row{
			role: p.Role,
			slot: fmt.Sprintf("%d.", p.Slot),
			who:  who(p),
			cls:  classOrDash(p.Class),
			gear: gearAbbrevList(bySlot[p.Slot].Gear),
		})
	}

	// Column widths across all rows so groups line up (best-effort in Discord's
	// proportional font; no code block so handles still notify).
	wSlot, wWho, wCls := 0, 0, 0
	for _, r := range rows {
		wSlot = max(wSlot, len(r.slot))
		wWho = max(wWho, len(r.who))
		wCls = max(wCls, len(r.cls))
	}

	order := []struct{ label, value string }{
		{"Tank", "tank"}, {"Healer", "healer"}, {"Support DPS", "support_dps"}, {"DPS", "dps"},
	}
	known := map[string]bool{"tank": true, "healer": true, "support_dps": true, "dps": true}
	groups := append([]struct{ label, value string }{}, order...)
	for _, r := range rows {
		if !known[r.role] {
			groups = append(groups, struct{ label, value string }{"Other", ""})
			break
		}
	}

	first := true
	for _, g := range groups {
		var grp []row
		for _, r := range rows {
			if g.value == "" {
				if !known[r.role] {
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
		L = append(L, fmt.Sprintf("__%s__", g.label))
		for _, r := range grp {
			L = append(L, fmt.Sprintf("%s  %s  %s  %s",
				padEnd(r.slot, wSlot), padEnd(r.who, wWho), padEnd(r.cls, wCls), r.gear))
		}
	}

	if gl := formatGroupings(team, groupings); len(gl) > 0 {
		L = append(L, "")
		L = append(L, gl...)
	}
	if note := strings.TrimSpace(team.SignupNote); note != "" {
		L = append(L, "")
		L = append(L, note)
	}
	return strings.TrimSpace(strings.Join(L, "\n"))
}

// PlayerDetail builds the detailed DM for a single player: role/class/race, the
// build line (subclass lines or masteries), and each encounter's loadout.
func PlayerDetail(team *models.Team, p models.Player, encounters []models.Encounter) string {
	var L []string
	name := p.Name
	if name == "" {
		name = fmt.Sprintf("Slot %d", p.Slot)
	}
	L = append(L, fmt.Sprintf("**%s** — %s", team.Name, "Your trial assignment"))
	L = append(L, "")
	header := fmt.Sprintf("__%d. %s__ — %s", p.Slot, name, esoref.RoleLabel(p.Role))
	if tag := discordTag(p.DiscordHandle); tag != "" {
		header += " · " + tag
	}
	L = append(L, header)
	L = append(L, fmt.Sprintf("Class: %s · Race: %s", esoref.ClassLabel(p.Class), esoref.RaceLabel(p.Race)))
	L = append(L, buildText(p))
	for _, enc := range encounters {
		lo, ok := loadoutForSlot(enc, p.Slot)
		parts := ""
		if ok {
			if dp := loadoutDetailParts(lo); len(dp) > 0 {
				parts = strings.Join(dp, " | ")
			}
		}
		if parts == "" {
			parts = "(no loadout set)"
		}
		L = append(L, fmt.Sprintf("• %s: %s", enc.Name, parts))
	}
	return strings.TrimSpace(strings.Join(L, "\n"))
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

// loadoutDetailParts mirrors the JS helper: labelled segments for one loadout.
func loadoutDetailParts(lo models.Loadout) []string {
	var parts []string
	if s := mapLabels(lo.Gear, esoref.GearLabel); s != "" {
		parts = append(parts, "Gear: "+s)
	}
	if s := mapLabels(lo.Skills, esoref.SkillLabel); s != "" {
		parts = append(parts, "Skills: "+s)
	}
	if s := mapLabels(lo.CritDmg, esoref.CritDmgLabel); s != "" {
		parts = append(parts, "Crit dmg: "+s)
	}
	if lo.Mundus != "" {
		parts = append(parts, "Mundus: "+esoref.MundusLabel(lo.Mundus))
	}
	if s := mapLabels(lo.Potions, esoref.PotionLabel); s != "" {
		parts = append(parts, "Potions: "+s)
	}
	if s := mapLabels(lo.CPBlue, esoref.CPBlueLabel); s != "" {
		parts = append(parts, "CP: "+s)
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
		parts = append(parts, "Armor: "+strings.Join(armor, "/"))
	}
	// pen_extra may repeat keys for stackable sources; collapse into "Label ×N".
	if pen := penExtraParts(lo.PenExtra); pen != "" {
		parts = append(parts, "Pen: "+pen)
	}
	return parts
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

// formatGroupings mirrors the JS helper: a section listing each grouping's
// numbered groups and assigned players. Returns nil when there are none.
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

// scheduleHeader renders the days + time line for the overview. The stored time
// is UTC; it is shown in UTC plus each of the team's extra display timezones
// (using Go's embedded tzdata). Recurring weekly times have no date, so the
// conversion is anchored on "today" and may be off by an hour near a DST change
// (same trade-off the web UI accepts).
func scheduleHeader(team *models.Team) string {
	days := "TBD"
	if labels := dayLabels(team.ScheduleDays); labels != "" {
		days = labels
	}
	if team.ScheduleTime == "" {
		return days
	}
	times := []string{team.ScheduleTime + " UTC"}
	for _, tz := range team.TeamTimezones {
		if conv, short, ok := convertWallTime(team.ScheduleTime, tz); ok {
			times = append(times, fmt.Sprintf("%s %s", conv, short))
		}
	}
	return fmt.Sprintf("%s · %s", days, strings.Join(times, " · "))
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

// convertWallTime converts an "HH:MM" UTC wall time to the given IANA zone,
// anchored on today. Returns the converted "HH:MM", a short zone abbreviation,
// and ok=false when the time or zone is invalid.
func convertWallTime(hhmm, zone string) (string, string, bool) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return "", "", false
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return "", "", false
	}
	now := time.Now().UTC()
	utc := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC)
	local := utc.In(loc)
	return local.Format("15:04"), local.Format("MST"), true
}

func padEnd(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}
