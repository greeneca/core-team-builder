// Package esoref exposes human-readable labels and abbreviations for the ESO
// reference data stored as keys in the database (gear sets, skills, masteries,
// potions, etc.). The label/abbreviation tables are code-generated from the
// frontend's single-source data files into data_gen.go; see
// tools/gen-esoref/gen.js. Unknown keys fall back to the raw key, mirroring the
// frontend lookup helpers.
package esoref

import "strings"

//go:generate node ../../../tools/gen-esoref/gen.js

// lookup returns the label for key in m, falling back to the raw key when the
// key is unknown (matching the frontend's *Label() helpers).
func lookup(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return key
}

// GearLabel returns the display name for a gear-set key.
func GearLabel(key string) string { return lookup(gearLabels, key) }

// SkillLabel returns the display name for a skill key.
func SkillLabel(key string) string { return lookup(skillLabels, key) }

// MasteryLabel returns the display name for a class-mastery key.
func MasteryLabel(key string) string { return lookup(masteryLabels, key) }

// SkillLineLabel returns the display name for a skill-line key.
func SkillLineLabel(key string) string { return lookup(skillLineLabels, key) }

// PotionLabel returns the display name for a potion key.
func PotionLabel(key string) string { return lookup(potionLabels, key) }

// MundusLabel returns the display name for a mundus-stone key.
func MundusLabel(key string) string { return lookup(mundusLabels, key) }

// CPBlueLabel returns the display name for a blue (Warfare) CP star key.
func CPBlueLabel(key string) string { return lookup(cpBlueLabels, key) }

// CritDmgLabel returns the display name for a crit-damage source key.
func CritDmgLabel(key string) string { return lookup(critDmgLabels, key) }

// PenExtraLabel returns the display name for an extra-penetration source key.
func PenExtraLabel(key string) string { return lookup(penExtraLabels, key) }

// RoleLabel returns the display name for a role key.
func RoleLabel(key string) string { return lookup(roleLabels, key) }

// ClassLabel returns the display name for a class key, or "—" when unset.
func ClassLabel(key string) string {
	if key == "" {
		return "—"
	}
	return lookup(classLabels, key)
}

// RaceLabel returns the display name for a race key, or "—" when unset.
func RaceLabel(key string) string {
	if key == "" {
		return "—"
	}
	return lookup(raceLabels, key)
}

// DayLabel returns the short display name for a weekday key (e.g. "mon"->"Mon").
func DayLabel(key string) string { return lookup(dayLabels, key) }

// GearAbbrev returns the curated abbreviation for a gear set, or an
// auto-generated acronym from its label as a fallback (e.g. "Pillar of Nirn" ->
// "PN"). Mirrors gearAbbrev() in frontend/js/data.js; used by the condensed
// Discord overview.
func GearAbbrev(key string) string {
	if a, ok := gearAbbrevs[key]; ok {
		return a
	}
	label := stripParens(GearLabel(key))
	var words []string
	for _, w := range strings.Fields(label) {
		switch strings.ToLower(w) {
		case "of", "the", "and":
			continue
		}
		words = append(words, w)
	}
	if len(words) <= 1 {
		if len(words) == 1 {
			return words[0]
		}
		return label
	}
	var b strings.Builder
	for _, w := range words {
		b.WriteString(strings.ToUpper(w[:1]))
	}
	return b.String()
}

// stripParens removes any "(...)" segments from a label, matching the JS
// /\(.*?\)/g replacement.
func stripParens(s string) string {
	for {
		open := strings.IndexByte(s, '(')
		if open < 0 {
			break
		}
		close := strings.IndexByte(s[open:], ')')
		if close < 0 {
			break
		}
		s = s[:open] + s[open+close+1:]
	}
	return strings.TrimSpace(s)
}
