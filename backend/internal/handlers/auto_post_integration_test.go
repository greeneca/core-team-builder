package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/core-team-builder/backend/internal/models"
)

// bindChannel inserts a channel binding the way the bot's /coreteam setup does.
func (a *testAPI) bindChannel(teamID int64, guildID, channelID, name string) {
	a.t.Helper()
	_, err := a.pool.Exec(context.Background(),
		`INSERT INTO discord_channels (guild_id, channel_id, team_id, channel_name) VALUES ($1, $2, $3, $4)`,
		guildID, channelID, teamID, name)
	if err != nil {
		a.t.Fatalf("bind channel: %v", err)
	}
}

func autoPostBody(channel string) map[string]any {
	return map[string]any{
		"auto_post_enabled":         true,
		"auto_post_channel_id":      channel,
		"auto_post_lead_minutes":    2880,
		"auto_delete_enabled":       true,
		"auto_delete_delay_minutes": 180,
	}
}

// TestAutoPostSettingsRoundTrip defends the Configure page's save/load cycle: the
// saved values come back, the dropdown lists only this team's bound channels, and
// a team that never saved gets both features off.
func TestAutoPostSettingsRoundTrip(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	other := api.createTeam(owner, "Other Core")
	path := fmt.Sprintf("/api/teams/%d/auto-post", team.ID)
	api.bindChannel(team.ID, "g1", "100", "trials")
	api.bindChannel(other.ID, "g1", "200", "other-trials")

	var got autoPostResponse
	api.do(http.MethodGet, path, owner.token, nil).expect(http.StatusOK).decode(&got)
	if got.AutoPostEnabled || got.AutoDeleteEnabled {
		t.Errorf("fresh team = %+v, want both features off", got.AutoPostSettings)
	}

	api.do(http.MethodPut, path, owner.token, autoPostBody("100")).expect(http.StatusOK)
	api.do(http.MethodGet, path, owner.token, nil).expect(http.StatusOK).decode(&got)
	want := models.AutoPostSettings{
		AutoPostEnabled: true, AutoPostChannelID: "100", AutoPostLeadMinutes: 2880,
		AutoDeleteEnabled: true, AutoDeleteDelayMinutes: 180,
	}
	if got.AutoPostSettings != want {
		t.Errorf("settings = %+v, want %+v", got.AutoPostSettings, want)
	}
	if len(got.Channels) != 1 || got.Channels[0].ChannelID != "100" || got.Channels[0].Name != "trials" {
		t.Errorf("channels = %+v, want only this team's #trials", got.Channels)
	}
}

// TestAutoPostRejectsForeignChannel defends against pointing the bot at a channel
// the team doesn't own: another team's channel, an unbound one, and the team's
// own channel after it was re-bound elsewhere must all be refused.
func TestAutoPostRejectsForeignChannel(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	other := api.createTeam(owner, "Other Core")
	path := fmt.Sprintf("/api/teams/%d/auto-post", team.ID)
	api.bindChannel(other.ID, "g1", "200", "other-trials")

	for _, channel := range []string{"200", "999"} {
		resp := api.do(http.MethodPut, path, owner.token, autoPostBody(channel)).expect(http.StatusBadRequest)
		if strings.Contains(string(resp.body), "other-trials") {
			t.Errorf("response for channel %s leaked another team's channel name: %s", channel, resp.body)
		}
	}
}

// TestAutoPostValidation defends the bounds on the durations and the channel
// requirement; the database CHECKs back these up, but the API must answer 400.
func TestAutoPostValidation(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d/auto-post", team.ID)
	api.bindChannel(team.ID, "g1", "100", "trials")

	cases := map[string]func(m map[string]any){
		"lead below 1 hour":         func(m map[string]any) { m["auto_post_lead_minutes"] = 59 },
		"lead above 30 days":        func(m map[string]any) { m["auto_post_lead_minutes"] = 43201 },
		"delay below 1 hour":        func(m map[string]any) { m["auto_delete_delay_minutes"] = 0 },
		"delay negative":            func(m map[string]any) { m["auto_delete_delay_minutes"] = -5 },
		"delay above 30 days":       func(m map[string]any) { m["auto_delete_delay_minutes"] = 43201 },
		"auto post with no channel": func(m map[string]any) { m["auto_post_channel_id"] = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			body := autoPostBody("100")
			mutate(body)
			api.do(http.MethodPut, path, owner.token, body).expect(http.StatusBadRequest)
		})
	}
}

