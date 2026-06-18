// Package handlers wires HTTP routes to business logic.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/email"
	"github.com/core-team-builder/backend/internal/models"
	"github.com/jackc/pgx/v5/pgconn"
)

// maxRequestBody bounds the size of any request body the API will read. JSON
// payloads here are small (a team with a 12-player roster and loadouts is a few
// KB), so 1 MiB is generous while still preventing memory-exhaustion DoS.
const maxRequestBody = 1 << 20 // 1 MiB

// Per-user / per-resource caps. These bound how much an authenticated user can
// create so a single account can't exhaust storage or degrade the service.
const (
	// maxTeamsPerOwner caps how many teams one user may own.
	maxTeamsPerOwner = 100
	// maxEncountersPerTeam caps how many encounters a team may hold.
	maxEncountersPerTeam = 10
	// maxPostFooterLen caps the free-form Discord bot post footer (in runes).
	maxPostFooterLen = 2000
	// maxDMFooterLen caps the free-form Discord bot DM footer (in runes).
	maxDMFooterLen = 2000
	// maxSignupPostLen caps the free-form Discord bot signup post body (in runes).
	maxSignupPostLen = 2000
	// maxGroupingsPerTeam caps how many groupings a team may hold.
	maxGroupingsPerTeam = 10
	// maxGroupingNameLen caps a grouping's name (in runes).
	maxGroupingNameLen = 100
	// maxGroupNameLen caps a single group's name within a grouping (in runes).
	maxGroupNameLen = 50
)

// Server holds the dependencies shared across HTTP handlers.
type Server struct {
	users            *models.UserStore
	teams            *models.TeamStore
	encounters       *models.EncounterStore
	groupings        *models.GroupingStore
	members          *models.MemberStore
	settings         *models.SettingsStore
	refreshTokens    *models.RefreshTokenStore
	passwordResets   *models.PasswordResetStore
	discord          *models.DiscordStore
	tokens           *auth.TokenManager
	mailer           email.Mailer
	corsOrigin       string
	appBaseURL       string
	passwordResetTTL time.Duration
	discordOAuth     DiscordOAuthConfig
}

// Config bundles the values needed to construct a Server.
type Config struct {
	Users            *models.UserStore
	Teams            *models.TeamStore
	Encounters       *models.EncounterStore
	Groupings        *models.GroupingStore
	Members          *models.MemberStore
	Settings         *models.SettingsStore
	RefreshTokens    *models.RefreshTokenStore
	PasswordResets   *models.PasswordResetStore
	Discord          *models.DiscordStore
	Tokens           *auth.TokenManager
	Mailer           email.Mailer
	CORSOrigin       string
	AppBaseURL       string
	PasswordResetTTL time.Duration
	DiscordOAuth     DiscordOAuthConfig
}

// DiscordOAuthConfig holds the "Sign in with Discord" settings the API needs.
// It mirrors config.DiscordOAuthConfig but is redeclared here so the handlers
// package doesn't depend on the config package.
type DiscordOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Enabled reports whether Discord sign-in is configured.
func (c DiscordOAuthConfig) Enabled() bool {
	return c.ClientID != "" && c.ClientSecret != "" && c.RedirectURL != ""
}

// New constructs a Server from the given configuration.
func New(c Config) *Server {
	return &Server{
		users:            c.Users,
		teams:            c.Teams,
		encounters:       c.Encounters,
		groupings:        c.Groupings,
		members:          c.Members,
		settings:         c.Settings,
		refreshTokens:    c.RefreshTokens,
		passwordResets:   c.PasswordResets,
		discord:          c.Discord,
		tokens:           c.Tokens,
		mailer:           c.Mailer,
		corsOrigin:       c.CORSOrigin,
		appBaseURL:       c.AppBaseURL,
		passwordResetTTL: c.PasswordResetTTL,
		discordOAuth:     c.DiscordOAuth,
	}
}

