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
)

// Team member role constants.
const (
	RoleOwner  = "owner"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

// Player is a single slot on a team's 12-person roster.
type Player struct {
	ID            int64  `json:"id"`
	Slot          int    `json:"slot"`
	Name          string `json:"name"`
	DiscordHandle string `json:"discord_handle"`
	Role          string `json:"role"`
	Class         string `json:"class"`
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

	const insertSlot = `INSERT INTO players (team_id, slot) VALUES ($1, $2)`
	for slot := 1; slot <= TeamSize; slot++ {
		if _, err := tx.Exec(ctx, insertSlot, team.ID, slot); err != nil {
			return nil, err
		}
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
		SELECT id, slot, name, discord_handle, role, class
		FROM players WHERE team_id = $1 ORDER BY slot`
	pRows, err := s.pool.Query(ctx, playersQ, teamID)
	if err != nil {
		return nil, err
	}
	defer pRows.Close()
	for pRows.Next() {
		var p Player
		if err := pRows.Scan(&p.ID, &p.Slot, &p.Name, &p.DiscordHandle, &p.Role, &p.Class); err != nil {
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
		SET name = $1, discord_handle = $2, role = $3, class = $4
		WHERE team_id = $5 AND slot = $6`
	for _, p := range players {
		if _, err := tx.Exec(ctx, updatePlayer, p.Name, p.DiscordHandle, p.Role, p.Class, teamID, p.Slot); err != nil {
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
