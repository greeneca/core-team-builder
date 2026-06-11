package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/models"
)

// teamAccess parses the {id} path value and resolves the caller's role on it.
// It writes the appropriate error response and returns ok=false when the team
// is missing or the caller lacks access (a 404 is used in both cases so the
// existence of other users' teams is not revealed). role is one of "owner",
// "editor", or "viewer".
func (s *Server) teamAccess(w http.ResponseWriter, r *http.Request) (teamID, userID int64, role string, ok bool) {
	userID, authed := auth.UserIDFromContext(r.Context())
	if !authed {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return 0, 0, "", false
	}

	teamID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid team id")
		return 0, 0, "", false
	}

	found, role, err := s.teams.Access(r.Context(), teamID, userID)
	if err != nil {
		log.Printf("team access check: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load team")
		return 0, 0, "", false
	}
	if !found {
		writeError(w, http.StatusNotFound, "team not found")
		return 0, 0, "", false
	}
	return teamID, userID, role, true
}

// canEdit reports whether a role may modify a team's name or roster.
func canEdit(role string) bool {
	return role == models.RoleOwner || role == models.RoleEditor
}

func (s *Server) handleListTeams(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	teams, err := s.teams.ListForUser(r.Context(), userID)
	if err != nil {
		log.Printf("list teams: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load teams")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": teams})
}

type teamNameRequest struct {
	Name string `json:"name"`
	// CopyFrom optionally identifies an existing team (which the caller must be
	// able to access) to seed the new team from: its schedule, roster, and
	// encounters/loadouts are copied. nil/0 = a fresh, empty team.
	CopyFrom *int64 `json:"copy_from"`
}

func (s *Server) handleCreateTeam(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	var req teamNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "team name is required")
		return
	}

	// Validate the optional copy source is a team the caller can access. A 404
	// from Access is reported as a generic "not found" so other users' teams
	// stay hidden.
	var copyFrom int64
	if req.CopyFrom != nil && *req.CopyFrom != 0 {
		copyFrom = *req.CopyFrom
		found, _, err := s.teams.Access(r.Context(), copyFrom, userID)
		if err != nil {
			log.Printf("create team copy access: %v", err)
			writeError(w, http.StatusInternalServerError, "could not create team")
			return
		}
		if !found {
			writeError(w, http.StatusBadRequest, "copy source team not found")
			return
		}
	}

	team, err := s.teams.Create(r.Context(), userID, req.Name, copyFrom)
	if err != nil {
		log.Printf("create team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create team")
		return
	}
	writeJSON(w, http.StatusCreated, team)
}

func (s *Server) handleGetTeam(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}

	// Any role (viewer/editor/owner) may read the team.
	team, err := s.teams.Get(r.Context(), teamID)
	if err != nil {
		log.Printf("get team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	writeJSON(w, http.StatusOK, team)
}

// updateTeamRequest is the "save everything" payload: team meta, schedule, and
// (optionally) the full roster. Players is keyed by Slot; omitted slots are
// left unchanged.
type updateTeamRequest struct {
	Name         string   `json:"name"`
	ScheduleDays []string `json:"schedule_days"`
	// ScheduleTime is the recurring trial time in UTC ("HH:MM"); the client
	// converts from the editor's current timezone before sending.
	ScheduleTime string `json:"schedule_time"`
	// TeamTimezones are extra IANA zones the team wants the time shown in.
	TeamTimezones []string        `json:"team_timezones"`
	Players       []playerPayload `json:"players"`
}

type playerPayload struct {
	Slot          int    `json:"slot"`
	Name          string `json:"name"`
	DiscordHandle string `json:"discord_handle"`
	Role          string `json:"role"`
	Class         string `json:"class"`
	Subclassed    bool   `json:"subclassed"`
	SkillLine1    string `json:"skill_line_1"`
	SkillLine2    string `json:"skill_line_2"`
	SkillLine3    string `json:"skill_line_3"`
	Mastery1      string `json:"mastery_1"`
	Mastery2      string `json:"mastery_2"`
}

func (s *Server) handleUpdateTeam(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "viewers cannot edit this team")
		return
	}

	var req updateTeamRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "team name is required")
		return
	}

	days, err := normalizeDays(req.ScheduleDays)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	scheduleTime := strings.TrimSpace(req.ScheduleTime)
	if !validTimeOfDay(scheduleTime) {
		writeError(w, http.StatusBadRequest, "time must be empty or HH:MM (24h)")
		return
	}

	teamTimezones, err := normalizeTimezones(req.TeamTimezones)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	players := make([]models.Player, 0, len(req.Players))
	for _, p := range req.Players {
		if p.Slot < 1 || p.Slot > models.TeamSize {
			writeError(w, http.StatusBadRequest, "invalid player slot")
			return
		}
		role := strings.TrimSpace(p.Role)
		class := strings.TrimSpace(p.Class)
		if !models.ValidRoles[role] {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if !models.ValidClasses[class] {
			writeError(w, http.StatusBadRequest, "invalid class")
			return
		}

		player := models.Player{
			Slot:          p.Slot,
			Name:          strings.TrimSpace(p.Name),
			DiscordHandle: strings.TrimSpace(p.DiscordHandle),
			Role:          role,
			Class:         class,
			Subclassed:    p.Subclassed,
		}

		// The subclass flag selects which build set applies. Validate only the
		// active set and clear the inactive one so stored data stays consistent.
		if p.Subclassed {
			s1 := strings.TrimSpace(p.SkillLine1)
			s2 := strings.TrimSpace(p.SkillLine2)
			s3 := strings.TrimSpace(p.SkillLine3)
			if !models.ValidSkillLine(s1) || !models.ValidSkillLine(s2) || !models.ValidSkillLine(s3) {
				writeError(w, http.StatusBadRequest, "invalid skill line")
				return
			}
			if err := models.ValidateSkillLines(class, []string{s1, s2, s3}); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			player.SkillLine1, player.SkillLine2, player.SkillLine3 = s1, s2, s3
		} else {
			m1 := strings.TrimSpace(p.Mastery1)
			m2 := strings.TrimSpace(p.Mastery2)
			if !models.ValidMastery(class, m1) || !models.ValidMastery(class, m2) {
				writeError(w, http.StatusBadRequest, "invalid class mastery")
				return
			}
			player.Mastery1, player.Mastery2 = m1, m2
		}

		players = append(players, player)
	}

	if err := s.teams.Save(r.Context(), teamID, req.Name, days, scheduleTime, teamTimezones, players); err != nil {
		log.Printf("update team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update team")
		return
	}
	team, err := s.teams.Get(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	writeJSON(w, http.StatusOK, team)
}

// normalizeDays lower-cases, validates, and de-duplicates day keys, returning
// them in canonical weekday order (mon→sun).
func normalizeDays(in []string) ([]string, error) {
	order := []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}
	seen := map[string]bool{}
	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if !models.ValidDays[d] {
			return nil, errors.New("invalid day: " + d)
		}
		seen[d] = true
	}
	out := make([]string, 0, len(seen))
	for _, d := range order {
		if seen[d] {
			out = append(out, d)
		}
	}
	return out, nil
}

