package models

import "errors"

// This file holds ESO game reference data (roles, classes, skill lines, class
// masteries) and the validators that enforce the subclassing build rules. It is
// kept separate from team.go so the persistence layer stays focused on storage.
//
// These are the canonical stored values; the UI renders friendlier labels and
// mirrors the same sets in frontend/js/data.js. Empty string ("") means "unset".

var (
	// ValidRoles are the allowed player role values for a trial team.
	//
	// A 12-person ESO trial is typically built from tanks, healers, and a mix
	// of pure-damage and support-oriented damage dealers, so we model four
	// roles: tank, healer, dps, and support_dps.
	ValidRoles = map[string]bool{
		"":            true,
		"tank":        true,
		"healer":      true,
		"dps":         true,
		"support_dps": true,
	}

	// ValidClasses are the current playable ESO classes.
	ValidClasses = map[string]bool{
		"":             true,
		"arcanist":     true,
		"dragonknight": true,
		"necromancer":  true,
		"nightblade":   true,
		"sorcerer":     true,
		"templar":      true,
		"warden":       true,
	}

	// ValidRaces are the playable ESO races. Race is a per-player attribute that
	// feeds the crit-damage calculator (e.g. the Khajiit "Feline Ambush"
	// passive). "" means "unset".
	ValidRaces = map[string]bool{
		"":         true,
		"altmer":   true,
		"argonian": true,
		"bosmer":   true,
		"breton":   true,
		"dunmer":   true,
		"imperial": true,
		"khajiit":  true,
		"nord":     true,
		"orc":      true,
		"redguard": true,
	}

	// ValidDays are the allowed schedule_days values (lowercase weekday keys).
	ValidDays = map[string]bool{
		"mon": true,
		"tue": true,
		"wed": true,
		"thu": true,
		"fri": true,
		"sat": true,
		"sun": true,
	}

	// ValidSkillLines are the 21 ESO class skill lines (3 per class). A
	// subclassed player may slot any of these in each of their 3 build slots.
	// "" means "unset".
	ValidSkillLines = map[string]bool{
		"": true,
		// Arcanist
		"herald_of_the_tome":   true,
		"soldier_of_apocrypha": true,
		"curative_runeforms":   true,
		// Dragonknight
		"ardent_flame":   true,
		"draconic_power": true,
		"earthen_heart":  true,
		// Necromancer
		"grave_lord":   true,
		"bone_tyrant":  true,
		"living_death": true,
		// Nightblade
		"assassination": true,
		"shadow":        true,
		"siphoning":     true,
		// Sorcerer
		"dark_magic":        true,
		"daedric_summoning": true,
		"storm_calling":     true,
		// Templar
		"aedric_spear":    true,
		"dawns_wrath":     true,
		"restoring_light": true,
		// Warden
		"animal_companions": true,
		"green_balance":     true,
		"winters_embrace":   true,
	}

	// MasteriesByClass maps each ESO class to its 5 class masteries. A
	// non-subclassed player may pick up to 2 masteries from their own class.
	MasteriesByClass = map[string]map[string]bool{
		"arcanist": {
			"abyssal_emergence": true,
			"fate_realigned":    true,
			"unbound_potential": true,
			"erudites_rigor":    true,
			"ink_scribes_verve": true,
		},
		"dragonknight": {
			"lead_from_the_front": true,
			"resolute_defense":    true,
			"wildfire_embers":     true,
			"booming_voice":       true,
			"inexorable_descent":  true,
		},
		"necromancer": {
			"nothing_wasted":     true,
			"malevolent_promise": true,
			"cycle_unending":     true,
			"pound_of_flesh":     true,
			"veils_forfeit":      true,
		},
		"nightblade": {
			"nocturnal_inspiration":   true,
			"an_eye_for_exploitation": true,
			"above_and_beyond":        true,
			"cutthroats_focus":        true,
			"share_the_spoils":        true,
		},
		"sorcerer": {
			"conservation_of_energy": true,
			"font_of_power":          true,
			"static_reverberation":   true,
			"calculated_defense":     true,
			"sphere_of_influence":    true,
		},
		"templar": {
			"bastion_of_light":      true,
			"devout_guardian":       true,
			"bright_harbinger":      true,
			"judgments_brand":       true,
			"steadfast_candescence": true,
		},
		"warden": {
			"tundras_maw":        true,
			"wild_adaptation":    true,
			"glacial_obstinance": true,
			"green_keepers_hide": true,
			"bountiful_harvest":  true,
		},
	}
)

// SkillLineClass maps each skill line value to the class it belongs to. Used to
// enforce subclassing build rules.
var SkillLineClass = map[string]string{
	"herald_of_the_tome":   "arcanist",
	"soldier_of_apocrypha": "arcanist",
	"curative_runeforms":   "arcanist",
	"ardent_flame":         "dragonknight",
	"draconic_power":       "dragonknight",
	"earthen_heart":        "dragonknight",
	"grave_lord":           "necromancer",
	"bone_tyrant":          "necromancer",
	"living_death":         "necromancer",
	"assassination":        "nightblade",
	"shadow":               "nightblade",
	"siphoning":            "nightblade",
	"dark_magic":           "sorcerer",
	"daedric_summoning":    "sorcerer",
	"storm_calling":        "sorcerer",
	"aedric_spear":         "templar",
	"dawns_wrath":          "templar",
	"restoring_light":      "templar",
	"animal_companions":    "warden",
	"green_balance":        "warden",
	"winters_embrace":      "warden",
}

// ValidSkillLine reports whether v is a known skill line value ("" allowed).
func ValidSkillLine(v string) bool {
	return ValidSkillLines[v]
}

// ValidRace reports whether r is a known race value ("" allowed).
func ValidRace(r string) bool {
	return ValidRaces[r]
}

// ValidateSkillLines enforces the subclassing build rules for a player's chosen
// skill lines (empty entries are ignored):
//   - all selected skill lines must be unique;
//   - if class is set, at least one selected line must belong to that class;
//   - if class is set, at most one selected line may come from any single class
//     other than the player's class.
//
// The class checks are skipped when class is "" (unset).
func ValidateSkillLines(class string, lines []string) error {
	seen := map[string]bool{}
	classCounts := map[string]int{}
	for _, l := range lines {
		if l == "" {
			continue
		}
		if seen[l] {
			return errors.New("skill lines must be unique")
		}
		seen[l] = true
		classCounts[SkillLineClass[l]]++
	}

	if class == "" {
		return nil
	}
	// Only require a class skill line once at least one line has been chosen, so
	// a fully-empty subclass build is still allowed.
	if len(seen) > 0 && classCounts[class] < 1 {
		return errors.New("at least one skill line must be from the player's class")
	}
	for c, n := range classCounts {
		if c != class && n > 1 {
			return errors.New("cannot have more than one skill line from another class")
		}
	}
	return nil
}

// ValidMastery reports whether mastery m is valid for the given class. "" is
// always allowed; a non-empty mastery must belong to a non-empty, known class.
func ValidMastery(class, m string) bool {
	if m == "" {
		return true
	}
	set, ok := MasteriesByClass[class]
	if !ok {
		return false
	}
	return set[m]
}
