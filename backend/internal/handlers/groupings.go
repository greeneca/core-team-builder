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

// groupingAccess resolves the team (and caller role) and the grouping, ensuring
// the grouping belongs to the team. It writes the error response and returns
// ok=false on any failure.
func (s *Server) groupingAccess(w http.ResponseWriter, r *http.Request) (teamID int64, role string, grouping *models.Grouping, ok bool) {
	teamID, _, role, ok = s.teamAccess(w, r)
	if !ok {
		return 0, "", nil, false
	}

	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid grouping id")
		return 0, "", nil, false
	}

	grouping, err = s.groupings.Get(r.Context(), gid)
	if errors.Is(err, models.ErrGroupingNotFound) {
		writeError(w, http.StatusNotFound, "grouping not found")
		return 0, "", nil, false
	}
	if err != nil {
		log.Printf("get grouping: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load grouping")
		return 0, "", nil, false
	}
	if grouping.TeamID != teamID {
		writeError(w, http.StatusNotFound, "grouping not found")
		return 0, "", nil, false
	}
	return teamID, role, grouping, true
}

func (s *Server) handleListGroupings(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	rosterID, ok := s.resolveRoster(w, r, teamID)
	if !ok {
		return
	}
	groupings, err := s.groupings.ListForRoster(r.Context(), rosterID)
	if err != nil {
		log.Printf("list groupings: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load groupings")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groupings": groupings})
}

type groupingCreateRequest struct {
	Name       string `json:"name"`
	GroupCount int    `json:"group_count"`
}

type groupingGroupPayload struct {
	GroupNumber int    `json:"group_number"`
	Name        string `json:"name"`
	Slots       []int  `json:"slots"`
}

type groupingUpdateRequest struct {
	Name       string                 `json:"name"`
	GroupCount int                    `json:"group_count"`
	Groups     []groupingGroupPayload `json:"groups"`
}

// clampGroupCount bounds a grouping's group count to the valid 1..max range.
func clampGroupCount(n int) int {
	if n < 1 {
		return 1
	}
	if n > models.MaxGroupsPerGrouping {
		return models.MaxGroupsPerGrouping
	}
	return n
}

func (s *Server) handleCreateGrouping(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	rosterID, ok := s.resolveRoster(w, r, teamID)
	if !ok {
		return
	}

	var req groupingCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Grouping"
	}
	if len([]rune(name)) > maxGroupingNameLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("grouping name too long (max %d characters)", maxGroupingNameLen))
		return
	}
	count := clampGroupCount(req.GroupCount)

	n, err := s.groupings.CountForRoster(r.Context(), rosterID)
	if err != nil {
		log.Printf("count groupings: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create grouping")
		return
	}
	if n >= maxGroupingsPerTeam {
		writeError(w, http.StatusConflict, fmt.Sprintf("grouping limit reached (max %d)", maxGroupingsPerTeam))
		return
	}

	grouping, err := s.groupings.Create(r.Context(), rosterID, name, count)
	if err != nil {
		log.Printf("create grouping: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create grouping")
		return
	}
	writeJSON(w, http.StatusCreated, grouping)
}

func (s *Server) handleGetGrouping(w http.ResponseWriter, r *http.Request) {
	_, _, grouping, ok := s.groupingAccess(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, grouping)
}

func (s *Server) handleUpdateGrouping(w http.ResponseWriter, r *http.Request) {
	_, role, grouping, ok := s.groupingAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	var req groupingUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "Grouping"
	}
	if len([]rune(name)) > maxGroupingNameLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("grouping name too long (max %d characters)", maxGroupingNameLen))
		return
	}
	count := clampGroupCount(req.GroupCount)

	// Build the sanitized group set, enforcing that every player slot appears in
	// at most one group within this grouping.
	seen := make(map[int]bool)
	groups := make([]models.GroupingGroup, 0, len(req.Groups))
	for _, g := range req.Groups {
		if g.GroupNumber < 1 || g.GroupNumber > count {
			// Skip groups outside the current count (e.g. stale entries after a
			// count reduction).
			continue
		}
		gName := strings.TrimSpace(g.Name)
		if len([]rune(gName)) > maxGroupNameLen {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("group name too long (max %d characters)", maxGroupNameLen))
			return
		}
		slots := make([]int, 0, len(g.Slots))
		for _, slot := range g.Slots {
			if slot < 1 || slot > models.TeamSize {
				writeError(w, http.StatusBadRequest, "invalid player slot")
				return
			}
			if seen[slot] {
				writeError(w, http.StatusBadRequest, "a player can only be in one group per grouping")
				return
			}
			seen[slot] = true
			slots = append(slots, slot)
		}
		groups = append(groups, models.GroupingGroup{GroupNumber: g.GroupNumber, Name: gName, Slots: slots})
	}

	if err := s.groupings.Save(r.Context(), grouping.ID, name, count, groups); err != nil {
		log.Printf("save grouping: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save grouping")
		return
	}
	updated, err := s.groupings.Get(r.Context(), grouping.ID)
	if err != nil {
		log.Printf("reload grouping: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load grouping")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteGrouping(w http.ResponseWriter, r *http.Request) {
	_, role, grouping, ok := s.groupingAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}
	if err := s.groupings.Delete(r.Context(), grouping.ID); err != nil {
		log.Printf("delete grouping: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete grouping")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
