package models

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrEncounterNotFound is returned when an encounter lookup matches nothing.
var ErrEncounterNotFound = errors.New("encounter not found")

// Encounter selection validation errors. These are surfaced to the client as
// 400s with a friendly message (see handlers).
var (
	// ErrEncounterNameInvalid means the name is not in the allow-list.
	ErrEncounterNameInvalid = errors.New("invalid encounter name")
	// ErrEncounterNameTaken means the team already has an encounter with that name.
	ErrEncounterNameTaken = errors.New("this encounter already exists for the team")
	// ErrEncounterTrialMismatch means the name belongs to a different trial than
	// the team's other encounters (only one trial is allowed per team).
	ErrEncounterTrialMismatch = errors.New("encounters must all be from the same trial")
)

// Loadout limits keep stored lists sane; the UI constrains choices to master
// data, but the backend still bounds free-form input defensively.
const (
	maxLoadoutItems  = 30
	maxLoadoutKeyLen = 100
)

// GeneralEncounterGroup is the group holding non-trial encounters (Default,
// Trash). General encounters may always coexist with a single trial's bosses.
const GeneralEncounterGroup = "General"

// EncounterNameGroup is a named set of encounter names: the "General" group or
// one ESO trial.
type EncounterNameGroup struct {
	Group string
	Names []string
}

// EncounterNameGroups is the ordered allow-list of encounter names, grouped by
// trial ("General" holds Default/Trash). This mirrors ENCOUNTER_NAME_GROUPS in
// the frontend (frontend/js/data.js) — keep the two in sync. Seed data; extend
// as trials/bosses are added.
var EncounterNameGroups = []EncounterNameGroup{
	{Group: GeneralEncounterGroup, Names: []string{"Default", "Trash"}},
	{Group: "Aetherian Archive", Names: []string{"Lightning Storm Atronach", "Foundation Stone Atronach", "Varlariel", "The Celestial Mage"}},
	{Group: "Hel Ra Citadel", Names: []string{"Ra Kotu", "Yokeda Kai and Yokeda Rok'dun", "The Celestial Warrior"}},
	{Group: "Sanctum Ophidia", Names: []string{"Possessed Mantikora", "Stonebreaker", "Ozara", "The Celestial Serpent"}},
	{Group: "Maw of Lorkhaj", Names: []string{"Zhaj'hassa the Forgotten", "The Twins", "Rakkhat"}},
	{Group: "Halls of Fabrication", Names: []string{"Hunter-Killer Fabricants", "Pinnacle Factotum", "Archcustodian", "Refabrication Committee", "The Assembly General"}},
	{Group: "Asylum Sanctorium", Names: []string{"Saint Llothis the Pious", "Saint Felms the Bold", "Saint Olms the Just"}},
	{Group: "Cloudrest", Names: []string{"Shade of Galenwe", "Shade of Siroria", "Shade of Relequen", "Z'Maja"}},
	{Group: "Sunspire", Names: []string{"Lokkestiiz", "Yolnahkriin", "Nahviintaas"}},
	{Group: "Kyne's Aegis", Names: []string{"Yandir the Butcher", "Captain Vrol", "Lord Falgravn"}},
	{Group: "Rockgrove", Names: []string{"Oaxiltso", "Flame-Herald Bahsei", "Xalvakka"}},
	{Group: "Dreadsail Reef", Names: []string{"Lylanar and Turlassil", "Reef Guardian", "Tideborn Taleria"}},
	{Group: "Sanity's Edge", Names: []string{"Exarchanic Yaseyla", "Archwizard Twelvane and Chimera", "Ansuul the Tormentor"}},
	{Group: "Lucent Citadel", Names: []string{"Count Ryelaz and Zilyesset", "Orphic Shattered Shard", "Xoryn"}},
	{Group: "Ossein Cage", Names: []string{"Shapers of Flesh", "Jynorah and Skorkhif", "Overfiend Kazpian"}},
}

// ValidEncounterNames is the flat allow-list for an encounter's name, derived
// from EncounterNameGroups. Stored as the display string.
var ValidEncounterNames = map[string]bool{}

// encounterTrialByName maps each valid encounter name to its group/trial.
var encounterTrialByName = map[string]string{}

func init() {
	for _, g := range EncounterNameGroups {
		for _, n := range g.Names {
			ValidEncounterNames[n] = true
			encounterTrialByName[n] = g.Group
		}
	}
}

