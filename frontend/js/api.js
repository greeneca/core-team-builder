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

  // Issue a single fetch with the current access token attached.
  _send(path, opts) {
    const headers = { "Content-Type": "application/json" };
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

  // --- Encounters ---

  listEncounters(teamId) {
    return this.request(`/api/teams/${teamId}/encounters`);
  },

  // copyFrom (optional) is an existing encounter id whose per-player gear/skills
  // are copied into the new encounter; pass a falsy value for an empty one.
  createEncounter(teamId, name, copyFrom) {
    return this.request(`/api/teams/${teamId}/encounters`, {
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

  // --- Groupings ---

  listGroupings(teamId) {
    return this.request(`/api/teams/${teamId}/groupings`);
  },

  createGrouping(teamId, name, groupCount) {
    return this.request(`/api/teams/${teamId}/groupings`, {
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
};