// validTimeOfDay accepts "" (unset) or a 24h "HH:MM" string.
func validTimeOfDay(t string) bool {
	if t == "" {
		return true
	}
	parsed, err := time.Parse("15:04", t)
	if err != nil {
		return false
	}
	return parsed.Format("15:04") == t
}

// normalizeTimezones validates, de-duplicates, and orders a team's timezone
// list. Each must be a non-empty, loadable IANA name; empties are dropped.
// Order is preserved (first occurrence wins).
func normalizeTimezones(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, tz := range in {
		tz = strings.TrimSpace(tz)
		if tz == "" {
			continue
		}
		if _, err := time.LoadLocation(tz); err != nil {
			return nil, errors.New("invalid timezone: " + tz)
		}
		if seen[tz] {
			continue
		}
		seen[tz] = true
		out = append(out, tz)
	}
	return out, nil
}

func (s *Server) handleDeleteTeam(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if role != models.RoleOwner {
		writeError(w, http.StatusForbidden, "only the owner can delete this team")
		return
	}

	if err := s.teams.Delete(r.Context(), teamID); err != nil {
		log.Printf("delete team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete team")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type shareRequest struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

func (s *Server) handleShareTeam(w http.ResponseWriter, r *http.Request) {
	teamID, ownerID, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if role != models.RoleOwner {
		writeError(w, http.StatusForbidden, "only the owner can share this team")
		return
	}

	var req shareRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Role = strings.TrimSpace(req.Role)
	if req.Username == "" {
		writeError(w, http.StatusBadRequest, "username is required")
		return
	}
	if req.Role == "" {
		req.Role = models.RoleEditor
	}
	if !models.ValidShareRoles[req.Role] {
		writeError(w, http.StatusBadRequest, "role must be 'viewer' or 'editor'")
		return
	}

	target, err := s.users.GetByUsername(r.Context(), req.Username)
	if errors.Is(err, models.ErrUserNotFound) {
		writeError(w, http.StatusNotFound, "no user with that username")
		return
	}
	if err != nil {
		log.Printf("share lookup user: %v", err)
		writeError(w, http.StatusInternalServerError, "could not share team")
		return
	}
	if target.ID == ownerID {
		writeError(w, http.StatusBadRequest, "you already own this team")
		return
	}

	if err := s.teams.AddMember(r.Context(), teamID, target.ID, req.Role); err != nil {
		log.Printf("share team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not share team")
		return
	}
	team, err := s.teams.Get(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	writeJSON(w, http.StatusOK, team)
}

func (s *Server) handleUnshareTeam(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if role != models.RoleOwner {
		writeError(w, http.StatusForbidden, "only the owner can manage sharing")
		return
	}

	targetID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	if err := s.teams.RemoveMember(r.Context(), teamID, targetID); err != nil {
		log.Printf("unshare team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update sharing")
		return
	}
	team, err := s.teams.Get(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	writeJSON(w, http.StatusOK, team)
}
