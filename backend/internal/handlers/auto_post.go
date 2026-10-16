package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/core-team-builder/backend/internal/models"
)

// autoPostResponse is what both auto-post endpoints return: the saved settings
// plus the channels bound to the team, which fill the channel dropdown.
type autoPostResponse struct {
	models.AutoPostSettings
	Channels []models.BoundChannel `json:"channels"`
}

func (s *Server) loadAutoPost(w http.ResponseWriter, r *http.Request, teamID int64) {
	settings, err := s.autoPost.Get(r.Context(), teamID)
	if err != nil {
		log.Printf("get auto post: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load auto post settings")
		return
	}
	channels, err := s.autoPost.ListChannels(r.Context(), teamID)
	if err != nil {
		log.Printf("list bound channels: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load auto post settings")
		return
	}
	writeJSON(w, http.StatusOK, autoPostResponse{AutoPostSettings: settings, Channels: channels})
}

// handleGetAutoPost returns the team's auto post / auto delete settings. Any
// team member may read them.
func (s *Server) handleGetAutoPost(w http.ResponseWriter, r *http.Request) {
	teamID, _, _, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	s.loadAutoPost(w, r, teamID)
}

// handlePutAutoPost saves the settings. Owners and editors only. The channel
// must be one bound to this team; anything else is rejected, so a request cannot
// aim the bot at a channel the team doesn't own.
func (s *Server) handlePutAutoPost(w http.ResponseWriter, r *http.Request) {
	teamID, _, role, ok := s.teamAccess(w, r)
	if !ok {
		return
	}
	if !canEdit(role) {
		writeError(w, http.StatusForbidden, "you do not have permission to edit this team")
		return
	}

	var req models.AutoPostSettings
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.autoPost.Save(r.Context(), teamID, req); err != nil {
		if errors.Is(err, models.ErrAutoPostChannelInvalid) {
			writeError(w, http.StatusBadRequest, "choose one of this team's bound channels")
			return
		}
		log.Printf("save auto post: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save auto post settings")
		return
	}
	s.loadAutoPost(w, r, teamID)
}
