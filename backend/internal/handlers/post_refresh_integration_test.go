package handlers

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/core-team-builder/backend/internal/models"
)

// TestActiveRosterChangeMarksUpcomingPostsForRefresh defends the rule that a
// posted overview must show the roster that will actually run. Switching the
// active roster, or deleting the active one (which promotes another), marks the
// team's posts for the run that hasn't started; posts whose run already started,
// and other teams' posts, are left alone. The mark is a database trigger, so
// both API paths are exercised.
func TestActiveRosterChangeMarksUpcomingPostsForRefresh(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	other := api.createTeam(owner, "Other Core")
	api.bindChannel(team.ID, "g1", "100", "trials")
	api.bindChannel(other.ID, "g1", "200", "other")

	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(id, channel string, runAt time.Time) {
		t.Helper()
		if _, err := api.pool.Exec(ctx, `INSERT INTO discord_posts (message_id, channel_id, run_at) VALUES ($1, $2, $3)`, id, channel, runAt); err != nil {
			t.Fatalf("insert post: %v", err)
		}
	}
	reset := func() {
		t.Helper()
		if _, err := api.pool.Exec(ctx, `UPDATE discord_posts SET refresh_requested_at = NULL`); err != nil {
			t.Fatal(err)
		}
	}
	marked := func() map[string]bool {
		t.Helper()
		rows, err := api.pool.Query(ctx, `SELECT message_id, refresh_requested_at IS NOT NULL FROM discord_posts`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[string]bool{}
		for rows.Next() {
			var id string
			var m bool
			if err := rows.Scan(&id, &m); err != nil {
				t.Fatal(err)
			}
			out[id] = m
		}
		return out
	}
	insert("upcoming", "100", now.Add(24*time.Hour))
	insert("started", "100", now.Add(-time.Hour))
	insert("other-team", "200", now.Add(24*time.Hour))

	second := api.createRoster(owner, team.ID, "Second", 0)
	if got := marked(); got["upcoming"] || got["started"] || got["other-team"] {
		t.Fatalf("creating a roster must not mark posts, got %v", got)
	}

	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/rosters/%d/activate", team.ID, second.ID), owner.token, nil).expect(http.StatusOK)
	got := marked()
	if !got["upcoming"] {
		t.Errorf("the upcoming post was not marked after activating another roster: %v", got)
	}
	if got["started"] {
		t.Errorf("a post whose run already started was marked: %v", got)
	}
	if got["other-team"] {
		t.Errorf("another team's post was marked: %v", got)
	}

	// Re-activating the already-active roster changes nothing, so it must not
	// trigger pointless edits.
	reset()
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/rosters/%d/activate", team.ID, second.ID), owner.token, nil).expect(http.StatusOK)
	if got := marked(); got["upcoming"] {
		t.Errorf("re-activating the active roster marked a post: %v", got)
	}

	// Deleting the active roster promotes another one, which is also a change.
	api.do(http.MethodDelete, fmt.Sprintf("/api/teams/%d/rosters/%d", team.ID, second.ID), owner.token, nil).expect(http.StatusNoContent)
	if got := marked(); !got["upcoming"] {
		t.Errorf("deleting the active roster did not mark the upcoming post: %v", got)
	}
}

// TestPostRefreshQueries defends the scheduler's hand-off: only upcoming marked
// posts are returned (with their guild), and clearing is a compare-and-clear so
// a request that arrives mid-refresh is not lost.
func TestPostRefreshQueries(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	api.bindChannel(team.ID, "g1", "100", "trials")
	ctx := context.Background()
	store := models.NewDiscordStore(api.pool)
	now := time.Now().UTC()

	for _, row := range []struct {
		id     string
		runAt  time.Time
		marked bool
	}{{"due", now.Add(time.Hour), true}, {"unmarked", now.Add(time.Hour), false}, {"started", now.Add(-time.Hour), true}} {
		mark := "NULL"
		if row.marked {
			mark = "now()"
		}
		q := fmt.Sprintf(`INSERT INTO discord_posts (message_id, channel_id, run_at, refresh_requested_at) VALUES ($1, '100', $2, %s)`, mark)
		if _, err := api.pool.Exec(ctx, q, row.id, row.runAt); err != nil {
			t.Fatal(err)
		}
	}

	due, err := store.DuePostRefreshes(ctx, now)
	if err != nil || len(due) != 1 || due[0].MessageID != "due" || due[0].GuildID != "g1" {
		t.Fatalf("due = %+v (err %v), want only the upcoming marked post in g1", due, err)
	}

	// A stale request time must not clear a newer mark.
	if err := store.ClearPostRefresh(ctx, "due", due[0].RequestedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if again, _ := store.DuePostRefreshes(ctx, now); len(again) != 1 {
		t.Errorf("a stale clear removed the mark: %+v", again)
	}
	if err := store.ClearPostRefresh(ctx, "due", due[0].RequestedAt); err != nil {
		t.Fatal(err)
	}
	if again, _ := store.DuePostRefreshes(ctx, now); len(again) != 0 {
		t.Errorf("the matching clear left the mark: %+v", again)
	}
}

// TestAnyActiveRosterEditMarksUpcomingPostsForRefresh defends the rule that a post
// shows the live lineup: any edit to the active roster (a player, an encounter, a
// loadout, a grouping and its groups/members) or to the team row marks the team's
// upcoming posts, while the same edits to a roster that isn't active do not. Each
// edit goes through the API where there is one and straight to SQL where the
// route needs a large payload — the triggers watch the tables, not the routes.
func TestAnyActiveRosterEditMarksUpcomingPostsForRefresh(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	api.bindChannel(team.ID, "g1", "100", "trials")
	inactive := api.createRoster(owner, team.ID, "Bench", 0)
	var active int64
	if err := api.pool.QueryRow(context.Background(), `SELECT active_roster_id FROM teams WHERE id = $1`, team.ID).Scan(&active); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if _, err := api.pool.Exec(ctx, `INSERT INTO discord_posts (message_id, channel_id, run_at) VALUES ('p', '100', now() + interval '1 day')`); err != nil {
		t.Fatal(err)
	}
	isMarked := func() bool {
		t.Helper()
		var m bool
		if err := api.pool.QueryRow(ctx, `SELECT refresh_requested_at IS NOT NULL FROM discord_posts WHERE message_id = 'p'`).Scan(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	clear := func() {
		t.Helper()
		if _, err := api.pool.Exec(ctx, `UPDATE discord_posts SET refresh_requested_at = NULL`); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := api.pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	savePlayer := func(rosterID int64) {
		api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d/players/1?roster_id=%d", team.ID, rosterID), owner.token,
			map[string]any{"slot": 1, "name": "Ayla", "role": "tank", "class": "dragonknight"}).expect(http.StatusOK)
	}

	// Edits to a roster that isn't active never touch the post.
	clear()
	savePlayer(inactive.ID)
	api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/groupings?roster_id=%d", team.ID, inactive.ID), owner.token,
		map[string]any{"name": "Bench cages", "group_count": 2}).expect(http.StatusCreated)
	exec(`UPDATE encounter_loadouts SET updated_at = now() WHERE encounter_id IN (SELECT id FROM encounters WHERE roster_id = $1)`, inactive.ID)
	if isMarked() {
		t.Fatal("editing a roster that is not active marked the post")
	}

	// Each of these edits the ACTIVE roster and must mark the post.
	edits := []struct {
		name string
		do   func()
	}{
		{"save a player", func() { savePlayer(active) }},
		{"create an encounter", func() {
			api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/encounters", team.ID), owner.token,
				map[string]any{"name": "Lokkestiiz"}).expect(http.StatusCreated)
		}},
		{"change a loadout", func() {
			exec(`UPDATE encounter_loadouts SET updated_at = now() WHERE encounter_id IN (SELECT id FROM encounters WHERE roster_id = $1)`, active)
		}},
		{"create a grouping", func() {
			api.do(http.MethodPost, fmt.Sprintf("/api/teams/%d/groupings", team.ID), owner.token,
				map[string]any{"name": "Ice cages", "group_count": 3}).expect(http.StatusCreated)
		}},
		{"rename a group", func() {
			exec(`UPDATE grouping_groups SET name = 'Renamed' WHERE grouping_id IN (SELECT id FROM groupings WHERE roster_id = $1)`, active)
		}},
		{"assign a player to a group", func() {
			exec(`INSERT INTO grouping_members (grouping_id, player_slot, group_number)
			      SELECT id, 1, 1 FROM groupings WHERE roster_id = $1 LIMIT 1`, active)
		}},
		{"delete a grouping", func() {
			exec(`DELETE FROM groupings WHERE roster_id = $1`, active)
		}},
		{"rename the team", func() {
			api.do(http.MethodPut, fmt.Sprintf("/api/teams/%d", team.ID), owner.token, updatePayload("Renamed Core")).expect(http.StatusOK)
		}},
	}
	for _, e := range edits {
		clear()
		e.do()
		if !isMarked() {
			t.Errorf("%s: the upcoming post was not marked for refresh", e.name)
		}
	}

	// A run that has already started is never marked, whatever changes.
	exec(`UPDATE discord_posts SET run_at = now() - interval '1 hour', refresh_requested_at = NULL`)
	savePlayer(active)
	if isMarked() {
		t.Error("a post whose run already started was marked")
	}
}
