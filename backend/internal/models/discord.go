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

// RSVP attendance statuses for a posted trial.
const (
	RSVPYes = "yes"
	RSVPNo  = "no"
)

// RSVP is one Discord user's attendance response to a posted trial overview.
type RSVP struct {
	DiscordUserID   string
	DiscordUsername string
	Status          string
}

// SetRSVP records (or updates) a user's attendance response for a posted message.
// A user has at most one RSVP per message; pressing the other button overwrites
// the prior choice.
func (s *DiscordStore) SetRSVP(ctx context.Context, messageID, channelID, discordUserID, discordUsername, status string) error {
	const q = `
		INSERT INTO discord_rsvps (message_id, channel_id, discord_user_id, discord_username, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (message_id, discord_user_id)
		DO UPDATE SET status = EXCLUDED.status,
		              discord_username = EXCLUDED.discord_username,
		              channel_id = EXCLUDED.channel_id,
		              updated_at = now()`
	_, err := s.pool.Exec(ctx, q, messageID, channelID, discordUserID, discordUsername, status)
	return err
}

// ListRSVPs returns all attendance responses for a posted message, ordered by
// when each was last set (so the displayed lists are stable).
func (s *DiscordStore) ListRSVPs(ctx context.Context, messageID string) ([]RSVP, error) {
	const q = `
		SELECT discord_user_id, discord_username, status
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
		if err := rows.Scan(&r.DiscordUserID, &r.DiscordUsername, &r.Status); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