// EncounterTrial returns the group/trial an encounter name belongs to
// (GeneralEncounterGroup for Default/Trash), or "" if the name is unknown.
func EncounterTrial(name string) string {
	return encounterTrialByName[name]
}

// ValidateEncounterSelection reports whether candidate is a valid name to add or
// rename to within a team whose other encounter names are `existing`:
//   - candidate must be a known encounter name;
//   - it must not duplicate one of `existing` (names are unique per team);
//   - all non-General encounters must belong to a single trial — candidate's
//     trial must match the team's already-chosen trial (General is always ok).
//
// For a rename, pass `existing` without the encounter being renamed.
func ValidateEncounterSelection(existing []string, candidate string) error {
	if !ValidEncounterNames[candidate] {
		return ErrEncounterNameInvalid
	}
	for _, n := range existing {
		if n == candidate {
			return ErrEncounterNameTaken
		}
	}
	candTrial := encounterTrialByName[candidate]
	if candTrial == GeneralEncounterGroup {
		return nil
	}
	for _, n := range existing {
		t := encounterTrialByName[n]
		if t == "" || t == GeneralEncounterGroup {
			continue
		}
		if t != candTrial {
			return ErrEncounterTrialMismatch
		}
	}
	return nil
}

// Loadout is a single player's gear, skills, potions, and crit-damage inputs
// for one encounter. CPBlue/CritDmg/Mundus and the armor counts feed the
// client-side crit-damage calculator; they are stored per encounter per slot.
type Loadout struct {
	Slot        int      `json:"slot"`
	Gear        []string `json:"gear"`
	Skills      []string `json:"skills"`
	Potions     []string `json:"potions"`
	CPBlue      []string `json:"cp_blue"`
	CritDmg     []string `json:"crit_dmg"`
	Mundus      string   `json:"mundus"`
	ArmorHeavy  int      `json:"armor_heavy"`
	ArmorMedium int      `json:"armor_medium"`
	ArmorLight  int      `json:"armor_light"`
	PenExtra    []string `json:"pen_extra"`
	// CatalystElements is how many distinct elemental damage types (1-3) the
	// Elemental Catalyst wearer applies; feeds the client-side crit calculator
	// (5% Critical Damage taken per element). Defaults to 3 (full bonus).
	CatalystElements int `json:"catalyst_elements"`
	// WeaponDamage is the player's higher of Weapon/Spell Damage; feeds the
	// penetration calculator for sets that scale off it (Anthelmir's Construct).
	WeaponDamage int `json:"weapon_damage"`
	// SplinteredSecretsSkills is how many Herald of the Tome abilities (0-5) are
	// slotted for the Arcanist "Splintered Secrets" passive; feeds the
	// penetration calculator (1240 Offensive Penetration each). Defaults to 2.
	SplinteredSecretsSkills int `json:"splintered_secrets_skills"`
	// ForceOfNatureStatus is how many negative status effects (0-5) are on the
	// enemy for the "Force of Nature" Warfare CP star; feeds the penetration
	// calculator (660 Offensive Penetration each). Defaults to 5 (full bonus).
	ForceOfNatureStatus int `json:"force_of_nature_status"`
}

// Encounter is a named fight within a team, with a per-player loadout list.
type Encounter struct {
	ID        int64     `json:"id"`
	TeamID    int64     `json:"team_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Loadouts  []Loadout `json:"loadouts,omitempty"`
}

// EncounterStore provides data access for encounters and their loadouts.
type EncounterStore struct {
	pool *pgxpool.Pool
}

// NewEncounterStore constructs an EncounterStore backed by the given pool.
func NewEncounterStore(pool *pgxpool.Pool) *EncounterStore {
	return &EncounterStore{pool: pool}
}

// createDefaultEncounterTx inserts a team's initial "Default" encounter and its
// 12 empty loadout slots within an existing transaction.
func createDefaultEncounterTx(ctx context.Context, tx pgx.Tx, teamID int64) error {
	var eid int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO encounters (team_id, name, position) VALUES ($1, 'Default', 0) RETURNING id`,
		teamID,
	).Scan(&eid); err != nil {
		return err
	}
	for slot := 1; slot <= TeamSize; slot++ {
		if _, err := tx.Exec(ctx,
			`INSERT INTO encounter_loadouts (encounter_id, slot) VALUES ($1, $2)`, eid, slot,
		); err != nil {
			return err
		}
	}
	return nil
}

