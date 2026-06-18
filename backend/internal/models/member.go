package models

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrMemberNotFound is returned when a roster-member lookup matches nothing.
var ErrMemberNotFound = errors.New("roster member not found")

// Roster member intake status + step values. The Discord DM flow walks the
// steps in order, persisting progress on the draft row after each answer.
const (
	MemberStatusDraft    = "draft"
	MemberStatusComplete = "complete"

	MemberStepDays     = "days"
	MemberStepTimezone = "timezone"
	MemberStepTimes    = "times"
	MemberStepRoles    = "roles"
	MemberStepClasses  = "classes"
	MemberStepDone     = "done"

	MemberSourceDiscord = "discord"
	MemberSourceManual  = "manual"
)

// DayWindow is an availability window for one day: start/end hours (0-23) in the
// member's timezone.
type DayWindow struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// RosterMember is one person in a team's recruitment/availability pool, distinct
// from the 12 fixed player slots and from team_members (app-account sharing).
// Discord-sourced members are gathered by the bot's /coreteam recruit DM flow;
// manual members are added in the web app.
type RosterMember struct {
	ID              int64                `json:"id"`
	TeamID          int64                `json:"team_id"`
	DiscordUserID   string               `json:"discord_user_id"`
	DiscordUsername string               `json:"discord_username"`
	DisplayName     string               `json:"display_name"`
	Timezone        string               `json:"timezone"`
	Days            []string             `json:"days"`
	Availability    map[string]DayWindow `json:"availability"`
	Roles           []string             `json:"roles"`
	ClassesByRole   map[string][]string  `json:"classes_by_role"`
	Status          string               `json:"status"`
	Step            string               `json:"step"`
	Source          string               `json:"source"`
}

// MemberStore provides data access for the per-team roster-member pool.
type MemberStore struct {
	pool *pgxpool.Pool
}

// NewMemberStore constructs a MemberStore backed by the given pool.
func NewMemberStore(pool *pgxpool.Pool) *MemberStore {
	return &MemberStore{pool: pool}
}

const memberColumns = `id, team_id, COALESCE(discord_user_id, ''), discord_username, display_name,
	timezone, days, availability, roles, classes_by_role, status, step, source`

func scanMember(row pgx.Row) (*RosterMember, error) {
	var m RosterMember
	var availability, classes []byte
	if err := row.Scan(
		&m.ID, &m.TeamID, &m.DiscordUserID, &m.DiscordUsername, &m.DisplayName,
		&m.Timezone, &m.Days, &availability, &m.Roles, &classes, &m.Status, &m.Step, &m.Source,
	); err != nil {
		return nil, err
	}
	m.Availability = map[string]DayWindow{}
	if len(availability) > 0 {
		_ = json.Unmarshal(availability, &m.Availability)
	}
	m.ClassesByRole = map[string][]string{}
	if len(classes) > 0 {
		_ = json.Unmarshal(classes, &m.ClassesByRole)
	}
	return &m, nil
}

// UpsertDraft starts (or restarts) the intake for a Discord user on a team. A
// fresh draft is created, or an existing row is reset to the first step so
// re-clicking "I'm Interested" begins the questionnaire again. Returns the row.
func (s *MemberStore) UpsertDraft(ctx context.Context, teamID int64, discordUserID, username, displayName string) (*RosterMember, error) {
	const q = `
		INSERT INTO team_roster_members
			(team_id, discord_user_id, discord_username, display_name, status, step, source,
			 days, availability, roles, classes_by_role)
		VALUES ($1, $2, $3, $4, 'draft', $5, 'discord', '{}', '{}'::jsonb, '{}', '{}'::jsonb)
		ON CONFLICT (team_id, discord_user_id) WHERE discord_user_id IS NOT NULL
		DO UPDATE SET discord_username = EXCLUDED.discord_username,
		              display_name = EXCLUDED.display_name,
		              status = 'draft', step = $5,
		              days = '{}', availability = '{}'::jsonb,
		              roles = '{}', classes_by_role = '{}'::jsonb
		RETURNING ` + memberColumns
	return scanMember(s.pool.QueryRow(ctx, q, teamID, discordUserID, username, displayName, MemberStepDays))
}

