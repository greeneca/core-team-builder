/*
 * api.js — thin client for the Core Team Builder backend.
 *
 * Requests are made to same-origin "/api/*" paths; nginx proxies these to the
 * Go backend, so no cross-origin configuration is needed in the browser.
 *
 * Auth uses a short-lived access token plus a long-lived refresh token, both
 * persisted in localStorage. The access token is attached automatically; when it
 * expires (401), the client transparently exchanges the refresh token for a new
 * pair and retries the request once.
 */

const TOKEN_KEY = "ctb_token";
const REFRESH_KEY = "ctb_refresh_token";

// A per-page-load random id sent on every request as X-Client-Id. It lets the
// client recognize (and ignore) the live-update echo of its own writes. Stable
// for the life of the tab; a new tab gets a new id so multi-tab editing still
// sees each other's changes.
const CLIENT_ID =
  (window.crypto && window.crypto.randomUUID && window.crypto.randomUUID()) ||
  `c${Date.now()}-${Math.random().toString(36).slice(2)}`;

// rosterQuery builds the optional "?roster_id=..." suffix for roster-scoped
// endpoints. An empty/falsy rosterId yields "" so the backend defaults to the
// team's active roster.
function rosterQuery(rosterId) {
  return rosterId ? `?roster_id=${encodeURIComponent(rosterId)}` : "";
}

