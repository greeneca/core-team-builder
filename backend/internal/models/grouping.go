package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrGroupingNotFound is returned when a grouping lookup matches nothing.
var ErrGroupingNotFound = errors.New("grouping not found")

// MaxGroupsPerGrouping caps the number of numbered groups a single grouping may
// hold (at most one per player slot makes more than TeamSize pointless).
const MaxGroupsPerGrouping = 12

// GroupingGroup is one numbered group within a grouping: an optional custom name
// (blank means the UI shows the default "Group N") and the player slots assigned
// to it.
type GroupingGroup struct {
	GroupNumber int    `json:"group_number"`
	Name        string `json:"name"`
	Slots       []int  `json:"slots"`
}

// Grouping splits a roster into a set of numbered groups (e.g. ice cages or
// slayer stacks). A player may belong to at most one group per grouping.
type Grouping struct {
	ID       int64 `json:"id"`
	RosterID int64 `json:"roster_id"`
	// TeamID is resolved via the grouping's roster (groupings no longer carry
	// team_id directly). Populated by the read paths for team-ownership checks.
	TeamID     int64           `json:"team_id"`
	Name       string          `json:"name"`
	GroupCount int             `json:"group_count"`
	Position   int             `json:"position"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	Groups     []GroupingGroup `json:"groups"`
}

// GroupingStore provides data access for groupings, their groups, and members.
type GroupingStore struct {
	pool *pgxpool.Pool
}

// NewGroupingStore constructs a GroupingStore backed by the given pool.
func NewGroupingStore(pool *pgxpool.Pool) *GroupingStore {
	return &GroupingStore{pool: pool}
}

// emptyGroups returns count groups numbered 1..count with blank names and no
// members. Used so a grouping always reports a full set of groups to the client.
func emptyGroups(count int) []GroupingGroup {
	groups := make([]GroupingGroup, 0, count)
	for n := 1; n <= count; n++ {
		groups = append(groups, GroupingGroup{GroupNumber: n, Name: "", Slots: []int{}})
	}
	return groups
}

// ListForRoster returns a roster's groupings (each fully populated with its
// groups and member slots), ordered by position.
func (s *GroupingStore) ListForRoster(ctx context.Context, rosterID int64) ([]Grouping, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT g.id, g.roster_id, r.team_id, g.name, g.group_count, g.position, g.created_at, g.updated_at
		 FROM groupings g JOIN rosters r ON r.id = g.roster_id
		 WHERE g.roster_id = $1 ORDER BY g.position, g.id`, rosterID)
	if err != nil {
		return nil, err
	}
	groupings := []Grouping{}
	idx := map[int64]int{}
	for rows.Next() {
		var g Grouping
		if err := rows.Scan(&g.ID, &g.RosterID, &g.TeamID, &g.Name, &g.GroupCount, &g.Position, &g.CreatedAt, &g.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		g.Groups = emptyGroups(g.GroupCount)
		groupings = append(groupings, g)
		idx[g.ID] = len(groupings) - 1
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(groupings) == 0 {
		return groupings, nil
	}

	// Group names.
	nrows, err := s.pool.Query(ctx,
		`SELECT gg.grouping_id, gg.group_number, gg.name
		 FROM grouping_groups gg JOIN groupings g ON g.id = gg.grouping_id
		 WHERE g.roster_id = $1`, rosterID)
	if err != nil {
		return nil, err
	}
	for nrows.Next() {
		var gid int64
		var num int
		var name string
		if err := nrows.Scan(&gid, &num, &name); err != nil {
			nrows.Close()
			return nil, err
		}
		if i, ok := idx[gid]; ok && num >= 1 && num <= groupings[i].GroupCount {
			groupings[i].Groups[num-1].Name = name
		}
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return nil, err
	}

	// Member slots.
	mrows, err := s.pool.Query(ctx,
		`SELECT gm.grouping_id, gm.group_number, gm.player_slot
		 FROM grouping_members gm JOIN groupings g ON g.id = gm.grouping_id
		 WHERE g.roster_id = $1 ORDER BY gm.player_slot`, rosterID)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var gid int64
		var num, slot int
		if err := mrows.Scan(&gid, &num, &slot); err != nil {
			mrows.Close()
			return nil, err
		}
		if i, ok := idx[gid]; ok && num >= 1 && num <= groupings[i].GroupCount {
			groupings[i].Groups[num-1].Slots = append(groupings[i].Groups[num-1].Slots, slot)
		}
	}
	mrows.Close()
	return groupings, mrows.Err()
}

// Get returns a single grouping with its groups and member slots.
func (s *GroupingStore) Get(ctx context.Context, groupingID int64) (*Grouping, error) {
	g := &Grouping{}
	err := s.pool.QueryRow(ctx,
		`SELECT g.id, g.roster_id, r.team_id, g.name, g.group_count, g.position, g.created_at, g.updated_at
		 FROM groupings g JOIN rosters r ON r.id = g.roster_id WHERE g.id = $1`, groupingID).Scan(
		&g.ID, &g.RosterID, &g.TeamID, &g.Name, &g.GroupCount, &g.Position, &g.CreatedAt, &g.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrGroupingNotFound
	}
	if err != nil {
		return nil, err
	}
	g.Groups = emptyGroups(g.GroupCount)

	nrows, err := s.pool.Query(ctx,
		`SELECT group_number, name FROM grouping_groups WHERE grouping_id = $1`, groupingID)
	if err != nil {
		return nil, err
	}
	for nrows.Next() {
		var num int
		var name string
		if err := nrows.Scan(&num, &name); err != nil {
			nrows.Close()
			return nil, err
		}
		if num >= 1 && num <= g.GroupCount {
			g.Groups[num-1].Name = name
		}
	}
	nrows.Close()
	if err := nrows.Err(); err != nil {
		return nil, err
	}

	mrows, err := s.pool.Query(ctx,
		`SELECT group_number, player_slot FROM grouping_members WHERE grouping_id = $1 ORDER BY player_slot`, groupingID)
	if err != nil {
		return nil, err
	}
	for mrows.Next() {
		var num, slot int
		if err := mrows.Scan(&num, &slot); err != nil {
			mrows.Close()
			return nil, err
		}
		if num >= 1 && num <= g.GroupCount {
			g.Groups[num-1].Slots = append(g.Groups[num-1].Slots, slot)
		}
	}
	mrows.Close()
	return g, mrows.Err()
}