// Routes returns the fully configured HTTP handler, including CORS handling.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/registration-status", s.handleRegistrationStatus)
	mux.HandleFunc("POST /api/register", s.handleRegister)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("POST /api/forgot-password", s.handleForgotPassword)
	mux.HandleFunc("POST /api/reset-password", s.handleResetPassword)

	// Discord sign-in / sign-up (OAuth2). Full-page redirects, so these are GET
	// navigations rather than JSON endpoints.
	mux.HandleFunc("GET /api/auth/discord/login", s.handleDiscordOAuthLogin)
	mux.HandleFunc("GET /api/auth/discord/callback", s.handleDiscordOAuthCallback)

	// Protected routes.
	protected := func(h http.HandlerFunc) http.Handler {
		return s.tokens.Middleware(h)
	}
	mux.Handle("GET /api/me", protected(s.handleMe))

	// Discord account linking (per-user).
	mux.Handle("POST /api/discord/link-code", protected(s.handleDiscordLinkCode))
	mux.Handle("GET /api/discord/link", protected(s.handleGetDiscordLink))
	mux.Handle("DELETE /api/discord/link", protected(s.handleDeleteDiscordLink))

	// Admin-only user/settings management.
	mux.Handle("GET /api/admin/users", protected(s.handleListUsers))
	mux.Handle("POST /api/admin/users", protected(s.handleCreateUser))
	mux.Handle("DELETE /api/admin/users/{id}", protected(s.handleDeleteUser))
	mux.Handle("PUT /api/admin/users/{id}/admin", protected(s.handleSetUserAdmin))
	mux.Handle("GET /api/admin/settings", protected(s.handleGetSettings))
	mux.Handle("PUT /api/admin/settings", protected(s.handleUpdateSettings))

	// Teams.
	mux.Handle("GET /api/teams", protected(s.handleListTeams))
	mux.Handle("POST /api/teams", protected(s.handleCreateTeam))
	mux.Handle("GET /api/teams/{id}", protected(s.handleGetTeam))
	mux.Handle("PUT /api/teams/{id}", protected(s.handleUpdateTeam))
	mux.Handle("DELETE /api/teams/{id}", protected(s.handleDeleteTeam))
	mux.Handle("POST /api/teams/{id}/share", protected(s.handleShareTeam))
	mux.Handle("DELETE /api/teams/{id}/members/{userID}", protected(s.handleUnshareTeam))

	// Roster member pool (the /coreteam signup recruitment pool).
	mux.Handle("GET /api/teams/{id}/roster-members", protected(s.handleListRosterMembers))
	mux.Handle("POST /api/teams/{id}/roster-members", protected(s.handleCreateRosterMember))
	mux.Handle("PUT /api/teams/{id}/roster-members/{memberID}", protected(s.handleUpdateRosterMember))
	mux.Handle("DELETE /api/teams/{id}/roster-members/{memberID}", protected(s.handleDeleteRosterMember))

	// Encounters.
	mux.Handle("GET /api/teams/{id}/encounters", protected(s.handleListEncounters))
	mux.Handle("POST /api/teams/{id}/encounters", protected(s.handleCreateEncounter))
	mux.Handle("GET /api/teams/{id}/encounters/{eid}", protected(s.handleGetEncounter))
	mux.Handle("PUT /api/teams/{id}/encounters/{eid}", protected(s.handleUpdateEncounter))
	mux.Handle("DELETE /api/teams/{id}/encounters/{eid}", protected(s.handleDeleteEncounter))
	mux.Handle("PUT /api/teams/{id}/encounters/{eid}/loadouts", protected(s.handleSaveLoadouts))

	// Groupings.
	mux.Handle("GET /api/teams/{id}/groupings", protected(s.handleListGroupings))
	mux.Handle("POST /api/teams/{id}/groupings", protected(s.handleCreateGrouping))
	mux.Handle("GET /api/teams/{id}/groupings/{gid}", protected(s.handleGetGrouping))
	mux.Handle("PUT /api/teams/{id}/groupings/{gid}", protected(s.handleUpdateGrouping))
	mux.Handle("DELETE /api/teams/{id}/groupings/{gid}", protected(s.handleDeleteGrouping))

	return s.withCORS(s.withMaxBytes(mux))
}

// withMaxBytes caps every request body via http.MaxBytesReader so an oversized
// body is rejected (with 413 by the decoder) instead of being read into memory.
// Applied globally so no handler can forget it.
func (s *Server) withMaxBytes(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

// withCORS adds permissive-but-scoped CORS headers for the configured frontend
// origin and handles preflight requests.
func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", s.corsOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Vary", "Origin")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type credentials struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token        string       `json:"token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int          `json:"expires_in"`
	User         *models.User `json:"user"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

