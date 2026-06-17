package esoref

// Types backing the code-generated crit/penetration coverage tables in
// data_gen.go (CritGroupSources, PenGroupSources, PenExtraSources + the Crit*/
// Pen* constants). They mirror the JS source tables in frontend/js/data.js so
// the Discord bot can compute the team's "self required" pen and crit the same
// way the web UI does. See internal/discordfmt for the computation.

// ClassPassive is a class/skill-line pair: a buff a player has either as a base
// (non-subclassed) member of Class, or as a subclassed player who slotted Line.
type ClassPassive struct {
	Class string
	Line  string
}

// DetectMap describes the ways a source can be detected on a player. Any single
// matching category/key means the player provides the source. Empty slices are
// ignored.
type DetectMap struct {
	Gear         []string
	Skills       []string
	Potions      []string
	Masteries    []string
	SkillLines   []string
	CP           []string
	Classes      []string
	Race         []string
	Mundus       []string
	Scribed      []string
	ClassPassive []ClassPassive
}

// CritGroupSource is a team-wide Critical Damage source (raid buff or boss
// debuff). PerElement sources (Elemental Catalyst) scale Pct by the wearer's
// applied elemental damage-type count instead of using Pct directly.
type CritGroupSource struct {
	Value      string
	Label      string
	Pct        int
	PerElement bool
	Detect     DetectMap
}

// PenGroupSource is a team-wide penetration source. PerWeaponDamage sources
// (Anthelmir's Construct) scale off the wearer's higher Weapon/Spell Damage
// instead of using Pen directly.
type PenGroupSource struct {
	Value           string
	Label           string
	Pen             int
	PerWeaponDamage bool
	Detect          DetectMap
}

// PenExtraSource is a flat penetration source from the free-form pen_extra
// bucket. Bucket is "group" (counts once for the team) or "self" (per player).
// MaxStack > 1 means a self source may be added multiple times.
type PenExtraSource struct {
	Value    string
	Label    string
	Pen      int
	Bucket   string
	MaxStack int
}

// Buff is one tracked group buff. SelfBuff marks a personal Major/Minor buff a
// player can maintain for themselves; Detect lists the group-wide sources that
// cover it for the whole team. The Discord bot lists self-buffs whose Detect
// matches no team member under a "Self Buffs" field.
type Buff struct {
	Value    string
	Label    string
	SelfBuff bool
	Detect   DetectMap
}
