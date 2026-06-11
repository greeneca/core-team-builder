/*
 * api.js — thin client for the Core Team Builder backend.
 *
 * Requests are made to same-origin "/api/*" paths; nginx proxies these to the
 * Go backend, so no cross-origin configuration is needed in the browser.
 *
 * The auth token is persisted in localStorage and attached automatically.
 */

const TOKEN_KEY = "ctb_token";

const api = {
  getToken() {
    return localStorage.getItem(TOKEN_KEY);
  },

  setToken(token) {
    localStorage.setItem(TOKEN_KEY, token);
  },

  clearToken() {
    localStorage.removeItem(TOKEN_KEY);
  },

  isAuthenticated() {
    return Boolean(this.getToken());
  },

  /**
   * Perform a JSON request against the API.
   * @param {string} path   API path beginning with "/api".
   * @param {object} [opts] fetch-style options: { method, body }.
   * @returns {Promise<object>} parsed JSON body.
   * @throws {Error} with `.status` and `.message` on non-2xx responses.
   */
  async request(path, opts = {}) {
    const headers = { "Content-Type": "application/json" };
    const token = this.getToken();
    if (token) {
      headers["Authorization"] = `Bearer ${token}`;
    }

    const res = await fetch(path, {
      method: opts.method || "GET",
      headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    });

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

  me() {
    return this.request("/api/me");
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
};
