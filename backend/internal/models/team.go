package models

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"

	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

// TeamSize is the fixed number of player slots on every team.
const TeamSize = 12

// TeamRole is one selectable roster role on a team: Key is the stable value
// stored on players.role; Label is its display name; Base is the color category
// (one of the keys in ValidRoleBases) that drives the roster's role color
// coding, so a custom role still gets a known color.
type TeamRole struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Base  string `json:"base"`
}

// ValidRoleBases are the color categories a custom roster role may map to. Each
// has a matching --role-* CSS token (tank=blue, healer=green, dps=red,
// support_dps=purple); the roster colors a slot by its role's Base, so any
// custom role still renders with one of these known colors.
var ValidRoleBases = map[string]bool{
	"tank":        true,
	"healer":      true,
	"dps":         true,
	"support_dps": true,
}

// DefaultRoleBase is the fallback color category for a role whose Base is empty
// or unrecognized (e.g. older saved data created before role bases existed).
const DefaultRoleBase = "dps"

// TeamRoles is a team's ordered set of roster roles, stored as JSONB. It
// implements sql.Scanner / driver.Valuer so it round-trips through the database
// as a JSON array of {key, label} objects.
type TeamRoles []TeamRole

// Scan decodes a JSONB column (text/bytes) into the role list.
func (r *TeamRoles) Scan(src any) error {
	if src == nil {
		*r = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return fmt.Errorf("models.TeamRoles: unsupported scan type %T", src)
	}
	if len(b) == 0 {
		*r = nil
		return nil
	}
	return json.Unmarshal(b, (*[]TeamRole)(r))
}

// Value encodes the role list as a JSON array for storage in a JSONB column.
func (r TeamRoles) Value() (driver.Value, error) {
	if len(r) == 0 {
		return []byte("[]"), nil
	}
	return json.Marshal([]TeamRole(r))
}

// DefaultTeamRoles returns the historical fixed roster roles used as the default
// for new teams and as a fallback when a team has none stored.
func DefaultTeamRoles() TeamRoles {
	return TeamRoles{
		{Key: "tank", Label: "Tank", Base: "tank"},
		{Key: "healer", Label: "Healer", Base: "healer"},
		{Key: "dps", Label: "DPS", Base: "dps"},
		{Key: "support_dps", Label: "Support DPS", Base: "support_dps"},
	}
}

// EffectiveRoles returns the team's roster roles, falling back to the default
// set when none are stored (older teams created before custom roles). The
// Discord bot uses this so its role labels and ordering follow the team's own
// customizable role set rather than a fixed global list.
func (t *Team) EffectiveRoles() TeamRoles {
	if len(t.Roles) > 0 {
		return t.Roles
	}
	return DefaultTeamRoles()
}

// RoleLabel returns the display label for a role key from the team's own role
// set, falling back to "Other" for an empty key or the raw key when it is not
// one of the team's roles (e.g. a player still on a since-removed role).
func (t *Team) RoleLabel(key string) string {
	for _, r := range t.EffectiveRoles() {
		if r.Key == key {
			if r.Label != "" {
				return r.Label
			}
			return r.Key
		}
	}
	if key == "" {
		return "Other"
	}
	return key
}

// RoleBase returns the color base category for a role key from the team's own
// role set, falling back to DefaultRoleBase when the role is unknown or has no
// base (e.g. a player on a since-removed role).
func (t *Team) RoleBase(key string) string {
	for _, r := range t.EffectiveRoles() {
		if r.Key == key {
			if r.Base != "" {
				return r.Base
			}
			return DefaultRoleBase
		}
	}
	return DefaultRoleBase
}

// roleBaseOrder is the display priority of each color base: tanks first, then
// healers, support DPS, and DPS. Bases not listed sort last. Used to order
// roster roles consistently across the Discord bot regardless of the team's
// stored role order.
var roleBaseOrder = map[string]int{
	"tank":        0,
	"healer":      1,
	"support_dps": 2,
	"dps":         3,
}