// CountForRoster returns how many groupings a roster has.
func (s *GroupingStore) CountForRoster(ctx context.Context, rosterID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM groupings WHERE roster_id = $1`, rosterID).Scan(&n)
	return n, err
}

// Create inserts a new grouping (appended after existing ones) with groupCount
// blank-named groups, in a single transaction.
func (s *GroupingStore) Create(ctx context.Context, rosterID int64, name string, groupCount int) (*Grouping, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var position int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(position), -1) + 1 FROM groupings WHERE roster_id = $1`, rosterID,
	).Scan(&position); err != nil {
		return nil, err
	}

	var id int64
	if err := tx.QueryRow(ctx,
		`INSERT INTO groupings (roster_id, name, group_count, position) VALUES ($1, $2, $3, $4) RETURNING id`,
		rosterID, name, groupCount, position,
	).Scan(&id); err != nil {
		return nil, err
	}
	for n := 1; n <= groupCount; n++ {
		if _, err := tx.Exec(ctx,
			`INSERT INTO grouping_groups (grouping_id, group_number, name) VALUES ($1, $2, '')`, id, n,
		); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Save replaces a grouping's name, group count, per-group names, and the full
// set of member assignments in one transaction. Groups (and their members)
// beyond groupCount are removed. Callers must validate that no slot appears in
// more than one group; the (grouping_id, player_slot) primary key is the final
// guard.
func (s *GroupingStore) Save(ctx context.Context, groupingID int64, name string, groupCount int, groups []GroupingGroup) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE groupings SET name = $1, group_count = $2 WHERE id = $3`, name, groupCount, groupingID,
	); err != nil {
		return err
	}
	// Drop groups (and any members) beyond the new count, then reset members so
	// we can rewrite them from the request.
	if _, err := tx.Exec(ctx,
		`DELETE FROM grouping_groups WHERE grouping_id = $1 AND group_number > $2`, groupingID, groupCount,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM grouping_members WHERE grouping_id = $1`, groupingID); err != nil {
		return err
	}

	nameByNum := map[int]string{}
	slotsByNum := map[int][]int{}
	for _, g := range groups {
		nameByNum[g.GroupNumber] = g.Name
		slotsByNum[g.GroupNumber] = g.Slots
	}
	for n := 1; n <= groupCount; n++ {
		if _, err := tx.Exec(ctx,
			`INSERT INTO grouping_groups (grouping_id, group_number, name) VALUES ($1, $2, $3)
			 ON CONFLICT (grouping_id, group_number) DO UPDATE SET name = EXCLUDED.name`,
			groupingID, n, nameByNum[n],
		); err != nil {
			return err
		}
		for _, slot := range slotsByNum[n] {
			if _, err := tx.Exec(ctx,
				`INSERT INTO grouping_members (grouping_id, group_number, player_slot) VALUES ($1, $2, $3)`,
				groupingID, n, slot,
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

// Delete removes a grouping and (via cascade) its groups and members.
func (s *GroupingStore) Delete(ctx context.Context, groupingID int64) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM groupings WHERE id = $1`, groupingID)
	return err
}

// copyGroupingsTx copies every grouping (names, group names, and member
// assignments) from srcRosterID to dstRosterID within an existing transaction.
// Used when copying a roster (or a whole team).
func copyGroupingsTx(ctx context.Context, tx pgx.Tx, srcRosterID, dstRosterID int64) error {
	rows, err := tx.Query(ctx,
		`SELECT id, name, group_count, position FROM groupings WHERE roster_id = $1 ORDER BY position, id`,
		srcRosterID)
	if err != nil {
		return err
	}
	type srcGrouping struct {
		id         int64
		name       string
		groupCount int
		position   int
	}
	var srcs []srcGrouping
	for rows.Next() {
		var g srcGrouping
		if err := rows.Scan(&g.id, &g.name, &g.groupCount, &g.position); err != nil {
			rows.Close()
			return err
		}
		srcs = append(srcs, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, src := range srcs {
		var newID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO groupings (roster_id, name, group_count, position) VALUES ($1, $2, $3, $4) RETURNING id`,
			dstRosterID, src.name, src.groupCount, src.position,
		).Scan(&newID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO grouping_groups (grouping_id, group_number, name)
			 SELECT $1, group_number, name FROM grouping_groups WHERE grouping_id = $2`,
			newID, src.id,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO grouping_members (grouping_id, group_number, player_slot)
			 SELECT $1, group_number, player_slot FROM grouping_members WHERE grouping_id = $2`,
			newID, src.id,
		); err != nil {
			return err
		}
	}
	return nil
}
