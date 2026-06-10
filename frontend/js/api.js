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

  createTeam(name) {
    return this.request("/api/teams", { method: "POST", body: { name } });
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
};

// Canonical role/class values shared with the backend, plus display labels.
const ROLES = [
  { value: "", label: "—" },
  { value: "tank", label: "Tank" },
  { value: "healer", label: "Healer" },
  { value: "dps", label: "DPS" },
  { value: "support_dps", label: "Support DPS" },
];

const CLASSES = [
  { value: "", label: "—" },
  { value: "arcanist", label: "Arcanist" },
  { value: "dragonknight", label: "Dragonknight" },
  { value: "necromancer", label: "Necromancer" },
  { value: "nightblade", label: "Nightblade" },
  { value: "sorcerer", label: "Sorcerer" },
  { value: "templar", label: "Templar" },
  { value: "warden", label: "Warden" },
];

function labelFor(list, value) {
  const match = list.find((item) => item.value === value);
  return match ? match.label : "—";
}

// Roles a team can be shared at (excludes "owner").
const SHARE_ROLES = [
  { value: "editor", label: "Editor" },
  { value: "viewer", label: "Viewer" },
];

// Human label for any membership role, including owner.
function memberRoleLabel(role) {
  if (role === "owner") return "Owner";
  return labelFor(SHARE_ROLES, role);
}

// Days of the week, in canonical order. `value` matches the backend keys.
const DAYS = [
  { value: "mon", label: "Mon" },
  { value: "tue", label: "Tue" },
  { value: "wed", label: "Wed" },
  { value: "thu", label: "Thu" },
  { value: "fri", label: "Fri" },
  { value: "sat", label: "Sat" },
  { value: "sun", label: "Sun" },
];

// The viewer's current IANA timezone (e.g. "America/New_York"), best-effort.
function localTimezone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

// All IANA timezone names the browser knows, falling back to the local zone
// (and UTC) when Intl.supportedValuesOf is unavailable.
function timezoneList() {
  try {
    if (typeof Intl.supportedValuesOf === "function") {
      return Intl.supportedValuesOf("timeZone");
    }
  } catch {
    /* fall through */
  }
  const local = localTimezone();
  return local === "UTC" ? ["UTC"] : [local, "UTC"];
}

// Build a short, human-readable schedule string, e.g.
// "Mon, Wed · 20:00 (America/New_York)".
function formatSchedule(days, time, timezone) {
  const labels = (days || []).map((d) => labelFor(DAYS, d));
  const dayText = labels.length ? labels.join(", ") : "";
  let core = "";
  if (dayText && time) core = `${dayText} · ${time}`;
  else if (dayText) core = dayText;
  else if (time) core = time;
  if (!core) return "No schedule set";
  return timezone ? `${core} (${timezone})` : core;
}