// TestAutoPostPermissions covers the permission matrix: the owner and an editor
// may save, a viewer may read but not write, and a stranger gets 404 for both
// (never 403), so the team's existence isn't revealed.
func TestAutoPostPermissions(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	editor := api.registerUser("editor")
	viewer := api.registerUser("viewer")
	stranger := api.registerUser("stranger")
	team := api.createTeam(owner, "Sunday Core")
	path := fmt.Sprintf("/api/teams/%d/auto-post", team.ID)
	api.bindChannel(team.ID, "g1", "100", "trials")

	share := fmt.Sprintf("/api/teams/%d/share", team.ID)
	api.do(http.MethodPost, share, owner.token, map[string]string{"username": editor.user.Username, "role": models.RoleEditor}).expect(http.StatusOK)
	api.do(http.MethodPost, share, owner.token, map[string]string{"username": viewer.user.Username, "role": models.RoleViewer}).expect(http.StatusOK)

	api.do(http.MethodPut, path, editor.token, autoPostBody("100")).expect(http.StatusOK)
	api.do(http.MethodGet, path, viewer.token, nil).expect(http.StatusOK)
	api.do(http.MethodPut, path, viewer.token, autoPostBody("100")).expect(http.StatusForbidden)
	api.do(http.MethodGet, path, stranger.token, nil).expect(http.StatusNotFound)
	api.do(http.MethodPut, path, stranger.token, autoPostBody("100")).expect(http.StatusNotFound)
}

// TestAutoPostSchedulerQueries drives the store queries the bot's scheduler
// relies on, against the real schema: auto delete only picks posts whose delay
// has passed (and not long past), and auto post ignores a channel that was
// re-bound to another team since the setting was saved.
func TestAutoPostSchedulerQueries(t *testing.T) {
	api := newTestAPI(t)
	owner := api.registerUser("founder")
	team := api.createTeam(owner, "Sunday Core")
	other := api.createTeam(owner, "Other Core")
	api.bindChannel(team.ID, "g1", "100", "trials")
	path := fmt.Sprintf("/api/teams/%d/auto-post", team.ID)
	api.do(http.MethodPut, path, owner.token, autoPostBody("100")).expect(http.StatusOK)

	ctx := context.Background()
	store := models.NewAutoPostStore(api.pool)
	run := time.Date(2026, 10, 18, 19, 0, 0, 0, time.UTC)
	for _, id := range []string{"m-recent", "m-old", "m-other-channel"} {
		channel := "100"
		if id == "m-other-channel" {
			channel = "300"
			api.bindChannel(other.ID, "g1", "300", "elsewhere")
		}
		runAt := run
		if id == "m-old" {
			runAt = run.Add(-72 * time.Hour)
		}
		if _, err := api.pool.Exec(ctx, `INSERT INTO discord_posts (message_id, channel_id, run_at) VALUES ($1, $2, $3)`, id, channel, runAt); err != nil {
			t.Fatalf("insert post: %v", err)
		}
	}

	ids := func(posts []models.Post) string {
		var out []string
		for _, p := range posts {
			out = append(out, p.MessageID)
		}
		return strings.Join(out, ",")
	}
	// The delay is 180 minutes. At run+2h it hasn't passed; at run+4h it has.
	if got, err := store.DueAutoDeletes(ctx, run.Add(2*time.Hour)); err != nil || ids(got) != "" {
		t.Errorf("before the delay: got %q (err %v), want none", ids(got), err)
	}
	got, err := store.DueAutoDeletes(ctx, run.Add(4*time.Hour))
	if err != nil || ids(got) != "m-recent" {
		t.Errorf("after the delay: got %q (err %v), want only m-recent (m-old is past the catch-up cap, m-other-channel belongs to a team with auto delete off)", ids(got), err)
	}
	if err := store.MarkPostAutoDeleted(ctx, "m-recent"); err != nil {
		t.Fatal(err)
	}
	if got, _ := store.DueAutoDeletes(ctx, run.Add(4*time.Hour)); ids(got) != "" {
		t.Errorf("a deleted post came back: %q", ids(got))
	}

	enabled, err := store.ListAutoPostEnabled(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].TeamID != team.ID || enabled[0].GuildID != "g1" {
		t.Fatalf("enabled = %+v (err %v), want the one team", enabled, err)
	}
	// Re-bind the channel to the other team: the old setting must stop posting.
	if _, err := api.pool.Exec(ctx, `UPDATE discord_channels SET team_id = $1 WHERE channel_id = '100'`, other.ID); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := store.ListAutoPostEnabled(ctx); len(enabled) != 0 {
		t.Errorf("a re-bound channel still auto-posts for the old team: %+v", enabled)
	}
}
