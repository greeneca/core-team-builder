package handlers

import (
	"crypto/rand"
	"encoding/base32"
	"log"
	"net/http"
	"time"

	"github.com/core-team-builder/backend/internal/auth"
)

// discordLinkCodeTTL is how long a generated Discord link code stays valid. Kept
// short since the user is expected to run /coreteam link right away.
const discordLinkCodeTTL = 15 * time.Minute

// linkCodeBytes is the entropy (in bytes) behind a link code. 5 bytes -> an
// 8-char base32 code, short enough to type into Discord but hard to guess.
const linkCodeBytes = 5

// generateLinkCode returns a short, uppercase, human-typable code plus its
// SHA-256 hash (only the hash is stored, mirroring other opaque credentials).
func generateLinkCode() (code string, hash string, err error) {
	raw := make([]byte, linkCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	code = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return code, auth.HashRefreshToken(code), nil
}

// handleDiscordLinkCode issues a one-time code the user types into Discord via
// /coreteam link to connect their Discord identity to this account.
func (s *Server) handleDiscordLinkCode(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	// Cancel any outstanding codes so only the newest is valid.
	if err := s.discord.InvalidateLinkCodesForUser(r.Context(), userID); err != nil {
		log.Printf("discord: invalidate link codes: %v", err)
		writeError(w, http.StatusInternalServerError, "could not generate code")
		return
	}

	code, hash, err := generateLinkCode()
	if err != nil {
		log.Printf("discord: generate link code: %v", err)
		writeError(w, http.StatusInternalServerError, "could not generate code")
		return
	}
	expiresAt := time.Now().Add(discordLinkCodeTTL)
	if err := s.discord.CreateLinkCode(r.Context(), userID, hash, expiresAt); err != nil {
		log.Printf("discord: persist link code: %v", err)
		writeError(w, http.StatusInternalServerError, "could not generate code")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"code":       code,
		"command":    "/coreteam link code:" + code,
		"expires_at": expiresAt,
	})
}

// handleGetDiscordLink reports whether the current user has linked a Discord
// account, and which.
func (s *Server) handleGetDiscordLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	link, err := s.discord.GetLink(r.Context(), userID)
	if err != nil {
		log.Printf("discord: get link: %v", err)
		writeError(w, http.StatusInternalServerError, "could not read link")
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleDeleteDiscordLink removes the current user's Discord link.
func (s *Server) handleDeleteDiscordLink(w http.ResponseWriter, r *http.Request) {
	userID, ok := auth.UserIDFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if err := s.discord.UnlinkUser(r.Context(), userID); err != nil {
		log.Printf("discord: unlink: %v", err)
		writeError(w, http.StatusInternalServerError, "could not unlink")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