// roleBaseEmoji maps a color base to the emoji shown beside its roles in the
// Discord signup posts. Keyed by base so a custom role inherits the emoji of its
// color category.
var roleBaseEmoji = map[string]string{
	"tank":        "\U0001F6E1\uFE0F", // 🛡️
	"healer":      "\u2747\uFE0F",     // ❇️ :sparkle:
	"support_dps": "\u2692\uFE0F",     // ⚒️ :hammer_pick:
	"dps":         "\u2694\uFE0F",     // ⚔️
}

// RoleEmoji returns the emoji for a role key based on its color base, so every
// role (including custom ones) gets a type-appropriate icon in the Discord
// signup posts. Falls back to the DefaultRoleBase emoji for unknown roles.
func (t *Team) RoleEmoji(key string) string {
	if e, ok := roleBaseEmoji[t.RoleBase(key)]; ok {
		return e
	}
	return roleBaseEmoji[DefaultRoleBase]
}

// OrderedRoleKeys returns the team's role keys ordered by their color base
// (tank, then healer, support DPS, DPS, then anything else), followed by any
// extra keys not in the team's set (e.g. roles read off players or waitlist
// entries). Within a base, the team's defined order is preserved. Empty keys are
// dropped. The Discord bot uses this to render roles in a stable, base-grouped
// order while still showing any orphaned roles.
func (t *Team) OrderedRoleKeys(extra ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(t.EffectiveRoles())+len(extra))
	for _, r := range t.EffectiveRoles() {
		if r.Key == "" || seen[r.Key] {
			continue
		}
		seen[r.Key] = true
		out = append(out, r.Key)
	}
	for _, k := range extra {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	basePriority := func(key string) int {
		if p, ok := roleBaseOrder[t.RoleBase(key)]; ok {
			return p
		}
		return len(roleBaseOrder)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return basePriority(out[i]) < basePriority(out[j])
	})
	return out
}

// ErrTeamNotFound is returned when a team lookup matches nothing the caller may
// access.
var ErrTeamNotFound = errors.New("team not found")

// ErrVersionConflict is returned by a version-checked save when the caller's
// expected updated_at no longer matches the stored row — i.e. someone else
// changed it first. Handlers surface this as a 409 so the client can refetch
// and retry instead of silently clobbering the concurrent edit.
var ErrVersionConflict = errors.New("version conflict")

// ValidShareRoles are the roles a team can be shared with. "owner" is excluded
// because it is assigned only at team creation. (ESO game reference data and the
// player build validators live in eso.go.)
var ValidShareRoles = map[string]bool{
	"viewer": true,
	"editor": true,
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
	Race          string `json:"race"`
	Subclassed    bool   `json:"subclassed"`
	SkillLine1    string `json:"skill_line_1"`
	SkillLine2    string `json:"skill_line_2"`
	SkillLine3    string `json:"skill_line_3"`
	Mastery1      string `json:"mastery_1"`
	Mastery2      string `json:"mastery_2"`
	// Werewolf marks a slot as running a werewolf build. When true, the default
	// werewolf skills are kept in that slot's skills loadout across every
	// encounter (see WerewolfDefaultSkills and TeamStore.Save); when false, all
	// werewolf-line skills (WerewolfSkills) are removed from them.
	Werewolf bool `json:"werewolf"`
	// UpdatedAt is the row's last-modified timestamp; it doubles as the
	// optimistic-concurrency token for a per-slot save (see SavePlayer).
	UpdatedAt time.Time `json:"updated_at"`
}

// WerewolfDefaultSkills are the skills added to a slot's skills loadout when its
// werewolf flag is turned on. Keys mirror the Werewolf skill line in
// frontend/js/gear-skills.js.
var WerewolfDefaultSkills = []string{
	"feral_pounce",
	"hircines_rage",
	"ferocious_roar",
	"bloody_gnash",
	"bloodclaws",
	"werewolf_berserker",
}

