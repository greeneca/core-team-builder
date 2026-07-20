package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLinkCodeInvalid is returned when a presented Discord link code is unknown,
// expired, or already used. A single error covers all cases so callers cannot
// distinguish them.
var ErrLinkCodeInvalid = errors.New("discord link code invalid")

// ErrDiscordAlreadyLinked is returned when a Discord identity is already linked
// to a different app account.
var ErrDiscordAlreadyLinked = errors.New("discord account already linked to another user")

// ErrChannelNotBound is returned when a channel has no team binding.
var ErrChannelNotBound = errors.New("channel not bound to a team")

// DiscordLink is a user's linked Discord identity.
type DiscordLink struct {
	Linked          bool   `json:"linked"`
	DiscordUserID   string `json:"discord_user_id,omitempty"`
	DiscordUsername string `json:"discord_username,omitempty"`
}

// DiscordStore provides data access for Discord account links, one-time link
// codes, and channel-to-team bindings. Link codes are stored only as a SHA-256
// hash, mirroring password_resets; plaintext codes never touch the database.
type DiscordStore struct {
	pool *pgxpool.Pool
}

// NewDiscordStore constructs a DiscordStore backed by the given pool.
func NewDiscordStore(pool *pgxpool.Pool) *DiscordStore {
	return &DiscordStore{pool: pool}
}

// CreateLinkCode persists a new link-code hash for a user with the given expiry.
func (s *DiscordStore) CreateLinkCode(ctx context.Context, userID int64, codeHash string, expiresAt time.Time) error {
	const q = `
		INSERT INTO discord_link_codes (user_id, code_hash, expires_at)
		VALUES ($1, $2, $3)`
	_, err := s.pool.Exec(ctx, q, userID, codeHash, expiresAt)
	return err
}

// ConsumeLinkCode atomically validates and marks a link code as used (single
// use), returning the owning user ID. It returns ErrLinkCodeInvalid when the
// code is unknown, expired, or already used.
func (s *DiscordStore) ConsumeLinkCode(ctx context.Context, codeHash string) (int64, error) {
	const q = `
		UPDATE discord_link_codes
		SET used_at = now()
		WHERE code_hash = $1
		  AND used_at IS NULL
		  AND expires_at > now()
		RETURNING user_id`
	var userID int64
	err := s.pool.QueryRow(ctx, q, codeHash).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrLinkCodeInvalid
	}
	if err != nil {
		return 0, err
	}
	return userID, nil
}

// InvalidateLinkCodesForUser marks every outstanding (unused) link code for a
// user as used, so generating a new code cancels older ones.
func (s *DiscordStore) InvalidateLinkCodesForUser(ctx context.Context, userID int64) error {
	const q = `UPDATE discord_link_codes SET used_at = now() WHERE user_id = $1 AND used_at IS NULL`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}

// DeleteExpiredLinkCodes removes link codes that expired or were used. Intended
// for periodic housekeeping.
func (s *DiscordStore) DeleteExpiredLinkCodes(ctx context.Context) error {
	const q = `DELETE FROM discord_link_codes WHERE expires_at < now() OR used_at IS NOT NULL`
	_, err := s.pool.Exec(ctx, q)
	return err
}

// LinkUser records a Discord identity on an app account. It returns
// ErrDiscordAlreadyLinked when that Discord identity is already linked to a
// different user.
func (s *DiscordStore) LinkUser(ctx context.Context, userID int64, discordUserID, discordUsername string) error {
	const q = `UPDATE users SET discord_user_id = $1, discord_username = $2 WHERE id = $3`
	_, err := s.pool.Exec(ctx, q, discordUserID, discordUsername, userID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrDiscordAlreadyLinked
	}
	return err
}

// UnlinkUser clears a user's Discord link.
func (s *DiscordStore) UnlinkUser(ctx context.Context, userID int64) error {
	const q = `UPDATE users SET discord_user_id = NULL, discord_username = '' WHERE id = $1`
	_, err := s.pool.Exec(ctx, q, userID)
	return err
}