// copyEncountersTx copies every encounter (and its per-player loadouts) from
// srcTeamID to dstTeamID within an existing transaction. Used when creating a
// team as a copy of another. Encounters keep their names and positions.
func copyEncountersTx(ctx context.Context, tx pgx.Tx, srcTeamID, dstTeamID int64) error {
	// Read all source encounters first; pgx allows only one active query per
	// transaction, so we must finish iterating before issuing the inserts below.
	rows, err := tx.Query(ctx,
		`SELECT id, name, position FROM encounters WHERE team_id = $1 ORDER BY position, id`,
		srcTeamID,
	)
	if err != nil {
		return err
	}
	type srcEncounter struct {
		id       int64
		name     string
		position int
	}
	var srcs []srcEncounter
	for rows.Next() {
		var e srcEncounter
		if err := rows.Scan(&e.id, &e.name, &e.position); err != nil {
			rows.Close()
			return err
		}
		srcs = append(srcs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, src := range srcs {
		var newID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO encounters (team_id, name, position) VALUES ($1, $2, $3) RETURNING id`,
			dstTeamID, src.name, src.position,
		).Scan(&newID); err != nil {
			return err
		}
		// Copy the 12 loadout rows (gear + skills + potions + crit inputs) slot-for-slot.
		if _, err := tx.Exec(ctx,
			`INSERT INTO encounter_loadouts (encounter_id, slot, gear, skills, potions, cp_blue, crit_dmg, mundus, armor_heavy, armor_medium, armor_light, pen_extra, catalyst_elements, weapon_damage, splintered_secrets_skills, force_of_nature_status)
			 SELECT $1, slot, gear, skills, potions, cp_blue, crit_dmg, mundus, armor_heavy, armor_medium, armor_light, pen_extra, catalyst_elements, weapon_damage, splintered_secrets_skills, force_of_nature_status FROM encounter_loadouts WHERE encounter_id = $2`,
			newID, src.id,
		); err != nil {
			return err
		}
	}
	return nil
}

// ListForTeam returns a team's encounters (without loadouts), ordered.
func (s *EncounterStore) ListForTeam(ctx context.Context, teamID int64) ([]Encounter, error) {
	const q = `
		SELECT id, team_id, name, position, created_at, updated_at
		FROM encounters
		WHERE team_id = $1
		ORDER BY position, id`
	rows, err := s.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	encounters := []Encounter{}
	for rows.Next() {
		var e Encounter
		if err := rows.Scan(&e.ID, &e.TeamID, &e.Name, &e.Position, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		encounters = append(encounters, e)
	}
	return encounters, rows.Err()
}

// Create inserts a new encounter (appended after existing ones) with 12 loadout
// slots, in a single transaction. When copyFromID is non-zero, each slot's
// gear/skills are copied from the source encounter (which must belong to the
// same team); otherwise the slots start empty.
func (s *EncounterStore) Create(ctx context.Context, teamID int64, name string, copyFromID int64) (*Encounter, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var position int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM encounters WHERE team_id = $1`, teamID,
	).Scan(&position); err != nil {
		return nil, err
	}

	e := &Encounter{}
	if err := tx.QueryRow(ctx,
		`INSERT INTO encounters (team_id, name, position) VALUES ($1, $2, $3)
		 RETURNING id, team_id, name, position, created_at, updated_at`,
		teamID, name, position,
	).Scan(&e.ID, &e.TeamID, &e.Name, &e.Position, &e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}

	for slot := 1; slot <= TeamSize; slot++ {
		if _, err := tx.Exec(ctx,
			`INSERT INTO encounter_loadouts (encounter_id, slot) VALUES ($1, $2)`, e.ID, slot,
		); err != nil {
			return nil, err
		}
	}

	if copyFromID != 0 {
		// Copy gear/skills/potions/crit inputs slot-for-slot from the source. The
		// join on encounters(team_id) ensures we only ever copy within the same team.
		if _, err := tx.Exec(ctx,
			`UPDATE encounter_loadouts dst
			 SET gear = src.gear, skills = src.skills, potions = src.potions,
			     cp_blue = src.cp_blue, crit_dmg = src.crit_dmg, mundus = src.mundus,
			     armor_heavy = src.armor_heavy, armor_medium = src.armor_medium, armor_light = src.armor_light,
			     pen_extra = src.pen_extra, catalyst_elements = src.catalyst_elements,
			     weapon_damage = src.weapon_damage,
			     splintered_secrets_skills = src.splintered_secrets_skills,
			     force_of_nature_status = src.force_of_nature_status
			 FROM encounter_loadouts src
			 JOIN encounters e ON e.id = src.encounter_id
			 WHERE dst.encounter_id = $1
			   AND src.encounter_id = $2
			   AND e.team_id = $3
			   AND dst.slot = src.slot`,
			e.ID, copyFromID, teamID,
		); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, e.ID)
}

// Get returns one encounter with its 12 loadouts (ordered by slot).
func (s *EncounterStore) Get(ctx context.Context, encounterID int64) (*Encounter, error) {
	e := &Encounter{}
	const q = `
		SELECT id, team_id, name, position, created_at, updated_at
		FROM encounters WHERE id = $1`
	err := s.pool.QueryRow(ctx, q, encounterID).Scan(
		&e.ID, &e.TeamID, &e.Name, &e.Position, &e.CreatedAt, &e.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrEncounterNotFound
	}
	if err != nil {
		return nil, err
	}

	const lq = `
		SELECT slot, gear, skills, potions, cp_blue, crit_dmg, mundus, armor_heavy, armor_medium, armor_light, pen_extra, catalyst_elements, weapon_damage, splintered_secrets_skills, force_of_nature_status
		FROM encounter_loadouts WHERE encounter_id = $1 ORDER BY slot`
	rows, err := s.pool.Query(ctx, lq, encounterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Loadout
		if err := rows.Scan(
			&l.Slot, &l.Gear, &l.Skills, &l.Potions,
			&l.CPBlue, &l.CritDmg, &l.Mundus, &l.ArmorHeavy, &l.ArmorMedium, &l.ArmorLight, &l.PenExtra, &l.CatalystElements, &l.WeaponDamage, &l.SplinteredSecretsSkills, &l.ForceOfNatureStatus,
		); err != nil {
			return nil, err
		}
		e.Loadouts = append(e.Loadouts, l)
	}
	return e, rows.Err()
}

// CountForTeam returns how many encounters a team has.
func (s *EncounterStore) CountForTeam(ctx context.Context, teamID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM encounters WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

// UpdateName renames an encounter.
func (s *EncounterStore) UpdateName(ctx context.Context, encounterID int64, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE encounters SET name = $1 WHERE id = $2`, name, encounterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrEncounterNotFound
	}
	return nil
}

// Delete removes an encounter and (via cascade) its loadouts.
func (s *EncounterStore) Delete(ctx context.Context, encounterID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM encounters WHERE id = $1`, encounterID)
	return err
}

// SaveLoadouts updates the gear/skills/potions and crit inputs for the given
// slots in one transaction.
func (s *EncounterStore) SaveLoadouts(ctx context.Context, encounterID int64, loadouts []Loadout) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const q = `
		UPDATE encounter_loadouts
		SET gear = $1, skills = $2, potions = $3, cp_blue = $4, crit_dmg = $5,
		    mundus = $6, armor_heavy = $7, armor_medium = $8, armor_light = $9, pen_extra = $10,
		    catalyst_elements = $11, weapon_damage = $12, splintered_secrets_skills = $13,
		    force_of_nature_status = $14
		WHERE encounter_id = $15 AND slot = $16`
	for _, l := range loadouts {
		if _, err := tx.Exec(ctx, q,
			l.Gear, l.Skills, l.Potions, l.CPBlue, l.CritDmg,
			l.Mundus, l.ArmorHeavy, l.ArmorMedium, l.ArmorLight, l.PenExtra,
			l.CatalystElements, l.WeaponDamage, l.SplinteredSecretsSkills,
			l.ForceOfNatureStatus,
			encounterID, l.Slot,
		); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// SanitizeLoadoutItems trims, drops empties, enforces length, and caps count.
// Returns the cleaned list or an error if a constraint is exceeded.
func SanitizeLoadoutItems(items []string) ([]string, error) {
	out := make([]string, 0, len(items))
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if len(it) > maxLoadoutKeyLen {
			return nil, errors.New("loadout item too long")
		}
		out = append(out, it)
	}
	if len(out) > maxLoadoutItems {
		return nil, errors.New("too many loadout items")
	}
	return out, nil
}
