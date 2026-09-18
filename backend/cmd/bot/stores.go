package main

import (
	"context"
	"time"

	"github.com/core-team-builder/backend/internal/models"
)

// The interfaces below are the slices of internal/models the bot's handlers
// actually call. They are declared here, at the consumer, rather than beside
// the implementations so the bot's dependencies are visible in one place and a
// test can substitute a fake without a database. The concrete *models.*Store
// types satisfy them implicitly; main() still passes the real ones.
//
// Keep these narrow: add a method only when a handler needs it.

// teamStore is the team data the bot reads and the sharing/publishing writes it
// performs on behalf of a Discord user.
type teamStore interface {
	Access(ctx context.Context, teamID, userID int64) (bool, string, error)
	AutoSharePoolEnabled(ctx context.Context, teamID int64) (bool, error)
	Create(ctx context.Context, ownerID int64, name string, copyFromTeamID int64) (*models.Team, error)
	Get(ctx context.Context, teamID int64) (*models.Team, error)
	ListForUser(ctx context.Context, userID int64) ([]models.Team, error)
	SharePoolMembers(ctx context.Context, teamID int64) error
	ShareAutoTeamsForDiscord(ctx context.Context, discordUserID string, userID int64) error

	// Guild-published signup templates, so a server can run someone else's
	// template with /coreteam signup.
	PublishTemplateToGuild(ctx context.Context, teamID int64, guildID string, publishedBy int64) error
	UnpublishTemplateFromGuild(ctx context.Context, teamID int64, guildID string) error
	IsTemplatePublishedToGuild(ctx context.Context, teamID int64, guildID string) (bool, error)
	ListPublishedTemplatesForGuild(ctx context.Context, guildID string) ([]models.Team, error)
}

// encounterStore is read-only for the bot: posts and build-detail DMs render
// the active roster's encounters but never edit them.
type encounterStore interface {
	Get(ctx context.Context, encounterID int64) (*models.Encounter, error)
	ListForRoster(ctx context.Context, rosterID int64) ([]models.Encounter, error)
}

// groupingStore is read-only for the bot, for the groupings section of a post.
type groupingStore interface {
	ListForRoster(ctx context.Context, rosterID int64) ([]models.Grouping, error)
}

// memberStore backs the /coreteam recruit DM intake, which persists the
// prospective member's answers onto a draft row as the questionnaire advances.
type memberStore interface {
	GetByID(ctx context.Context, id int64) (*models.RosterMember, error)
	SaveProgress(ctx context.Context, m *models.RosterMember) error
	UpsertDraft(ctx context.Context, teamID int64, discordUserID, username, displayName string) (*models.RosterMember, error)
}

// discordStore holds everything keyed by Discord identity: account links, the
// channel→team binding, per-guild permission/action-log settings, and the state
// of a posted overview (RSVPs, fills, thread, scheduled pings).
type discordStore interface {
	// Account linking.
	ConsumeLinkCode(ctx context.Context, codeHash string) (int64, error)
	LinkUser(ctx context.Context, userID int64, discordUserID, discordUsername string) error
	GetLink(ctx context.Context, userID int64) (models.DiscordLink, error)
	GetUserByDiscordID(ctx context.Context, discordUserID string) (int64, error)
	GetUserTimezone(ctx context.Context, userID int64) (string, error)
	SetUserTimezone(ctx context.Context, userID int64, tz string) error

	// Channel binding.
	BindChannel(ctx context.Context, guildID, channelID string, teamID, setByUserID int64) error
	GetChannelTeam(ctx context.Context, channelID string) (int64, error)
	UnbindChannel(ctx context.Context, channelID string) error

	// Per-guild settings: /coreteam permissions and /coreteam actionlog.
	AddEditRole(ctx context.Context, guildID, roleID string) error
	RemoveEditRole(ctx context.Context, guildID, roleID string) error
	ListEditRoles(ctx context.Context, guildID string) ([]string, error)
	SetActionLogChannel(ctx context.Context, guildID, channelID, setByDiscordUserID string) error
	GetActionLogChannel(ctx context.Context, guildID string) (string, error)
	ClearActionLogChannel(ctx context.Context, guildID string) error

	// The /coreteam post overview and its controls.
	RecordPost(ctx context.Context, messageID, channelID string, runAt *time.Time) error
	GetPostRunAt(ctx context.Context, messageID string) (int64, error)
	SetPostThread(ctx context.Context, messageID, threadID string) error
	MarkPostGone(ctx context.Context, messageID string) error
	SetRSVP(ctx context.Context, messageID, channelID, discordUserID, discordUsername, discordGlobalName, status string) error
	DeleteRSVP(ctx context.Context, messageID, discordUserID string) error
	ListRSVPs(ctx context.Context, messageID string) ([]models.RSVP, error)
	ClaimFill(ctx context.Context, messageID, channelID string, slot int, discordUserID, discordUsername string) error
	LeaveFill(ctx context.Context, messageID, discordUserID string) error
	MoveFillToList(ctx context.Context, messageID string, slot int) (models.PostFill, bool, error)
	ListFills(ctx context.Context, messageID string) ([]models.PostFill, error)

	// Scheduled pre-run pings and RSVP reminders (see scheduler.go).
	DuePostPings(ctx context.Context, now time.Time) ([]models.Post, error)
	MarkPostPinged(ctx context.Context, messageID string) error
	DueReminders(ctx context.Context, now time.Time) ([]models.Post, error)
	MarkPostReminded(ctx context.Context, messageID string) error
}