// GetLink returns a user's linked Discord identity (Linked=false when unset).
func (s *DiscordStore) GetLink(ctx context.Context, userID int64) (DiscordLink, error) {
	const q = `SELECT discord_user_id, discord_username FROM users WHERE id = $1`
	var did *string
	var dname string
	err := s.pool.QueryRow(ctx, q, userID).Scan(&did, &dname)
	if errors.Is(err, pgx.ErrNoRows) {
		return DiscordLink{}, ErrUserNotFound
	}
	if err != nil {
		return DiscordLink{}, err
	}
	if did == nil || *did == "" {
		return DiscordLink{Linked: false}, nil
	}
	return DiscordLink{Linked: true, DiscordUserID: *did, DiscordUsername: dname}, nil
}

// GetUserByDiscordID returns the app user ID linked to a Discord identity, or
// ErrUserNotFound when none is linked.
func (s *DiscordStore) GetUserByDiscordID(ctx context.Context, discordUserID string) (int64, error) {
	const q = `SELECT id FROM users WHERE discord_user_id = $1`
	var id int64
	err := s.pool.QueryRow(ctx, q, discordUserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrUserNotFound
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}

// GetUserTimezone returns a user's remembered IANA timezone, or "" when unset.
func (s *DiscordStore) GetUserTimezone(ctx context.Context, userID int64) (string, error) {
	const q = `SELECT timezone FROM users WHERE id = $1`
	var tz string
	err := s.pool.QueryRow(ctx, q, userID).Scan(&tz)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrUserNotFound
	}
	if err != nil {
		return "", err
	}
	return tz, nil
}

// SetUserTimezone stores a user's IANA timezone so future signup flows can
// resolve natural-language times without re-asking.
func (s *DiscordStore) SetUserTimezone(ctx context.Context, userID int64, tz string) error {
	const q = `UPDATE users SET timezone = $1 WHERE id = $2`
	_, err := s.pool.Exec(ctx, q, tz, userID)
	return err
}

// BindChannel binds a Discord channel to a team (upsert on channel_id).
func (s *DiscordStore) BindChannel(ctx context.Context, guildID, channelID string, teamID, setByUserID int64) error {
	const q = `
		INSERT INTO discord_channels (guild_id, channel_id, team_id, set_by_user_id, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (channel_id)
		DO UPDATE SET guild_id = EXCLUDED.guild_id, team_id = EXCLUDED.team_id,
		              set_by_user_id = EXCLUDED.set_by_user_id, updated_at = now()`
	_, err := s.pool.Exec(ctx, q, guildID, channelID, teamID, setByUserID)
	return err
}

// GetChannelTeam returns the team ID bound to a channel, or ErrChannelNotBound.
func (s *DiscordStore) GetChannelTeam(ctx context.Context, channelID string) (int64, error) {
	const q = `SELECT team_id FROM discord_channels WHERE channel_id = $1`
	var teamID int64
	err := s.pool.QueryRow(ctx, q, channelID).Scan(&teamID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrChannelNotBound
	}
	if err != nil {
		return 0, err
	}
	return teamID, nil
}

// UnbindChannel removes a channel's team binding. It is idempotent.
func (s *DiscordStore) UnbindChannel(ctx context.Context, channelID string) error {
	const q = `DELETE FROM discord_channels WHERE channel_id = $1`
	_, err := s.pool.Exec(ctx, q, channelID)
	return err
}

// AddEditRole designates a Discord role (in a guild) as allowed to use the
// restricted run buttons. Idempotent: re-adding the same role is a no-op.
func (s *DiscordStore) AddEditRole(ctx context.Context, guildID, roleID string) error {
	const q = `
		INSERT INTO discord_edit_roles (guild_id, role_id)
		VALUES ($1, $2)
		ON CONFLICT (guild_id, role_id) DO NOTHING`
	_, err := s.pool.Exec(ctx, q, guildID, roleID)
	return err
}

