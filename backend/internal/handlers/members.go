package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/core-team-builder/backend/internal/models"
)

// maxRosterMembers caps how many pool members a team may hold, bounding storage.
const maxRosterMembers = 200

// handleListRosterMembers returns the team's recruitment/availability pool. Any
// team role (viewer/editor/owner) may read it.
func (s *Server) handleListRosterMembers(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	members, err := s.members.List(r.Context(), teamID)
	if err != nil {
		log.Printf("list roster members: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load members")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

type createRosterMemberRequest struct {
	DisplayName     string                      `json:"display_name"`
	DiscordUsername string                      `json:"discord_username"`
	Timezone        string                      `json:"timezone"`
	Days            []string                    `json:"days"`
	Availability    map[string]models.DayWindow `json:"availability"`
	Roles           []string                    `json:"roles"`
	ClassesByRole   map[string][]string         `json:"classes_by_role"`
}

// handleCreateRosterMember adds a manual pool entry from the web app. Requires
// edit access.
func (s *Server) handleCreateRosterMember(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "viewers cannot edit this team")
		return
	}

	var req createRosterMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display name is required")
		return
	}

	existing, err := s.members.List(r.Context(), teamID)
	if err != nil {
		log.Printf("count roster members: %v", err)
		writeError(w, http.StatusInternalServerError, "could not add member")
		return
	}
	if len(existing) >= maxRosterMembers {
		writeError(w, http.StatusConflict, "member limit reached")
		return
	}

	days, err := normalizeDays(req.Days)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	roles := normalizeRoles(req.Roles)
	classes := normalizeClassesByRole(req.ClassesByRole)
	availability := normalizeAvailability(req.Availability, days)

	m := &models.RosterMember{
		TeamID:          teamID,
		DiscordUsername: strings.TrimSpace(req.DiscordUsername),
		DisplayName:     req.DisplayName,
		Timezone:        strings.TrimSpace(req.Timezone),
		Days:            days,
		Availability:    availability,
		Roles:           roles,
		ClassesByRole:   classes,
	}
	created, err := s.members.Create(r.Context(), m)
	if err != nil {
		log.Printf("create roster member: %v", err)
		writeError(w, http.StatusInternalServerError, "could not add member")
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// handleUpdateRosterMember edits an existing pool entry's display name, Discord
// handle, timezone, availability, roles, and classes from the web app (e.g. to
// set or adjust availability time limits). Works for both manual and
// Discord-sourced members; the intake status/step and source are left untouched.
// Requires edit access.
func (s *Server) handleUpdateRosterMember(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "viewers cannot edit this team")
		return
	}
	memberID, err := strconv.ParseInt(r.PathValue("memberID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid member id")
		return
	}

	var req createRosterMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		writeError(w, http.StatusBadRequest, "display name is required")
		return
	}

	days, err := normalizeDays(req.Days)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	m := &models.RosterMember{
		ID:              memberID,
		TeamID:          teamID,
		DiscordUsername: strings.TrimSpace(req.DiscordUsername),
		DisplayName:     req.DisplayName,
		Timezone:        strings.TrimSpace(req.Timezone),
		Days:            days,
		Availability:    normalizeAvailability(req.Availability, days),
		Roles:           normalizeRoles(req.Roles),
		ClassesByRole:   normalizeClassesByRole(req.ClassesByRole),
	}
	updated, err := s.members.Update(r.Context(), m)
	if errors.Is(err, models.ErrMemberNotFound) {
		writeError(w, http.StatusNotFound, "member not found")
		return
	}
	if err != nil {
		log.Printf("update roster member: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update member")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleDeleteRosterMember removes a pool entry. Requires edit access.
func (s *Server) handleDeleteRosterMember(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "viewers cannot edit this team")
		return
	}
	memberID, err := strconv.ParseInt(r.PathValue("memberID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid member id")
		return
	}
	if err := s.members.Delete(r.Context(), teamID, memberID); err != nil {
		log.Printf("delete roster member: %v", err)
		writeError(w, http.StatusInternalServerError, "could not remove member")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizeRoles keeps only valid, de-duplicated role keys.
func normalizeRoles(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, r := range in {
		r = strings.TrimSpace(strings.ToLower(r))
		if r == "" || seen[r] || !models.ValidRoles[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	return out
}

// normalizeClassesByRole keeps only valid roles mapping to valid, de-duplicated
// class keys.
func normalizeClassesByRole(in map[string][]string) map[string][]string {
	out := map[string][]string{}
	for role, classes := range in {
		role = strings.TrimSpace(strings.ToLower(role))
		if !models.ValidRoles[role] {
			continue
		}
		seen := map[string]bool{}
		list := []string{}
		for _, c := range classes {
			c = strings.TrimSpace(strings.ToLower(c))
			if c == "" || seen[c] || !models.ValidClasses[c] {
				continue
			}
			seen[c] = true
			list = append(list, c)
		}
		if len(list) > 0 {
			out[role] = list
		}
	}
	return out
}

// normalizeAvailability keeps windows for chosen days. Start hours are clamped to
// 0-23; end hours to 1-24 (24 = midnight / end of day, so a window can run to the
// end of the day).
func normalizeAvailability(in map[string]models.DayWindow, days []string) map[string]models.DayWindow {
	allowed := map[string]bool{}
	for _, d := range days {
		allowed[d] = true
	}
	out := map[string]models.DayWindow{}
	for day, w := range in {
		day = strings.TrimSpace(strings.ToLower(day))
		if !allowed[day] {
			continue
		}
		out[day] = models.DayWindow{Start: clampHour(w.Start, 0, 23), End: clampHour(w.End, 1, 24)}
	}
	return out
}

func clampHour(h, lo, hi int) int {
	if h < lo {
		return lo
	}
	if h > hi {
		return hi
	}
	return h
}
