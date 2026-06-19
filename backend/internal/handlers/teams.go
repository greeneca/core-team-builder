package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
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

	// Enforce the per-owner team cap so one account can't create unbounded teams.
	owned, err := s.teams.CountOwned(r.Context(), userID)
	if err != nil {
		log.Printf("count owned teams: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create team")
		return
	}
	if owned >= maxTeamsPerOwner {
		writeError(w, http.StatusConflict, fmt.Sprintf("team limit reached (max %d)", maxTeamsPerOwner))
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
	// EncountersEnabled toggles whether the team uses multiple encounters.
	EncountersEnabled bool `json:"encounters_enabled"`
	// PostFooter is the free-form footer the bot appends to its /coreteam post.
	PostFooter string `json:"post_footer"`
	// DMFooter is the free-form footer the bot appends to the build-details DM.
	DMFooter string `json:"dm_footer"`
	// SignupPost is the free-form body the bot posts with /coreteam recruit.
	SignupPost string `json:"signup_post"`
	// AutoSharePoolViewers, when true, auto-grants viewer access to the app
	// accounts of everyone in the team's member pool (current and future).
	AutoSharePoolViewers bool `json:"auto_share_pool_viewers"`
	// PreMade turns the team into a one-off pre-made trial run (slot signups via
	// the Discord bot's /coreteam signup flow).
	PreMade bool `json:"pre_made"`
	// PremadePost is the free-form body the bot prepends to a pre-made run post.
	PremadePost string `json:"premade_post"`
	// SimpleSignup switches a pre-made run to role-based "simple" signups (hides
	// class/gear and the details dropdown; claiming takes the first matching slot).
	SimpleSignup bool `json:"simple_signup"`
	// WaitlistEnabled lets players join a per-role waitlist on a pre-made run;
	// freed slots auto-promote the head of that role's waitlist.
	WaitlistEnabled bool `json:"waitlist_enabled"`
	// Roles is the team's customizable roster role set (key + label). When
	// omitted/empty the server falls back to the default role set.
	Roles   []models.TeamRole `json:"roles"`
	Players []playerPayload   `json:"players"`
	// ExpectedUpdatedAt, when set, enables optimistic concurrency: the save is
	// rejected with 409 if the team was modified by someone else since the
	// client loaded it. Omitted by older clients (last-write-wins).
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

type playerPayload struct {
	Slot          int    `json:"slot"`
	Name          string `json:"name"`
	DiscordHandle string `json:"discord_handle"`
	Role          string `json:"role"`
	Class         string `json:"class"`
	Race          string `json:"race"`
	Subclassed    bool   `json:"subclassed"`
	SkillLine1    string `json:"skill_line_1"`
	SkillLine2    string `json:"skill_line_2"`
	SkillLine3    string `json:"skill_line_3"`
	Mastery1      string `json:"mastery_1"`
	Mastery2      string `json:"mastery_2"`
	Werewolf      bool   `json:"werewolf"`
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

	// A team has exactly TeamSize slots, so a payload with more player entries
	// than that is malformed (or abusive) — reject before doing per-row work.
	if len(req.Players) > models.TeamSize {
		writeError(w, http.StatusBadRequest, "too many players")
		return
	}

	postFooter := strings.TrimRight(req.PostFooter, " \t\r\n")
	if len([]rune(postFooter)) > maxPostFooterLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("post footer too long (max %d characters)", maxPostFooterLen))
		return
	}

	dmFooter := strings.TrimRight(req.DMFooter, " \t\r\n")
	if len([]rune(dmFooter)) > maxDMFooterLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("DM footer too long (max %d characters)", maxDMFooterLen))
		return
	}

	signupPost := strings.TrimRight(req.SignupPost, " \t\r\n")
	if len([]rune(signupPost)) > maxSignupPostLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("signup post too long (max %d characters)", maxSignupPostLen))
		return
	}

	premadePost := strings.TrimRight(req.PremadePost, " \t\r\n")
	if len([]rune(premadePost)) > maxPremadePostLen {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("pre-made post too long (max %d characters)", maxPremadePostLen))
		return
	}

	// Normalize the team's roster role set. An empty/omitted list falls back to
	// the default roles so older clients (and pre-roles teams) keep working.
	roles, err := normalizeTeamRoles(req.Roles)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	validRole := make(map[string]bool, len(roles))
	for _, rl := range roles {
		validRole[rl.Key] = true
	}

	players := make([]models.Player, 0, len(req.Players))
	for _, p := range req.Players {
		player, verr := validatePlayer(p, validRole)
		if verr != nil {
			writeError(w, http.StatusBadRequest, verr.Error())
			return
		}
		players = append(players, player)
	}

	var expected time.Time
	if req.ExpectedUpdatedAt != nil {
		expected = *req.ExpectedUpdatedAt
	}
	if err := s.teams.Save(r.Context(), teamID, req.Name, days, scheduleTime, req.EncountersEnabled, postFooter, dmFooter, signupPost, req.AutoSharePoolViewers, req.PreMade, premadePost, req.SimpleSignup, req.WaitlistEnabled, roles, players, expected); err != nil {
		if errors.Is(err, models.ErrVersionConflict) {
			writeError(w, http.StatusConflict, "this team was changed by someone else; reload to get the latest")
			return
		}
		log.Printf("update team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update team")
		return
	}

	// When auto-share is on, reconcile the member pool into viewer shares so
	// enabling it (or saving while it's on) immediately shares with every current
	// pool member who has an app account. Idempotent and non-destructive; a
	// failure here shouldn't fail the save, so it's logged and ignored.
	if req.AutoSharePoolViewers {
		if err := s.teams.SharePoolMembers(r.Context(), teamID); err != nil {
			log.Printf("auto-share pool members: %v", err)
		}
	}

	team, err := s.teams.Get(r.Context(), teamID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	writeJSON(w, http.StatusOK, team)
}