// handleRegistrationStatus reports whether public self-registration is open. It
// is unauthenticated so the login page can hide the Register tab accordingly.
func (s *Server) handleRegistrationStatus(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.settings.RegistrationEnabled(r.Context())
	if err != nil {
		log.Printf("registration status: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{
		"enabled":         enabled,
		"discord_enabled": s.discordOAuth.Enabled(),
	})
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var creds credentials
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if creds.Username == "" || creds.Email == "" || creds.Password == "" {
		writeError(w, http.StatusBadRequest, "username, email, and password are required")
		return
	}

	// The very first account bootstraps the system and is always allowed (and
	// becomes an admin). Once users exist, honor the registration toggle.
	count, err := s.users.Count(r.Context())
	if err != nil {
		log.Printf("count users: %v", err)
		writeError(w, http.StatusInternalServerError, "could not process registration")
		return
	}
	firstUser := count == 0
	if !firstUser {
		enabled, err := s.settings.RegistrationEnabled(r.Context())
		if err != nil {
			log.Printf("registration enabled: %v", err)
			writeError(w, http.StatusInternalServerError, "could not process registration")
			return
		}
		if !enabled {
			writeError(w, http.StatusForbidden, "registration is currently disabled")
			return
		}
	}

	hash, err := auth.HashPassword(creds.Password)
	if auth.IsPasswordPolicyError(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		log.Printf("hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "could not process registration")
		return
	}

	user, err := s.users.Create(r.Context(), creds.Username, creds.Email, hash, firstUser)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "username or email already in use")
			return
		}
		log.Printf("create user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	s.issueAndRespond(w, r, user)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var creds credentials
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	user, err := s.users.GetByUsername(r.Context(), creds.Username)
	// Always run a comparison to keep timing uniform whether or not the user
	// exists, then return the same generic error for any failure.
	if errors.Is(err, models.ErrUserNotFound) {
		auth.CheckPassword("$2a$12$invalidinvalidinvalidinvalidinvalidinvalidinvalidinv", creds.Password)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		log.Printf("lookup user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not process login")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, creds.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	s.issueAndRespond(w, r, user)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// issueTokens mints a new access token and a persisted refresh token for the
// user. It is the shared core behind both the JSON auth endpoints and the
// Discord OAuth redirect flow.
func (s *Server) issueTokens(ctx context.Context, user *models.User) (token, refreshToken string, expiresIn int, err error) {
	token, err = s.tokens.Issue(user.ID, user.Username)
	if err != nil {
		return "", "", 0, fmt.Errorf("issue access token: %w", err)
	}

	refreshToken, refreshHash, err := auth.GenerateRefreshToken()
	if err != nil {
		return "", "", 0, fmt.Errorf("generate refresh token: %w", err)
	}
	expiresAt := time.Now().Add(s.tokens.RefreshTTL())
	if err := s.refreshTokens.Create(ctx, user.ID, refreshHash, expiresAt); err != nil {
		return "", "", 0, fmt.Errorf("persist refresh token: %w", err)
	}
	return token, refreshToken, int(s.tokens.AccessTTL().Seconds()), nil
}

func (s *Server) issueAndRespond(w http.ResponseWriter, r *http.Request, user *models.User) {
	token, refreshToken, expiresIn, err := s.issueTokens(r.Context(), user)
	if err != nil {
		log.Printf("issue tokens: %v", err)
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}

	writeJSON(w, http.StatusOK, authResponse{
		Token:        token,
		RefreshToken: refreshToken,
		ExpiresIn:    expiresIn,
		User:         user,
	})
}

// handleRefresh exchanges a valid refresh token for a new access token and a new
// refresh token (single-use rotation). The presented token is consumed
// atomically, so replaying it fails and a stolen-then-used token is detectable.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RefreshToken == "" {
		writeError(w, http.StatusBadRequest, "refresh_token is required")
		return
	}

	userID, err := s.refreshTokens.Consume(r.Context(), auth.HashRefreshToken(req.RefreshToken))
	if errors.Is(err, models.ErrRefreshTokenInvalid) {
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}
	if err != nil {
		log.Printf("consume refresh token: %v", err)
		writeError(w, http.StatusInternalServerError, "could not refresh session")
		return
	}

	user, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid refresh token")
		return
	}

	s.issueAndRespond(w, r, user)
}

// handleLogout revokes the supplied refresh token so it can no longer mint
// access tokens. It is idempotent and always returns 204.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err == nil && req.RefreshToken != "" {
		if err := s.refreshTokens.Revoke(r.Context(), auth.HashRefreshToken(req.RefreshToken)); err != nil {
			log.Printf("revoke refresh token: %v", err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
