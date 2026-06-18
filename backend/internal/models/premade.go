package models

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrPremadeRunNotFound is returned when a run lookup matches nothing.
var ErrPremadeRunNotFound = errors.New("premade run not found")

// ErrSlotTaken is returned when claiming a slot that another user already holds.
var ErrSlotTaken = errors.New("slot already taken")

// PremadeRun is one posted pre-made trial run. The bookkeeping timestamps let
// the bot's scheduler create the thread (15 min before) and clean up (2 h after)
// exactly once, surviving restarts.
type PremadeRun struct {
	ID              int64
	TeamID          int64
	GuildID         string
	ChannelID       string
	MessageID       string
	ThreadID        string
	Title           string
	ScheduledAt     time.Time
	CreatedBy       *int64
	ThreadStartedAt *time.Time
	CleanedUpAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PremadeSignup is one claimed roster slot on a run.
type PremadeSignup struct {
	Slot            int
	DiscordUserID   string
	DiscordUsername string
}

// PremadeWaitlistEntry is one user waiting for a given role on a run. CreatedAt
// orders the queue (FIFO).
type PremadeWaitlistEntry struct {
	ID              int64
	Role            string
	DiscordUserID   string
	DiscordUsername string
	CreatedAt       time.Time
}

// PremadeStore provides data access for pre-made runs and their slot signups.
type PremadeStore struct {
	pool *pgxpool.Pool
}

// NewPremadeStore constructs a PremadeStore backed by the given pool.
func NewPremadeStore(pool *pgxpool.Pool) *PremadeStore {
	return &PremadeStore{pool: pool}
}

const premadeRunCols = `id, team_id, guild_id, channel_id, message_id, thread_id, title, scheduled_at, created_by, thread_started_at, cleaned_up_at, created_at, updated_at`

func scanRun(row pgx.Row) (*PremadeRun, error) {
	r := &PremadeRun{}
	err := row.Scan(
		&r.ID, &r.TeamID, &r.GuildID, &r.ChannelID, &r.MessageID, &r.ThreadID,
		&r.Title, &r.ScheduledAt, &r.CreatedBy, &r.ThreadStartedAt, &r.CleanedUpAt,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// CreateRun inserts a new run (without a message id yet — set it with
// SetRunMessage once the announcement is posted).
func (s *PremadeStore) CreateRun(ctx context.Context, teamID int64, guildID, channelID, title string, scheduledAt time.Time, createdBy int64) (*PremadeRun, error) {
	const q = `
		INSERT INTO premade_runs (team_id, guild_id, channel_id, title, scheduled_at, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + premadeRunCols
	return scanRun(s.pool.QueryRow(ctx, q, teamID, guildID, channelID, title, scheduledAt, createdBy))
}

// SetRunMessage records the posted announcement's message id.
func (s *PremadeStore) SetRunMessage(ctx context.Context, runID int64, messageID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE premade_runs SET message_id = $1 WHERE id = $2`, messageID, runID)
	return err
}

// GetRun returns a run by id.
func (s *PremadeStore) GetRun(ctx context.Context, runID int64) (*PremadeRun, error) {
	const q = `SELECT ` + premadeRunCols + ` FROM premade_runs WHERE id = $1`
	r, err := scanRun(s.pool.QueryRow(ctx, q, runID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPremadeRunNotFound
	}
	return r, err
}

// GetRunByMessage returns the run posted as the given Discord message.
func (s *PremadeStore) GetRunByMessage(ctx context.Context, messageID string) (*PremadeRun, error) {
	const q = `SELECT ` + premadeRunCols + ` FROM premade_runs WHERE message_id = $1`
	r, err := scanRun(s.pool.QueryRow(ctx, q, messageID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPremadeRunNotFound
	}
	return r, err
}

// ClaimSlot claims a slot for a user. One slot per user: any prior claim by the
// same user on this run is released first, then the new slot is taken. If the
// target slot is already held by someone else, the whole operation is rolled
// back (preserving the user's prior claim) and ErrSlotTaken is returned.
func (s *PremadeStore) ClaimSlot(ctx context.Context, runID int64, slot int, discordUserID, discordUsername string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM premade_signups WHERE run_id = $1 AND discord_user_id = $2`, runID, discordUserID); err != nil {
		return err
	}
	const ins = `
		INSERT INTO premade_signups (run_id, slot, discord_user_id, discord_username)
		VALUES ($1, $2, $3, $4)`
	if _, err := tx.Exec(ctx, ins, runID, slot, discordUserID, discordUsername); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrSlotTaken
		}
		return err
	}
	return tx.Commit(ctx)
}

// LeaveSlot releases the user's claim on this run (if any). It is a no-op when
// they hold no slot.
func (s *PremadeStore) LeaveSlot(ctx context.Context, runID int64, discordUserID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM premade_signups WHERE run_id = $1 AND discord_user_id = $2`, runID, discordUserID)
	return err
}

// ListSignups returns the run's claimed slots, ordered by slot.
func (s *PremadeStore) ListSignups(ctx context.Context, runID int64) ([]PremadeSignup, error) {
	const q = `
		SELECT slot, discord_user_id, discord_username
		FROM premade_signups WHERE run_id = $1 ORDER BY slot`
	rows, err := s.pool.Query(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PremadeSignup
	for rows.Next() {
		var sg PremadeSignup
		if err := rows.Scan(&sg.Slot, &sg.DiscordUserID, &sg.DiscordUsername); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, rows.Err()
}

// JoinWaitlist puts a user on the run's waitlist for a role. One entry per user
// per run: switching roles replaces the entry and resets queue position.
func (s *PremadeStore) JoinWaitlist(ctx context.Context, runID int64, role, discordUserID, discordUsername string) error {
	const q = `
		INSERT INTO premade_waitlist (run_id, role, discord_user_id, discord_username)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (run_id, discord_user_id)
		DO UPDATE SET role = EXCLUDED.role, discord_username = EXCLUDED.discord_username, created_at = now()`
	_, err := s.pool.Exec(ctx, q, runID, role, discordUserID, discordUsername)
	return err
}

// LeaveWaitlist removes the user's waitlist entry on this run (if any).
func (s *PremadeStore) LeaveWaitlist(ctx context.Context, runID int64, discordUserID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM premade_waitlist WHERE run_id = $1 AND discord_user_id = $2`, runID, discordUserID)
	return err
}

// ListWaitlist returns the run's waitlist entries in queue order.
func (s *PremadeStore) ListWaitlist(ctx context.Context, runID int64) ([]PremadeWaitlistEntry, error) {
	const q = `
		SELECT id, role, discord_user_id, discord_username, created_at
		FROM premade_waitlist WHERE run_id = $1 ORDER BY created_at, id`
	rows, err := s.pool.Query(ctx, q, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PremadeWaitlistEntry
	for rows.Next() {
		var e PremadeWaitlistEntry
		if err := rows.Scan(&e.ID, &e.Role, &e.DiscordUserID, &e.DiscordUsername, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PromoteToSlot fills a freshly-opened slot from the head of that role's
// waitlist, all in one transaction. It returns the promoted entry (and true)
// when someone was moved in; (nil, false) when the role's waitlist is empty or
// the slot was taken first (leaving the waitlist intact).
func (s *PremadeStore) PromoteToSlot(ctx context.Context, runID int64, slot int, role string) (*PremadeWaitlistEntry, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)

	var e PremadeWaitlistEntry
	err = tx.QueryRow(ctx, `
		SELECT id, role, discord_user_id, discord_username, created_at
		FROM premade_waitlist
		WHERE run_id = $1 AND role = $2
		ORDER BY created_at, id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, runID, role).Scan(&e.ID, &e.Role, &e.DiscordUserID, &e.DiscordUsername, &e.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	// Defensive: release any prior claim the promotee holds, then take the slot.
	if _, err := tx.Exec(ctx, `DELETE FROM premade_signups WHERE run_id = $1 AND discord_user_id = $2`, runID, e.DiscordUserID); err != nil {
		return nil, false, err
	}
	ct, err := tx.Exec(ctx, `
		INSERT INTO premade_signups (run_id, slot, discord_user_id, discord_username)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (run_id, slot) DO NOTHING`, runID, slot, e.DiscordUserID, e.DiscordUsername)
	if err != nil {
		return nil, false, err
	}
	if ct.RowsAffected() == 0 {
		// Slot was taken first; don't consume the waitlist entry (rollback).
		return nil, false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM premade_waitlist WHERE id = $1`, e.ID); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	return &e, true, nil
}

// DueThreadRuns returns posted runs whose thread should be started now (within
// 15 minutes of the scheduled time) but hasn't been, and that aren't past
// cleanup. Catch-up safe: if the bot was offline, these are returned as soon as
// it polls again.
func (s *PremadeStore) DueThreadRuns(ctx context.Context, now time.Time) ([]PremadeRun, error) {
	const q = `
		SELECT ` + premadeRunCols + `
		FROM premade_runs
		WHERE message_id <> ''
		  AND thread_started_at IS NULL
		  AND cleaned_up_at IS NULL
		  AND $1 >= scheduled_at - INTERVAL '15 minutes'
		  AND $1 < scheduled_at + INTERVAL '2 hours'
		ORDER BY scheduled_at`
	return s.queryRuns(ctx, q, now)
}

// DueCleanupRuns returns posted runs whose cleanup is due (2 h past the
// scheduled time) but hasn't run yet.
func (s *PremadeStore) DueCleanupRuns(ctx context.Context, now time.Time) ([]PremadeRun, error) {
	const q = `
		SELECT ` + premadeRunCols + `
		FROM premade_runs
		WHERE cleaned_up_at IS NULL
		  AND $1 >= scheduled_at + INTERVAL '2 hours'
		ORDER BY scheduled_at`
	return s.queryRuns(ctx, q, now)
}

func (s *PremadeStore) queryRuns(ctx context.Context, q string, args ...any) ([]PremadeRun, error) {
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PremadeRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// MarkThreadStarted records that the run's thread was created.
func (s *PremadeStore) MarkThreadStarted(ctx context.Context, runID int64, threadID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE premade_runs SET thread_id = $1, thread_started_at = now() WHERE id = $2`, threadID, runID)
	return err
}

// MarkCleanedUp records that the run's post and thread were cleaned up.
func (s *PremadeStore) MarkCleanedUp(ctx context.Context, runID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE premade_runs SET cleaned_up_at = now() WHERE id = $1`, runID)
	return err
}
