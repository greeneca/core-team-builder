package models

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Bounds for the auto post lead time and auto delete delay, in minutes. They
// mirror the CHECK constraints in migration 060: 1 hour to 30 days.
const (
	MinAutoPostMinutes = 60
	MaxAutoPostMinutes = 30 * 24 * 60
)

// Defaults used until a team saves its own settings.
const (
	DefaultAutoPostLeadMinutes    = 24 * 60
	DefaultAutoDeleteDelayMinutes = 2 * 60
)

// ErrAutoPostChannelInvalid is returned when the chosen auto post channel is not
// bound to the team. It covers "unknown channel" and "another team's channel"
// alike so a caller cannot probe which channels exist.
var ErrAutoPostChannelInvalid = errors.New("auto post channel is not bound to this team")

// AutoPostSettings are a team's auto post / auto delete options.
type AutoPostSettings struct {
	AutoPostEnabled        bool   `json:"auto_post_enabled"`
	AutoPostChannelID      string `json:"auto_post_channel_id"`
	AutoPostLeadMinutes    int    `json:"auto_post_lead_minutes"`
	AutoDeleteEnabled      bool   `json:"auto_delete_enabled"`
	AutoDeleteDelayMinutes int    `json:"auto_delete_delay_minutes"`
}

// DefaultAutoPostSettings returns the settings of a team that never saved any:
// both features off, with sensible durations ready for when they are enabled.
func DefaultAutoPostSettings() AutoPostSettings {
	return AutoPostSettings{
		AutoPostLeadMinutes:    DefaultAutoPostLeadMinutes,
		AutoDeleteDelayMinutes: DefaultAutoDeleteDelayMinutes,
	}
}

// Validate checks the settings are storable. Durations are bounded even while a
// feature is off, so the stored value is always sane; the channel is only
// required once auto post is on. That the channel really belongs to the team is
// enforced by the store, which can see the bindings.
func (a AutoPostSettings) Validate() error {
	if a.AutoPostLeadMinutes < MinAutoPostMinutes || a.AutoPostLeadMinutes > MaxAutoPostMinutes {
		return fmt.Errorf("auto post lead time must be between 1 hour and 30 days")
	}
	if a.AutoDeleteDelayMinutes < MinAutoPostMinutes || a.AutoDeleteDelayMinutes > MaxAutoPostMinutes {
		return fmt.Errorf("auto delete delay must be between 1 hour and 30 days")
	}
	if a.AutoPostEnabled && a.AutoPostChannelID == "" {
		return fmt.Errorf("choose a channel for auto post")
	}
	return nil
}

// BoundChannel is a Discord channel bound to a team, as offered in the auto post
// channel dropdown. Name is ” until the bot has recorded it.
type BoundChannel struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
}

// AutoPostDue is a team whose auto post is switched on, with what the scheduler
// needs to decide whether it is time to post.
type AutoPostDue struct {
	TeamID       int64
	GuildID      string
	ChannelID    string
	LeadMinutes  int
	LastPostedAt *time.Time
}

// AutoPostStore provides data access for auto post / auto delete settings and
// the scheduler's bookkeeping.
type AutoPostStore struct {
	pool *pgxpool.Pool
}

// NewAutoPostStore constructs an AutoPostStore backed by the given pool.
func NewAutoPostStore(pool *pgxpool.Pool) *AutoPostStore {
	return &AutoPostStore{pool: pool}
}

// Get returns a team's settings, or the defaults when none were saved.
func (s *AutoPostStore) Get(ctx context.Context, teamID int64) (AutoPostSettings, error) {
	const q = `
		SELECT auto_post_enabled, COALESCE(auto_post_channel_id, ''), auto_post_lead_minutes,
		       auto_delete_enabled, auto_delete_delay_minutes
		FROM team_auto_post WHERE team_id = $1`
	var a AutoPostSettings
	err := s.pool.QueryRow(ctx, q, teamID).Scan(&a.AutoPostEnabled, &a.AutoPostChannelID,
		&a.AutoPostLeadMinutes, &a.AutoDeleteEnabled, &a.AutoDeleteDelayMinutes)
	if errors.Is(err, pgx.ErrNoRows) {
		return DefaultAutoPostSettings(), nil
	}
	return a, err
}