// RemoveEditRole revokes a role's permission to use the restricted run buttons.
// It is idempotent (removing a role that isn't designated is a no-op).
func (s *DiscordStore) RemoveEditRole(ctx context.Context, guildID, roleID string) error {
	const q = `DELETE FROM discord_edit_roles WHERE guild_id = $1 AND role_id = $2`
	_, err := s.pool.Exec(ctx, q, guildID, roleID)
	return err
}

// ListEditRoles returns the Discord role IDs designated as run editors for a
// guild, newest first. Empty when none are set.
func (s *DiscordStore) ListEditRoles(ctx context.Context, guildID string) ([]string, error) {
	const q = `SELECT role_id FROM discord_edit_roles WHERE guild_id = $1 ORDER BY created_at DESC`
	rows, err := s.pool.Query(ctx, q, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		roles = append(roles, id)
	}
	return roles, rows.Err()
}

// RSVP attendance statuses for a posted trial.
const (
	RSVPYes = "yes"
	RSVPNo  = "no"
)

// RSVP is one Discord user's attendance response to a posted trial overview.
type RSVP struct {
	DiscordUserID string
	// DiscordUsername is the responder's Discord username (the unique @handle),
	// and DiscordGlobalName their display/global name. Both are captured so the
	// post's status marks can match an RSVP to a roster slot whose discord_handle
	// is set to either form (mirroring the live user the buttons see).
	DiscordUsername   string
	DiscordGlobalName string
	Status            string
}

// SetRSVP records (or updates) a user's attendance response for a posted message.
// A user has at most one RSVP per message; pressing the other button overwrites
// the prior choice. Both the username and the global (display) name are stored so
// slot matching works regardless of which the roster's discord_handle uses.
func (s *DiscordStore) SetRSVP(ctx context.Context, messageID, channelID, discordUserID, discordUsername, discordGlobalName, status string) error {
	const q = `
		INSERT INTO discord_rsvps (message_id, channel_id, discord_user_id, discord_username, discord_global_name, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (message_id, discord_user_id)
		DO UPDATE SET status = EXCLUDED.status,
		              discord_username = EXCLUDED.discord_username,
		              discord_global_name = EXCLUDED.discord_global_name,
		              channel_id = EXCLUDED.channel_id,
		              updated_at = now()`
	_, err := s.pool.Exec(ctx, q, messageID, channelID, discordUserID, discordUsername, discordGlobalName, status)
	return err
}