// validatePlayer validates and normalizes one player payload against the team's
// valid role set, returning the model ready to save. It enforces the same rules
// for the bulk team save and the per-slot save so both stay consistent. The
// returned error message is safe to surface to the client (400).
func validatePlayer(p playerPayload, validRole map[string]bool) (models.Player, error) {
	if p.Slot < 1 || p.Slot > models.TeamSize {
		return models.Player{}, errors.New("invalid player slot")
	}
	role := strings.TrimSpace(p.Role)
	class := strings.TrimSpace(p.Class)
	race := strings.TrimSpace(p.Race)
	if !validRole[role] {
		return models.Player{}, errors.New("invalid role")
	}
	if !models.ValidClasses[class] {
		return models.Player{}, errors.New("invalid class")
	}
	if !models.ValidRace(race) {
		return models.Player{}, errors.New("invalid race")
	}

	player := models.Player{
		Slot:          p.Slot,
		Name:          strings.TrimSpace(p.Name),
		DiscordHandle: strings.TrimSpace(p.DiscordHandle),
		Role:          role,
		Class:         class,
		Race:          race,
		Subclassed:    p.Subclassed,
		Werewolf:      p.Werewolf,
	}

	// The subclass flag selects which build set applies. Validate only the active
	// set and clear the inactive one so stored data stays consistent.
	if p.Subclassed {
		s1 := strings.TrimSpace(p.SkillLine1)
		s2 := strings.TrimSpace(p.SkillLine2)
		s3 := strings.TrimSpace(p.SkillLine3)
		if !models.ValidSkillLine(s1) || !models.ValidSkillLine(s2) || !models.ValidSkillLine(s3) {
			return models.Player{}, errors.New("invalid skill line")
		}
		if err := models.ValidateSkillLines(class, []string{s1, s2, s3}); err != nil {
			return models.Player{}, err
		}
		player.SkillLine1, player.SkillLine2, player.SkillLine3 = s1, s2, s3
	} else {
		m1 := strings.TrimSpace(p.Mastery1)
		m2 := strings.TrimSpace(p.Mastery2)
		if !models.ValidMastery(class, m1) || !models.ValidMastery(class, m2) {
			return models.Player{}, errors.New("invalid class mastery")
		}
		player.Mastery1, player.Mastery2 = m1, m2
	}
	return player, nil
}

// savePlayerRequest is the per-slot roster save payload: one player plus an
// optional optimistic-concurrency token.
type savePlayerRequest struct {
	playerPayload
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at"`
}

