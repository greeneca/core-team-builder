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

// Loadout limits keep stored lists sane; the UI constrains choices to master
// data, but the backend still bounds free-form input defensively.
const (
	maxLoadoutItems  = 30
	maxLoadoutKeyLen = 100
)

// ValidEncounterNames is the allow-list for an encounter's name: "Default",
// "Trash", or any ESO trial boss (grouped by trial in the frontend). Stored as
// the display string. Seed list — extend as trials/bosses are added.
var ValidEncounterNames = buildEncounterNameSet([]string{
	"Default", "Trash",
	// Aetherian Archive
	"Lightning Storm Atronach", "Foundation Stone Atronach", "Varlariel", "The Celestial Mage",
	// Hel Ra Citadel
	"Ra Kotu", "Yokeda Kai and Yokeda Rok'dun", "The Celestial Warrior",
	// Sanctum Ophidia
	"Possessed Mantikora", "Stonebreaker", "Ozara", "The Celestial Serpent",
	// Maw of Lorkhaj
	"Zhaj'hassa the Forgotten", "The Twins", "Rakkhat",
	// Halls of Fabrication
	"Hunter-Killer Fabricants", "Pinnacle Factotum", "Archcustodian", "Refabrication Committee", "The Assembly General",
	// Asylum Sanctorium
	"Saint Llothis the Pious", "Saint Felms the Bold", "Saint Olms the Just",
	// Cloudrest
	"Shade of Galenwe", "Shade of Siroria", "Shade of Relequen", "Z'Maja",
	// Sunspire
	"Lokkestiiz", "Yolnahkriin", "Nahviintaas",
	// Kyne's Aegis
	"Yandir the Butcher", "Captain Vrol", "Lord Falgravn",
	// Rockgrove
	"Oaxiltso", "Flame-Herald Bahsei", "Xalvakka",
	// Dreadsail Reef
	"Lylanar and Turlassil", "Reef Guardian", "Tideborn Taleria",
	// Sanity's Edge
	"Exarchanic Yaseyla", "Archwizard Twelvane and Chimera", "Ansuul the Tormentor",
	// Lucent Citadel
	"Count Ryelaz and Zilyesset", "Orphic Shattered Shard", "Xoryn",
	// Ossein Cage
	"Shapers of Flesh", "Jynorah and Skorkhif", "Overfiend Kazpian",
})

func buildEncounterNameSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// Loadout is a single player's gear + skills for one encounter.
type Loadout struct {
	Slot   int      `json:"slot"`
	Gear   []string `json:"gear"`
	Skills []string `json:"skills"`
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

// Create inserts a new encounter (appended after existing ones) with 12 empty
// loadout slots, in a single transaction.
func (s *EncounterStore) Create(ctx context.Context, teamID int64, name string) (*Encounter, error) {
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
		SELECT slot, gear, skills
		FROM encounter_loadouts WHERE encounter_id = $1 ORDER BY slot`
	rows, err := s.pool.Query(ctx, lq, encounterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var l Loadout
		if err := rows.Scan(&l.Slot, &l.Gear, &l.Skills); err != nil {
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

// SaveLoadouts updates the gear/skills for the given slots in one transaction.
func (s *EncounterStore) SaveLoadouts(ctx context.Context, encounterID int64, loadouts []Loadout) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	const q = `
		UPDATE encounter_loadouts SET gear = $1, skills = $2
		WHERE encounter_id = $3 AND slot = $4`
	for _, l := range loadouts {
		if _, err := tx.Exec(ctx, q, l.Gear, l.Skills, encounterID, l.Slot); err != nil {
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