// GetByID returns one roster member by primary key.
func (s *MemberStore) GetByID(ctx context.Context, id int64) (*RosterMember, error) {
	const q = `SELECT ` + memberColumns + ` FROM team_roster_members WHERE id = $1`
	m, err := scanMember(s.pool.QueryRow(ctx, q, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMemberNotFound
	}
	return m, err
}

// List returns every roster member for a team, most recently updated first.
func (s *MemberStore) List(ctx context.Context, teamID int64) ([]RosterMember, error) {
	const q = `SELECT ` + memberColumns + ` FROM team_roster_members WHERE team_id = $1 ORDER BY updated_at DESC`
	rows, err := s.pool.Query(ctx, q, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RosterMember{}
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// SaveProgress persists the mutable intake fields (days, timezone, availability,
// roles, classes, status, step) of an in-progress draft, identified by m.ID.
func (s *MemberStore) SaveProgress(ctx context.Context, m *RosterMember) error {
	availability, err := json.Marshal(m.Availability)
	if err != nil {
		return err
	}
	classes, err := json.Marshal(m.ClassesByRole)
	if err != nil {
		return err
	}
	const q = `
		UPDATE team_roster_members
		SET days = $1, timezone = $2, availability = $3, roles = $4,
		    classes_by_role = $5, status = $6, step = $7
		WHERE id = $8`
	_, err = s.pool.Exec(ctx, q, m.Days, m.Timezone, availability, m.Roles, classes, m.Status, m.Step, m.ID)
	return err
}

// Create inserts a manually-added roster member (from the web app).
func (s *MemberStore) Create(ctx context.Context, m *RosterMember) (*RosterMember, error) {
	availability, err := json.Marshal(m.Availability)
	if err != nil {
		return nil, err
	}
	classes, err := json.Marshal(m.ClassesByRole)
	if err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO team_roster_members
			(team_id, discord_username, display_name, timezone, days, availability,
			 roles, classes_by_role, status, step, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'complete', 'done', 'manual')
		RETURNING ` + memberColumns
	return scanMember(s.pool.QueryRow(ctx, q,
		m.TeamID, m.DiscordUsername, m.DisplayName, m.Timezone, m.Days, availability,
		m.Roles, classes,
	))
}

// Update edits a roster member's web-editable fields (display name, Discord
// handle, timezone, availability, roles, classes) by id within a team. The
// intake status/step and source are deliberately left untouched so editing a
// Discord-sourced member doesn't reset their flow. Returns the updated row, or
// ErrMemberNotFound if no row matches.
func (s *MemberStore) Update(ctx context.Context, m *RosterMember) (*RosterMember, error) {
	availability, err := json.Marshal(m.Availability)
	if err != nil {
		return nil, err
	}
	classes, err := json.Marshal(m.ClassesByRole)
	if err != nil {
		return nil, err
	}
	const q = `
		UPDATE team_roster_members
		SET discord_username = $1, display_name = $2, timezone = $3, days = $4,
		    availability = $5, roles = $6, classes_by_role = $7
		WHERE id = $8 AND team_id = $9
		RETURNING ` + memberColumns
	updated, err := scanMember(s.pool.QueryRow(ctx, q,
		m.DiscordUsername, m.DisplayName, m.Timezone, m.Days, availability,
		m.Roles, classes, m.ID, m.TeamID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrMemberNotFound
	}
	return updated, err
}

// Delete removes a roster member from a team.
func (s *MemberStore) Delete(ctx context.Context, teamID, id int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM team_roster_members WHERE id = $1 AND team_id = $2`, id, teamID)
	return err
}
