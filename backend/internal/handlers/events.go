package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/core-team-builder/backend/internal/realtime"
)

// ssePingInterval is how often a keepalive comment is sent so idle connections
// (and any intermediary proxy with a read timeout) stay open.
const ssePingInterval = 25 * time.Second

// handleTeamEvents streams a team's change + presence events to the browser over
// Server-Sent Events. The browser's EventSource cannot set an Authorization
// header, so the access token is read from the access_token query parameter and
// validated here directly (the route is registered without the bearer
// middleware). Any role with access to the team may subscribe.
func (s *Server) handleTeamEvents(w http.ResponseWriter, r *http.Request) {
	if s.realtime == nil {
		writeError(w, http.StatusServiceUnavailable, "live updates unavailable")
		return
	}

	claims, err := s.tokens.Parse(r.URL.Query().Get("access_token"))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token subject")
		return
	}

	teamID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid team id")
		return
	}
	found, _, err := s.teams.Access(r.Context(), teamID, userID)
	if err != nil {
		log.Printf("events team access: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load team")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "team not found")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	// This is a long-lived response, so clear the server's write deadline (set
	// from http.Server.WriteTimeout) for this connection only.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tell nginx not to buffer this response so events are delivered promptly.
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := s.realtime.Subscribe(teamID, claims.Username)
	defer unsubscribe()

	ctx := r.Context()
	ping := time.NewTicker(ssePingInterval)
	defer ping.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if !writeSSE(w, ev) {
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE serializes one event as an SSE "data:" frame. Returns false if the
// connection write failed (caller should stop).
func writeSSE(w io.Writer, ev realtime.Event) bool {
	b, err := json.Marshal(ev)
	if err != nil {
		return true
	}
	_, err = fmt.Fprintf(w, "data: %s\n\n", b)
	return err == nil
}