// ListRSVPs returns all attendance responses for a posted message, ordered by
// when each was last set (so the displayed lists are stable).
func (s *DiscordStore) ListRSVPs(ctx context.Context, messageID string) ([]RSVP, error) {
	const q = `
		SELECT discord_user_id, discord_username, discord_global_name, status
		FROM discord_rsvps
		WHERE message_id = $1
		ORDER BY updated_at`
	rows, err := s.pool.Query(ctx, q, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RSVP
	for rows.Next() {
		var r RSVP
		if err := rows.Scan(&r.DiscordUserID, &r.DiscordUsername, &r.DiscordGlobalName, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Post is a tracked /coreteam post overview message, used by the scheduler to
// ping attendees in its discussion thread ~15 minutes before the run and to
// remind assigned roster members who haven't RSVP'd ~48 hours before it. RunAt
// is the post's next-run time (nil when the team has no concrete schedule, so
// neither fires); PingedAt is set once the pre-run ping has been sent, and
// RemindedAt once the RSVP reminder has been sent.
type Post struct {
	MessageID  string
	ChannelID  string
	ThreadID   string
	RunAt      *time.Time
	PingedAt   *time.Time
	RemindedAt *time.Time
}

// RecordPost tracks a posted overview message so the scheduler can ping its
// thread before the run. runAt is the next-run time (nil = no schedule, never
// pinged). Upserts on message_id so a re-render of the same message is a no-op
// on the bookkeeping.
func (s *DiscordStore) RecordPost(ctx context.Context, messageID, channelID string, runAt *time.Time) error {
	// The run date is locked at first post time: COALESCE keeps any already-set
	// run_at on conflict so re-recording the same message can never move the
	// advertised date. A NULL is still fillable (e.g. the schedule was set after
	// posting), but once set it stays put.
	const q = `
		INSERT INTO discord_posts (message_id, channel_id, run_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (message_id)
		DO UPDATE SET channel_id = EXCLUDED.channel_id,
		              run_at = COALESCE(discord_posts.run_at, EXCLUDED.run_at)`
	_, err := s.pool.Exec(ctx, q, messageID, channelID, runAt)
	return err
}

// GetPostRunAt returns the run date locked for a tracked post as a Unix
// timestamp (seconds), or 0 when the post has no recorded run time (NULL run_at)
// or isn't tracked. Used to re-render a post with the date fixed at first post
// time (see BuildPost) instead of recomputing it from the live team schedule.
func (s *DiscordStore) GetPostRunAt(ctx context.Context, messageID string) (int64, error) {
	var runAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT run_at FROM discord_posts WHERE message_id = $1`, messageID).Scan(&runAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if runAt == nil {
		return 0, nil
	}
	return runAt.Unix(), nil
}

// SetPostThread records the discussion thread opened off a tracked post.
func (s *DiscordStore) SetPostThread(ctx context.Context, messageID, threadID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE discord_posts SET thread_id = $1 WHERE message_id = $2`, threadID, messageID)
	return err
}

// DuePostPings returns tracked posts whose pre-run ping is due now (within 15
// minutes before the run, up to the start time) but hasn't been sent, and that
// have a discussion thread to ping. Catch-up safe: a post missed while the bot
// was offline is returned as soon as it polls again, as long as the run hasn't
// started yet.
func (s *DiscordStore) DuePostPings(ctx context.Context, now time.Time) ([]Post, error) {
	const q = `
		SELECT message_id, channel_id, thread_id, run_at, pinged_at
		FROM discord_posts
		WHERE pinged_at IS NULL
		  AND thread_id <> ''
		  AND run_at IS NOT NULL
		  AND $1 >= run_at - INTERVAL '15 minutes'
		  AND $1 < run_at
		ORDER BY run_at`
	rows, err := s.pool.Query(ctx, q, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.MessageID, &p.ChannelID, &p.ThreadID, &p.RunAt, &p.PingedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPostPinged records that a tracked post's pre-run ping has been sent, so it
// fires only once.
func (s *DiscordStore) MarkPostPinged(ctx context.Context, messageID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE discord_posts SET pinged_at = now() WHERE message_id = $1`, messageID)
	return err
}

// DueReminders returns tracked posts whose RSVP reminder is due now (within 48
// hours before the run, up to the start time) but hasn't been sent. Catch-up
// safe: a post created inside the 48-hour window, or missed while the bot was
// offline, is returned as soon as it polls again, as long as the run hasn't
// started yet. Unlike DuePostPings it doesn't require a thread — the reminder
// falls back to the post's channel when there's no discussion thread.
func (s *DiscordStore) DueReminders(ctx context.Context, now time.Time) ([]Post, error) {
	const q = `
		SELECT message_id, channel_id, thread_id, run_at, pinged_at, reminded_at
		FROM discord_posts
		WHERE reminded_at IS NULL
		  AND run_at IS NOT NULL
		  AND $1 >= run_at - INTERVAL '48 hours'
		  AND $1 < run_at
		ORDER BY run_at`
	rows, err := s.pool.Query(ctx, q, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.MessageID, &p.ChannelID, &p.ThreadID, &p.RunAt, &p.PingedAt, &p.RemindedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// MarkPostReminded records that a tracked post's RSVP reminder has been sent, so
// it fires only once.
func (s *DiscordStore) MarkPostReminded(ctx context.Context, messageID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE discord_posts SET reminded_at = now() WHERE message_id = $1`, messageID)
	return err
}

// MarkPostGone records that a tracked post's message no longer exists on Discord
// (it was deleted), so neither its pre-run ping nor its RSVP reminder fires
// again. Both one-shot markers are set (preserving any already-recorded time via
// COALESCE) so whichever scheduled action detects the deletion stops the other.
func (s *DiscordStore) MarkPostGone(ctx context.Context, messageID string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE discord_posts SET pinged_at = COALESCE(pinged_at, now()), reminded_at = COALESCE(reminded_at, now()) WHERE message_id = $1`,
		messageID)
	return err
}

// PostFillList is the sentinel slot value meaning "the general fill list" (a
// backup pool not tied to a specific roster slot). Any slot > 0 is a real open
// roster slot a single user can fill.
const PostFillList = 0

// PostFill is one Discord user's signup on a posted trial overview: either a
// specific open roster slot (Slot > 0) or the general fill list
// (Slot == PostFillList).
type PostFill struct {
	Slot            int
	DiscordUserID   string
	DiscordUsername string
}

// ClaimFill records the user's signup for a posted message: an open roster slot
// (slot > 0) or the general fill list (slot == PostFillList). A user holds at
// most one signup per message, so any prior choice is released first. When slot
// > 0 is already held by someone else, the operation rolls back and returns
// ErrSlotTaken (mirrors PremadeStore.ClaimSlot).
func (s *DiscordStore) ClaimFill(ctx context.Context, messageID, channelID string, slot int, discordUserID, discordUsername string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM discord_post_fills WHERE message_id = $1 AND discord_user_id = $2`, messageID, discordUserID); err != nil {
		return err
	}
	const ins = `
		INSERT INTO discord_post_fills (message_id, channel_id, slot, discord_user_id, discord_username, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())`
	if _, err := tx.Exec(ctx, ins, messageID, channelID, slot, discordUserID, discordUsername); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrSlotTaken
		}
		return err
	}
	return tx.Commit(ctx)
}

// LeaveFill removes the user's signup on a posted message (if any). Idempotent.
func (s *DiscordStore) LeaveFill(ctx context.Context, messageID, discordUserID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM discord_post_fills WHERE message_id = $1 AND discord_user_id = $2`, messageID, discordUserID)
	return err
}

// MoveFillToList moves a specific-slot fill (slot > 0) for a posted message to
// the general fill list (PostFillList), returning the moved fill. found=false
// when nobody was filling that slot. Used when a slot's assigned player returns
// (RSVPs "coming"): their slot is theirs again, so the displaced filler becomes
// a backup on the fill list rather than being dropped.
func (s *DiscordStore) MoveFillToList(ctx context.Context, messageID string, slot int) (PostFill, bool, error) {
	if slot <= 0 {
		return PostFill{}, false, nil
	}
	const q = `
		UPDATE discord_post_fills
		SET slot = $1, updated_at = now()
		WHERE message_id = $2 AND slot = $3
		RETURNING slot, discord_user_id, discord_username`
	var f PostFill
	err := s.pool.QueryRow(ctx, q, PostFillList, messageID, slot).Scan(&f.Slot, &f.DiscordUserID, &f.DiscordUsername)
	if errors.Is(err, pgx.ErrNoRows) {
		return PostFill{}, false, nil
	}
	if err != nil {
		return PostFill{}, false, err
	}
	return f, true, nil
}

// ListFills returns all signups for a posted message, ordered by when each was
// set (so displayed lists are stable).
func (s *DiscordStore) ListFills(ctx context.Context, messageID string) ([]PostFill, error) {
	const q = `
		SELECT slot, discord_user_id, discord_username
		FROM discord_post_fills
		WHERE message_id = $1
		ORDER BY updated_at, slot`
	rows, err := s.pool.Query(ctx, q, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PostFill
	for rows.Next() {
		var f PostFill
		if err := rows.Scan(&f.Slot, &f.DiscordUserID, &f.DiscordUsername); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
