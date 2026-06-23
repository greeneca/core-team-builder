package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/core-team-builder/backend/internal/models"
)

// maxRosterNameLen caps a roster's display name (in runes).
const maxRosterNameLen = 100

// rosterAccess resolves the team (and caller role) and the {rid} roster, ensuring
// the roster belongs to the team. It writes the error response and returns
// ok=false on any failure (a 404 hides both missing teams and mismatched rosters).
func (s *Server) rosterAccess(w http.ResponseWriter, r *http.Request) (teamID int64, role string, rosterID int64, ok bool) {
	teamID, _, role, ok = s.teamAccess(w, r)
	if !ok {
		return 0, "", 0, false
	}
	rid, err := strconv.ParseInt(r.PathValue("rid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid roster id")
		return 0, "", 0, false
	}
	owner, err := s.rosters.TeamForRoster(r.Context(), rid)
	if errors.Is(err, models.ErrRosterNotFound) {
		writeError(w, http.StatusNotFound, "roster not found")
		return 0, "", 0, false
	}
	if err != nil {
		log.Printf("roster access: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load roster")
		return 0, "", 0, false
	}
	if owner != teamID {
		writeError(w, http.StatusNotFound, "roster not found")
		return 0, "", 0, false
	}
	return teamID, role, rid, true
}

// resolveRoster determines which roster a roster-scoped request targets. It reads
// the optional ?roster_id= query, validating it belongs to the team; when absent,
// it defaults to the team's active roster. It writes the error response and
// returns ok=false on any failure.
func (s *Server) resolveRoster(w http.ResponseWriter, r *http.Request, teamID int64) (int64, bool) {
	q := strings.TrimSpace(r.URL.Query().Get("roster_id"))
	if q == "" {
		rid, err := s.rosters.ActiveForTeam(r.Context(), teamID)
		if err != nil {
			log.Printf("resolve active roster: %v", err)
			writeError(w, http.StatusInternalServerError, "could not load roster")
			return 0, false
		}
		return rid, true
	}
	rid, err := strconv.ParseInt(q, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid roster id")
		return 0, false
	}
	owner, err := s.rosters.TeamForRoster(r.Context(), rid)
	if errors.Is(err, models.ErrRosterNotFound) {
		writeError(w, http.StatusNotFound, "roster not found")
		return 0, false
	}
	if err != nil {
		log.Printf("resolve roster: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load roster")
		return 0, false
	}
	if owner != teamID {
		writeError(w, http.StatusNotFound, "roster not found")
		return 0, false
	}
	return rid, true
}

func (s *Server) handleListRosters(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	rosters, err := s.rosters.ListForTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("list rosters: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load rosters")
		return
	}
	active, err := s.rosters.ActiveForTeam(r.Context(), teamID)
	if err != nil && !errors.Is(err, models.ErrRosterNotFound) {
		log.Printf("list rosters active: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load rosters")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rosters": rosters, "active_roster_id": active})
}

type createRosterRequest struct {
	Name string `json:"name"`
	// CopyFrom optionally identifies an existing roster on the SAME team whose
	// players, encounters/loadouts, and groupings are copied into the new roster.
	// nil/0 = a fresh roster (12 empty slots + a single Default encounter).
	CopyFrom *int64 `json:"copy_from"`
}

func (s *Server) handleCreateRoster(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	var req createRosterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Roster"
	}
	if len([]rune(name)) > maxRosterNameLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("roster name too long (max %d characters)", maxRosterNameLen))
		return
	}

	count, err := s.rosters.CountForTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("count rosters: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create roster")
		return
	}
	if count >= models.MaxRostersPerTeam {
		writeError(w, http.StatusConflict, fmt.Sprintf("roster limit reached (max %d)", models.MaxRostersPerTeam))
		return
	}

	// Validate the optional copy source belongs to this team.
	var copyFrom int64
	if req.CopyFrom != nil && *req.CopyFrom != 0 {
		copyFrom = *req.CopyFrom
		owner, terr := s.rosters.TeamForRoster(r.Context(), copyFrom)
		if errors.Is(terr, models.ErrRosterNotFound) || (terr == nil && owner != teamID) {
			writeError(w, http.StatusBadRequest, "copy source roster not found")
			return
		}
		if terr != nil && !errors.Is(terr, models.ErrRosterNotFound) {
			log.Printf("create roster copy access: %v", terr)
			writeError(w, http.StatusInternalServerError, "could not create roster")
			return
		}
	}

	roster, err := s.rosters.Create(r.Context(), teamID, name, copyFrom)
	if err != nil {
		log.Printf("create roster: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create roster")
		return
	}
	writeJSON(w, http.StatusCreated, roster)
}

func (s *Server) handleGetRoster(w http.ResponseWriter, r *http.Request) {
	_, _, rosterID, ok := s.rosterAccess(w, r)
	if !ok {
		return
	}
	roster, err := s.rosters.Get(r.Context(), rosterID)
	if err != nil {
		log.Printf("get roster: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load roster")
		return
	}
	writeJSON(w, http.StatusOK, roster)
}

type renameRosterRequest struct {
	Name string `json:"name"`
}

func (s *Server) handleRenameRoster(w http.ResponseWriter, r *http.Request) {
	_, role, rosterID, ok := s.rosterAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	var req renameRosterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "roster name is required")
		return
	}
	if len([]rune(name)) > maxRosterNameLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("roster name too long (max %d characters)", maxRosterNameLen))
		return
	}
	if err := s.rosters.Rename(r.Context(), rosterID, name); err != nil {
		log.Printf("rename roster: %v", err)
		writeError(w, http.StatusInternalServerError, "could not rename roster")
		return
	}
	roster, err := s.rosters.Get(r.Context(), rosterID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load roster")
		return
	}
	writeJSON(w, http.StatusOK, roster)
}

func (s *Server) handleDeleteRoster(w http.ResponseWriter, r *http.Request) {
	teamID, role, rosterID, ok := s.rosterAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	if err := s.rosters.Delete(r.Context(), teamID, rosterID); err != nil {
		if errors.Is(err, models.ErrLastRoster) {
			writeError(w, http.StatusBadRequest, "a team must have at least one roster")
			return
		}
		if errors.Is(err, models.ErrRosterNotFound) {
			writeError(w, http.StatusNotFound, "roster not found")
			return
		}
		log.Printf("delete roster: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete roster")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleActivateRoster(w http.ResponseWriter, r *http.Request) {
	teamID, role, rosterID, ok := s.rosterAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	if err := s.rosters.SetActive(r.Context(), teamID, rosterID); err != nil {
		if errors.Is(err, models.ErrRosterNotFound) {
			writeError(w, http.StatusNotFound, "roster not found")
			return
		}
		log.Printf("activate roster: %v", err)
		writeError(w, http.StatusInternalServerError, "could not set active roster")
		return
	}
	team, err := s.teams.Get(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	writeJSON(w, http.StatusOK, team)
}
