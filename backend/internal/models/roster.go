package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRosterNotFound is returned when a roster lookup matches nothing (or the
// roster does not belong to the expected team).
var ErrRosterNotFound = errors.New("roster not found")

// ErrLastRoster is returned when attempting to delete a team's only roster. A
// team must always have at least one roster.
var ErrLastRoster = errors.New("cannot delete the team's only roster")

// MaxRostersPerTeam caps how many rosters a single team may hold.
const MaxRostersPerTeam = 50

// Roster is a named composition within a team: its own 12-player lineup plus
// (via roster_id) its encounters/loadouts and groupings. A team always has at
// least one roster and designates exactly one as active (teams.active_roster_id);
// the Discord bot always uses the active roster.
type Roster struct {
	ID        int64     `json:"id"`
	TeamID    int64     `json:"team_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Players is the roster's 12-slot lineup, populated by Get (ordered by slot)
	// and omitted from the list view.
	Players []Player `json:"players,omitempty"`
}

// RosterStore provides data access for a team's rosters.
type RosterStore struct {
	pool *pgxpool.Pool
}

// NewRosterStore constructs a RosterStore backed by the given pool.
func NewRosterStore(pool *pgxpool.Pool) *RosterStore {
	return &RosterStore{pool: pool}
}

const rosterCols = `id, team_id, name, position, created_at, updated_at`

func scanRoster(row pgx.Row) (*Roster, error) {
	r := &Roster{}
	if err := row.Scan(&r.ID, &r.TeamID, &r.Name, &r.Position, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	return r, nil
}

// ListForTeam returns a team's rosters (without players), ordered by position.
func (s *RosterStore) ListForTeam(ctx context.Context, teamID int64) ([]Roster, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+rosterCols+` FROM rosters WHERE team_id = $1 ORDER BY position, id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rosters := []Roster{}
	for rows.Next() {
		var r Roster
		if err := rows.Scan(&r.ID, &r.TeamID, &r.Name, &r.Position, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		rosters = append(rosters, r)
	}
	return rosters, rows.Err()
}

// Get returns one roster with its players (ordered by slot).
func (s *RosterStore) Get(ctx context.Context, rosterID int64) (*Roster, error) {
	r, err := scanRoster(s.pool.QueryRow(ctx, `SELECT `+rosterCols+` FROM rosters WHERE id = $1`, rosterID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRosterNotFound
	}
	if err != nil {
		return nil, err
	}
	players, err := loadRosterPlayers(ctx, s.pool, rosterID)
	if err != nil {
		return nil, err
	}
	r.Players = players
	return r, nil
}

// TeamForRoster returns the team a roster belongs to, or ErrRosterNotFound. Used
// by handlers to verify a roster id in the path/query belongs to the team being
// accessed.
func (s *RosterStore) TeamForRoster(ctx context.Context, rosterID int64) (int64, error) {
	var teamID int64
	err := s.pool.QueryRow(ctx, `SELECT team_id FROM rosters WHERE id = $1`, rosterID).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRosterNotFound
	}
	if err != nil {
		return 0, err
	}
	return teamID, nil
}

// CountForTeam returns how many rosters a team has.
func (s *RosterStore) CountForTeam(ctx context.Context, teamID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rosters WHERE team_id = $1`, teamID).Scan(&n)
	return n, err
}

// ActiveForTeam returns the team's active roster id, falling back to its first
// roster when the pointer is unset. Returns ErrRosterNotFound when the team does
// not exist or has no rosters. Used to default roster-scoped requests to the
// active roster.
func (s *RosterStore) ActiveForTeam(ctx context.Context, teamID int64) (int64, error) {
	var active *int64
	err := s.pool.QueryRow(ctx, `SELECT active_roster_id FROM teams WHERE id = $1`, teamID).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRosterNotFound
	}
	if err != nil {
		return 0, err
	}
	if active != nil && *active != 0 {
		return *active, nil
	}
	var rid int64
	err = s.pool.QueryRow(ctx,
		`SELECT id FROM rosters WHERE team_id = $1 ORDER BY position, id LIMIT 1`, teamID).Scan(&rid)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrRosterNotFound
	}
	if err != nil {
		return 0, err
	}
	return rid, nil
}

// Create inserts a new roster on the team and seeds its composition. When
// copyFromRosterID is non-zero, the new roster is seeded from that source roster
// (which the caller must have validated belongs to the same team): its 12
// players, every encounter with per-player loadouts, and every grouping are
// copied. When zero, the roster starts fresh with 12 default-role player slots
// and a single empty "Default" encounter. It is not made active here; activation
// is a separate explicit step. The new roster (with players) is returned.
func (s *RosterStore) Create(ctx context.Context, teamID int64, name string, copyFromRosterID int64) (*Roster, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var position int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM rosters WHERE team_id = $1`, teamID,
	).Scan(&position); err != nil {
		return nil, err
	}

	var rosterID int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO rosters (team_id, name, position) VALUES ($1, $2, $3) RETURNING id`,
		teamID, name, position,
	).Scan(&rosterID); err != nil {
		return nil, err
	}

	if copyFromRosterID != 0 {
		if err := copyPlayersTx(ctx, tx, copyFromRosterID, rosterID); err != nil {
			return nil, err
		}
		if err := copyEncountersTx(ctx, tx, copyFromRosterID, rosterID); err != nil {
			return nil, err
		}
		if err := copyGroupingsTx(ctx, tx, copyFromRosterID, rosterID); err != nil {
			return nil, err
		}
		if err := copyRosterImagesTx(ctx, tx, copyFromRosterID, rosterID); err != nil {
			return nil, err
		}
	} else {
		if err := seedRosterPlayersTx(ctx, tx, rosterID); err != nil {
			return nil, err
		}
		if err := createDefaultEncounterTx(ctx, tx, rosterID); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, rosterID)
}

// Rename changes a roster's display name.
func (s *RosterStore) Rename(ctx context.Context, rosterID int64, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE rosters SET name = $1 WHERE id = $2`, name, rosterID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRosterNotFound
	}
	return nil
}

