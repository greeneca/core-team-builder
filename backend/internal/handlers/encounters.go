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

// encounterAccess resolves both the team (and caller role) and the encounter,
// ensuring the encounter belongs to the team. It writes the error response and
// returns ok=false on any failure.
func (s *Server) encounterAccess(w http.ResponseWriter, r *http.Request) (teamID int64, role string, enc *models.Encounter, ok bool) {
	teamID, _, role, ok = s.teamAccess(w, r)
	if !ok {
		return 0, "", nil, false
	}

	encID, err := strconv.ParseInt(r.PathValue("eid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid encounter id")
		return 0, "", nil, false
	}

	enc, err = s.encounters.Get(r.Context(), encID)
	if errors.Is(err, models.ErrEncounterNotFound) {
		writeError(w, http.StatusNotFound, "encounter not found")
		return 0, "", nil, false
	}
	if err != nil {
		log.Printf("get encounter: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load encounter")
		return 0, "", nil, false
	}
	if enc.TeamID != teamID {
		writeError(w, http.StatusNotFound, "encounter not found")
		return 0, "", nil, false
	}
	return teamID, role, enc, true
}

func (s *Server) handleListEncounters(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	encounters, err := s.encounters.ListForTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("list encounters: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load encounters")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"encounters": encounters})
}

type encounterNameRequest struct {
	Name string `json:"name"`
	// CopyFrom optionally identifies an existing encounter (same team) whose
	// per-player gear/skills are copied into the new one. nil/0 = empty.
	CopyFrom *int64 `json:"copy_from"`
}

// encounterNames returns the names of the given encounters, skipping excludeID
// (pass 0 to keep all). Used to validate uniqueness + single-trial rules.
func encounterNames(encs []models.Encounter, excludeID int64) []string {
	names := make([]string, 0, len(encs))
	for _, e := range encs {
		if e.ID == excludeID {
			continue
		}
		names = append(names, e.Name)
	}
	return names
}

func (s *Server) handleCreateEncounter(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	var req encounterNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	existing, err := s.encounters.ListForTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("list encounters: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create encounter")
		return
	}
	if verr := models.ValidateEncounterSelection(encounterNames(existing, 0), req.Name); verr != nil {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}

	// Validate the optional copy source belongs to this team.
	var copyFrom int64
	if req.CopyFrom != nil && *req.CopyFrom != 0 {
		copyFrom = *req.CopyFrom
		found := false
		for _, e := range existing {
			if e.ID == copyFrom {
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusBadRequest, "copy source encounter not found")
			return
		}
	}

	enc, err := s.encounters.Create(r.Context(), teamID, req.Name, copyFrom)
	if err != nil {
		log.Printf("create encounter: %v", err)
		writeError(w, http.StatusInternalServerError, "could not create encounter")
		return
	}
	writeJSON(w, http.StatusCreated, enc)
}

func (s *Server) handleGetEncounter(w http.ResponseWriter, r *http.Request) {
	_, _, enc, ok := s.encounterAccess(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, enc)
}

func (s *Server) handleUpdateEncounter(w http.ResponseWriter, r *http.Request) {
	teamID, role, enc, ok := s.encounterAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	var req encounterNameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	existing, err := s.encounters.ListForTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("list encounters: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update encounter")
		return
	}
	// Exclude the encounter being renamed so it doesn't conflict with itself.
	if verr := models.ValidateEncounterSelection(encounterNames(existing, enc.ID), req.Name); verr != nil {
		writeError(w, http.StatusBadRequest, verr.Error())
		return
	}

	if err := s.encounters.UpdateName(r.Context(), enc.ID, req.Name); err != nil {
		log.Printf("update encounter: %v", err)
		writeError(w, http.StatusInternalServerError, "could not update encounter")
		return
	}
	updated, err := s.encounters.Get(r.Context(), enc.ID)
	if err != nil {
		log.Printf("reload encounter: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load encounter")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteEncounter(w http.ResponseWriter, r *http.Request) {
	teamID, role, enc, ok := s.encounterAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	count, err := s.encounters.CountForTeam(r.Context(), teamID)
	if err != nil {
		log.Printf("count encounters: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete encounter")
		return
	}
	if count <= 1 {
		writeError(w, http.StatusBadRequest, "a team must have at least one encounter")
		return
	}

	if err := s.encounters.Delete(r.Context(), enc.ID); err != nil {
		log.Printf("delete encounter: %v", err)
		writeError(w, http.StatusInternalServerError, "could not delete encounter")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type loadoutPayload struct {
	Slot   int      `json:"slot"`
	Gear   []string `json:"gear"`
	Skills []string `json:"skills"`
}

type saveLoadoutsRequest struct {
	Loadouts []loadoutPayload `json:"loadouts"`
}

func (s *Server) handleSaveLoadouts(w http.ResponseWriter, r *http.Request) {
	_, role, enc, ok := s.encounterAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	var req saveLoadoutsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	loadouts := make([]models.Loadout, 0, len(req.Loadouts))
	for _, p := range req.Loadouts {
		if p.Slot < 1 || p.Slot > models.TeamSize {
			writeError(w, http.StatusBadRequest, "invalid player slot")
			return
		}
		gear, err := models.SanitizeLoadoutItems(p.Gear)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid gear list")
			return
		}
		skills, err := models.SanitizeLoadoutItems(p.Skills)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid skills list")
			return
		}
		loadouts = append(loadouts, models.Loadout{Slot: p.Slot, Gear: gear, Skills: skills})
	}

	if err := s.encounters.SaveLoadouts(r.Context(), enc.ID, loadouts); err != nil {
		log.Printf("save loadouts: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save loadouts")
		return
	}

	updated, err := s.encounters.Get(r.Context(), enc.ID)
	if err != nil {
		log.Printf("reload encounter: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load encounter")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}