// Save stores a team's settings (the caller has already run Validate). A chosen
// channel must be bound to this team: it is looked up in discord_channels
// filtered by team, so a channel from another team yields
// ErrAutoPostChannelInvalid. An empty channel clears the selection.
func (s *AutoPostStore) Save(ctx context.Context, teamID int64, a AutoPostSettings) error {
	var channel *string
	if a.AutoPostChannelID != "" {
		const check = `SELECT 1 FROM discord_channels WHERE channel_id = $1 AND team_id = $2`
		var one int
		err := s.pool.QueryRow(ctx, check, a.AutoPostChannelID, teamID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrAutoPostChannelInvalid
		}
		if err != nil {
			return err
		}
		channel = &a.AutoPostChannelID
	}
	const q = `
		INSERT INTO team_auto_post (team_id, auto_post_enabled, auto_post_channel_id, auto_post_lead_minutes,
		                            auto_delete_enabled, auto_delete_delay_minutes, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (team_id) DO UPDATE SET
			auto_post_enabled = EXCLUDED.auto_post_enabled,
			auto_post_channel_id = EXCLUDED.auto_post_channel_id,
			auto_post_lead_minutes = EXCLUDED.auto_post_lead_minutes,
			auto_delete_enabled = EXCLUDED.auto_delete_enabled,
			auto_delete_delay_minutes = EXCLUDED.auto_delete_delay_minutes,
			updated_at = now()`
	_, err := s.pool.Exec(ctx, q, teamID, a.AutoPostEnabled, channel, a.AutoPostLeadMinutes,
		a.AutoDeleteEnabled, a.AutoDeleteDelayMinutes)
	return err
}

// ListChannels returns the channels bound to a team, for the channel dropdown.
func (s *AutoPostStore) ListChannels(ctx context.Context, teamID int64) ([]BoundChannel, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT channel_id, channel_name FROM discord_channels WHERE team_id = $1 ORDER BY channel_name, channel_id`, teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BoundChannel{}
	for rows.Next() {
		var c BoundChannel
		if err := rows.Scan(&c.ChannelID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SetChannelName records a bound channel's display name.
func (s *AutoPostStore) SetChannelName(ctx context.Context, channelID, name string) error {
	_, err := s.pool.Exec(ctx, `UPDATE discord_channels SET channel_name = $1 WHERE channel_id = $2`, name, channelID)
	return err
}

// UnnamedChannels returns the IDs of bound channels with no recorded name, so the
// bot can backfill them.
func (s *AutoPostStore) UnnamedChannels(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT channel_id FROM discord_channels WHERE channel_name = ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListAutoPostEnabled returns every regular (non-template) team with auto post
// on whose chosen channel is still bound to it. The join on team_id is the
// guard against a channel that was re-bound to a different team since the
// setting was saved.
func (s *AutoPostStore) ListAutoPostEnabled(ctx context.Context) ([]AutoPostDue, error) {
	const q = `
		SELECT a.team_id, c.guild_id, c.channel_id, a.auto_post_lead_minutes, a.auto_posted_run_at
		FROM team_auto_post a
		JOIN teams t ON t.id = a.team_id
		JOIN discord_channels c ON c.channel_id = a.auto_post_channel_id AND c.team_id = a.team_id
		WHERE a.auto_post_enabled AND NOT t.pre_made`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AutoPostDue
	for rows.Next() {
		var d AutoPostDue
		if err := rows.Scan(&d.TeamID, &d.GuildID, &d.ChannelID, &d.LeadMinutes, &d.LastPostedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// MarkAutoPosted records the run instant a team was just auto-posted for, so the
// same run is never posted twice.
func (s *AutoPostStore) MarkAutoPosted(ctx context.Context, teamID int64, runAt time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE team_auto_post SET auto_posted_run_at = $2 WHERE team_id = $1`, teamID, runAt)
	return err
}

// autoDeleteCatchUp is how long past its due time a post is still deleted. It
// lets a bot that was briefly offline catch up, while stopping a newly enabled
// setting from sweeping away old posts.
const autoDeleteCatchUp = 24 * time.Hour

// DueAutoDeletes returns tracked posts due for deletion: the post's team has
// auto delete on, the delay after its run time has passed (but by no more than
// autoDeleteCatchUp), and it hasn't been deleted already. The post's team is
// found through its channel's binding.
func (s *AutoPostStore) DueAutoDeletes(ctx context.Context, now time.Time) ([]Post, error) {
	const q = `
		SELECT p.message_id, p.channel_id, p.thread_id, p.run_at
		FROM discord_posts p
		JOIN discord_channels c ON c.channel_id = p.channel_id
		JOIN team_auto_post a ON a.team_id = c.team_id
		WHERE a.auto_delete_enabled
		  AND p.auto_deleted_at IS NULL
		  AND p.run_at IS NOT NULL
		  AND $1 >= p.run_at + a.auto_delete_delay_minutes * INTERVAL '1 minute'
		  AND $1 <  p.run_at + a.auto_delete_delay_minutes * INTERVAL '1 minute' + $2 * INTERVAL '1 second'
		ORDER BY p.run_at`
	rows, err := s.pool.Query(ctx, q, now, autoDeleteCatchUp.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.MessageID, &p.ChannelID, &p.ThreadID, &p.RunAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPostAutoDeleted records that a post was auto-deleted so it isn't retried.
func (s *AutoPostStore) MarkPostAutoDeleted(ctx context.Context, messageID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE discord_posts SET auto_deleted_at = now() WHERE message_id = $1`, messageID)
	return err
}
