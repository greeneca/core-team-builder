/*
 * app.js — drives the authenticated dashboard (index.html).
 *
 * Responsibilities:
 *   - Route guard (redirect unauthenticated visitors to login).
 *   - Teams list: create and open teams; each card has a Share button.
 *   - Team detail: rename, delete (owner), schedule, and edit the 12 player
 *     slots (name, discord handle, role, class).
 *   - Share page: view members and (owner) share/unshare and change roles.
 *
 * Relies on `api`, `ROLES`, `CLASSES`, and `labelFor` from api.js.
 */

(function () {
  // Route guard: bounce unauthenticated visitors back to login.
  if (!api.isAuthenticated()) {
    window.location.replace("login.html");
    return;
  }

  // --- Element references ---
  const el = (id) => document.getElementById(id);
  const message = el("message");
  const teamsView = el("teams-view");
  const detailView = el("team-detail-view");
  const shareView = el("share-view");

  let currentUser = null;
  let currentTeam = null;

  // --- Helpers ---
  function showMessage(text, kind = "error") {
    message.textContent = text;
    message.className = `message message--${kind}`;
    if (kind === "success") {
      setTimeout(clearMessage, 2500);
    }
  }

  function clearMessage() {
    message.textContent = "";
    message.className = "message is-hidden";
  }

  function handleError(err) {
    if (err.status === 401) {
      api.clearToken();
      window.location.replace("login.html");
      return;
    }
    showMessage(err.message || "Something went wrong");
  }

  function showView(view) {
    teamsView.classList.toggle("is-hidden", view !== "teams");
    detailView.classList.toggle("is-hidden", view !== "detail");
    shareView.classList.toggle("is-hidden", view !== "share");
    window.scrollTo(0, 0);
  }

  // --- Sign out ---
  el("logout-btn").addEventListener("click", () => {
    api.clearToken();
    window.location.replace("login.html");
  });

  // --- Teams list ---
  async function loadTeams() {
    clearMessage();
    try {
      const { teams } = await api.listTeams();
      renderTeamsList(teams);
    } catch (err) {
      handleError(err);
    }
  }

  function renderTeamsList(teams) {
    const list = el("teams-list");
    list.innerHTML = "";
    el("teams-empty").classList.toggle("is-hidden", teams.length > 0);

    teams.forEach((team) => {
      const card = document.createElement("div");
      card.className = "team-card";
      const owned = team.owner_id === currentUser.id;
      card.innerHTML = `
        <button class="team-card-open" type="button">
          <span class="team-card-name"></span>
          <span class="team-card-schedule text-muted"></span>
        </button>
        <div class="team-card-side">
          <span class="badge ${owned ? "badge--owner" : "badge--shared"}">
            ${owned ? "Owner" : "Shared"}
          </span>
          <button class="btn btn--ghost btn--sm team-card-share" type="button">Share</button>
        </div>`;
      card.querySelector(".team-card-name").textContent = team.name;
      card.querySelector(".team-card-schedule").textContent = formatSchedule(
        team.schedule_days,
        team.schedule_time,
        team.schedule_timezone
      );
      card.querySelector(".team-card-open").addEventListener("click", () => openTeam(team.id));
      card.querySelector(".team-card-share").addEventListener("click", () => openShare(team.id));
      list.appendChild(card);
    });
  }

  // New team form toggling.
  const newTeamForm = el("new-team-form");
  el("new-team-btn").addEventListener("click", () => {
    newTeamForm.classList.remove("is-hidden");
    el("new-team-name").focus();
  });
  el("new-team-cancel").addEventListener("click", () => {
    newTeamForm.classList.add("is-hidden");
    newTeamForm.reset();
  });
  newTeamForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = el("new-team-name").value.trim();
    if (!name) return;
    try {
      const team = await api.createTeam(name);
      newTeamForm.classList.add("is-hidden");
      newTeamForm.reset();
      showMessage(`Created team “${team.name}”`, "success");
      openTeam(team.id);
    } catch (err) {
      handleError(err);
    }
  });

  // --- Team detail ---
  async function openTeam(id) {
    clearMessage();
    try {
      currentTeam = await api.getTeam(id);
      renderTeamDetail();
      showView("detail");
    } catch (err) {
      handleError(err);
    }
  }

  // --- Share page ---
  async function openShare(id) {
    clearMessage();
    try {
      currentTeam = await api.getTeam(id);
      renderShare();
      showView("share");
    } catch (err) {
      handleError(err);
    }
  }

  function renderShare() {
    el("share-team-name").textContent = currentTeam.name;
    renderMembers();
  }

  function isOwner() {
    return currentTeam && currentTeam.owner_id === currentUser.id;
  }

  // The current user's role on the open team: "owner" | "editor" | "viewer".
  function myRole() {
    if (isOwner()) return "owner";
    const m = (currentTeam.members || []).find((x) => x.user_id === currentUser.id);
    return m ? m.role : "viewer";
  }

  function canEdit() {
    const r = myRole();
    return r === "owner" || r === "editor";
  }

  function renderTeamDetail() {
    const editable = canEdit();

    const nameInput = el("team-name-input");
    nameInput.value = currentTeam.name;
    nameInput.readOnly = !editable;

    const created = new Date(currentTeam.created_at);
    const roleLabel = isOwner()
      ? "Owned by you"
      : `Shared with you (${memberRoleLabel(myRole())})`;
    el("team-meta").textContent = `${roleLabel} · created ${created.toLocaleDateString()}`;

    el("save-all-btn").classList.toggle("is-hidden", !editable);
    el("delete-team-btn").classList.toggle("is-hidden", !isOwner());

    renderSchedule(editable);
    renderRoster();
  }

  // --- Schedule ---
  function renderSchedule(editable) {
    const container = el("schedule-days");
    container.innerHTML = "";
    const selected = new Set(currentTeam.schedule_days || []);

    DAYS.forEach((d) => {
      const label = document.createElement("label");
      label.className = "day-toggle";
      const cb = document.createElement("input");
      cb.type = "checkbox";
      cb.value = d.value;
      cb.checked = selected.has(d.value);
      cb.disabled = !editable;
      const span = document.createElement("span");
      span.textContent = d.label;
      label.appendChild(cb);
      label.appendChild(span);
      container.appendChild(label);
    });

    const timeInput = el("schedule-time");
    timeInput.value = currentTeam.schedule_time || "";
    timeInput.disabled = !editable;

    // Default an unset timezone to the viewer's current zone.
    const tz = currentTeam.schedule_timezone || localTimezone();
    populateTimezones(tz);
    const tzSelect = el("schedule-timezone");
    tzSelect.value = tz;
    tzSelect.disabled = !editable;
  }

  // Fill the timezone <select> once, ensuring `desired` is present even if the
  // browser's list omits it.
  function populateTimezones(desired) {
    const select = el("schedule-timezone");
    const zones = timezoneList();
    if (desired && !zones.includes(desired)) {
      zones.unshift(desired);
    }
    if (select.options.length === zones.length) {
      return; // already populated
    }
    select.innerHTML = zones
      .map((z) => `<option value="${z}">${z}</option>`)
      .join("");
  }

  function collectScheduleDays() {
    return Array.from(
      el("schedule-days").querySelectorAll("input:checked")
    ).map((cb) => cb.value);
  }

  function collectPlayers() {
    return Array.from(el("roster").querySelectorAll(".player-slot")).map((slot) => ({
      slot: Number(slot.dataset.slot),
      name: slot.querySelector('[data-field="name"]').value.trim(),
      discord_handle: slot.querySelector('[data-field="discord_handle"]').value.trim(),
      role: slot.querySelector('[data-field="role"]').value,
      class: slot.querySelector('[data-field="class"]').value,
    }));
  }

  el("back-btn").addEventListener("click", () => {
    currentTeam = null;
    showView("teams");
    loadTeams();
  });

  el("share-back-btn").addEventListener("click", () => {
    currentTeam = null;
    showView("teams");
    loadTeams();
  });

  el("save-all-btn").addEventListener("click", async () => {
    const name = el("team-name-input").value.trim();
    if (!name) {
      showMessage("Team name cannot be empty");
      return;
    }
    const payload = {
      name,
      schedule_days: collectScheduleDays(),
      schedule_time: el("schedule-time").value,
      schedule_timezone: el("schedule-timezone").value,
      players: collectPlayers(),
    };
    try {
      currentTeam = await api.saveTeam(currentTeam.id, payload);
      renderTeamDetail();
      showMessage("All changes saved", "success");
    } catch (err) {
      handleError(err);
    }
  });

  el("delete-team-btn").addEventListener("click", async () => {
    if (!confirm(`Delete team “${currentTeam.name}”? This cannot be undone.`)) {
      return;
    }
    try {
      await api.deleteTeam(currentTeam.id);
      currentTeam = null;
      showView("teams");
      showMessage("Team deleted", "success");
      loadTeams();
    } catch (err) {
      handleError(err);
    }
  });

  // --- Sharing ---
  function renderMembers() {
    const container = el("members-list");
    container.innerHTML = "";

    (currentTeam.members || []).forEach((m) => {
      const row = document.createElement("div");
      row.className = "member-row";

      const label = document.createElement("span");
      label.innerHTML = `<strong></strong> <span class="badge ${m.role === "owner" ? "badge--owner" : "badge--shared"}">${memberRoleLabel(m.role)}</span>`;
      label.querySelector("strong").textContent = m.username;
      row.appendChild(label);

      // Owners can change a non-owner member's role and revoke access.
      if (isOwner() && m.role !== "owner") {
        const controls = document.createElement("span");
        controls.className = "member-controls";

        const roleSelect = document.createElement("select");
        roleSelect.className = "input input--sm";
        roleSelect.innerHTML = SHARE_ROLES.map(
          (sr) => `<option value="${sr.value}" ${sr.value === m.role ? "selected" : ""}>${sr.label}</option>`
        ).join("");
        roleSelect.addEventListener("change", async () => {
          try {
            currentTeam = await api.shareTeam(currentTeam.id, m.username, roleSelect.value);
            renderShare();
            showMessage(`${m.username} is now ${memberRoleLabel(roleSelect.value)}`, "success");
          } catch (err) {
            handleError(err);
          }
        });
        controls.appendChild(roleSelect);

        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "btn btn--ghost btn--sm";
        remove.textContent = "Remove";
        remove.addEventListener("click", async () => {
          try {
            currentTeam = await api.unshareTeam(currentTeam.id, m.user_id);
            renderShare();
            showMessage(`Removed ${m.username}`, "success");
          } catch (err) {
            handleError(err);
          }
        });
        controls.appendChild(remove);
        row.appendChild(controls);
      }
      container.appendChild(row);
    });

    el("share-form").classList.toggle("is-hidden", !isOwner());
    el("share-note").classList.toggle("is-hidden", isOwner());
  }

  el("share-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const username = el("share-username").value.trim();
    const role = el("share-role").value;
    if (!username) return;
    try {
      currentTeam = await api.shareTeam(currentTeam.id, username, role);
      el("share-username").value = "";
      renderShare();
      showMessage(`Shared with ${username} as ${memberRoleLabel(role)}`, "success");
    } catch (err) {
      handleError(err);
    }
  });

  // --- Roster ---
  function optionsHtml(list, selected) {
    return list
      .map(
        (item) =>
          `<option value="${item.value}" ${item.value === selected ? "selected" : ""}>${item.label}</option>`
      )
      .join("");
  }

  function renderRoster() {
    const roster = el("roster");
    roster.innerHTML = "";
    const editable = canEdit();

    currentTeam.players.forEach((player) => {
      const slot = document.createElement("div");
      slot.className = "player-slot";
      slot.dataset.slot = player.slot;
      slot.innerHTML = `
        <span class="slot-number">${player.slot}</span>
        <div class="player-fields">
          <div class="form-group">
            <label>Name</label>
            <input class="input" data-field="name" maxlength="100" />
          </div>
          <div class="form-group">
            <label>Discord handle</label>
            <input class="input" data-field="discord_handle" maxlength="100" />
          </div>
          <div class="form-group">
            <label>Role</label>
            <select class="input" data-field="role">${optionsHtml(ROLES, player.role)}</select>
          </div>
          <div class="form-group">
            <label>Class</label>
            <select class="input" data-field="class">${optionsHtml(CLASSES, player.class)}</select>
          </div>
        </div>`;

      slot.querySelector('[data-field="name"]').value = player.name;
      slot.querySelector('[data-field="discord_handle"]').value = player.discord_handle;

      // Viewers get a read-only roster; editors/owner save via the top Save All.
      if (!editable) {
        slot.querySelectorAll("input, select").forEach((field) => {
          field.disabled = true;
        });
      }

      roster.appendChild(slot);
    });
  }

  // --- Bootstrap ---
  async function init() {
    try {
      currentUser = await api.me();
      el("username").textContent = currentUser.username;
      await loadTeams();
    } catch (err) {
      handleError(err);
    }
  }

  init();
})();
