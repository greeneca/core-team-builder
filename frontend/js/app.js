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
  // The teams shown in the list; cached so the "copy from team" picker can list
  // them when creating a new team.
  let allTeams = [];
  let currentEncounters = [];
  // The encounter currently selected on the team page; its per-player loadouts
  // are shown inline in the roster. Always set once a team is open.
  let currentEncounter = null;
  // The team's extra display timezones (chips); edited on the team page.
  let teamTimezones = [];

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

  // --- Tooltips toggle ---
  // Reflects the persisted preference (see setTooltipsEnabled in components.js)
  // and lets the user turn hover descriptions off entirely.
  const tooltipsToggle = el("tooltips-toggle");
  if (tooltipsToggle) {
    tooltipsToggle.checked = tooltipsEnabled();
    tooltipsToggle.addEventListener("change", () => {
      setTooltipsEnabled(tooltipsToggle.checked);
    });
  }

  // --- Brand "home" link ---
  // The title acts as a shortcut back to the teams list (SPA navigation; the
  // href is a no-JS fallback that reloads the dashboard).
  const brandHome = el("brand-home");
  if (brandHome) {
    brandHome.addEventListener("click", (e) => {
      e.preventDefault();
      currentTeam = null;
      showView("teams");
      loadTeams();
    });
  }

  // Expose the sticky topbar's height as a CSS var so the sticky encounters
  // panel can pin just beneath it (kept in sync on resize).
  function syncTopbarHeight() {
    const topbar = document.querySelector(".topbar");
    if (!topbar) return;
    document.documentElement.style.setProperty(
      "--topbar-height",
      `${topbar.offsetHeight}px`
    );
  }
  syncTopbarHeight();
  window.addEventListener("resize", syncTopbarHeight);

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
    allTeams = teams;
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
        team.schedule_time
      );
      card.querySelector(".team-card-open").addEventListener("click", () => openTeam(team.id));
      card.querySelector(".team-card-share").addEventListener("click", () => openShare(team.id));
      list.appendChild(card);
    });
  }

  // Fill the "copy from team" picker with the user's teams, plus a leading
  // "None (empty team)" option that creates a blank team.
  function populateCopyTeamSelect(select) {
    const none = `<option value="">None (empty team)</option>`;
    select.innerHTML =
      none +
      allTeams
        .map((t) => `<option value="${t.id}">${escapeAttr(t.name)}</option>`)
        .join("");
    select.value = "";
  }

  // New team form toggling.
  const newTeamForm = el("new-team-form");
  el("new-team-btn").addEventListener("click", () => {
    populateCopyTeamSelect(el("new-team-copy"));
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
    const copyFromRaw = el("new-team-copy").value;
    const copyFrom = copyFromRaw ? Number(copyFromRaw) : 0;
    try {
      const team = await api.createTeam(name, copyFrom);
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
      // Show the detail view *before* rendering so the buff/crit coverage
      // refreshes (which bail when the view is hidden) paint on first load.
      showView("detail");
      renderTeamDetail();
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
    refreshBuffCoverage();
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

    // The stored time is in UTC. Always show/edit it in the **viewer's**
    // current timezone.
    const localZone = localTimezone();
    const timeInput = el("schedule-time");
    timeInput.value = currentTeam.schedule_time
      ? convertWallTime(currentTeam.schedule_time, "UTC", localZone)
      : "";
    timeInput.disabled = !editable;
    el("schedule-tz-note").textContent = `(in your timezone: ${localZone})`;

    // Extra display timezones the team uses.
    teamTimezones = (currentTeam.team_timezones || []).slice();
    populateTeamTzAdd();
    renderTeamTimezones(editable);
  }

  // (Re)build the "add timezone" picker — the same searchable select used by
  // gear/skills — with every known zone not already in the team's list. Viewers
  // see no picker (they cannot edit).
  function populateTeamTzAdd() {
    const mount = el("team-tz-add");
    mount.innerHTML = "";
    if (!canEdit()) return;
    const already = new Set(teamTimezones);
    const zones = timezoneList().filter((z) => !already.has(z));
    const select = createSearchableSelect({
      groups: [{ group: null, items: zones.map((z) => ({ value: z, label: tzLabel(z) })) }],
      placeholder: "Add a timezone…",
      onSelect: (tz) => addTeamTimezone(tz),
    });
    mount.appendChild(select);
  }

  // Render the removable chips for the team's display timezones.
  function renderTeamTimezones(editable) {
    if (editable === undefined) editable = canEdit();
    const list = el("team-tz-list");
    list.innerHTML = "";
    teamTimezones.forEach((tz) => {
      const chip = document.createElement("span");
      chip.className = "chip";
      chip.innerHTML = `<span class="chip-label">${escapeAttr(tzLabel(tz))}</span>`;
      if (editable) {
        const remove = document.createElement("button");
        remove.type = "button";
        remove.className = "chip-remove";
        remove.setAttribute("aria-label", "Remove");
        remove.textContent = "×";
        remove.addEventListener("click", () => removeTeamTimezone(tz));
        chip.appendChild(remove);
      }
      list.appendChild(chip);
    });
  }

  function addTeamTimezone(tz) {
    if (!tz || teamTimezones.includes(tz)) return;
    teamTimezones.push(tz);
    populateTeamTzAdd();
    renderTeamTimezones();
    scheduleAutosave("team");
  }

  function removeTeamTimezone(tz) {
    teamTimezones = teamTimezones.filter((z) => z !== tz);
    populateTeamTzAdd();
    renderTeamTimezones();
    scheduleAutosave("team");
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
        race: val("race"),
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
      // The time input is in the viewer's current zone; store it in UTC so any
      // viewer can convert it back to their own zone.
      schedule_time: convertWallTime(el("schedule-time").value, localTimezone(), "UTC"),
      team_timezones: teamTimezones,
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
    // The team-timezone picker manages its own state + autosave (via onSelect).
    if (e.target.closest("#team-tz-add")) return;
    if (e.target.matches("input, select, textarea")) {
      scheduleAutosave("team");
      // Build/role/class/race changes can change buff + crit coverage; repaint.
      refreshBuffCoverage();
      refreshCritCoverage();
      refreshPenCoverage();
    }
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
      // Drives the role-based background color (see .player-slot[data-role] CSS).
      slot.dataset.role = player.role;
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
            <div class="form-group">
              <label>Race</label>
              <select class="input" data-field="race">${optionsHtml(RACES, player.race)}</select>
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
              <div class="loadout-col" data-type="potions">
                <label>Potions</label>
                <div class="chip-list" data-list></div>
              </div>
              <div class="loadout-col" data-type="cp_blue">
                <label>Blue CP</label>
                <div class="chip-list" data-list></div>
              </div>
              <div class="loadout-col" data-type="weapons">
                <label>Weapons</label>
                <div class="chip-list" data-list></div>
              </div>
              <div class="loadout-col" data-type="pen_extra">
                <label>Pen sources</label>
                <div class="chip-list" data-list></div>
              </div>
            </div>
            <div class="crit-setup" data-crit>
              <div class="crit-field">
                <label>Mundus</label>
                <select class="input" data-crit-field="mundus">${optionsHtml(MUNDUS_STONES, "")}</select>
              </div>
              <div class="crit-field crit-armor">
                <label>Armor pieces (H / M / L)</label>
                <div class="armor-steppers">
                  <input class="input armor-count" type="number" min="0" max="7" data-crit-field="armor_heavy" aria-label="Heavy armor pieces" />
                  <input class="input armor-count" type="number" min="0" max="7" data-crit-field="armor_medium" aria-label="Medium armor pieces" />
                  <input class="input armor-count" type="number" min="0" max="7" data-crit-field="armor_light" aria-label="Light armor pieces" />
                </div>
              </div>
              <div class="crit-field crit-result">
                <label>Crit damage</label>
                <span class="crit-label" data-crit-label>—</span>
              </div>
              <div class="crit-field crit-result">
                <label>Penetration</label>
                <span class="crit-label" data-pen-label>—</span>
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

      // Recolor the slot live when its role changes (autosave is handled by the
      // global roster change listener).
      const roleSel = slot.querySelector('[data-field="role"]');
      roleSel.addEventListener("change", () => {
        slot.dataset.role = roleSel.value;
      });

      // Build the loadout add-controls (one per gear/skills column) for the
      // currently selected encounter. The chips themselves are filled by
      // renderRosterLoadouts so they can be refreshed when the encounter changes.
      slot.querySelectorAll("[data-loadout] .loadout-col").forEach((col) => {
        const type = col.dataset.type;
        const listEl = col.querySelector("[data-list]");
        if (editable) col.appendChild(buildAddControl(listEl, type));
      });

      // The crit-setup fields (mundus, armor counts) live inside [data-loadout],
      // so the generic detail-view change listener skips them; wire their own
      // encounter autosave + crit refresh here.
      if (editable) {
        slot.querySelectorAll("[data-crit] [data-crit-field]").forEach((field) => {
          const evt = field.tagName === "SELECT" ? "change" : "input";
          field.addEventListener(evt, () => {
            scheduleAutosave("encounter");
            refreshCritCoverage();
            refreshPenCoverage();
          });
        });
      }

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

      // Mundus + armor counts are encounter-scoped too; refresh them on switch.
      const critEl = slot.querySelector("[data-crit]");
      if (critEl) {
        const mundusSel = critEl.querySelector('[data-crit-field="mundus"]');
        if (mundusSel) mundusSel.value = lo.mundus || "";
        ["armor_heavy", "armor_medium", "armor_light"].forEach((f) => {
          const input = critEl.querySelector(`[data-crit-field="${f}"]`);
          if (input) input.value = Number(lo[f]) || 0;
        });
      }
    });

    refreshCritCoverage();
    refreshPenCoverage();
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
      const names = currentEncounters.map((enc) => enc.name);
      populateEncounterNameSelect(rename, names, currentEncounter.name);
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
      refreshBuffCoverage();
    } catch (err) {
      handleError(err);
    }
  }

  // Populate an encounter-name picker with only the valid choices for the team
  // (unique names + a single trial; see validEncounterGroups). `keepName` is the
  // current name when renaming. Returns true if any choice is available.
  function populateEncounterNameSelect(select, existingNames, keepName) {
    const groups = validEncounterGroups(existingNames || [], keepName);
    select.innerHTML = groups
      .map(
        (g) =>
          `<optgroup label="${escapeAttr(g.group)}">` +
          g.names.map((n) => `<option value="${escapeAttr(n)}">${escapeAttr(n)}</option>`).join("") +
          `</optgroup>`
      )
      .join("");
    return groups.length > 0;
  }

  // Fill the "copy gear & skills from" picker with the team's existing
  // encounters, plus a leading "None (empty)" option that creates a blank one.
  function populateCopyFromSelect(select) {
    const none = `<option value="">None (empty encounter)</option>`;
    select.innerHTML =
      none +
      currentEncounters
        .map((enc) => `<option value="${enc.id}">${escapeAttr(enc.name)}</option>`)
        .join("");
    select.value = "";
  }

  const addEncounterForm = el("add-encounter-form");
  el("add-encounter-btn").addEventListener("click", () => {
    const names = currentEncounters.map((enc) => enc.name);
    const hasChoices = populateEncounterNameSelect(el("add-encounter-name"), names);
    if (!hasChoices) {
      showMessage("No more encounters available to add for this trial.", "error");
      return;
    }
    populateCopyFromSelect(el("add-encounter-copy"));
    addEncounterForm.classList.remove("is-hidden");
  });
  el("add-encounter-cancel").addEventListener("click", () => {
    addEncounterForm.classList.add("is-hidden");
  });
  addEncounterForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    const name = el("add-encounter-name").value;
    const copyFromRaw = el("add-encounter-copy").value;
    const copyFrom = copyFromRaw ? Number(copyFromRaw) : 0;
    try {
      const enc = await api.createEncounter(currentTeam.id, name, copyFrom);
      addEncounterForm.classList.add("is-hidden");
      const { encounters } = await api.listEncounters(currentTeam.id);
      currentEncounters = encounters || [];
      // Select the newly added encounter so its loadouts show in the roster.
      currentEncounter = await api.getEncounter(currentTeam.id, enc.id);
      renderEncountersBar();
      renderEncounterControls();
      renderRosterLoadouts();
      refreshBuffCoverage();
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
    // Show the gear set description on hover (same floating tooltip the picker
    // options use; see initTooltips in components.js).
    const desc = cfg.desc(key);
    if (desc) chip.dataset.tip = desc;
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
        refreshBuffCoverage();
        refreshCritCoverage();
        refreshPenCoverage();
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
        refreshBuffCoverage();
        refreshCritCoverage();
        refreshPenCoverage();
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
      const critEl = slot.querySelector("[data-crit]");
      const critVal = (f) => {
        const e = critEl ? critEl.querySelector(`[data-crit-field="${f}"]`) : null;
        return e ? e.value : "";
      };
      const armor = (f) => {
        const v = parseInt(critVal(f), 10);
        if (!Number.isFinite(v)) return 0;
        return Math.max(0, Math.min(7, v));
      };
      return {
        slot: Number(slot.dataset.slot),
        gear: read("gear"),
        skills: read("skills"),
        potions: read("potions"),
        cp_blue: read("cp_blue"),
        weapons: read("weapons"),
        pen_extra: read("pen_extra"),
        mundus: critVal("mundus"),
        armor_heavy: armor("armor_heavy"),
        armor_medium: armor("armor_medium"),
        armor_light: armor("armor_light"),
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
      refreshBuffCoverage();
      showMessage("Encounter deleted", "success");
    } catch (err) {
      handleError(err);
    }
  });

  // --- Buffs coverage ---
  // Coverage is computed live from the current DOM state (roster build + the
  // selected encounter's gear/skills/potions) so it stays correct after
  // autosaves (which intentionally don't re-render). A buff is "covered" when at
  // least one player provides one of its sources.
  let lastBuffCoverage = null;

  function currentLoadoutBySlot() {
    const map = {};
    collectLoadouts().forEach((l) => {
      map[l.slot] = l;
    });
    return map;
  }

  // Recompute and repaint the summary card (count + pip bar). Cheap; safe to
  // call on every roster/loadout change. Keeps an open modal in sync.
  function refreshBuffCoverage() {
    const countEl = el("buffs-count");
    if (!countEl || !currentTeam || detailView.classList.contains("is-hidden")) return;
    if (!el("roster").querySelector(".player-slot")) return;

    const coverage = computeBuffCoverage(collectPlayers(), currentLoadoutBySlot());
    lastBuffCoverage = coverage;

    countEl.textContent = `${coverage.met} / ${coverage.total}`;
    countEl.classList.toggle("is-full", coverage.total > 0 && coverage.met === coverage.total);

    const bar = el("buffs-bar");
    if (bar) {
      bar.innerHTML = "";
      coverage.items.forEach((item) => {
        const pip = document.createElement("span");
        pip.className = `buff-pip ${item.met ? "is-met" : "is-unmet"}`;
        pip.dataset.tip = `${item.buff.label}: ${item.met ? "covered" : "not covered"}`;
        bar.appendChild(pip);
      });
    }

    if (!el("buffs-modal").classList.contains("is-hidden")) {
      renderBuffsModal();
    }
  }

  // Render the per-buff breakdown into the details modal.
  function renderBuffsModal() {
    const coverage =
      lastBuffCoverage || computeBuffCoverage(collectPlayers(), currentLoadoutBySlot());
    el("buffs-modal-sub").textContent =
      `${coverage.met} of ${coverage.total} buffs covered` +
      (currentEncounter ? ` · ${currentEncounter.name}` : "");

    const list = el("buffs-modal-list");
    list.innerHTML = "";
    coverage.items.forEach((item) => {
      const row = document.createElement("div");
      row.className = `buff-row ${item.met ? "is-met" : "is-unmet"}`;

      let providersHtml;
      if (item.met) {
        const parts = item.providers.map(
          (p) =>
            `<span class="buff-provider">P${p.slot} · ${escapeAttr(
              BUFF_CATEGORY_LABELS[p.category] || p.category
            )}: ${escapeAttr(buffSourceLabel(p.category, p.key))}</span>`
        );
        providersHtml = `<div class="buff-providers">${parts.join("")}</div>`;
      } else {
        providersHtml = `<div class="buff-providers text-muted">Not covered</div>`;
      }

      const known = buffKnownSources(item.buff);
      const tipText = [
        item.buff.desc || "",
        known.length ? `Known sources: ${known.join("; ")}` : "",
      ]
        .filter(Boolean)
        .join("\n");
      const tip = tipText ? ` data-tip="${escapeAttr(tipText)}"` : "";
      row.innerHTML = `
        <div class="buff-row-head">
          <span class="buff-status" aria-hidden="true">${item.met ? "✓" : "✗"}</span>
          <span class="buff-name"${tip}>${escapeAttr(item.buff.label)}</span>
        </div>
        ${providersHtml}`;
      list.appendChild(row);
    });
  }

  function openBuffsModal() {
    renderBuffsModal();
    el("buffs-modal").classList.remove("is-hidden");
  }

  function closeBuffsModal() {
    el("buffs-modal").classList.add("is-hidden");
  }

  el("buffs-details-btn").addEventListener("click", openBuffsModal);
  el("buffs-modal-close").addEventListener("click", closeBuffsModal);
  el("buffs-modal").addEventListener("click", (e) => {
    // Click on the dimmed backdrop (outside the dialog) closes it.
    if (e.target === el("buffs-modal")) closeBuffsModal();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !el("buffs-modal").classList.contains("is-hidden")) {
      closeBuffsModal();
    }
  });

  // --- Crit damage coverage ---
  // Like buffs, crit is computed live from the current DOM (roster build + the
  // selected encounter's gear/skills/CP/weapons/mundus/armor) so it survives
  // autosaves. The card shows group/target/solo-required; each roster slot gets a
  // crit-damage label + met/unmet indicator against the cap.
  let lastCritCoverage = null;

  function refreshCritCoverage() {
    const groupEl = el("crit-group");
    if (!groupEl || !currentTeam || detailView.classList.contains("is-hidden")) return;
    if (!el("roster").querySelector(".player-slot")) return;

    const cov = computeCritCoverage(collectPlayers(), currentLoadoutBySlot());
    lastCritCoverage = cov;

    el("crit-group").textContent = `${cov.group}%`;
    el("crit-target").textContent = `${cov.target}%`;
    el("crit-required").textContent = `${cov.soloRequired}%`;

    const bySlot = {};
    cov.players.forEach((p) => {
      bySlot[p.slot] = p;
    });
    el("roster").querySelectorAll(".player-slot").forEach((slot) => {
      const label = slot.querySelector("[data-crit-label]");
      if (!label) return;
      const r = bySlot[Number(slot.dataset.slot)];
      if (!r) {
        label.textContent = "—";
        return;
      }
      label.textContent = `${r.total}%`;
      label.classList.toggle("is-met", r.met);
      label.classList.toggle("is-unmet", !r.met);
      const breakdown = `self ${r.self}% + group ${cov.group}% + target ${cov.target}%`;
      label.dataset.tip = r.met
        ? `Meets the ${cov.cap}% cap (${breakdown}).`
        : `${r.deficit}% under the ${cov.cap}% cap (${breakdown}).`;
    });

    if (!el("crit-modal").classList.contains("is-hidden")) renderCritModal();
  }

  // Render the group/target source breakdown + per-player breakdown into the
  // crit details modal.
  function renderCritModal() {
    const cov =
      lastCritCoverage || computeCritCoverage(collectPlayers(), currentLoadoutBySlot());
    el("crit-modal-sub").textContent =
      `Cap ${cov.cap}% · Group ${cov.group}% · Target ${cov.target}% · Each player needs ${cov.soloRequired}% of their own` +
      (currentEncounter ? ` · ${currentEncounter.name}` : "");

    const provs = (sources) =>
      sources.length
        ? sources
            .map(
              (s) =>
                `<span class="buff-provider">${escapeAttr(s.label)} +${s.pct}% (P${s.providers
                  .map((pr) => pr.slot)
                  .join(", P")})</span>`
            )
            .join("")
        : `<span class="text-muted">none detected</span>`;

    el("crit-modal-sources").innerHTML = `
      <div class="buff-row is-met">
        <div class="buff-row-head"><span class="buff-name">Group provided (${cov.group}%)</span></div>
        <div class="buff-providers"><span class="buff-provider">Base +${cov.base}%</span>${provs(cov.groupSources)}</div>
      </div>
      <div class="buff-row is-met">
        <div class="buff-row-head"><span class="buff-name">Target applied (${cov.target}%)</span></div>
        <div class="buff-providers">${provs(cov.targetSources)}</div>
      </div>`;

    const list = el("crit-modal-list");
    list.innerHTML = "";
    cov.players.forEach((p) => {
      const row = document.createElement("div");
      row.className = `buff-row ${p.met ? "is-met" : "is-unmet"}`;
      const selfParts = p.sources.length
        ? p.sources
            .map((s) => `<span class="buff-provider">${escapeAttr(s.label)} +${s.pct}%</span>`)
            .join("")
        : `<span class="text-muted">No self sources</span>`;
      row.innerHTML = `
        <div class="buff-row-head">
          <span class="buff-status" aria-hidden="true">${p.met ? "✓" : "✗"}</span>
          <span class="buff-name">P${p.slot} — ${p.total}%${p.met ? "" : ` (−${p.deficit}%)`}</span>
        </div>
        <div class="buff-providers">${selfParts}</div>`;
      list.appendChild(row);
    });
  }

  function openCritModal() {
    renderCritModal();
    el("crit-modal").classList.remove("is-hidden");
  }

  function closeCritModal() {
    el("crit-modal").classList.add("is-hidden");
  }

  el("crit-details-btn").addEventListener("click", openCritModal);
  el("crit-modal-close").addEventListener("click", closeCritModal);
  el("crit-modal").addEventListener("click", (e) => {
    if (e.target === el("crit-modal")) closeCritModal();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !el("crit-modal").classList.contains("is-hidden")) {
      closeCritModal();
    }
  });

  // --- Penetration coverage (mirrors crit) ---
  // Evaluates the roster against the selected encounter. The card shows the
  // group total + per-player self requirement; each roster slot gets a pen label
  // with a met/unmet indicator against the target resistance.
  let lastPenCoverage = null;

  function refreshPenCoverage() {
    const groupEl = el("pen-group");
    if (!groupEl || !currentTeam || detailView.classList.contains("is-hidden")) return;
    if (!el("roster").querySelector(".player-slot")) return;

    const cov = computePenCoverage(collectPlayers(), currentLoadoutBySlot());
    lastPenCoverage = cov;

    el("pen-target").textContent = cov.target.toLocaleString();
    el("pen-group").textContent = cov.group.toLocaleString();
    el("pen-required").textContent = cov.selfRequired.toLocaleString();

    const bySlot = {};
    cov.players.forEach((p) => {
      bySlot[p.slot] = p;
    });
    el("roster").querySelectorAll(".player-slot").forEach((slot) => {
      const label = slot.querySelector("[data-pen-label]");
      if (!label) return;
      const r = bySlot[Number(slot.dataset.slot)];
      if (!r) {
        label.textContent = "—";
        return;
      }
      label.textContent = r.total.toLocaleString();
      label.classList.toggle("is-met", r.met);
      label.classList.toggle("is-unmet", !r.met);
      const breakdown = `self ${r.self.toLocaleString()} + group ${cov.group.toLocaleString()}`;
      label.dataset.tip = r.met
        ? `Reaches the ${cov.target.toLocaleString()} target (${breakdown}).`
        : `${r.deficit.toLocaleString()} under the ${cov.target.toLocaleString()} target (${breakdown}).`;
    });

    if (!el("pen-modal").classList.contains("is-hidden")) renderPenModal();
  }

  // Render the group source breakdown + per-player breakdown into the pen modal.
  function renderPenModal() {
    const cov =
      lastPenCoverage || computePenCoverage(collectPlayers(), currentLoadoutBySlot());
    el("pen-modal-sub").textContent =
      `Target ${cov.target.toLocaleString()} · Group ${cov.group.toLocaleString()} · Each player needs ${cov.selfRequired.toLocaleString()} of their own` +
      (currentEncounter ? ` · ${currentEncounter.name}` : "");

    const provs = (sources) =>
      sources.length
        ? sources
            .map(
              (s) =>
                `<span class="buff-provider">${escapeAttr(s.label)} +${s.pen.toLocaleString()} (P${s.providers
                  .map((pr) => pr.slot)
                  .join(", P")})</span>`
            )
            .join("")
        : `<span class="text-muted">none detected</span>`;

    el("pen-modal-sources").innerHTML = `
      <div class="buff-row is-met">
        <div class="buff-row-head"><span class="buff-name">Group provided (${cov.group.toLocaleString()})</span></div>
        <div class="buff-providers">${provs(cov.groupSources)}</div>
      </div>`;

    const list = el("pen-modal-list");
    list.innerHTML = "";
    cov.players.forEach((p) => {
      const row = document.createElement("div");
      row.className = `buff-row ${p.met ? "is-met" : "is-unmet"}`;
      const selfParts = p.sources.length
        ? p.sources
            .map(
              (s) => `<span class="buff-provider">${escapeAttr(s.label)} +${s.pen.toLocaleString()}</span>`
            )
            .join("")
        : `<span class="text-muted">No self sources</span>`;
      row.innerHTML = `
        <div class="buff-row-head">
          <span class="buff-status" aria-hidden="true">${p.met ? "✓" : "✗"}</span>
          <span class="buff-name">P${p.slot} — ${p.total.toLocaleString()}${
        p.met ? "" : ` (−${p.deficit.toLocaleString()})`
      }</span>
        </div>
        <div class="buff-providers">${selfParts}</div>`;
      list.appendChild(row);
    });
  }

  function openPenModal() {
    renderPenModal();
    el("pen-modal").classList.remove("is-hidden");
  }

  function closePenModal() {
    el("pen-modal").classList.add("is-hidden");
  }

  el("pen-details-btn").addEventListener("click", openPenModal);
  el("pen-modal-close").addEventListener("click", closePenModal);
  el("pen-modal").addEventListener("click", (e) => {
    if (e.target === el("pen-modal")) closePenModal();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !el("pen-modal").classList.contains("is-hidden")) {
      closePenModal();
    }
  });

  // --- Encounter chip panel stickiness ---
  // Only the chip panel (#encounters-panel) is `position: sticky`; it pins just
  // below the topbar while scrolling the roster. A zero-height sentinel placed
  // just above the panel tells us (via IntersectionObserver) when it has reached
  // the pin point, so we can toggle the elevated "stuck" style that splits it off
  // from the encounters card above. The observer's top rootMargin matches the
  // topbar height so the style flips exactly as the panel pins.
  function setupEncounterStickiness() {
    const sentinel = el("encounters-sentinel");
    const panel = el("encounters-panel");
    if (!sentinel || !panel || typeof IntersectionObserver === "undefined") {
      return;
    }
    const topbar = document.querySelector(".topbar");
    const topOffset = topbar ? topbar.offsetHeight : 0;
    const observer = new IntersectionObserver(
      ([entry]) => {
        // Not intersecting means the sentinel has scrolled past the pin point,
        // i.e. the chip panel is now pinned.
        panel.classList.toggle("is-stuck", !entry.isIntersecting);
      },
      { threshold: [0], rootMargin: `-${topOffset}px 0px 0px 0px` }
    );
    observer.observe(sentinel);
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

  setupEncounterStickiness();
  init();
})();