// WerewolfSkills is the full set of Werewolf skill-line keys. Turning the
// werewolf flag off removes any of these from a slot's skills loadout. Mirrors
// the Werewolf group in frontend/js/gear-skills.js.
var WerewolfSkills = []string{
	"bloodclaws",
	"bloody_gnash",
	"brutal_pounce",
	"claw_fury",
	"deafening_roar",
	"feral_pounce",
	"ferocious_roar",
	"gnash",
	"hircines_bounty",
	"hircines_fortitude",
	"hircines_rage",
	"pack_leader",
	"pounce",
	"rending_claws",
	"rip_and_tear",
	"roar",
	"werewolf_berserker",
	"werewolf_transformation",
}

// TeamMember is a user with access to a team. Role is "owner" or "member".
type TeamMember struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

// Team is a named, shareable roster of players with an optional trial schedule.
type Team struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	OwnerID      int64    `json:"owner_id"`
	ScheduleDays []string `json:"schedule_days"`
	// ScheduleTime is the recurring trial time stored in UTC ("HH:MM", '' when
	// unset). The client converts it to/from each viewer's current timezone.
	ScheduleTime string `json:"schedule_time"`
	// EncountersEnabled controls whether the team uses multiple encounters. When
	// false the UI hides the encounters section and shows only the first one.
	EncountersEnabled bool `json:"encounters_enabled"`
	// PostFooter is a free-form footer the Discord bot appends to its /coreteam
	// post overview. Editable from the team detail page.
	PostFooter string `json:"post_footer"`
	// DMFooter is a free-form footer the Discord bot appends to the "Get My Build
	// Details" direct message. Editable from the team detail page.
	DMFooter string `json:"dm_footer"`
	// SignupPost is the free-form body the Discord bot posts with /coreteam
	// signup to recruit new members. Editable from the team detail page.
	SignupPost string `json:"signup_post"`
	// AutoSharePoolViewers, when true, automatically grants viewer access to the
	// app accounts of everyone in the team's member pool — current and future. A
	// pool member is shared with only once their Discord identity is tied to an
	// app account. Disabling it never revokes existing shares.
	AutoSharePoolViewers bool `json:"auto_share_pool_viewers"`
	// PreMade, when true, turns the team into a one-off "pre-made" trial run:
	// players claim individual slots via the Discord bot's /coreteam signup flow
	// instead of being a fixed recurring roster. The web UI hides the recurring
	// schedule, bot texts, per-player Discord handles, and the member pool.
	PreMade bool `json:"pre_made"`
	// PremadePost is the free-form body the bot prepends to a pre-made run
	// announcement. Only meaningful when PreMade is true.
	PremadePost string `json:"premade_post"`
	// SimpleSignup controls how players claim slots on a pre-made run (only
	// meaningful when PreMade is true). When false (default), signup is
	// "specific": players claim an exact slot and the post shows class/gear plus a
	// build-details dropdown. When true, signup is "simple": the post hides
	// class/gear and the details dropdown, players pick a role, and claiming takes
	// the first empty slot matching that role.
	SimpleSignup bool `json:"simple_signup"`
	// WaitlistEnabled, when true, lets players join a per-role waitlist on a
	// pre-made run; when a slot of that role frees up, the head of that role's
	// waitlist is auto-promoted into it. Only meaningful when PreMade is true.
	WaitlistEnabled bool `json:"waitlist_enabled"`
	// Roles is the team's customizable set of roster roles (key + display
	// label). The roster role picker reads from this; defaults to the historical
	// fixed set (see DefaultTeamRoles).
	Roles     TeamRoles    `json:"roles"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
	Players   []Player     `json:"players,omitempty"`
	Members   []TeamMember `json:"members,omitempty"`
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
//
// When copyFromTeamID is non-zero, the new team is seeded from that source team
// (which the caller must be allowed to access — enforced by the handler): its
// trial schedule, the full 12-player roster, and every encounter with its
// per-player gear/skill loadouts are copied. Sharing/membership is never copied;
// the new team is owned solely by ownerID. When zero, the team starts fresh with
// default roles and a single empty "Default" encounter.
func (s *TeamStore) Create(ctx context.Context, ownerID int64, name string, copyFromTeamID int64) (*Team, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	team := &Team{}
	if copyFromTeamID != 0 {
		// Copy the source team's schedule onto the new team.
		const insertTeamCopy = `
			INSERT INTO teams (name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, roles)
			SELECT $1, $2, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, roles
			FROM teams WHERE id = $3
			RETURNING id, name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, roles, created_at, updated_at`
		if err := tx.QueryRow(ctx, insertTeamCopy, name, ownerID, copyFromTeamID).Scan(
			&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.EncountersEnabled, &team.PostFooter, &team.DMFooter, &team.SignupPost, &team.AutoSharePoolViewers, &team.PreMade, &team.PremadePost, &team.SimpleSignup, &team.WaitlistEnabled, &team.Roles, &team.CreatedAt, &team.UpdatedAt,
		); err != nil {
			return nil, err
		}
	} else {
		const insertTeam = `
			INSERT INTO teams (name, owner_id)
			VALUES ($1, $2)
			RETURNING id, name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, roles, created_at, updated_at`
		if err := tx.QueryRow(ctx, insertTeam, name, ownerID).Scan(
			&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.EncountersEnabled, &team.PostFooter, &team.DMFooter, &team.SignupPost, &team.AutoSharePoolViewers, &team.PreMade, &team.PremadePost, &team.SimpleSignup, &team.WaitlistEnabled, &team.Roles, &team.CreatedAt, &team.UpdatedAt,
		); err != nil {
			return nil, err
		}
	}

	const insertOwner = `
		INSERT INTO team_members (team_id, user_id, role)
		VALUES ($1, $2, 'owner')`
	if _, err := tx.Exec(ctx, insertOwner, team.ID, ownerID); err != nil {
		return nil, err
	}

	if copyFromTeamID != 0 {
		// Copy the roster slot-for-slot, then all encounters + loadouts.
		const copyPlayers = `
			INSERT INTO players (team_id, slot, name, discord_handle, role, class, race,
			                     subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2, werewolf)
			SELECT $1, slot, name, discord_handle, role, class, race,
			       subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2, werewolf
			FROM players WHERE team_id = $2`
		if _, err := tx.Exec(ctx, copyPlayers, team.ID, copyFromTeamID); err != nil {
			return nil, err
		}
		if err := copyEncountersTx(ctx, tx, copyFromTeamID, team.ID); err != nil {
			return nil, err
		}
		if err := copyGroupingsTx(ctx, tx, copyFromTeamID, team.ID); err != nil {
			return nil, err
		}
	} else {
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
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, team.ID)
}

// CountOwned returns how many teams the given user owns. Used to enforce the
// per-owner team cap before creating another.
func (s *TeamStore) CountOwned(ctx context.Context, ownerID int64) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM teams WHERE owner_id = $1`, ownerID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ListForUser returns every team the user owns or has been granted access to,
// most recently updated first. Players and members are not populated here.
func (s *TeamStore) ListForUser(ctx context.Context, userID int64) ([]Team, error) {
	const q = `
		SELECT t.id, t.name, t.owner_id, t.schedule_days, t.schedule_time, t.encounters_enabled, t.post_footer, t.dm_footer, t.signup_post, t.auto_share_pool_viewers, t.pre_made, t.premade_post, t.simple_signup, t.waitlist_enabled, t.roles, t.created_at, t.updated_at
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
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &t.ScheduleDays, &t.ScheduleTime, &t.EncountersEnabled, &t.PostFooter, &t.DMFooter, &t.SignupPost, &t.AutoSharePoolViewers, &t.PreMade, &t.PremadePost, &t.SimpleSignup, &t.WaitlistEnabled, &t.Roles, &t.CreatedAt, &t.UpdatedAt); err != nil {
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
		SELECT id, name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, roles, created_at, updated_at
		FROM teams WHERE id = $1`
	err := s.pool.QueryRow(ctx, teamQ, teamID).Scan(
		&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.EncountersEnabled, &team.PostFooter, &team.DMFooter, &team.SignupPost, &team.AutoSharePoolViewers, &team.PreMade, &team.PremadePost, &team.SimpleSignup, &team.WaitlistEnabled, &team.Roles, &team.CreatedAt, &team.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTeamNotFound
	}
	if err != nil {
		return nil, err
	}

	const playersQ = `
		SELECT id, slot, name, discord_handle, role, class, race,
		       subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2, werewolf, updated_at
		FROM players WHERE team_id = $1 ORDER BY slot`
	pRows, err := s.pool.Query(ctx, playersQ, teamID)
	if err != nil {
		return nil, err
	}
	defer pRows.Close()
	for pRows.Next() {
		var p Player
		if err := pRows.Scan(
			&p.ID, &p.Slot, &p.Name, &p.DiscordHandle, &p.Role, &p.Class, &p.Race,
			&p.Subclassed, &p.SkillLine1, &p.SkillLine2, &p.SkillLine3, &p.Mastery1, &p.Mastery2, &p.Werewolf, &p.UpdatedAt,
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

// Save updates a team's name, trial schedule (days and the UTC time), the
// encounters-enabled flag, the bot footers, the signup post, the auto-share flag,
// the pre-made flag and its post body, the simple-signup flag, the waitlist flag,
// and (when players is non-nil) the roster, all within a single transaction. Each
// player in players must have a valid Slot (1..TeamSize); slots not present are
// left unchanged.
// expectedUpdatedAt enables optimistic concurrency: when non-zero, the team row
// is updated only if its current updated_at still matches, otherwise
// ErrVersionConflict is returned so a stale save doesn't clobber a concurrent
// edit. A zero value skips the check (used by callers that don't track a
// version, e.g. older clients).
func (s *TeamStore) Save(ctx context.Context, teamID int64, name string, days []string, scheduleTime string, encountersEnabled bool, postFooter string, dmFooter string, signupPost string, autoSharePoolViewers bool, preMade bool, premadePost string, simpleSignup bool, waitlistEnabled bool, roles TeamRoles, players []Player, expectedUpdatedAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if expectedUpdatedAt.IsZero() {
		const updateTeam = `
			UPDATE teams
			SET name = $1, schedule_days = $2, schedule_time = $3, encounters_enabled = $4, post_footer = $5, dm_footer = $6, signup_post = $7, auto_share_pool_viewers = $8, pre_made = $9, premade_post = $10, simple_signup = $11, waitlist_enabled = $12, roles = $13
			WHERE id = $14`
		if _, err := tx.Exec(ctx, updateTeam, name, days, scheduleTime, encountersEnabled, postFooter, dmFooter, signupPost, autoSharePoolViewers, preMade, premadePost, simpleSignup, waitlistEnabled, roles, teamID); err != nil {
			return err
		}
	} else {
		const updateTeamVer = `
			UPDATE teams
			SET name = $1, schedule_days = $2, schedule_time = $3, encounters_enabled = $4, post_footer = $5, dm_footer = $6, signup_post = $7, auto_share_pool_viewers = $8, pre_made = $9, premade_post = $10, simple_signup = $11, waitlist_enabled = $12, roles = $13
			WHERE id = $14 AND updated_at = $15`
		tag, err := tx.Exec(ctx, updateTeamVer, name, days, scheduleTime, encountersEnabled, postFooter, dmFooter, signupPost, autoSharePoolViewers, preMade, premadePost, simpleSignup, waitlistEnabled, roles, teamID, expectedUpdatedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrVersionConflict
		}
	}

	const updatePlayer = `
		UPDATE players
		SET name = $1, discord_handle = $2, role = $3, class = $4, race = $5,
		    subclassed = $6, skill_line_1 = $7, skill_line_2 = $8, skill_line_3 = $9,
		    mastery_1 = $10, mastery_2 = $11, werewolf = $12
		WHERE team_id = $13 AND slot = $14`
	for _, p := range players {
		if _, err := tx.Exec(ctx, updatePlayer,
			p.Name, p.DiscordHandle, p.Role, p.Class, p.Race,
			p.Subclassed, p.SkillLine1, p.SkillLine2, p.SkillLine3, p.Mastery1, p.Mastery2, p.Werewolf,
			teamID, p.Slot,
		); err != nil {
			return err
		}
		if err := reconcileWerewolfSkillsTx(ctx, tx, teamID, p.Slot, p.Werewolf); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

// reconcileWerewolfSkillsTx keeps a slot's skills loadout in sync with its
// werewolf flag across every one of the team's encounters. When on, it appends
// any missing WerewolfDefaultSkills (preserving existing order, no duplicates);
// when off, it strips every WerewolfSkills entry. Runs in the given transaction.
func reconcileWerewolfSkillsTx(ctx context.Context, tx pgx.Tx, teamID int64, slot int, werewolf bool) error {
	if werewolf {
		// Only touch rows actually missing a default skill. The trailing
		// `NOT (el.skills @> $1)` guard means a slot that already has every
		// default is left untouched, so we don't bump its updated_at (the
		// loadout's optimistic-concurrency token) on an unrelated player save —
		// otherwise a follow-up loadout save for the same slot (e.g. the "copy
		// player" flow) would spuriously 409 against its own just-stale token.
		const addWW = `
			UPDATE encounter_loadouts el
			SET skills = el.skills || COALESCE((
			    SELECT array_agg(k ORDER BY ord)
			    FROM unnest($1::text[]) WITH ORDINALITY AS u(k, ord)
			    WHERE NOT (k = ANY(el.skills))
			), '{}')
			FROM encounters e
			WHERE el.encounter_id = e.id AND e.team_id = $2 AND el.slot = $3
			  AND NOT (el.skills @> $1::text[])`
		_, err := tx.Exec(ctx, addWW, WerewolfDefaultSkills, teamID, slot)
		return err
	}
	// Only touch rows that still contain at least one werewolf skill (array
	// overlap). A slot with no werewolf skills is left untouched so its
	// updated_at token is preserved across an unrelated player save (see above).
	const removeWW = `
		UPDATE encounter_loadouts el
		SET skills = COALESCE((
		    SELECT array_agg(s ORDER BY ord)
		    FROM unnest(el.skills) WITH ORDINALITY AS u(s, ord)
		    WHERE NOT (s = ANY($1::text[]))
		), '{}')
		FROM encounters e
		WHERE el.encounter_id = e.id AND e.team_id = $2 AND el.slot = $3
		  AND el.skills && $1::text[]`
	_, err := tx.Exec(ctx, removeWW, WerewolfSkills, teamID, slot)
	return err
}

// SavePlayer updates a single roster slot and (re)reconciles its werewolf skills
// across the team's encounters, all in one transaction. It is the per-slot
// counterpart to Save's bulk roster update, used by the finer-grained autosave
// so two editors changing different slots don't overwrite each other.
//
// expectedUpdatedAt enables optimistic concurrency: when non-zero the row is
// updated only if its updated_at still matches, otherwise ErrVersionConflict is
// returned. A zero value skips the check. The refreshed player (with its new
// updated_at) is returned on success.
func (s *TeamStore) SavePlayer(ctx context.Context, teamID int64, p Player, expectedUpdatedAt time.Time) (*Player, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if expectedUpdatedAt.IsZero() {
		const updatePlayer = `
			UPDATE players
			SET name = $1, discord_handle = $2, role = $3, class = $4, race = $5,
			    subclassed = $6, skill_line_1 = $7, skill_line_2 = $8, skill_line_3 = $9,
			    mastery_1 = $10, mastery_2 = $11, werewolf = $12
			WHERE team_id = $13 AND slot = $14`
		tag, err := tx.Exec(ctx, updatePlayer,
			p.Name, p.DiscordHandle, p.Role, p.Class, p.Race,
			p.Subclassed, p.SkillLine1, p.SkillLine2, p.SkillLine3, p.Mastery1, p.Mastery2, p.Werewolf,
			teamID, p.Slot,
		)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrTeamNotFound
		}
	} else {
		const updatePlayerVer = `
			UPDATE players
			SET name = $1, discord_handle = $2, role = $3, class = $4, race = $5,
			    subclassed = $6, skill_line_1 = $7, skill_line_2 = $8, skill_line_3 = $9,
			    mastery_1 = $10, mastery_2 = $11, werewolf = $12
			WHERE team_id = $13 AND slot = $14 AND updated_at = $15`
		tag, err := tx.Exec(ctx, updatePlayerVer,
			p.Name, p.DiscordHandle, p.Role, p.Class, p.Race,
			p.Subclassed, p.SkillLine1, p.SkillLine2, p.SkillLine3, p.Mastery1, p.Mastery2, p.Werewolf,
			teamID, p.Slot, expectedUpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 0 {
			return nil, ErrVersionConflict
		}
	}

	if err := reconcileWerewolfSkillsTx(ctx, tx, teamID, p.Slot, p.Werewolf); err != nil {
		return nil, err
	}

	var out Player
	const reread = `
		SELECT id, slot, name, discord_handle, role, class, race,
		       subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2, werewolf, updated_at
		FROM players WHERE team_id = $1 AND slot = $2`
	if err := tx.QueryRow(ctx, reread, teamID, p.Slot).Scan(
		&out.ID, &out.Slot, &out.Name, &out.DiscordHandle, &out.Role, &out.Class, &out.Race,
		&out.Subclassed, &out.SkillLine1, &out.SkillLine2, &out.SkillLine3, &out.Mastery1, &out.Mastery2, &out.Werewolf, &out.UpdatedAt,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetRoles returns a team's stored roster roles, or the default set when none
// are stored. Used to validate a role on a per-slot save without loading the
// whole team.
func (s *TeamStore) GetRoles(ctx context.Context, teamID int64) (TeamRoles, error) {
	var roles TeamRoles
	err := s.pool.QueryRow(ctx, `SELECT roles FROM teams WHERE id = $1`, teamID).Scan(&roles)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTeamNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(roles) == 0 {
		return DefaultTeamRoles(), nil
	}
	return roles, nil
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

// AutoSharePoolEnabled reports whether the team has member-pool auto-sharing
// turned on. Returns false (no error) when the team doesn't exist.
func (s *TeamStore) AutoSharePoolEnabled(ctx context.Context, teamID int64) (bool, error) {
	var enabled bool
	err := s.pool.QueryRow(ctx, `SELECT auto_share_pool_viewers FROM teams WHERE id = $1`, teamID).Scan(&enabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled, nil
}

// SharePoolMembers grants viewer access to the app accounts of everyone in the
// team's member pool, but only when the team has auto-share enabled. A pool
// member counts only once their Discord identity is tied to an app account
// (users.discord_user_id), since sharing needs a real user row. Idempotent and
// safe to call repeatedly: ON CONFLICT DO NOTHING leaves existing roles (owner,
// editor, prior viewer) untouched, so it never downgrades anyone. It is a no-op
// when the flag is off, so callers may invoke it unconditionally.
func (s *TeamStore) SharePoolMembers(ctx context.Context, teamID int64) error {
	const q = `
		INSERT INTO team_members (team_id, user_id, role)
		SELECT trm.team_id, u.id, 'viewer'
		FROM team_roster_members trm
		JOIN teams t ON t.id = trm.team_id AND t.auto_share_pool_viewers = true
		JOIN users u ON u.discord_user_id = trm.discord_user_id
		WHERE trm.team_id = $1 AND trm.discord_user_id IS NOT NULL
		ON CONFLICT (team_id, user_id) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, teamID)
	return err
}

// ShareAutoTeamsForDiscord grants the given app user viewer access to every team
// that has auto-share enabled and lists their Discord identity in its member
// pool. Used when a user signs in / links via Discord so they immediately see
// the teams whose pools they belong to. Idempotent; ON CONFLICT DO NOTHING
// preserves any existing (owner/editor/viewer) role. A no-op when discordUserID
// is empty.
func (s *TeamStore) ShareAutoTeamsForDiscord(ctx context.Context, discordUserID string, userID int64) error {
	if discordUserID == "" {
		return nil
	}
	const q = `
		INSERT INTO team_members (team_id, user_id, role)
		SELECT trm.team_id, $2, 'viewer'
		FROM team_roster_members trm
		JOIN teams t ON t.id = trm.team_id AND t.auto_share_pool_viewers = true
		WHERE trm.discord_user_id = $1
		ON CONFLICT (team_id, user_id) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, discordUserID, userID)
	return err
}

// PublishTemplateToGuild makes a template (pre-made team) runnable by anyone in
// the given Discord guild, without sharing edit access to the team. Idempotent:
// re-publishing the same (team, guild) leaves the original publisher/timestamp
// untouched.
func (s *TeamStore) PublishTemplateToGuild(ctx context.Context, teamID int64, guildID string, publishedBy int64) error {
	const q = `
		INSERT INTO team_guild_templates (team_id, guild_id, published_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (team_id, guild_id) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, teamID, guildID, publishedBy)
	return err
}

// UnpublishTemplateFromGuild revokes a template's availability in a guild. It is
// idempotent (a no-op when no grant exists).
func (s *TeamStore) UnpublishTemplateFromGuild(ctx context.Context, teamID int64, guildID string) error {
	const q = `DELETE FROM team_guild_templates WHERE team_id = $1 AND guild_id = $2`
	_, err := s.pool.Exec(ctx, q, teamID, guildID)
	return err
}

// IsTemplatePublishedToGuild reports whether a template is published to a guild.
func (s *TeamStore) IsTemplatePublishedToGuild(ctx context.Context, teamID int64, guildID string) (bool, error) {
	const q = `SELECT 1 FROM team_guild_templates WHERE team_id = $1 AND guild_id = $2`
	var x int
	err := s.pool.QueryRow(ctx, q, teamID, guildID).Scan(&x)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ListPublishedTemplatesForGuild returns the pre-made teams published to the
// given guild, most-recently-updated first. Only teams still flagged pre_made
// are returned (a template that was un-flagged stops being runnable).
func (s *TeamStore) ListPublishedTemplatesForGuild(ctx context.Context, guildID string) ([]Team, error) {
	const q = `
		SELECT t.id, t.name, t.owner_id, t.schedule_days, t.schedule_time, t.encounters_enabled, t.post_footer, t.dm_footer, t.signup_post, t.auto_share_pool_viewers, t.pre_made, t.premade_post, t.simple_signup, t.waitlist_enabled, t.roles, t.created_at, t.updated_at
		FROM teams t
		JOIN team_guild_templates g ON g.team_id = t.id
		WHERE g.guild_id = $1 AND t.pre_made = true
		ORDER BY t.updated_at DESC`
	rows, err := s.pool.Query(ctx, q, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	teams := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &t.ScheduleDays, &t.ScheduleTime, &t.EncountersEnabled, &t.PostFooter, &t.DMFooter, &t.SignupPost, &t.AutoSharePoolViewers, &t.PreMade, &t.PremadePost, &t.SimpleSignup, &t.WaitlistEnabled, &t.Roles, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}
