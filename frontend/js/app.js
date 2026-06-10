/*
 * app.js — drives the authenticated dashboard (index.html).
 *
 * Responsibilities:
 *   - Route guard (redirect unauthenticated visitors to login).
 *   - Teams list: create and open teams; each card has a Share button.
 *   - Team detail: rename, delete (owner), schedule, encounters, and edit the
 *     12 player slots (name, discord handle, role, class, subclass build, and
 *     the gear/skills loadout for the currently selected encounter).
 *   - Share page: view members and (owner) share/unshare and change roles.
 *
 * Relies on `api` from api.js, the shared reference data + helpers from data.js
 * (`ROLES`, `CLASSES`, `labelFor`, schedule/skill/mastery helpers, etc.), and
 * `createSearchableSelect` from components.js.
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
  let currentEncounters = [];
  // The encounter currently selected on the team page; its per-player loadouts
  // are shown inline in the roster. Always set once a team is open.
  let currentEncounter = null;

  // --- Helpers ---
  // A fixed toast at the top of the screen (see #message in index.html). It is
  // position:fixed so it shows regardless of scroll position.
  let messageTimer = null;

  function showMessage(text, kind = "error") {
    if (messageTimer) clearTimeout(messageTimer);
    message.textContent = text;
    message.className = `toast toast--${kind} message--${kind}`;
    // Auto-dismiss; errors linger a bit longer than success confirmations.
    const ttl = kind === "success" ? 2500 : 5000;
    messageTimer = setTimeout(clearMessage, ttl);
  }

  function clearMessage() {
    if (messageTimer) {
      clearTimeout(messageTimer);
      messageTimer = null;
    }
    message.textContent = "";
    message.className = "toast is-hidden";
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
      const { encounters } = await api.listEncounters(id);
      currentEncounters = encounters || [];
      // Select the first encounter (e.g. Default) and load its loadouts so the
      // roster can show each player's gear/skills for it.
      currentEncounter = null;
      if (currentEncounters.length) {
        currentEncounter = await api.getEncounter(id, currentEncounters[0].id);
      }
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

    el("team-save-status").classList.toggle("is-hidden", !editable);
    setSaveStatus("team", "");
    el("delete-team-btn").classList.toggle("is-hidden", !isOwner());

    renderSchedule(editable);
    renderEncountersBar();
    renderEncounterControls();
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
    return Array.from(el("roster").querySelectorAll(".player-slot")).map((slot) => {
      const val = (f) => {
        const e = slot.querySelector(`[data-field="${f}"]`);
        return e ? e.value : "";
      };
      const subEl = slot.querySelector('[data-field="subclassed"]');
      const subclassed = subEl ? subEl.checked : false;
      return {
        slot: Number(slot.dataset.slot),
        name: val("name").trim(),
        discord_handle: val("discord_handle").trim(),
        role: val("role"),
        class: val("class"),
        subclassed,
        // Only the active build set is sent; the backend clears the rest too.
        skill_line_1: subclassed ? val("skill_line_1") : "",
        skill_line_2: subclassed ? val("skill_line_2") : "",
        skill_line_3: subclassed ? val("skill_line_3") : "",
        mastery_1: subclassed ? "" : val("mastery_1"),
        mastery_2: subclassed ? "" : val("mastery_2"),
      };
    });
  }

  // Validate subclass skill-line rules before saving. Returns an error message
  // (naming the slot) or null when all builds are valid. Mirrors the backend
  // rules in models.ValidateSkillLines.
  function validateBuilds(players) {
    for (const p of players) {
      if (!p.subclassed) continue;
      const lines = [p.skill_line_1, p.skill_line_2, p.skill_line_3].filter(Boolean);

      if (new Set(lines).size !== lines.length) {
        return `Slot ${p.slot}: skill lines must be unique.`;
      }
      if (!p.class) continue;

      const counts = {};
      for (const l of lines) {
        const c = skillLineClass(l);
        counts[c] = (counts[c] || 0) + 1;
      }
      // Only require a class skill line once at least one line is chosen, so a
      // fully-empty subclass build is allowed.
      if (lines.length > 0 && (counts[p.class] || 0) < 1) {
        return `Slot ${p.slot}: at least one skill line must be from the player's class.`;
      }
      for (const c of Object.keys(counts)) {
        if (c !== p.class && counts[c] > 1) {
          return `Slot ${p.slot}: only one skill line allowed from a class other than the player's class.`;
        }
      }
    }
    return null;
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

  // --- Autosave ---
  // Changes are persisted automatically: text inputs fire on `change` (i.e. when
  // the field loses focus after editing — "input finished"); selects, checkboxes,
  // toggles, and loadout chips fire immediately. Saves are debounced and
  // coalesced, and we intentionally do NOT re-render after an autosave so focus
  // and in-progress edits (e.g. adding multiple chips) are never interrupted.
  const AUTOSAVE_DELAY = 700;
  let autosaveTimer = null;
  let autosavePending = null; // "team" | "encounter"

  function setSaveStatus(scope, state) {
    const node = el(scope === "encounter" ? "encounter-save-status" : "team-save-status");
    if (!node) return;
    node.classList.remove("is-saving", "is-saved", "is-error");
    if (state === "saving") {
      node.textContent = "Saving…";
      node.classList.add("is-saving");
    } else if (state === "saved") {
      node.textContent = "Saved ✓";
      node.classList.add("is-saved");
    } else if (state === "error") {
      node.textContent = "Not saved";
      node.classList.add("is-error");
    } else {
      node.textContent = "";
    }
  }

  function scheduleAutosave(scope) {
    if (!canEdit()) return;
    autosavePending = scope;
    setSaveStatus(scope, "saving");
    clearTimeout(autosaveTimer);
    autosaveTimer = setTimeout(flushAutosave, AUTOSAVE_DELAY);
  }

  // Flush any pending debounced autosave immediately. Returns a promise that
  // resolves once the in-flight save (if any) completes, so callers can await it
  // before doing something that would otherwise clobber the unsaved changes.
  function flushAutosave() {
    clearTimeout(autosaveTimer);
    autosaveTimer = null;
    const scope = autosavePending;
    autosavePending = null;
    if (scope === "encounter") return saveLoadouts();
    if (scope === "team") return saveAll();
    return Promise.resolve();
  }

  async function saveAll() {
    // Only saveable from the team detail view as an editor/owner.
    if (!currentTeam || detailView.classList.contains("is-hidden") || !canEdit()) {
      return;
    }
    const name = el("team-name-input").value.trim();
    if (!name) {
      setSaveStatus("team", "error");
      showMessage("Team name cannot be empty");
      return;
    }
    const players = collectPlayers();
    const buildError = validateBuilds(players);
    if (buildError) {
      setSaveStatus("team", "error");
      showMessage(buildError);
      return;
    }
    const payload = {
      name,
      schedule_days: collectScheduleDays(),
      schedule_time: el("schedule-time").value,
      schedule_timezone: el("schedule-timezone").value,
      players,
    };
    setSaveStatus("team", "saving");
    try {
      currentTeam = await api.saveTeam(currentTeam.id, payload);
      setSaveStatus("team", "saved");
    } catch (err) {
      setSaveStatus("team", "error");
      handleError(err);
    }
  }

  // Autosave on any field change within the team detail view. Native `change`
  // covers both cases we want: text inputs fire on blur (finished), while
  // selects/checkboxes fire immediately. The add-encounter form, the encounter
  // controls (rename), and the per-player loadouts handle their own saves and
  // are excluded so they don't trigger a redundant team save.
  detailView.addEventListener("change", (e) => {
    if (!currentTeam || !canEdit()) return;
    if (e.target.closest("#add-encounter-form")) return;
    if (e.target.closest("#encounter-controls")) return;
    if (e.target.closest("[data-loadout]")) return;
    if (e.target.matches("input, select, textarea")) scheduleAutosave("team");
  });

  // Ctrl+S / Cmd+S forces an immediate save of the team page (roster + the
  // selected encounter's loadouts, which are both on this page now).
  document.addEventListener("keydown", (e) => {
    if ((e.ctrlKey || e.metaKey) && (e.key === "s" || e.key === "S")) {
      e.preventDefault();
      clearTimeout(autosaveTimer);
      autosaveTimer = null;
      autosavePending = null;
      if (!detailView.classList.contains("is-hidden")) {
        saveAll();
        if (currentEncounter) saveLoadouts();
      }
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
        <div class="player-body">
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
          </div>
          <div class="player-build">
            <label class="subclass-toggle">
              <input type="checkbox" data-field="subclassed" />
              <span>Subclassed</span>
            </label>
            <div class="build-selects" data-build></div>
          </div>
          <div class="player-loadout" data-loadout>
            <div class="loadout-lists">
              <div class="loadout-col" data-type="gear">
                <label>Gear</label>
                <div class="chip-list" data-list></div>
              </div>
              <div class="loadout-col" data-type="skills">
                <label>Skills</label>
                <div class="chip-list" data-list></div>
              </div>
            </div>
          </div>
        </div>`;

      slot.querySelector('[data-field="name"]').value = player.name;
      slot.querySelector('[data-field="discord_handle"]').value = player.discord_handle;
      slot.querySelector('[data-field="subclassed"]').checked = player.subclassed;

      // Re-render the conditional build area when subclass or class changes.
      const subCb = slot.querySelector('[data-field="subclassed"]');
      const classSel = slot.querySelector('[data-field="class"]');
      subCb.addEventListener("change", () => renderBuild(slot, player));
      classSel.addEventListener("change", () => {
        if (!subCb.checked) renderBuild(slot, player);
      });
      renderBuild(slot, player);

      // Build the loadout add-controls (one per gear/skills column) for the
      // currently selected encounter. The chips themselves are filled by
      // renderRosterLoadouts so they can be refreshed when the encounter changes.
      slot.querySelectorAll("[data-loadout] .loadout-col").forEach((col) => {
        const type = col.dataset.type;
        const listEl = col.querySelector("[data-list]");
        if (editable) col.appendChild(buildAddControl(listEl, type));
      });

      // Viewers get a read-only roster; editors/owner autosave on change.
      if (!editable) {
        slot.querySelectorAll("input, select").forEach((field) => {
          field.disabled = true;
        });
      }

      roster.appendChild(slot);
    });

    renderRosterLoadouts(editable);
  }

  // Fill each roster slot's gear/skills chip lists from the currently selected
  // encounter's loadouts. Re-run when the selected encounter changes; it only
  // touches the chip lists so in-progress player-field edits are preserved.
  function renderRosterLoadouts(editable) {
    if (editable === undefined) editable = canEdit();
    const loadoutBySlot = {};
    if (currentEncounter) {
      (currentEncounter.loadouts || []).forEach((l) => {
        loadoutBySlot[l.slot] = l;
      });
    }

    el("roster").querySelectorAll(".player-slot").forEach((slot) => {
      const slotNum = Number(slot.dataset.slot);
      const lo = loadoutBySlot[slotNum] || { gear: [], skills: [] };
      slot.querySelectorAll("[data-loadout] .loadout-col").forEach((col) => {
        const type = col.dataset.type;
        const listEl = col.querySelector("[data-list]");
        listEl.innerHTML = "";
        (lo[type] || []).forEach((key) => addChip(listEl, type, key, editable));
      });
    });
  }

  // Render the conditional build controls for one slot based on its current
  // subclass checkbox and selected class. `player` supplies the saved values to
  // pre-select on (re)render.
  function renderBuild(slot, player) {
    const subclassed = slot.querySelector('[data-field="subclassed"]').checked;
    const cls = slot.querySelector('[data-field="class"]').value;
    const buildEl = slot.querySelector("[data-build]");

    if (subclassed) {
      buildEl.innerHTML = [1, 2, 3]
        .map(
          (n) => `
        <div class="form-group">
          <label>Skill line ${n}</label>
          <select class="input" data-field="skill_line_${n}">${skillLineOptionsHtml(
            player[`skill_line_${n}`]
          )}</select>
        </div>`
        )
        .join("");
    } else if (!cls) {
      buildEl.innerHTML = `<p class="text-muted build-hint">Select a class to choose class masteries.</p>`;
    } else {
      const masteries = MASTERIES_BY_CLASS[cls] || [];
      buildEl.innerHTML = [1, 2]
        .map(
          (n) => `
        <div class="form-group">
          <label>Class mastery ${n}</label>
          <select class="input" data-field="mastery_${n}" title="${escapeAttr(
            masteryDesc(cls, player[`mastery_${n}`])
          )}">${masteryOptionsHtml(masteries, player[`mastery_${n}`])}</select>
        </div>`
        )
        .join("");

      // Hovering a mastery dropdown shows the selected mastery's description;
      // keep the field's tooltip in sync as the selection changes.
      buildEl.querySelectorAll('select[data-field^="mastery_"]').forEach((sel) => {
        sel.addEventListener("change", () => {
          sel.title = masteryDesc(cls, sel.value);
        });
      });
    }

    // Keep newly created controls read-only for viewers.
    if (!canEdit()) {
      buildEl.querySelectorAll("input, select").forEach((f) => (f.disabled = true));
    }
  }

  // --- Encounters bar (team detail) ---
  // The encounters bar lets you pick the *current* encounter (whose per-player
  // loadouts are shown inline in the roster) and add new ones. There is no
  // separate encounter page anymore.
  function renderEncountersBar() {
    const bar = el("encounters-bar");
    bar.innerHTML = "";
    currentEncounters.forEach((enc) => {
      const chip = document.createElement("button");
      chip.type = "button";
      chip.className = "encounter-chip";
      if (currentEncounter && enc.id === currentEncounter.id) {
        chip.classList.add("is-active");
      }
      chip.textContent = enc.name;
      chip.addEventListener("click", () => selectEncounter(enc.id));
      bar.appendChild(chip);
    });
    el("add-encounter-btn").classList.toggle("is-hidden", !canEdit());
  }

  // Show the controls for the currently selected encounter (name, rename,
  // delete, save status). The loadouts themselves render inline in the roster.
  function renderEncounterControls() {
    const controls = el("encounter-controls");
    if (!currentEncounter) {
      controls.classList.add("is-hidden");
      return;
    }
    controls.classList.remove("is-hidden");

    const editable = canEdit();
    el("current-encounter-name").textContent = currentEncounter.name;

    // Editors get a rename dropdown; viewers just see the name.
    const rename = el("encounter-rename");
    rename.classList.toggle("is-hidden", !editable);
    if (editable) {
      populateEncounterNameSelect(rename);
      rename.value = currentEncounter.name;
    }

    // Delete only when editable and more than one encounter exists.
    el("encounter-delete-btn").classList.toggle(
      "is-hidden",
      !editable || currentEncounters.length <= 1
    );
    el("encounter-save-status").classList.toggle("is-hidden", !editable);
    setSaveStatus("encounter", "");
  }

  // Switch the selected encounter: load its loadouts, refresh the bar + controls,
  // and re-fill the roster's per-player gear/skill chips (only — player fields
  // and any in-progress edits are left untouched).
  async function selectEncounter(encounterId) {
    if (currentEncounter && currentEncounter.id === encounterId) return;
    clearMessage();
    try {
      // Flush any pending loadout autosave for the current encounter first, so
      // switching never drops unsaved gear/skill edits.
      if (autosavePending === "encounter") {
        await flushAutosave();
      }
      currentEncounter = await api.getEncounter(currentTeam.id, encounterId);
      renderEncountersBar();
      renderEncounterControls();
      renderRosterLoadouts();
    } catch (err) {
      handleError(err);
    }
  }

  // Populate the "add encounter" picker once with grouped boss names.
  function populateEncounterNameSelect(select) {
    if (select.options.length > 0) return;
    select.innerHTML = ENCOUNTER_NAME_GROUPS.map(
      (g) =>
        `<optgroup label="${g.group}">` +
        g.names.map((n) => `<option value="${escapeAttr(n)}">${n}</option>`).join("") +
        `</optgroup>`
    ).join("");
  }

  const addEncounterForm = el("add-encounter-form");
  el("add-encounter-btn").addEventListener("click", () => {
    populateEncounterNameSelect(el("add-encounter-name"));
    addEncounterForm.classList.remove("is-hidden");
  });
  el("add-encounter-cancel").addEventListener("click", () => {
    addEncounterForm.classList.add("is-hidden");
  });
  addEncounterForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = el("add-encounter-name").value;
    try {
      const enc = await api.createEncounter(currentTeam.id, name);
      addEncounterForm.classList.add("is-hidden");
      const { encounters } = await api.listEncounters(currentTeam.id);
      currentEncounters = encounters || [];
      // Select the newly added encounter so its loadouts show in the roster.
      currentEncounter = await api.getEncounter(currentTeam.id, enc.id);
      renderEncountersBar();
      renderEncounterControls();
      renderRosterLoadouts();
      showMessage(`Added encounter “${enc.name}”`, "success");
    } catch (err) {
      handleError(err);
    }
  });

  // Create a removable chip for one loadout item.
  function addChip(listEl, type, key, editable) {
    if (!key) return;
    // Avoid duplicates within the same list.
    if (listEl.querySelector(`.chip[data-value="${escapeAttr(key)}"]`)) return;

    const cfg = LOADOUT_TYPES[type];
    const chip = document.createElement("span");
    chip.className = "chip";
    chip.dataset.value = key;
    const desc = cfg.desc(key);
    if (desc) chip.title = desc;
    chip.innerHTML = `<span class="chip-label">${escapeAttr(cfg.label(key))}</span>`;
    if (editable) {
      const remove = document.createElement("button");
      remove.type = "button";
      remove.className = "chip-remove";
      remove.setAttribute("aria-label", "Remove");
      remove.textContent = "×";
      remove.addEventListener("click", () => {
        chip.remove();
        scheduleAutosave("encounter");
      });
      chip.appendChild(remove);
    }
    listEl.appendChild(chip);
  }

  // Build the add-control for a loadout list. Both gear and skills use the same
  // searchable-select component (createSearchableSelect); skills supply skill-
  // line group headers, gear is a single headerless group.
  function buildAddControl(listEl, type) {
    const cfg = LOADOUT_TYPES[type];
    return createSearchableSelect({
      groups: cfg.groups,
      placeholder: cfg.addPlaceholder,
      onSelect: (value) => {
        addChip(listEl, type, value, true);
        scheduleAutosave("encounter");
      },
    });
  }

  // Collect each player's loadout (gear/skills chips) from the roster slots.
  function collectLoadouts() {
    return Array.from(el("roster").querySelectorAll(".player-slot")).map((slot) => {
      const read = (type) =>
        Array.from(
          slot
            .querySelector(`[data-loadout] .loadout-col[data-type="${type}"] .chip-list`)
            .querySelectorAll(".chip")
        ).map((c) => c.dataset.value);
      return {
        slot: Number(slot.dataset.slot),
        gear: read("gear"),
        skills: read("skills"),
      };
    });
  }

  async function saveLoadouts() {
    if (!currentEncounter || detailView.classList.contains("is-hidden") || !canEdit()) {
      return;
    }
    setSaveStatus("encounter", "saving");
    try {
      currentEncounter = await api.saveLoadouts(
        currentTeam.id,
        currentEncounter.id,
        collectLoadouts()
      );
      setSaveStatus("encounter", "saved");
    } catch (err) {
      setSaveStatus("encounter", "error");
      handleError(err);
    }
  }

  el("encounter-rename").addEventListener("change", async (e) => {
    const name = e.target.value;
    try {
      currentEncounter = await api.renameEncounter(currentTeam.id, currentEncounter.id, name);
      const { encounters } = await api.listEncounters(currentTeam.id);
      currentEncounters = encounters || [];
      renderEncountersBar();
      renderEncounterControls();
      showMessage("Encounter renamed", "success");
    } catch (err) {
      handleError(err);
    }
  });

  el("encounter-delete-btn").addEventListener("click", async () => {
    if (!confirm(`Delete encounter “${currentEncounter.name}”? This cannot be undone.`)) {
      return;
    }
    try {
      await api.deleteEncounter(currentTeam.id, currentEncounter.id);
      const { encounters } = await api.listEncounters(currentTeam.id);
      currentEncounters = encounters || [];
      // Fall back to the first remaining encounter.
      currentEncounter = currentEncounters.length
        ? await api.getEncounter(currentTeam.id, currentEncounters[0].id)
        : null;
      renderEncountersBar();
      renderEncounterControls();
      renderRosterLoadouts();
      showMessage("Encounter deleted", "success");
    } catch (err) {
      handleError(err);
    }
  });

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
