package handlers

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/core-team-builder/backend/internal/models"
)

// Discord OAuth2 endpoints and the scopes we request. `identify` yields the
// user's id/username; `email` yields the (possibly verified) email used to
// auto-link an existing account.
const (
	discordAuthorizeURL = "https://discord.com/api/oauth2/authorize"
	discordTokenURL     = "https://discord.com/api/oauth2/token"
	discordUserURL      = "https://discord.com/api/users/@me"
	discordOAuthScopes  = "identify email"
)

// oauthStateCookie is the name of the short-lived, HttpOnly cookie holding the
// CSRF state we compare against the `state` query parameter on callback.
const oauthStateCookie = "ctb_doauth_state"

// oauthConsentCookie marks that the current sign-in attempt already showed
// Discord's interactive consent screen (prompt=consent). It lets the callback
// tell "user hasn't authorized yet" (silent attempt failed → retry with consent)
// apart from "user declined the consent screen" (→ show an error).
const oauthConsentCookie = "ctb_doauth_consent"

// oauthStateTTL bounds how long a started Discord sign-in may take to complete.
const oauthStateTTL = 10 * time.Minute

// discordHTTPClient is a bounded client for the server-to-Discord OAuth calls so
// a slow/hung Discord can't tie up a request goroutine indefinitely.
var discordHTTPClient = &http.Client{Timeout: 10 * time.Second}

// discordOAuthUser is the subset of Discord's /users/@me we consume.
type discordOAuthUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Email      string `json:"email"`
	Verified   bool   `json:"verified"`
}