// SetActive designates rosterID as the team's active roster. The roster must
// belong to the team (ErrRosterNotFound otherwise).
func (s *RosterStore) SetActive(ctx context.Context, teamID, rosterID int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE teams SET active_roster_id = $1
		 WHERE id = $2 AND EXISTS (SELECT 1 FROM rosters r WHERE r.id = $1 AND r.team_id = $2)`,
		rosterID, teamID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrRosterNotFound
	}
	return nil
}

// Delete removes a roster (and, by cascade, its players/encounters/groupings).
// A team's only roster cannot be deleted (ErrLastRoster). When the deleted
// roster was the active one, another roster is promoted to active. All in one
// transaction.
func (s *RosterStore) Delete(ctx context.Context, teamID, rosterID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Confirm the roster belongs to the team.
	var belongs bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM rosters WHERE id = $1 AND team_id = $2)`, rosterID, teamID,
	).Scan(&belongs); err != nil {
		return err
	}
	if !belongs {
		return ErrRosterNotFound
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM rosters WHERE team_id = $1`, teamID).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastRoster
	}

	// If this roster is active, promote the next roster (by position) first so the
	// team is never left without an active roster.
	var activeID *int64
	if err := tx.QueryRow(ctx, `SELECT active_roster_id FROM teams WHERE id = $1`, teamID).Scan(&activeID); err != nil {
		return err
	}
	if activeID != nil && *activeID == rosterID {
		var nextID int64
		if err := tx.QueryRow(ctx,
			`SELECT id FROM rosters WHERE team_id = $1 AND id <> $2 ORDER BY position, id LIMIT 1`,
			teamID, rosterID,
		).Scan(&nextID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE teams SET active_roster_id = $1 WHERE id = $2`, nextID, teamID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM rosters WHERE id = $1`, rosterID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// loadRosterPlayers reads a roster's 12 player slots (ordered by slot).
func loadRosterPlayers(ctx context.Context, q pgxQuerier, rosterID int64) ([]Player, error) {
	const playersQ = `
		SELECT id, slot, name, discord_handle, role, class, race,
		       subclassed, skill_line_1, skill_line_2, skill_line_3, mastery_1, mastery_2, werewolf, updated_at
		FROM players WHERE roster_id = $1 ORDER BY slot`
	rows, err := q.Query(ctx, playersQ, rosterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	players := []Player{}
	for rows.Next() {
		var p Player
		if err := rows.Scan(
			&p.ID, &p.Slot, &p.Name, &p.DiscordHandle, &p.Role, &p.Class, &p.Race,
			&p.Subclassed, &p.SkillLine1, &p.SkillLine2, &p.SkillLine3, &p.Mastery1, &p.Mastery2, &p.Werewolf, &p.UpdatedAt,
		); err != nil {
			return nil, err
		}
		players = append(players, p)
	}
	return players, rows.Err()
}

// pgxQuerier is the subset of pgxpool.Pool / pgx.Tx used by the shared roster
// player loader, so it can run both on the pool and inside a transaction.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
