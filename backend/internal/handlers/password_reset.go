package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/core-team-builder/backend/internal/auth"
	"github.com/core-team-builder/backend/internal/models"
)

type forgotPasswordRequest struct {
	Email string `json:"email"`
}

type resetPasswordRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// genericResetMessage is returned for any forgot-password request, whether or
// not the email matches an account, so the endpoint cannot be used to enumerate
// registered addresses.
const genericResetMessage = "If an account exists for that email, a password reset link has been sent."

// handleForgotPassword starts the password-reset flow. It always responds 200
// with a generic message; when the email matches a user, it issues a single-use
// reset token and emails a reset link. Token generation/email delivery happen
// without blocking the response on slow SMTP.
func (s *Server) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	email := strings.TrimSpace(req.Email)
	if email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	user, err := s.users.GetByEmail(r.Context(), email)
	if errors.Is(err, models.ErrUserNotFound) {
		// Do not reveal that the email is unknown.
		writeJSON(w, http.StatusOK, map[string]string{"message": genericResetMessage})
		return
	}
	if err != nil {
		log.Printf("forgot password lookup: %v", err)
		// Still respond generically so failures don't leak account existence.
		writeJSON(w, http.StatusOK, map[string]string{"message": genericResetMessage})
		return
	}

	// Cancel any outstanding reset links for this user, then mint a fresh one.
	if err := s.passwordResets.InvalidateForUser(r.Context(), user.ID); err != nil {
		log.Printf("invalidate prior resets: %v", err)
	}

	token, hash, err := auth.GenerateOpaqueToken()
	if err != nil {
		log.Printf("generate reset token: %v", err)
		writeJSON(w, http.StatusOK, map[string]string{"message": genericResetMessage})
		return
	}
	expiresAt := time.Now().Add(s.passwordResetTTL)
	if err := s.passwordResets.Create(r.Context(), user.ID, hash, expiresAt); err != nil {
		log.Printf("persist reset token: %v", err)
		writeJSON(w, http.StatusOK, map[string]string{"message": genericResetMessage})
		return
	}

	s.sendResetEmail(user.Email, user.Username, token)

	writeJSON(w, http.StatusOK, map[string]string{"message": genericResetMessage})
}

// sendResetEmail composes and sends the reset link. Delivery runs in the
// background with its own timeout so a slow SMTP server doesn't stall the HTTP
// response (which is intentionally generic regardless of outcome).
func (s *Server) sendResetEmail(toEmail, username, token string) {
	link := fmt.Sprintf("%s/reset.html?token=%s", strings.TrimRight(s.appBaseURL, "/"), url.QueryEscape(token))
	ttl := s.passwordResetTTL.Round(time.Minute)
	subject := "Reset your Core Team Builder password"
	body := fmt.Sprintf(
		"Hi %s,\r\n\r\n"+
			"We received a request to reset the password for your Core Team Builder account.\r\n"+
			"Use the link below to choose a new password. This link expires in %s.\r\n\r\n"+
			"%s\r\n\r\n"+
			"If you didn't request this, you can safely ignore this email — your password will not change.\r\n",
		username, formatDuration(ttl), link,
	)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.mailer.Send(ctx, toEmail, subject, body); err != nil {
			log.Printf("send reset email: %v", err)
		}
	}()
}

// handleResetPassword completes the flow: it consumes a valid reset token, sets
// the new password, and revokes the user's existing sessions ("sign out
// everywhere") so a compromised session can't survive a reset.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "token and password are required")
		return
	}

	// Validate and hash the new password before consuming the token, so a policy
	// failure leaves the token usable for a retry.
	hash, err := auth.HashPassword(req.Password)
	if auth.IsPasswordPolicyError(err) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		log.Printf("hash password: %v", err)
		writeError(w, http.StatusInternalServerError, "could not reset password")
		return
	}

	userID, err := s.passwordResets.Consume(r.Context(), auth.HashRefreshToken(req.Token))
	if errors.Is(err, models.ErrPasswordResetInvalid) {
		writeError(w, http.StatusBadRequest, "invalid or expired reset link")
		return
	}
	if err != nil {
		log.Printf("consume reset token: %v", err)
		writeError(w, http.StatusInternalServerError, "could not reset password")
		return
	}

	if err := s.users.UpdatePassword(r.Context(), userID, hash); err != nil {
		log.Printf("update password: %v", err)
		writeError(w, http.StatusInternalServerError, "could not reset password")
		return
	}

	// Invalidate all active sessions after a password change.
	if err := s.refreshTokens.RevokeAllForUser(r.Context(), userID); err != nil {
		log.Printf("revoke sessions after reset: %v", err)
	}

	writeJSON(w, http.StatusOK, map[string]string{"message": "Your password has been reset. You can now sign in."})
}

// formatDuration renders a reset-link lifetime in human terms (e.g. "1 hour",
// "30 minutes") for the email body.
func formatDuration(d time.Duration) string {
	if d >= time.Hour && d%time.Hour == 0 {
		h := int(d / time.Hour)
		if h == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", h)
	}
	m := int(d / time.Minute)
	if m <= 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", m)
}
