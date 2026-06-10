package models

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

// TeamSize is the fixed number of player slots on every team.
const TeamSize = 12

// ErrTeamNotFound is returned when a team lookup matches nothing the caller may
// access.
var ErrTeamNotFound = errors.New("team not found")

// Player roles and ESO classes. These are the canonical stored values; the UI
// renders friendlier labels. Empty string ("") means "unset".
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

	// ValidShareRoles are the roles a team can be shared with. "owner" is
	// excluded because it is assigned only at team creation.
	ValidShareRoles = map[string]bool{
		"viewer": true,
		"editor": true,
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
			"abyssal_pact":          true,
			"mind_over_matter":      true,
			"manifest_destiny":      true,
			"fleshborne_fate":       true,
			"self_perpetuated_fate": true,
		},
		"dragonknight": {
			"booming_voice":      true,
			"immovable_mountain": true,
			"unstoppable_force":  true,
			"rousing_roar":       true,
			"recursive_flame":    true,
		},
		"necromancer": {
			"cycle_of_death":    true,
			"at_the_precipice":  true,
			"lord_of_the_cycle": true,
			"pound_of_flesh":    true,
			"nothing_wasted":    true,
		},
		"nightblade": {
			"critical_motivation": true,
			"evasive_trance":      true,
			"detect_weakness":     true,
			"share_the_spoils":    true,
			"above_and_beyond":    true,
		},
		"sorcerer": {
			"conservation_of_energy": true,
			"efficient_defense":      true,
			"implosion":              true,
			"font_of_power":          true,
			"parallel_protection":    true,
		},
		"templar": {
			"hold_the_line":         true,
			"missionary_of_light":   true,
			"sacred_anchor":         true,
			"illuminary_of_bravery": true,
			"in_radiance_judgement": true,
		},
		"warden": {
			"hypothermia":     true,
			"wild_adaptation": true,
			"thick_hide":      true,
			"one_with_winter": true,
			"natures_bounty":  true,
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

// Team member role constants.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// defaultPlayerRole returns the role a freshly created slot starts with. New
// teams default to a standard ESO trial composition: 2 tanks, 2 healers, and
// 8 DPS (slots 1–2 tank, 3–4 healer, 5–12 dps).
func defaultPlayerRole(slot int) string {
	switch {
	case slot <= 2:
		return "tank"
	case slot <= 4:
		return "healer"
	default:
		return "dps"
	}
}

// Player is a single slot on a team's 12-person roster.
//
// A player either runs a subclassed build (three class skill lines) or a
// standard build (two class masteries from their selected class). The two sets
// are mutually exclusive: when Subclassed is true the masteries are blank, and
// when it is false the skill lines are blank.
type Player struct {
	ID            int64  `json:"id"`
	Slot          int    `json:"slot"`
	Name          string `json:"name"`
	DiscordHandle string `json:"discord_handle"`
	Role          string `json:"role"`
	Class         string `json:"class"`
	Subclassed    bool   `json:"subclassed"`
	SkillLine1    string `json:"skill_line_1"`
	SkillLine2    string `json:"skill_line_2"`
	SkillLine3    string `json:"skill_line_3"`
	Mastery1      string `json:"mastery_1"`
	Mastery2      string `json:"mastery_2"`
}

// TeamMember is a user with access to a team. Role is "owner" or "member".
type TeamMember struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Team is a named, shareable roster of players with an optional trial schedule.
type Team struct {
	ID               int64        `json:"id"`
	Name             string       `json:"name"`
	OwnerID          int64        `json:"owner_id"`
	ScheduleDays     []string     `json:"schedule_days"`
	ScheduleTime     string       `json:"schedule_time"`
	ScheduleTimezone string       `json:"schedule_timezone"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Players          []Player     `json:"players,omitempty"`
	Members          []TeamMember `json:"members,omitempty"`
}

// TeamStore provides data access for teams, their members, and players.
type TeamStore struct {
	pool *pgxpool.Pool
}

// NewTeamStore constructs a TeamStore backed by the given pool.
func NewTeamStore(pool *pgxpool.Pool) *TeamStore {
	return &TeamStore{pool: pool}
}

// Create inserts a new team owned by ownerID, records the owner as a member,
// and pre-creates the 12 empty player slots — all in a single transaction.
func (s *TeamStore) Create(ctx context.Context, ownerID int64, name string) (*Team, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	team := &Team{}
	const insertTeam = `
		INSERT INTO teams (name, owner_id)
		VALUES ($1, $2)
		RETURNING id, name, owner_id, schedule_days, schedule_time, schedule_timezone, created_at, updated_at`
	if err := tx.QueryRow(ctx, insertTeam, name, ownerID).Scan(
		&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.ScheduleTimezone, &team.CreatedAt, &team.UpdatedAt,
	); err != nil {
		return nil, err
	}

	const insertOwner = `
		INSERT INTO team_members (team_id, user_id, role)
		VALUES ($1, $2, 'owner')`
	if _, err := tx.Exec(ctx, insertOwner, team.ID, ownerID); err != nil {
		return nil, err
	}

	const insertSlot = `INSERT INTO players (team_id, slot, role) VALUES ($1, $2, $3)`
	for slot := 1; slot <= TeamSize; slot++ {
		if _, err := tx.Exec(ctx, insertSlot, team.ID, slot, defaultPlayerRole(slot)); err != nil {
			return nil, err
		}
	}

	// Every team starts with one "Default" encounter (with its 12 loadouts).
	if err := createDefaultEncounterTx(ctx, tx, team.ID); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, team.ID)
}

// ListForUser returns every team the user owns or has been granted access to,
// most recently updated first. Players and members are not populated here.
func (s *TeamStore) ListForUser(ctx context.Context, userID int64) ([]Team, error) {
	const q = `
		SELECT t.id, t.name, t.owner_id, t.schedule_days, t.schedule_time, t.schedule_timezone, t.created_at, t.updated_at
		FROM teams t
		JOIN team_members m ON m.team_id = t.id
		WHERE m.user_id = $1
		ORDER BY t.updated_at DESC`

	rows, err := s.pool.Query(ctx, q, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &t.ScheduleDays, &t.ScheduleTime, &t.ScheduleTimezone, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}

// Get returns a single team with its players (ordered by slot) and members.
func (s *TeamStore) Get(ctx context.Context, teamID int64) (*Team, error) {
	team := &Team{}
	const teamQ = `
		SELECT id, name, owner_id, schedule_days, schedule_time, schedule_timezone, created_at, updated_at
		FROM teams WHERE id = $1`
	err := s.pool.QueryRow(ctx, teamQ, teamID).Scan(
		&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.ScheduleTimezone, &team.CreatedAt, &team.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTeamNotFound
	}
	if err != nil {
		return nil, err
	}

	const playersQ = `
		SELECT id, slot, name, discord_handle, role, class,
		       subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2
		FROM players WHERE team_id = $1 ORDER BY slot`
	pRows, err := s.pool.Query(ctx, playersQ, teamID)
	if err != nil {
		return nil, err
	}
	defer pRows.Close()
	for pRows.Next() {
		var p Player
		if err := pRows.Scan(
			&p.ID, &p.Slot, &p.Name, &p.DiscordHandle, &p.Role, &p.Class,
			&p.Subclassed, &p.SkillLine1, &p.SkillLine2, &p.SkillLine3, &p.Mastery1, &p.Mastery2,
		); err != nil {
			return nil, err
		}
		team.Players = append(team.Players, p)
	}
	if err := pRows.Err(); err != nil {
		return nil, err
	}

	const membersQ = `
		SELECT m.user_id, u.username, m.role
		FROM team_members m
		JOIN users u ON u.id = m.user_id
		WHERE m.team_id = $1
		ORDER BY (m.role = 'owner') DESC, u.username`
	mRows, err := s.pool.Query(ctx, membersQ, teamID)
	if err != nil {
		return nil, err
	}
	defer mRows.Close()
	for mRows.Next() {
		var m TeamMember
		if err := mRows.Scan(&m.UserID, &m.Username, &m.Role); err != nil {
			return nil, err
		}
		team.Members = append(team.Members, m)
	}
	return team, mRows.Err()
}

// Access returns the caller's role on the team ("owner", "editor", or
// "viewer"). If the team is inaccessible to the user, found is false.
func (s *TeamStore) Access(ctx context.Context, teamID, userID int64) (found bool, role string, err error) {
	const q = `SELECT role FROM team_members WHERE team_id = $1 AND user_id = $2`
	err = s.pool.QueryRow(ctx, q, teamID, userID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return true, role, nil
}

// Save updates a team's name and trial schedule (days, time, timezone), and
// (when players is non-nil) the roster, all within a single transaction. Each
// player in players must have a valid Slot (1..TeamSize); slots not present are
// left unchanged.
func (s *TeamStore) Save(ctx context.Context, teamID int64, name string, days []string, scheduleTime, scheduleTimezone string, players []Player) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const updateTeam = `
		UPDATE teams
		SET name = $1, schedule_days = $2, schedule_time = $3, schedule_timezone = $4
		WHERE id = $5`
	if _, err := tx.Exec(ctx, updateTeam, name, days, scheduleTime, scheduleTimezone, teamID); err != nil {
		return err
	}

	const updatePlayer = `
		UPDATE players
		SET name = $1, discord_handle = $2, role = $3, class = $4,
		    subclassed = $5, skill_line_1 = $6, skill_line_2 = $7, skill_line_3 = $8,
		    mastery_1 = $9, mastery_2 = $10
		WHERE team_id = $11 AND slot = $12`
	for _, p := range players {
		if _, err := tx.Exec(ctx, updatePlayer,
			p.Name, p.DiscordHandle, p.Role, p.Class,
			p.Subclassed, p.SkillLine1, p.SkillLine2, p.SkillLine3, p.Mastery1, p.Mastery2,
			teamID, p.Slot,
		); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// Delete removes a team and (via cascade) its members and players.
func (s *TeamStore) Delete(ctx context.Context, teamID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM teams WHERE id = $1`, teamID)
	return err
}

// AddMember grants a user access to a team at the given role ("viewer" or
// "editor"). It is idempotent and acts as an upsert: re-sharing with an existing
// member updates their role. The owner's role is never changed.
func (s *TeamStore) AddMember(ctx context.Context, teamID, userID int64, role string) error {
	const q = `
		INSERT INTO team_members (team_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (team_id, user_id)
		DO UPDATE SET role = EXCLUDED.role
		WHERE team_members.role <> 'owner'`
	_, err := s.pool.Exec(ctx, q, teamID, userID, role)
	return err
}

// RemoveMember revokes a non-owner user's access to a team.
func (s *TeamStore) RemoveMember(ctx context.Context, teamID, userID int64) error {
	const q = `DELETE FROM team_members WHERE team_id = $1 AND user_id = $2 AND role <> 'owner'`
	_, err := s.pool.Exec(ctx, q, teamID, userID)
	return err
}
