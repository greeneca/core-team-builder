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
	WaitlistEnabled bool         `json:"waitlist_enabled"`
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
	Players         []Player     `json:"players,omitempty"`
	Members         []TeamMember `json:"members,omitempty"`
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
			INSERT INTO teams (name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled)
			SELECT $1, $2, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled
			FROM teams WHERE id = $3
			RETURNING id, name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, created_at, updated_at`
		if err := tx.QueryRow(ctx, insertTeamCopy, name, ownerID, copyFromTeamID).Scan(
			&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.EncountersEnabled, &team.PostFooter, &team.DMFooter, &team.SignupPost, &team.AutoSharePoolViewers, &team.PreMade, &team.PremadePost, &team.SimpleSignup, &team.WaitlistEnabled, &team.CreatedAt, &team.UpdatedAt,
		); err != nil {
			return nil, err
		}
	} else {
		const insertTeam = `
			INSERT INTO teams (name, owner_id)
			VALUES ($1, $2)
			RETURNING id, name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, created_at, updated_at`
		if err := tx.QueryRow(ctx, insertTeam, name, ownerID).Scan(
			&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.EncountersEnabled, &team.PostFooter, &team.DMFooter, &team.SignupPost, &team.AutoSharePoolViewers, &team.PreMade, &team.PremadePost, &team.SimpleSignup, &team.WaitlistEnabled, &team.CreatedAt, &team.UpdatedAt,
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
			                     subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2)
			SELECT $1, slot, name, discord_handle, role, class, race,
			       subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2
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
		SELECT t.id, t.name, t.owner_id, t.schedule_days, t.schedule_time, t.encounters_enabled, t.post_footer, t.dm_footer, t.signup_post, t.auto_share_pool_viewers, t.pre_made, t.premade_post, t.simple_signup, t.waitlist_enabled, t.created_at, t.updated_at
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
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &t.ScheduleDays, &t.ScheduleTime, &t.EncountersEnabled, &t.PostFooter, &t.DMFooter, &t.SignupPost, &t.AutoSharePoolViewers, &t.PreMade, &t.PremadePost, &t.SimpleSignup, &t.WaitlistEnabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
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
		SELECT id, name, owner_id, schedule_days, schedule_time, encounters_enabled, post_footer, dm_footer, signup_post, auto_share_pool_viewers, pre_made, premade_post, simple_signup, waitlist_enabled, created_at, updated_at
		FROM teams WHERE id = $1`
	err := s.pool.QueryRow(ctx, teamQ, teamID).Scan(
		&team.ID, &team.Name, &team.OwnerID, &team.ScheduleDays, &team.ScheduleTime, &team.EncountersEnabled, &team.PostFooter, &team.DMFooter, &team.SignupPost, &team.AutoSharePoolViewers, &team.PreMade, &team.PremadePost, &team.SimpleSignup, &team.WaitlistEnabled, &team.CreatedAt, &team.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTeamNotFound
	}
	if err != nil {
		return nil, err
	}

	const playersQ = `
		SELECT id, slot, name, discord_handle, role, class, race,
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
			&p.ID, &p.Slot, &p.Name, &p.DiscordHandle, &p.Role, &p.Class, &p.Race,
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

// Save updates a team's name, trial schedule (days and the UTC time), the
// encounters-enabled flag, the bot footers, the signup post, the auto-share flag,
// the pre-made flag and its post body, the simple-signup flag, the waitlist flag,
// and (when players is non-nil) the roster, all within a single transaction. Each
// player in players must have a valid Slot (1..TeamSize); slots not present are
// left unchanged.
func (s *TeamStore) Save(ctx context.Context, teamID int64, name string, days []string, scheduleTime string, encountersEnabled bool, postFooter string, dmFooter string, signupPost string, autoSharePoolViewers bool, preMade bool, premadePost string, simpleSignup bool, waitlistEnabled bool, players []Player) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const updateTeam = `
		UPDATE teams
		SET name = $1, schedule_days = $2, schedule_time = $3, encounters_enabled = $4, post_footer = $5, dm_footer = $6, signup_post = $7, auto_share_pool_viewers = $8, pre_made = $9, premade_post = $10, simple_signup = $11, waitlist_enabled = $12
		WHERE id = $13`
	if _, err := tx.Exec(ctx, updateTeam, name, days, scheduleTime, encountersEnabled, postFooter, dmFooter, signupPost, autoSharePoolViewers, preMade, premadePost, simpleSignup, waitlistEnabled, teamID); err != nil {
		return err
	}

	const updatePlayer = `
		UPDATE players
		SET name = $1, discord_handle = $2, role = $3, class = $4, race = $5,
		    subclassed = $6, skill_line_1 = $7, skill_line_2 = $8, skill_line_3 = $9,
		    mastery_1 = $10, mastery_2 = $11
		WHERE team_id = $12 AND slot = $13`
	for _, p := range players {
		if _, err := tx.Exec(ctx, updatePlayer,
			p.Name, p.DiscordHandle, p.Role, p.Class, p.Race,
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
		SELECT t.id, t.name, t.owner_id, t.schedule_days, t.schedule_time, t.encounters_enabled, t.post_footer, t.dm_footer, t.signup_post, t.auto_share_pool_viewers, t.pre_made, t.premade_post, t.simple_signup, t.waitlist_enabled, t.created_at, t.updated_at
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
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerID, &t.ScheduleDays, &t.ScheduleTime, &t.EncountersEnabled, &t.PostFooter, &t.DMFooter, &t.SignupPost, &t.AutoSharePoolViewers, &t.PreMade, &t.PremadePost, &t.SimpleSignup, &t.WaitlistEnabled, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		teams = append(teams, t)
	}
	return teams, rows.Err()
}