const api = {
  // In-flight refresh shared across concurrent 401s so the single-use refresh
  // token is only rotated once.
  _refreshPromise: null,

  getToken() {
    return localStorage.getItem(TOKEN_KEY);
  },

  getRefreshToken() {
    return localStorage.getItem(REFRESH_KEY);
  },

  setToken(token) {
    localStorage.setItem(TOKEN_KEY, token);
  },

  // Persist a full auth response ({ token, refresh_token }).
  setSession(data) {
    if (data && data.token) {
      localStorage.setItem(TOKEN_KEY, data.token);
    }
    if (data && data.refresh_token) {
      localStorage.setItem(REFRESH_KEY, data.refresh_token);
    }
  },

  clearToken() {
    localStorage.removeItem(TOKEN_KEY);
    localStorage.removeItem(REFRESH_KEY);
  },

  isAuthenticated() {
    return Boolean(this.getToken());
  },

  // The stable per-tab client id (exposed for the live-update layer).
  clientId() {
    return CLIENT_ID;
  },

  // Issue a single fetch with the current access token attached.
  _send(path, opts) {
    const headers = { "Content-Type": "application/json", "X-Client-Id": CLIENT_ID };
    const token = this.getToken();
    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }
    return fetch(path, {
      method: opts.method || "GET",
      headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    });
  },

  // Parse a Response, throwing an Error (with .status) on non-2xx.
  async _parse(res) {
    let data = null;
    const text = await res.text();
    if (text) {
      try {
        data = JSON.parse(text);
      } catch {
        data = { error: text };
      }
    }
    if (!res.ok) {
      const err = new Error((data && data.error) || `Request failed (${res.status})`);
      err.status = res.status;
      throw err;
    }
    return data;
  },

  // Exchange the refresh token for a fresh token pair. Concurrent callers share
  // one in-flight request. Resolves true on success, false otherwise.
  async tryRefresh() {
    if (!this._refreshPromise) {
      const refreshToken = this.getRefreshToken();
      if (!refreshToken) {
        return false;
      }
      this._refreshPromise = (async () => {
        try {
          const res = await fetch("/api/refresh", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ refresh_token: refreshToken }),
          });
          if (!res.ok) {
            this.clearToken();
            return false;
          }
          this.setSession(await res.json());
          return true;
        } catch {
          return false;
        }
      })();
      this._refreshPromise.finally(() => {
        this._refreshPromise = null;
      });
    }
    return this._refreshPromise;
  },

  /**
   * Perform a JSON request against the API. On a 401 from an expired access
   * token, it refreshes once and retries transparently.
   * @param {string} path   API path beginning with "/api".
   * @param {object} [opts] fetch-style options: { method, body }.
   * @returns {Promise<object>} parsed JSON body.
   * @throws {Error} with `.status` and `.message` on non-2xx responses.
   */
  async request(path, opts = {}) {
    let res = await this._send(path, opts);

    // Don't try to refresh the auth endpoints themselves.
    const isAuthPath =
      path === "/api/refresh" || path === "/api/login" || path === "/api/register";
    if (res.status === 401 && !isAuthPath && this.getRefreshToken()) {
      if (await this.tryRefresh()) {
        res = await this._send(path, opts);
      }
    }

    return this._parse(res);
  },

  login(username, password) {
    return this.request("/api/login", {
      method: "POST",
      body: { username, password },
    });
  },

  register(username, email, password) {
    return this.request("/api/register", {
      method: "POST",
      body: { username, email, password },
    });
  },

  // Revoke the refresh token server-side, then clear local credentials.
  async logout() {
    const refreshToken = this.getRefreshToken();
    if (refreshToken) {
      try {
        await fetch("/api/logout", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ refresh_token: refreshToken }),
        });
      } catch {
        /* Best-effort: clear locally even if the network call fails. */
      }
    }
    this.clearToken();
  },

  me() {
    return this.request("/api/me");
  },

  // --- Discord account linking ---

  // Generate a one-time code to type into Discord via /coreteam link.
  // Returns { code, command, expires_at }.
  discordLinkCode() {
    return this.request("/api/discord/link-code", { method: "POST" });
  },

  // Current user's Discord link status: { linked, discord_username }.
  discordLink() {
    return this.request("/api/discord/link");
  },

  // Remove the current user's Discord link.
  discordUnlink() {
    return this.request("/api/discord/link", { method: "DELETE" });
  },

  // Public: whether self-registration is currently open (login page reads this).
  registrationStatus() {
    return this.request("/api/registration-status");
  },

  // Public: request a password-reset email. Always resolves with a generic
  // message (the backend never reveals whether the email is registered).
  forgotPassword(email) {
    return this.request("/api/forgot-password", {
      method: "POST",
      body: { email },
    });
  },

  // Public: complete a password reset with the emailed token and a new password.
  resetPassword(token, password) {
    return this.request("/api/reset-password", {
      method: "POST",
      body: { token, password },
    });
  },

  // --- Admin: user management ---

  listUsers() {
    return this.request("/api/admin/users");
  },

  createUser(username, email, password, isAdmin) {
    return this.request("/api/admin/users", {
      method: "POST",
      body: { username, email, password, is_admin: Boolean(isAdmin) },
    });
  },

  deleteUser(id) {
    return this.request(`/api/admin/users/${id}`, { method: "DELETE" });
  },

  setUserAdmin(id, isAdmin) {
    return this.request(`/api/admin/users/${id}/admin`, {
      method: "PUT",
      body: { is_admin: Boolean(isAdmin) },
    });
  },

  getAdminSettings() {
    return this.request("/api/admin/settings");
  },

  setRegistrationEnabled(enabled) {
    return this.request("/api/admin/settings", {
      method: "PUT",
      body: { registration_enabled: Boolean(enabled) },
    });
  },

  // --- Teams ---

  listTeams() {
    return this.request("/api/teams");
  },

  // copyFrom (optional) is an existing team id to seed the new team from
  // (schedule, roster, encounters/loadouts); pass a falsy value for an empty one.
  createTeam(name, copyFrom) {
    return this.request("/api/teams", {
      method: "POST",
      body: { name, copy_from: copyFrom || null },
    });
  },

  getTeam(id) {
    return this.request(`/api/teams/${id}`);
  },

  // Save team name, schedule, and the full roster in one request.
  saveTeam(id, payload) {
    return this.request(`/api/teams/${id}`, { method: "PUT", body: payload });
  },

  deleteTeam(id) {
    return this.request(`/api/teams/${id}`, { method: "DELETE" });
  },

  shareTeam(id, username, role) {
    return this.request(`/api/teams/${id}/share`, {
      method: "POST",
      body: { username, role },
    });
  },

  unshareTeam(id, userId) {
    return this.request(`/api/teams/${id}/members/${userId}`, {
      method: "DELETE",
    });
  },

  // Remove your own access to a team that was shared with you. The owner cannot
  // leave their own team. Returns no content.
  leaveTeam(id) {
    return this.request(`/api/teams/${id}/membership`, {
      method: "DELETE",
    });
  },

  // Save a single roster slot (finer-grained than saveTeam). payload is one
  // player object; include expected_updated_at for optimistic concurrency (a
  // stale save returns 409). rosterId targets a specific roster (default: the
  // team's active roster). Returns the refreshed player.
  savePlayer(id, slot, payload, rosterId) {
    return this.request(
      `/api/teams/${id}/players/${slot}${rosterQuery(rosterId)}`,
      { method: "PUT", body: payload }
    );
  },

  // --- Rosters ---

  // List a team's rosters: { rosters: [...], active_roster_id }.
  listRosters(teamId) {
    return this.request(`/api/teams/${teamId}/rosters`);
  },

  // Create a roster. copyFrom (optional) is an existing roster id on the same
  // team whose players/encounters/groupings are copied into the new one.
  createRoster(teamId, name, copyFrom) {
    return this.request(`/api/teams/${teamId}/rosters`, {
      method: "POST",
      body: { name, copy_from: copyFrom || null },
    });
  },

  // Fetch one roster with its 12-player lineup.
  getRoster(teamId, rosterId) {
    return this.request(`/api/teams/${teamId}/rosters/${rosterId}`);
  },

  renameRoster(teamId, rosterId, name) {
    return this.request(`/api/teams/${teamId}/rosters/${rosterId}`, {
      method: "PUT",
      body: { name },
    });
  },

  deleteRoster(teamId, rosterId) {
    return this.request(`/api/teams/${teamId}/rosters/${rosterId}`, {
      method: "DELETE",
    });
  },

  // Mark a roster as the team's active one (used by the Discord bot). Returns
  // the refreshed team.
  activateRoster(teamId, rosterId) {
    return this.request(`/api/teams/${teamId}/rosters/${rosterId}/activate`, {
      method: "POST",
    });
  },

  // Live-collaboration SSE stream URL for a team. The access token is passed as
  // a query param because EventSource can't set an Authorization header.
  teamEventsURL(id) {
    const token = this.getToken();
    return `/api/teams/${id}/events?access_token=${encodeURIComponent(token || "")}`;
  },

  // --- Roster members (the /coreteam recruit recruitment pool) ---

  // List a team's member pool: { members: [...] }.
  listRosterMembers(teamId) {
    return this.request(`/api/teams/${teamId}/roster-members`);
  },

  // Manually add a pool entry. body: { display_name, timezone, days,
  // availability, roles, classes_by_role }.
  createRosterMember(teamId, body) {
    return this.request(`/api/teams/${teamId}/roster-members`, {
      method: "POST",
      body,
    });
  },

  // Edit a pool entry (e.g. set/adjust availability time limits). Same body
  // shape as createRosterMember.
  updateRosterMember(teamId, memberId, body) {
    return this.request(`/api/teams/${teamId}/roster-members/${memberId}`, {
      method: "PUT",
      body,
    });
  },

  // Remove a pool entry by member id.
  deleteRosterMember(teamId, memberId) {
    return this.request(`/api/teams/${teamId}/roster-members/${memberId}`, {
      method: "DELETE",
    });
  },

  // --- Encounters ---

  // rosterId targets a specific roster (default: the team's active roster).
  listEncounters(teamId, rosterId) {
    return this.request(`/api/teams/${teamId}/encounters${rosterQuery(rosterId)}`);
  },

  // copyFrom (optional) is an existing encounter id whose per-player gear/skills
  // are copied into the new encounter; pass a falsy value for an empty one.
  // rosterId targets a specific roster (default: the team's active roster).
  createEncounter(teamId, name, copyFrom, rosterId) {
    return this.request(`/api/teams/${teamId}/encounters${rosterQuery(rosterId)}`, {
      method: "POST",
      body: { name, copy_from: copyFrom || null },
    });
  },

  getEncounter(teamId, encounterId) {
    return this.request(`/api/teams/${teamId}/encounters/${encounterId}`);
  },

  renameEncounter(teamId, encounterId, name) {
    return this.request(`/api/teams/${teamId}/encounters/${encounterId}`, {
      method: "PUT",
      body: { name },
    });
  },

  deleteEncounter(teamId, encounterId) {
    return this.request(`/api/teams/${teamId}/encounters/${encounterId}`, {
      method: "DELETE",
    });
  },

  saveLoadouts(teamId, encounterId, loadouts) {
    return this.request(`/api/teams/${teamId}/encounters/${encounterId}/loadouts`, {
      method: "PUT",
      body: { loadouts },
    });
  },

  // Save a single slot's loadout (finer-grained than saveLoadouts). payload is
  // one loadout object; include expected_updated_at for optimistic concurrency
  // (a stale save returns 409). Returns the refreshed loadout.
  saveLoadoutSlot(teamId, encounterId, slot, payload) {
    return this.request(
      `/api/teams/${teamId}/encounters/${encounterId}/loadouts/${slot}`,
      { method: "PUT", body: payload }
    );
  },

  // --- Groupings ---

  // rosterId targets a specific roster (default: the team's active roster).
  listGroupings(teamId, rosterId) {
    return this.request(`/api/teams/${teamId}/groupings${rosterQuery(rosterId)}`);
  },

  // rosterId targets a specific roster (default: the team's active roster).
  createGrouping(teamId, name, groupCount, rosterId) {
    return this.request(`/api/teams/${teamId}/groupings${rosterQuery(rosterId)}`, {
      method: "POST",
      body: { name, group_count: groupCount },
    });
  },

  getGrouping(teamId, groupingId) {
    return this.request(`/api/teams/${teamId}/groupings/${groupingId}`);
  },

  // payload: { name, group_count, groups: [{ group_number, name, slots: [] }] }
  saveGrouping(teamId, groupingId, payload) {
    return this.request(`/api/teams/${teamId}/groupings/${groupingId}`, {
      method: "PUT",
      body: payload,
    });
  },

  deleteGrouping(teamId, groupingId) {
    return this.request(`/api/teams/${teamId}/groupings/${groupingId}`, {
      method: "DELETE",
    });
  },

  // --- Roster positioning images ---

  // rosterId targets a specific roster (default: the team's active roster).
  listRosterImages(teamId, rosterId) {
    return this.request(`/api/teams/${teamId}/images${rosterQuery(rosterId)}`);
  },

  // Upload an image file (multipart) with an optional caption. rosterId targets a
  // specific roster (default: active).
  uploadRosterImage(teamId, rosterId, file, caption) {
    const form = new FormData();
    form.append("image", file);
    if (caption) form.append("caption", caption);
    return this._sendMultipart(
      `/api/teams/${teamId}/images${rosterQuery(rosterId)}`,
      form
    );
  },

  updateRosterImageCaption(teamId, imageId, caption) {
    return this.request(`/api/teams/${teamId}/images/${imageId}`, {
      method: "PUT",
      body: { caption },
    });
  },

  deleteRosterImage(teamId, imageId) {
    return this.request(`/api/teams/${teamId}/images/${imageId}`, {
      method: "DELETE",
    });
  },

  // Fetch an image's bytes (with auth) and return an object URL for use as an
  // <img src>. Callers should URL.revokeObjectURL() it when done.
  async rosterImageObjectURL(teamId, imageId) {
    const res = await this._sendRaw(`/api/teams/${teamId}/images/${imageId}/raw`);
    const blob = await res.blob();
    return URL.createObjectURL(blob);
  },

  // Like request(), but sends a multipart FormData body and parses the JSON
  // response. Content-Type is intentionally left unset so the browser adds the
  // multipart boundary. Refreshes once on a 401, mirroring request().
  async _sendMultipart(path, form) {
    const send = () => {
      const headers = { "X-Client-Id": CLIENT_ID };
      const token = this.getToken();
      if (token) headers["Authorization"] = `Bearer ${token}`;
      return fetch(path, { method: "POST", headers, body: form });
    };
    let res = await send();
    if (res.status === 401 && this.getRefreshToken()) {
      if (await this.tryRefresh()) res = await send();
    }
    return this._parse(res);
  },

  // Fetch a binary resource with auth, refreshing once on a 401. Returns the raw
  // Response (throws on other non-2xx).
  async _sendRaw(path) {
    const send = () => {
      const headers = { "X-Client-Id": CLIENT_ID };
      const token = this.getToken();
      if (token) headers["Authorization"] = `Bearer ${token}`;
      return fetch(path, { headers });
    };
    let res = await send();
    if (res.status === 401 && this.getRefreshToken()) {
      if (await this.tryRefresh()) res = await send();
    }
    if (!res.ok) {
      const err = new Error(`Request failed (${res.status})`);
      err.status = res.status;
      throw err;
    }
    return res;
  },
};