// handleSavePlayer updates a single roster slot. It is the finer-grained
// counterpart to handleUpdateTeam's bulk roster save, so two editors changing
// different slots don't overwrite each other. The slot comes from the path; the
// optional expected_updated_at guards against clobbering a concurrent edit (409).
func (s *Server) handleSavePlayer(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "viewers cannot edit this team")
		return
	}

	slot, err := strconv.Atoi(r.PathValue("slot"))
	if err != nil || slot < 1 || slot > models.TeamSize {
		writeError(w, http.StatusBadRequest, "invalid player slot")
		return
	}

	var req savePlayerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Slot = slot // the path is authoritative

	roles, err := s.teams.GetRoles(r.Context(), teamID)
	if err != nil {
		log.Printf("save player roles: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update player")
		return
	}
	validRole := make(map[string]bool, len(roles))
	for _, rl := range roles {
		validRole[rl.Key] = true
	}

	player, verr := validatePlayer(req.playerPayload, validRole)
	if verr != nil {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}

	var expected time.Time
	if req.ExpectedUpdatedAt != nil {
		expected = *req.ExpectedUpdatedAt
	}
	saved, err := s.teams.SavePlayer(r.Context(), teamID, player, expected)
	if errors.Is(err, models.ErrVersionConflict) {
		writeError(w, http.StatusConflict, "this slot was changed by someone else; reload to get the latest")
		return
	}
	if errors.Is(err, models.ErrTeamNotFound) {
		writeError(w, http.StatusNotFound, "player not found")
		return
	}
	if err != nil {
		log.Printf("save player: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update player")
		return
	}
	writeJSON(w, http.StatusOK, saved)
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

// normalizeRoles validates, slugifies, and de-duplicates the team's roster role
// set, preserving input order. An empty list falls back to the default roles so
// older clients (and teams created before custom roles) keep working. Each role
// needs a non-empty label; its key is slugified from the provided key (or the
// label) and made unique within the set. Each role must also map to a color
// category (Base) in models.ValidRoleBases so the roster color coding keeps
// working; a missing base falls back to the key when that is itself a base, else
// to models.DefaultRoleBase.
func normalizeTeamRoles(in []models.TeamRole) (models.TeamRoles, error) {
	if len(in) == 0 {
		return models.DefaultTeamRoles(), nil
	}
	if len(in) > maxTeamRoles {
		return nil, fmt.Errorf("too many roles (max %d)", maxTeamRoles)
	}
	out := make(models.TeamRoles, 0, len(in))
	seen := map[string]bool{}
	for _, rl := range in {
		label := strings.TrimSpace(rl.Label)
		if label == "" {
			return nil, errors.New("role label cannot be empty")
		}
		if len([]rune(label)) > maxRoleLabelLen {
			return nil, fmt.Errorf("role label too long (max %d characters)", maxRoleLabelLen)
		}
		key := slugifyRole(rl.Key)
		if key == "" {
			key = slugifyRole(label)
		}
		if key == "" {
			return nil, fmt.Errorf("role %q has no usable key", label)
		}
		// Resolve the color category. An explicit base must be one of the known
		// categories; an empty one derives from the key (when it is itself a
		// base) or the default, so older clients keep working.
		roleBase := strings.TrimSpace(rl.Base)
		if roleBase == "" {
			if models.ValidRoleBases[key] {
				roleBase = key
			} else {
				roleBase = models.DefaultRoleBase
			}
		} else if !models.ValidRoleBases[roleBase] {
			return nil, fmt.Errorf("role %q has an invalid color category", label)
		}
		// Ensure the key is unique within the set (append a counter on collision).
		base, n := key, 2
		for seen[key] {
			key = fmt.Sprintf("%s_%d", base, n)
			n++
		}
		seen[key] = true
		out = append(out, models.TeamRole{Key: key, Label: label, Base: roleBase})
	}
	return out, nil
}

// slugifyRole reduces a string to a stable role key: lowercase, with runs of
// non-alphanumeric characters collapsed to single underscores and trimmed.
func slugifyRole(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevUnderscore := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
		} else if !prevUnderscore && b.Len() > 0 {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
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
		// Audit username lookups so enumeration via this endpoint (a known,
		// by-design oracle since sharing is by username) is detectable. The
		// client-facing message is intentionally unchanged.
		log.Printf("audit: share lookup miss: caller=%d team=%d username=%q", ownerID, teamID, req.Username)
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
	log.Printf("audit: share granted: caller=%d team=%d target=%d role=%s", ownerID, teamID, target.ID, req.Role)
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

// handleLeaveTeam lets a shared member (editor/viewer) remove their own access
// to a team. The owner cannot leave their own team (they delete it instead).
// Returns 204 since the caller no longer has access to the team afterward.
func (s *Server) handleLeaveTeam(w http.ResponseWriter, r *http.Request) {
	teamID, userID, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if role == models.RoleOwner {
		writeError(w, http.StatusForbidden, "the owner cannot leave their own team")
		return
	}

	if err := s.teams.RemoveMember(r.Context(), teamID, userID); err != nil {
		log.Printf("leave team: %v", err)
		writeError(w, http.StatusInternalServerError, "could not leave team")
		return
	}
	log.Printf("audit: member left: user=%d team=%d", userID, teamID)
	w.WriteHeader(http.StatusNoContent)
}
