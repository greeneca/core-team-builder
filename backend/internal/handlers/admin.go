package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/models"
	"github.com/jackc/pgx/v5/pgconn"
)

// requireAdmin resolves the authenticated caller and ensures they are an admin.
// It writes the error response and returns ok=false on any failure.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) (*models.User, bool) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	caller, err := s.users.GetByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return nil, false
	}
	if !caller.IsAdmin {
		writeError(w, http.StatusForbidden, "admin access required")
		return nil, false
	}
	return caller, true
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	users, err := s.users.List(r.Context())
	if err != nil {
		log.Printf("list users: %v", err)
		writeError(w, http.StatusInternalServerError, "could not list users")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

type createUserRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
	IsAdmin  bool   `json:"is_admin"`
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Username == "" || req.Email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "username, email, and password are required")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if errors.Is(err, auth.ErrPasswordTooShort) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		log.Printf("hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}

	user, err := s.users.Create(r.Context(), req.Username, req.Email, hash, req.IsAdmin)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			writeError(w, http.StatusConflict, "username or email already in use")
			return
		}
		log.Printf("create user (admin): %v", err)
		writeError(w, http.StatusInternalServerError, "could not create user")
		return
	}
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	caller, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if id == caller.ID {
		writeError(w, http.StatusBadRequest, "you cannot delete your own account")
		return
	}

	target, err := s.users.GetByID(r.Context(), id)
	if errors.Is(err, models.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		log.Printf("lookup user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	// Never let the last admin be removed.
	if target.IsAdmin {
		admins, err := s.users.CountAdmins(r.Context())
		if err != nil {
			log.Printf("count admins: %v", err)
			writeError(w, http.StatusInternalServerError, "could not delete user")
			return
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "cannot delete the last admin")
			return
		}
	}

	if err := s.users.Delete(r.Context(), id); err != nil {
		log.Printf("delete user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete user")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type setAdminRequest struct {
	IsAdmin bool `json:"is_admin"`
}

func (s *Server) handleSetUserAdmin(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req setAdminRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	target, err := s.users.GetByID(r.Context(), id)
	if errors.Is(err, models.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if err != nil {
		log.Printf("lookup user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update user")
		return
	}

	// Block demoting the last remaining admin (covers demoting yourself when
	// you're the only admin).
	if target.IsAdmin && !req.IsAdmin {
		admins, err := s.users.CountAdmins(r.Context())
		if err != nil {
			log.Printf("count admins: %v", err)
			writeError(w, http.StatusInternalServerError, "could not update user")
			return
		}
		if admins <= 1 {
			writeError(w, http.StatusBadRequest, "cannot remove the last admin")
			return
		}
	}

	if err := s.users.SetAdmin(r.Context(), id, req.IsAdmin); err != nil {
		log.Printf("set admin: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update user")
		return
	}
	updated, err := s.users.GetByID(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not update user")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	enabled, err := s.settings.RegistrationEnabled(r.Context())
	if err != nil {
		log.Printf("get settings: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registration_enabled": enabled})
}

type updateSettingsRequest struct {
	RegistrationEnabled *bool `json:"registration_enabled"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RegistrationEnabled != nil {
		if err := s.settings.SetRegistrationEnabled(r.Context(), *req.RegistrationEnabled); err != nil {
			log.Printf("set registration: %v", err)
			writeError(w, http.StatusInternalServerError, "could not update settings")
			return
		}
	}
	enabled, err := s.settings.RegistrationEnabled(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not read settings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"registration_enabled": enabled})
}