// handleDiscordOAuthLogin starts the Discord sign-in flow: it sets a CSRF state
// cookie and redirects the browser to Discord's authorization page.
func (s *Server) handleDiscordOAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.discordOAuth.Enabled() {
		http.NotFound(w, r)
		return
	}

	state, err := randomState()
	if err != nil {
		log.Printf("discord oauth: generate state: %v", err)
		s.redirectDiscordError(w, r, "server_error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/api/auth/discord",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})

	// First we try silently (prompt=none) so returning users who already
	// authorized the app sign in with one click instead of re-confirming every
	// time. ?reauth=1 (set by the callback when the silent attempt reports the
	// user hasn't authorized yet) switches to the interactive consent screen.
	interactive := r.URL.Query().Get("reauth") == "1"
	prompt := "none"
	if interactive {
		prompt = "consent"
		// Record that this attempt is the interactive one, so the callback can
		// distinguish a genuine decline from "needs consent".
		http.SetCookie(w, &http.Cookie{
			Name:     oauthConsentCookie,
			Value:    "1",
			Path:     "/api/auth/discord",
			MaxAge:   int(oauthStateTTL.Seconds()),
			HttpOnly: true,
			Secure:   isHTTPS(r),
			SameSite: http.SameSiteLaxMode,
		})
	}

	log.Printf("discord oauth: login start (prompt=%s)", prompt)

	q := url.Values{}
	q.Set("client_id", s.discordOAuth.ClientID)
	q.Set("redirect_uri", s.discordOAuth.RedirectURL)
	q.Set("response_type", "code")
	q.Set("scope", discordOAuthScopes)
	q.Set("state", state)
	q.Set("prompt", prompt)
	http.Redirect(w, r, discordAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

// handleDiscordOAuthCallback completes the flow: it verifies the state, exchanges
// the code for a Discord identity, resolves (or creates) the matching app
// account, issues tokens, and redirects back to the frontend with them.
func (s *Server) handleDiscordOAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !s.discordOAuth.Enabled() {
		http.NotFound(w, r)
		return
	}

	// Clear the state + consent cookies regardless of outcome so they can't be
	// replayed.
	defer http.SetCookie(w, &http.Cookie{
		Name: oauthStateCookie, Value: "", Path: "/api/auth/discord", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	defer http.SetCookie(w, &http.Cookie{
		Name: oauthConsentCookie, Value: "", Path: "/api/auth/discord", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		desc := r.URL.Query().Get("error_description")
		// A failed *silent* (prompt=none) attempt usually just means the user
		// hasn't authorized the app yet (or the scopes changed) — fall back to the
		// interactive consent screen once. If the *interactive* attempt also fails
		// (consent cookie present), the user genuinely declined.
		if _, err := r.Cookie(oauthConsentCookie); err != nil {
			log.Printf("discord oauth: silent auth failed (error=%s desc=%q); retrying with consent", errParam, desc)
			http.Redirect(w, r, s.appBaseURL+"/api/auth/discord/login?reauth=1", http.StatusFound)
			return
		}
		log.Printf("discord oauth: interactive consent declined (error=%s desc=%q)", errParam, desc)
		s.redirectDiscordError(w, r, "access_denied")
		return
	}

	// CSRF: the state in the query must match the one we put in the cookie.
	cookie, err := r.Cookie(oauthStateCookie)
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(cookie.Value)) != 1 {
		s.redirectDiscordError(w, r, "invalid_state")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		s.redirectDiscordError(w, r, "invalid_request")
		return
	}

	du, err := s.exchangeDiscordCode(r.Context(), code)
	if err != nil {
		log.Printf("discord oauth: exchange: %v", err)
		s.redirectDiscordError(w, r, "server_error")
		return
	}
	if du.Email == "" {
		// Without an email we can neither auto-link nor satisfy the NOT NULL
		// email column; ask the user to use a password account instead.
		s.redirectDiscordError(w, r, "no_email")
		return
	}

	user, errCode := s.resolveDiscordUser(r.Context(), du)
	if errCode != "" {
		s.redirectDiscordError(w, r, errCode)
		return
	}

	token, refreshToken, expiresIn, err := s.issueTokens(r.Context(), user)
	if err != nil {
		log.Printf("discord oauth: issue tokens: %v", err)
		s.redirectDiscordError(w, r, "server_error")
		return
	}

	log.Printf("discord oauth: sign-in ok (user=%d %s)", user.ID, user.Username)

	frag := url.Values{}
	frag.Set("token", token)
	frag.Set("refresh_token", refreshToken)
	frag.Set("expires_in", strconv.Itoa(expiresIn))
	// Tokens go in the URL fragment so they are never sent to the server (and
	// thus never logged); discord.js reads them client-side and clears the hash.
	http.Redirect(w, r, s.appBaseURL+"/discord.html#"+frag.Encode(), http.StatusFound)
}

// resolveDiscordUser maps a Discord identity to an app user, creating or
// auto-linking as needed. It returns a non-empty error code (for the redirect)
// instead of writing a response. The rules:
//   - Already linked → sign in as that account.
//   - Email matches an existing account → auto-link, but only when the Discord
//     email is verified and that account isn't already linked elsewhere.
//   - Otherwise → create a new account, honoring the registration toggle (the
//     first-ever account always succeeds and becomes admin).
func (s *Server) resolveDiscordUser(ctx context.Context, du discordOAuthUser) (*models.User, string) {
	dname := du.GlobalName
	if dname == "" {
		dname = du.Username
	}

	// 1. This Discord identity is already linked to an account.
	if appUserID, err := s.discord.GetUserByDiscordID(ctx, du.ID); err == nil {
		user, err := s.users.GetByID(ctx, appUserID)
		if err != nil {
			log.Printf("discord oauth: load linked user: %v", err)
			return nil, "server_error"
		}
		return user, ""
	} else if !errors.Is(err, models.ErrUserNotFound) {
		log.Printf("discord oauth: lookup by discord id: %v", err)
		return nil, "server_error"
	}

	// 2. An account with this email already exists → consider auto-linking.
	existing, err := s.users.GetByEmail(ctx, du.Email)
	if err == nil {
		if !du.Verified {
			return nil, "email_unverified"
		}
		link, err := s.discord.GetLink(ctx, existing.ID)
		if err != nil {
			log.Printf("discord oauth: get link: %v", err)
			return nil, "server_error"
		}
		if link.Linked && link.DiscordUserID != du.ID {
			return nil, "already_linked_other"
		}
		if err := s.discord.LinkUser(ctx, existing.ID, du.ID, dname); err != nil {
			if errors.Is(err, models.ErrDiscordAlreadyLinked) {
				return nil, "already_linked_other"
			}
			log.Printf("discord oauth: link existing: %v", err)
			return nil, "server_error"
		}
		return existing, ""
	} else if !errors.Is(err, models.ErrUserNotFound) {
		log.Printf("discord oauth: lookup by email: %v", err)
		return nil, "server_error"
	}

	// 3. Brand-new account. Honor the registration toggle (first user bootstraps).
	count, err := s.users.Count(ctx)
	if err != nil {
		log.Printf("discord oauth: count users: %v", err)
		return nil, "server_error"
	}
	firstUser := count == 0
	if !firstUser {
		enabled, err := s.settings.RegistrationEnabled(ctx)
		if err != nil {
			log.Printf("discord oauth: registration enabled: %v", err)
			return nil, "server_error"
		}
		if !enabled {
			return nil, "registration_disabled"
		}
	}

	username, err := s.uniqueUsername(ctx, dname)
	if err != nil {
		log.Printf("discord oauth: unique username: %v", err)
		return nil, "server_error"
	}
	user, err := s.users.CreateDiscordUser(ctx, username, strings.ToLower(du.Email), du.ID, dname, firstUser)
	if err != nil {
		log.Printf("discord oauth: create user: %v", err)
		return nil, "server_error"
	}
	return user, ""
}

// exchangeDiscordCode trades an authorization code for an access token and uses
// it to fetch the authorizing user's identity.
func (s *Server) exchangeDiscordCode(ctx context.Context, code string) (discordOAuthUser, error) {
	form := url.Values{}
	form.Set("client_id", s.discordOAuth.ClientID)
	form.Set("client_secret", s.discordOAuth.ClientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.discordOAuth.RedirectURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, discordTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return discordOAuthUser{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := discordHTTPClient.Do(req)
	if err != nil {
		return discordOAuthUser{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return discordOAuthUser{}, fmt.Errorf("token endpoint returned %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<16)).Decode(&tok); err != nil {
		return discordOAuthUser{}, fmt.Errorf("decode token response: %w", err)
	}
	if tok.AccessToken == "" {
		return discordOAuthUser{}, errors.New("token response had no access_token")
	}

	userReq, err := http.NewRequestWithContext(ctx, http.MethodGet, discordUserURL, nil)
	if err != nil {
		return discordOAuthUser{}, err
	}
	tokenType := tok.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	userReq.Header.Set("Authorization", tokenType+" "+tok.AccessToken)

	userRes, err := discordHTTPClient.Do(userReq)
	if err != nil {
		return discordOAuthUser{}, err
	}
	defer userRes.Body.Close()
	if userRes.StatusCode != http.StatusOK {
		return discordOAuthUser{}, fmt.Errorf("user endpoint returned %d", userRes.StatusCode)
	}
	var du discordOAuthUser
	if err := json.NewDecoder(io.LimitReader(userRes.Body, 1<<16)).Decode(&du); err != nil {
		return discordOAuthUser{}, fmt.Errorf("decode user response: %w", err)
	}
	if du.ID == "" {
		return discordOAuthUser{}, errors.New("user response had no id")
	}
	return du, nil
}

// usernameSuffixAttempts bounds how many "<base>-N" variants we try before
// falling back to a random suffix, keeping account creation snappy.
const usernameSuffixAttempts = 12

// uniqueUsername derives an available username from a Discord display name. It
// sanitizes the source, then resolves collisions with a numeric suffix and,
// failing that, a short random suffix.
func (s *Server) uniqueUsername(ctx context.Context, source string) (string, error) {
	base := sanitizeUsername(source)
	if base == "" {
		base = "discord_user"
	}

	candidate := base
	for attempt := 1; attempt <= usernameSuffixAttempts; attempt++ {
		_, err := s.users.GetByUsername(ctx, candidate)
		if errors.Is(err, models.ErrUserNotFound) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
		suffix := "-" + strconv.Itoa(attempt+1)
		candidate = truncateUsername(base, len(suffix)) + suffix
	}

	// All simple variants are taken; append random entropy. base32 of 4 bytes is
	// 7 chars; trim base to keep within the column limit.
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	suffix := "-" + strings.ToLower(base64.RawURLEncoding.EncodeToString(raw))
	return truncateUsername(base, len(suffix)) + suffix, nil
}

// maxUsernameLen mirrors the users.username VARCHAR(50) column width.
const maxUsernameLen = 50

// sanitizeUsername reduces an arbitrary display name to a tidy username: it keeps
// alphanumerics, dot, dash, and underscore; collapses spaces to underscores; and
// caps the length to the column width.
func sanitizeUsername(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	return truncateUsername(strings.Trim(b.String(), "._-"), 0)
}

// truncateUsername caps a username base to fit the column, optionally reserving
// `reserve` characters for a suffix that will be appended by the caller.
func truncateUsername(s string, reserve int) string {
	limit := maxUsernameLen - reserve
	if limit < 1 {
		limit = 1
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

// redirectDiscordError sends the browser back to the login page with a short
// machine-readable error code the page turns into a friendly message.
func (s *Server) redirectDiscordError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, s.appBaseURL+"/login.html?discord_error="+url.QueryEscape(code), http.StatusFound)
}

// randomState returns a high-entropy, URL-safe CSRF state value.
func randomState() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// isHTTPS reports whether the original client request used HTTPS, honoring the
// upstream TLS proxy's X-Forwarded-Proto header (the backend itself speaks HTTP
// on the internal network).
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
