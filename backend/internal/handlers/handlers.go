// Package handlers wires HTTP routes to business logic.
package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/models"
	"github.com/jackc/pgx/v5/pgconn"
)

// Server holds the dependencies shared across HTTP handlers.
type Server struct {
	users      *models.UserStore
	teams      *models.TeamStore
	encounters *models.EncounterStore
	tokens     *auth.TokenManager
	corsOrigin string
}

// New constructs a Server.
func New(users *models.UserStore, teams *models.TeamStore, encounters *models.EncounterStore, tokens *auth.TokenManager, corsOrigin string) *Server {
	return &Server{users: users, teams: teams, encounters: encounters, tokens: tokens, corsOrigin: corsOrigin}
}

// Routes returns the fully configured HTTP handler, including CORS handling.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("POST /api/register", s.handleRegister)
	mux.HandleFunc("POST /api/login", s.handleLogin)

	// Protected routes.
	protected := func(h http.HandlerFunc) http.Handler {
		return s.tokens.Middleware(h)
	}
	mux.Handle("GET /api/me", protected(s.handleMe))

	// Teams.
	mux.Handle("GET /api/teams", protected(s.handleListTeams))
	mux.Handle("POST /api/teams", protected(s.handleCreateTeam))
	mux.Handle("GET /api/teams/{id}", protected(s.handleGetTeam))
	mux.Handle("PUT /api/teams/{id}", protected(s.handleUpdateTeam))
	mux.Handle("DELETE /api/teams/{id}", protected(s.handleDeleteTeam))
	mux.Handle("POST /api/teams/{id}/share", protected(s.handleShareTeam))
	mux.Handle("DELETE /api/teams/{id}/members/{userID}", protected(s.handleUnshareTeam))

	// Encounters.
	mux.Handle("GET /api/teams/{id}/encounters", protected(s.handleListEncounters))
	mux.Handle("POST /api/teams/{id}/encounters", protected(s.handleCreateEncounter))
	mux.Handle("GET /api/teams/{id}/encounters/{eid}", protected(s.handleGetEncounter))
	mux.Handle("PUT /api/teams/{id}/encounters/{eid}", protected(s.handleUpdateEncounter))
	mux.Handle("DELETE /api/teams/{id}/encounters/{eid}", protected(s.handleDeleteEncounter))
	mux.Handle("PUT /api/teams/{id}/encounters/{eid}/loadouts", protected(s.handleSaveLoadouts))

	return s.withCORS(mux)
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
	Token string       `json:"token"`
	User  *models.User `json:"user"`
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

	hash, err := auth.HashPassword(creds.Password)
	if errors.Is(err, auth.ErrPasswordTooShort) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		log.Printf("hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "could not process registration")
		return
	}

	user, err := s.users.Create(r.Context(), creds.Username, creds.Email, hash)
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

	s.issueAndRespond(w, user)
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

	s.issueAndRespond(w, user)
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

func (s *Server) issueAndRespond(w http.ResponseWriter, user *models.User) {
	token, err := s.tokens.Issue(user.ID, user.Username)
	if err != nil {
		log.Printf("issue token: %v", err)
		writeError(w, http.StatusInternalServerError, "could not issue token")
		return
	}
	writeJSON(w, http.StatusOK, authResponse{Token: token, User: user})
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