// premadeStore backs /coreteam signup: the run itself, its slot claims,
// waitlist and tentative lists, the scheduler's thread/cleanup passes, and the
// short-lived DM session that walks a user through creating a run.
type premadeStore interface {
	CreateRun(ctx context.Context, teamID int64, guildID, channelID, title, postOverride string, scheduledAt time.Time, createdBy int64) (*models.PremadeRun, error)
	UpdateRun(ctx context.Context, runID int64, title, postOverride string, scheduledAt time.Time) (*models.PremadeRun, error)
	GetRun(ctx context.Context, runID int64) (*models.PremadeRun, error)
	GetRunByMessage(ctx context.Context, messageID string) (*models.PremadeRun, error)
	SetRunMessage(ctx context.Context, runID int64, messageID string) error

	ClaimSlot(ctx context.Context, runID int64, slot int, discordUserID, discordUsername string) error
	LeaveSlot(ctx context.Context, runID int64, discordUserID string) error
	ListSignups(ctx context.Context, runID int64) ([]models.PremadeSignup, error)
	ReplaceSignups(ctx context.Context, runID int64, signups []models.PremadeSignup) error

	JoinWaitlist(ctx context.Context, runID int64, role, discordUserID, discordUsername string) error
	LeaveWaitlist(ctx context.Context, runID int64, discordUserID string) error
	ListWaitlist(ctx context.Context, runID int64) ([]models.PremadeWaitlistEntry, error)
	PromoteToSlot(ctx context.Context, runID int64, slot int, role string) (*models.PremadeWaitlistEntry, bool, error)

	JoinTentative(ctx context.Context, runID int64, role, discordUserID, discordUsername string) error
	LeaveTentative(ctx context.Context, runID int64, discordUserID string) error
	ListTentative(ctx context.Context, runID int64) ([]models.PremadeTentativeEntry, error)

	// Scheduler passes: open the discussion thread before the run, clean up
	// after it, with backoff on a failed cleanup.
	DueThreadRuns(ctx context.Context, now time.Time) ([]models.PremadeRun, error)
	DueCleanupRuns(ctx context.Context, now time.Time) ([]models.PremadeRun, error)
	SetRunThread(ctx context.Context, runID int64, threadID string) error
	MarkThreadStarted(ctx context.Context, runID int64, threadID string) error
	MarkCleanedUp(ctx context.Context, runID int64) error
	RecordCleanupFailure(ctx context.Context, runID int64, attempts int, nextAt time.Time) error
	MarkCleanupFailed(ctx context.Context, runID int64, attempts int) error

	GetSession(ctx context.Context, discordUserID string) (*models.PremadeSession, error)
	UpsertSession(ctx context.Context, sess *models.PremadeSession) error
	DeleteSession(ctx context.Context, discordUserID string) error
}

// rosterImageStore supplies the positioning screenshots the bot posts into a
// run's discussion thread.
type rosterImageStore interface {
	ListDataForActiveRoster(ctx context.Context, teamID int64) ([]models.RosterImage, error)
}

// Compile-time proof that the production stores still satisfy the interfaces
// above, so a signature change in internal/models fails here with a clear
// message rather than at the assignment in main().
var (
	_ teamStore        = (*models.TeamStore)(nil)
	_ encounterStore   = (*models.EncounterStore)(nil)
	_ groupingStore    = (*models.GroupingStore)(nil)
	_ memberStore      = (*models.MemberStore)(nil)
	_ discordStore     = (*models.DiscordStore)(nil)
	_ premadeStore     = (*models.PremadeStore)(nil)
	_ rosterImageStore = (*models.RosterImageStore)(nil)
)
