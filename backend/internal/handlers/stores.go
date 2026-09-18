package handlers

import (
	"context"
	"time"

	"github.com/core-team-builder/backend/internal/models"
)

// The interfaces below are the slices of internal/models the HTTP handlers
// actually call. They are declared here, at the consumer, rather than beside
// the implementations so the API's dependencies are visible in one place and a
// test can substitute a fake without a database. The concrete *models.*Store
// types satisfy them implicitly; cmd/server still passes the real ones.
//
// Keep these narrow: add a method only when a handler needs it.

// userStore backs registration, login, /api/me, and the admin user list.
type userStore interface {
	Create(ctx context.Context, username, email, passwordHash string, isAdmin bool) (*models.User, error)
	CreateDiscordUser(ctx context.Context, username, email, discordUserID, discordUsername string, isAdmin bool) (*models.User, error)
	GetByUsername(ctx context.Context, username string) (*models.User, error)
	GetByEmail(ctx context.Context, email string) (*models.User, error)
	GetByID(ctx context.Context, id int64) (*models.User, error)
	Count(ctx context.Context) (int, error)
	CountAdmins(ctx context.Context) (int, error)
	List(ctx context.Context) ([]models.User, error)
	SetAdmin(ctx context.Context, id int64, isAdmin bool) error
	UpdatePassword(ctx context.Context, id int64, passwordHash string) error
	Delete(ctx context.Context, id int64) error
}

// teamStore covers team metadata, membership/sharing, and the per-slot roster
// save. Access is the permission lookup every team-scoped handler starts with.
type teamStore interface {
	Access(ctx context.Context, teamID, userID int64) (bool, string, error)
	Create(ctx context.Context, ownerID int64, name string, copyFromTeamID int64) (*models.Team, error)
	CountOwned(ctx context.Context, ownerID int64) (int, error)
	ListForUser(ctx context.Context, userID int64) ([]models.Team, error)
	Get(ctx context.Context, teamID int64) (*models.Team, error)
	Save(ctx context.Context, teamID int64, name string, days []string, scheduleTime string, encountersEnabled bool, postFooter string, dmFooter string, signupPost string, autoSharePoolViewers bool, preMade bool, premadePost string, simpleSignup bool, waitlistEnabled bool, simpleSignupStyle string, roles models.TeamRoles, expectedUpdatedAt time.Time) error
	SavePlayer(ctx context.Context, rosterID int64, p models.Player, expectedUpdatedAt time.Time) (*models.Player, error)
	GetRoles(ctx context.Context, teamID int64) (models.TeamRoles, error)
	Delete(ctx context.Context, teamID int64) error
	AddMember(ctx context.Context, teamID, userID int64, role string) error
	RemoveMember(ctx context.Context, teamID, userID int64) error
	SharePoolMembers(ctx context.Context, teamID int64) error
	ShareAutoTeamsForDiscord(ctx context.Context, discordUserID string, userID int64) error
}

// rosterStore covers the named 12-player lineups a team owns and which one is
// active. TeamForRoster and ActiveForTeam resolve the ?roster_id= query that
// the roster-scoped collection endpoints take.
type rosterStore interface {
	ListForTeam(ctx context.Context, teamID int64) ([]models.Roster, error)
	Get(ctx context.Context, rosterID int64) (*models.Roster, error)
	TeamForRoster(ctx context.Context, rosterID int64) (int64, error)
	CountForTeam(ctx context.Context, teamID int64) (int, error)
	ActiveForTeam(ctx context.Context, teamID int64) (int64, error)
	Create(ctx context.Context, teamID int64, name string, copyFromRosterID int64) (*models.Roster, error)
	Rename(ctx context.Context, rosterID int64, name string) error
	SetActive(ctx context.Context, teamID, rosterID int64) error
	Delete(ctx context.Context, teamID, rosterID int64) error
}

// encounterStore covers a roster's encounters and their per-slot loadouts.
type encounterStore interface {
	ListForRoster(ctx context.Context, rosterID int64) ([]models.Encounter, error)
	Get(ctx context.Context, encounterID int64) (*models.Encounter, error)
	CountForRoster(ctx context.Context, rosterID int64) (int, error)
	Create(ctx context.Context, rosterID int64, name string, copyFromID int64) (*models.Encounter, error)
	UpdateName(ctx context.Context, encounterID int64, name string) error
	Delete(ctx context.Context, encounterID int64) error
	SaveLoadouts(ctx context.Context, encounterID int64, loadouts []models.Loadout) error
	SaveLoadoutSlot(ctx context.Context, encounterID int64, l models.Loadout, expectedUpdatedAt time.Time) (*models.Loadout, error)
}

// groupingStore covers a roster's groupings (named sets of numbered groups).
type groupingStore interface {
	ListForRoster(ctx context.Context, rosterID int64) ([]models.Grouping, error)
	Get(ctx context.Context, groupingID int64) (*models.Grouping, error)
	CountForRoster(ctx context.Context, rosterID int64) (int, error)
	Create(ctx context.Context, rosterID int64, name string, groupCount int) (*models.Grouping, error)
	Save(ctx context.Context, groupingID int64, name string, groupCount int, groups []models.GroupingGroup) error
	Delete(ctx context.Context, groupingID int64) error
}

// memberStore covers the team's member pool (prospective players).
type memberStore interface {
	List(ctx context.Context, teamID int64) ([]models.RosterMember, error)
	Create(ctx context.Context, m *models.RosterMember) (*models.RosterMember, error)
	Update(ctx context.Context, m *models.RosterMember) (*models.RosterMember, error)
	Delete(ctx context.Context, teamID, id int64) error
}

// rosterImageStore covers the positioning screenshots attached to a roster.
type rosterImageStore interface {
	ListForRoster(ctx context.Context, rosterID int64) ([]models.RosterImage, error)
	Get(ctx context.Context, imageID int64) (*models.RosterImage, error)
	GetData(ctx context.Context, imageID int64) ([]byte, string, error)
	CountForRoster(ctx context.Context, rosterID int64) (int, error)
	Create(ctx context.Context, rosterID int64, caption, contentType string, data []byte) (*models.RosterImage, error)
	UpdateCaption(ctx context.Context, imageID int64, caption string) error
	Delete(ctx context.Context, imageID int64) error
}

// settingsStore is the global key/value config the API reads; only the
// registration toggle is surfaced today.
type settingsStore interface {
	RegistrationEnabled(ctx context.Context) (bool, error)
	SetRegistrationEnabled(ctx context.Context, enabled bool) error
}

// refreshTokenStore persists refresh tokens by hash. Consume is the atomic
// single-use rotation the refresh endpoint depends on.
type refreshTokenStore interface {
	Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error
	Consume(ctx context.Context, tokenHash string) (int64, error)
	Revoke(ctx context.Context, tokenHash string) error
	RevokeAllForUser(ctx context.Context, userID int64) error
}

// passwordResetStore persists single-use password-reset tokens by hash.
type passwordResetStore interface {
	Create(ctx context.Context, userID int64, tokenHash string, expiresAt time.Time) error
	Consume(ctx context.Context, tokenHash string) (int64, error)
	InvalidateForUser(ctx context.Context, userID int64) error
}

// discordStore is the account-linking slice the API needs; the bot's much
// larger slice of the same store is declared in cmd/bot.
type discordStore interface {
	CreateLinkCode(ctx context.Context, userID int64, codeHash string, expiresAt time.Time) error
	InvalidateLinkCodesForUser(ctx context.Context, userID int64) error
	LinkUser(ctx context.Context, userID int64, discordUserID, discordUsername string) error
	UnlinkUser(ctx context.Context, userID int64) error
	GetLink(ctx context.Context, userID int64) (models.DiscordLink, error)
	GetUserByDiscordID(ctx context.Context, discordUserID string) (int64, error)
}

// Compile-time proof that the production stores still satisfy the interfaces
// above, so a signature change in internal/models fails here with a clear
// message rather than at the assignment in cmd/server.
var (
	_ userStore          = (*models.UserStore)(nil)
	_ teamStore          = (*models.TeamStore)(nil)
	_ rosterStore        = (*models.RosterStore)(nil)
	_ encounterStore     = (*models.EncounterStore)(nil)
	_ groupingStore      = (*models.GroupingStore)(nil)
	_ memberStore        = (*models.MemberStore)(nil)
	_ rosterImageStore   = (*models.RosterImageStore)(nil)
	_ settingsStore      = (*models.SettingsStore)(nil)
	_ refreshTokenStore  = (*models.RefreshTokenStore)(nil)
	_ passwordResetStore = (*models.PasswordResetStore)(nil)
	_ discordStore       = (*models.DiscordStore)(nil)
)
