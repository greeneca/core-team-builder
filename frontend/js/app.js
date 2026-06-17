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
  const membersView = el("members-view");

  let currentUser = null;
  let currentTeam = null;
  // The teams shown in the list; cached so the "copy from team" picker can list
  // them when creating a new team.
  let allTeams = [];
  let currentEncounters = [];
  // The encounter currently selected on the team page; its per-player loadouts
  // are shown inline in the roster. Always set once a team is open.
  let currentEncounter = null;
  // The open team's groupings (e.g. ice cages / slayer stacks). Each grouping is
  // { id, name, group_count, position, groups: [{ group_number, name, slots }] }.
  // Edited locally and autosaved per-grouping.
  let currentGroupings = [];
  // The open team's recruitment/availability pool (team_roster_members), loaded
  // when a team is opened. Feeds the Members page and the roster Discord-handle
  // combobox suggestions.
  let currentRosterMembers = [];
  // Member ids the viewer has unchecked from the availability heatmap. Members
  // default to included; this is a view-only client-side filter (not persisted).
  const heatmapExcluded = new Set();

  // Client-side mirrors of the backend caps (the server still enforces these).
  const MAX_GROUPINGS = 10;
  const MAX_GROUPS_PER_GROUPING = 12;
  // Per-grouping debounce timers, keyed by grouping id.
  const groupingSaveTimers = {};
  const GROUPING_SAVE_DELAY = 700;

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
    membersView.classList.toggle("is-hidden", view !== "members");
    window.scrollTo(0, 0);
  }

  // --- Sign out ---
  el("logout-btn").addEventListener("click", async () => {
    await api.logout();
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
          ${owned ? `<button class="btn btn--danger btn--sm team-card-delete" type="button">Delete</button>` : ""}
        </div>`;
      card.querySelector(".team-card-name").textContent = team.name;
      card.querySelector(".team-card-schedule").textContent = formatSchedule(
        team.schedule_days,
        team.schedule_time
      );
      card.querySelector(".team-card-open").addEventListener("click", () => openTeam(team.id));
      card.querySelector(".team-card-share").addEventListener("click", () => openShare(team.id));
      const deleteBtn = card.querySelector(".team-card-delete");
      if (deleteBtn) {
        deleteBtn.addEventListener("click", () => deleteTeam(team));
      }
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
      const { groupings } = await api.listGroupings(id);
      currentGroupings = groupings || [];
      // Load the member pool so the roster's Discord-handle combobox can suggest
      // known members. Best-effort: a failure here shouldn't block the team page.
      try {
        const { members } = await api.listRosterMembers(id);
        currentRosterMembers = members || [];
      } catch {
        currentRosterMembers = [];
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

  // --- Members page (recruitment / availability pool) ---

  const pad2 = (n) => String(n).padStart(2, "0");

  // Roles offered in the member pool — the trial's three core roles. "Support
  // DPS" is a player-build distinction only, so it's excluded here (this also
  // mirrors the Discord signup intake, which gathers tank/healer/dps).
  const MEMBER_ROLES = ROLES.filter((r) => r.value !== "support_dps");

  // memberHandleOptions(): combobox suggestions for the roster Discord handle,
  // drawn from the member pool. Value = the handle stored on the player; the
  // label adds the display name for context.
  function memberHandleOptions() {
    const seen = new Set();
    const opts = [];
    currentRosterMembers.forEach((m) => {
      const handle = (m.discord_username || "").trim();
      if (!handle || seen.has(handle.toLowerCase())) return;
      seen.add(handle.toLowerCase());
      const name = (m.display_name || "").trim();
      const label =
        name && name.toLowerCase() !== handle.toLowerCase()
          ? `${name} (@${handle})`
          : `@${handle}`;
      opts.push({ value: handle, label });
    });
    return opts;
  }

  async function openMembers(id) {
    clearMessage();
    try {
      const { members } = await api.listRosterMembers(id);
      currentRosterMembers = members || [];
      renderRosterMembers();
      showView("members");
    } catch (err) {
      handleError(err);
    }
  }

  function renderRosterMembers() {
    const editable = canEdit();
    el("members-team-name").textContent = currentTeam ? currentTeam.name : "Members";
    el("members-local-tz").textContent = localTimezone();

    const members = currentRosterMembers.slice();
    el("members-count").textContent = String(members.length);
    el("members-empty").classList.toggle("is-hidden", members.length > 0);

    el("member-add-btn").classList.toggle("is-hidden", !editable);
    if (!editable) el("member-add-form").classList.add("is-hidden");

    renderMembersRoleSummary(members);
    renderMembersHeatmap(members.filter((m) => !heatmapExcluded.has(m.id)));
    renderMembersCards(members, editable);
  }

  // Count how many members are comfortable with each role.
  function renderMembersRoleSummary(members) {
    const container = el("members-roles-summary");
    container.innerHTML = "";
    MEMBER_ROLES.forEach((role) => {
      const count = members.filter((m) => (m.roles || []).includes(role.value)).length;
      const chip = document.createElement("div");
      chip.className = "role-summary";
      chip.dataset.role = role.value;
      const c = document.createElement("span");
      c.className = "role-summary-count";
      c.textContent = String(count);
      const l = document.createElement("span");
      l.className = "role-summary-label";
      l.textContent = role.label;
      chip.appendChild(c);
      chip.appendChild(l);
      container.appendChild(chip);
    });
  }

  // Expand members' availability windows into a viewer-local 7×24 count grid.
  // Each member records hours in their own timezone; we shift them into the
  // viewer's zone so the heatmap overlays everyone on a common clock. Recurring
  // weekly times have no date, so DST conversions can be off by an hour — the
  // same accepted trade-off as the trial schedule.
  function availabilityGrid(members) {
    const grid = Array.from({ length: 7 }, () => new Array(24).fill(0));
    const now = new Date();
    const localOff = tzOffsetMinutes(localTimezone(), now);
    const dayIndex = (d) => DAYS.findIndex((x) => x.value === d);

    members.forEach((m) => {
      const memberOff = m.timezone ? tzOffsetMinutes(m.timezone, now) : localOff;
      const diffHours = (localOff - memberOff) / 60;
      const avail = m.availability || {};
      (m.days || []).forEach((d) => {
        const di = dayIndex(d);
        if (di < 0) return;
        const w = avail[d];
        if (!w) return;
        let end = w.end;
        if (end <= w.start) end = w.start + 1; // single-hour / malformed window
        for (let h = w.start; h < end; h++) {
          const shifted = h + diffHours;
          const localHour = ((Math.floor(shifted) % 24) + 24) % 24;
          const dayShift = Math.floor(shifted / 24);
          const localDay = (((di + dayShift) % 7) + 7) % 7;
          grid[localDay][localHour] += 1;
        }
      });
    });
    return grid;
  }

  function renderMembersHeatmap(members) {
    const container = el("members-heatmap");
    container.innerHTML = "";
    const grid = availabilityGrid(members);
    // Show only *overlapping* times: a slot counts as available only when every
    // displayed member is available then (the intersection), not the union. With
    // the per-member chart checkboxes, this surfaces the windows the selected
    // group can actually all make. Members with no availability recorded don't
    // contribute a window, so require at least one to avoid an all-green grid.
    const withAvailability = members.filter((m) => (m.days || []).length > 0);
    const needed = withAvailability.length;

    const table = document.createElement("div");
    table.className = "heatmap-table";

    const header = document.createElement("div");
    header.className = "heatmap-row heatmap-row--head";
    const corner = document.createElement("div");
    corner.className = "heatmap-day heatmap-corner";
    header.appendChild(corner);
    for (let h = 0; h < 24; h++) {
      const c = document.createElement("div");
      c.className = "heatmap-hour";
      c.textContent = h % 3 === 0 ? String(h) : "";
      header.appendChild(c);
    }
    table.appendChild(header);

    let overlaps = 0;
    DAYS.forEach((day, di) => {
      const row = document.createElement("div");
      row.className = "heatmap-row";
      const label = document.createElement("div");
      label.className = "heatmap-day";
      label.textContent = day.label;
      row.appendChild(label);
      for (let h = 0; h < 24; h++) {
        const count = grid[di][h];
        const everyone = needed > 0 && count >= needed;
        const c = document.createElement("div");
        c.className = "heatmap-cell";
        // Overlap is all-or-nothing, so paint matching cells at full strength.
        c.style.setProperty("--heat", everyone ? "1" : "0");
        if (everyone) {
          overlaps++;
          c.classList.add("has-availability");
          const who = needed === 1 ? "1 available" : `all ${needed} available`;
          c.dataset.tip = `${day.label} ${pad2(h)}:00 — ${who}`;
        }
        row.appendChild(c);
      }
      table.appendChild(row);
    });
    container.appendChild(table);

    if (needed === 0) {
      const note = document.createElement("p");
      note.className = "text-muted mt-2";
      note.textContent = "No availability recorded yet.";
      container.appendChild(note);
    } else if (overlaps === 0) {
      const note = document.createElement("p");
      note.className = "text-muted mt-2";
      note.textContent =
        needed === 1
          ? "No availability recorded yet."
          : "No time works for everyone shown — uncheck members to find overlaps for a smaller group.";
      container.appendChild(note);
    }
  }

  function renderMembersCards(members, editable) {
    const grid = el("members-grid");
    grid.innerHTML = "";
    members.forEach((m) => {
      const card = document.createElement("div");
      card.className = "member-card";

      const head = document.createElement("div");
      head.className = "member-card-head";
      const title = document.createElement("div");
      title.className = "member-card-title";
      const name = document.createElement("strong");
      name.className = "member-card-name";
      name.textContent = (m.display_name || m.discord_username || "Unknown").trim();
      title.appendChild(name);
      const source = document.createElement("span");
      source.className = "badge badge--shared";
      source.textContent = m.source === "manual" ? "Manual" : "Discord";
      title.appendChild(source);
      if (m.status === "draft") {
        const draft = document.createElement("span");
        draft.className = "badge badge--draft";
        draft.textContent = "In progress";
        title.appendChild(draft);
      }
      head.appendChild(title);
      if (editable) {
        const actions = document.createElement("div");
        actions.className = "member-card-actions";
        const edit = document.createElement("button");
        edit.className = "btn btn--ghost btn--sm";
        edit.type = "button";
        edit.textContent = "Edit";
        edit.addEventListener("click", () => openMemberForm(m));
        actions.appendChild(edit);
        const del = document.createElement("button");
        del.className = "btn btn--danger btn--sm";
        del.type = "button";
        del.textContent = "Remove";
        del.addEventListener("click", () => deleteMember(m));
        actions.appendChild(del);
        head.appendChild(actions);
      }
      card.appendChild(head);

      // Per-member toggle to include/exclude them from the availability chart.
      // Only meaningful for members who actually have availability recorded.
      if ((m.days || []).length) {
        const chart = document.createElement("label");
        chart.className = "member-chart-toggle";
        const cb = document.createElement("input");
        cb.type = "checkbox";
        cb.checked = !heatmapExcluded.has(m.id);
        cb.addEventListener("change", () => {
          if (cb.checked) heatmapExcluded.delete(m.id);
          else heatmapExcluded.add(m.id);
          renderMembersHeatmap(
            currentRosterMembers.filter((x) => !heatmapExcluded.has(x.id))
          );
        });
        const span = document.createElement("span");
        span.textContent = "Show in availability chart";
        chart.appendChild(cb);
        chart.appendChild(span);
        card.appendChild(chart);
      }

      const bits = [];
      if (m.discord_username) bits.push(`@${m.discord_username}`);
      if (m.timezone) bits.push(m.timezone);
      if (bits.length) {
        const meta = document.createElement("div");
        meta.className = "member-card-meta text-muted";
        meta.textContent = bits.join(" · ");
        card.appendChild(meta);
      }

      if ((m.days || []).length) {
        const avail = document.createElement("div");
        avail.className = "member-avail";
        (m.days || []).forEach((d) => {
          const w = (m.availability || {})[d];
          const row = document.createElement("div");
          row.className = "member-avail-row";
          const dayEl = document.createElement("span");
          dayEl.className = "member-avail-day";
          dayEl.textContent = labelFor(DAYS, d);
          const timeEl = document.createElement("span");
          timeEl.className = "member-avail-time";
          timeEl.textContent = w ? `${pad2(w.start)}:00 – ${pad2(w.end)}:00` : "—";
          row.appendChild(dayEl);
          row.appendChild(timeEl);
          avail.appendChild(row);
        });
        card.appendChild(avail);
      }

      if ((m.roles || []).length) {
        const rolesEl = document.createElement("div");
        rolesEl.className = "member-roles-list";
        (m.roles || []).forEach((r) => {
          const classes = (m.classes_by_role || {})[r] || [];
          const row = document.createElement("div");
          row.className = "member-role-row";
          const tag = document.createElement("span");
          tag.className = "role-tag";
          tag.dataset.role = r;
          tag.textContent = labelFor(ROLES, r);
          row.appendChild(tag);
          const classLabels = classes
            .map((c) => labelFor(CLASSES, c))
            .filter((x) => x && x !== "—");
          const cls = document.createElement("span");
          cls.className = "member-role-classes";
          cls.textContent = classLabels.length ? classLabels.join(", ") : "Any class";
          row.appendChild(cls);
          rolesEl.appendChild(row);
        });
        card.appendChild(rolesEl);
      }

      grid.appendChild(card);
    });
  }

  async function deleteMember(m) {
    if (!currentTeam) return;
    const who = m.display_name || m.discord_username || "this member";
    if (!confirm(`Remove ${who} from the member pool?`)) return;
    try {
      await api.deleteRosterMember(currentTeam.id, m.id);
      currentRosterMembers = currentRosterMembers.filter((x) => x.id !== m.id);
      renderRosterMembers();
      showMessage("Member removed", "success");
    } catch (err) {
      handleError(err);
    }
  }

  // Add/edit member form (editors): a compact builder for availability + roles/
  // classes. Used to add someone who didn't sign up through Discord, and to edit
  // an existing member (e.g. set or adjust availability time limits).
  const memberAddForm = el("member-add-form");
  // null = add mode; otherwise the id of the member currently being edited.
  let editingMemberId = null;

  // openMemberForm(member|null): open the form in edit mode (prefilled) when a
  // member is given, otherwise in add mode.
  function openMemberForm(member) {
    editingMemberId = member ? member.id : null;
    buildMemberAddForm(member || null);
    el("member-form-title").textContent = member ? "Edit member" : "Add member";
    el("member-form-submit").textContent = member ? "Save changes" : "Add member";
    memberAddForm.classList.remove("is-hidden");
    el("member-name").focus();
    memberAddForm.scrollIntoView({ behavior: "smooth", block: "nearest" });
  }

  function closeMemberForm() {
    memberAddForm.classList.add("is-hidden");
    memberAddForm.reset();
    editingMemberId = null;
  }

  el("member-add-btn").addEventListener("click", () => openMemberForm(null));
  el("member-add-cancel").addEventListener("click", closeMemberForm);

  function hourOptionsHtml(selected) {
    let out = "";
    for (let h = 0; h < 24; h++) {
      out += `<option value="${h}"${h === selected ? " selected" : ""}>${pad2(h)}:00</option>`;
    }
    return out;
  }

  // End-hour options 01:00–24:00 where 24:00 = midnight (end of day), so a window
  // can run to the end of the day — which a plain 00:00–23:00 list cannot express.
  function endHourOptionsHtml(selected) {
    let out = "";
    for (let h = 1; h <= 24; h++) {
      const label = h === 24 ? "24:00 (midnight)" : `${pad2(h)}:00`;
      out += `<option value="${h}"${h === selected ? " selected" : ""}>${label}</option>`;
    }
    return out;
  }

  function buildMemberAddForm(member) {
    memberAddForm.reset();
    const avail = (member && member.availability) || {};
    const memberDays = (member && member.days) || [];
    const memberRoles = (member && member.roles) || [];
    const classesByRole = (member && member.classes_by_role) || {};

    el("member-name").value = member ? member.display_name || "" : "";
    el("member-discord").value = member ? member.discord_username || "" : "";

    const tzSel = el("member-tz");
    const zones = timezoneList();
    const local = localTimezone();
    if (!zones.includes(local)) zones.unshift(local);
    const memberTz = member && member.timezone;
    if (memberTz && !zones.includes(memberTz)) zones.unshift(memberTz);
    tzSel.innerHTML = zones
      .map((z) => `<option value="${escapeAttr(z)}">${escapeAttr(z)}</option>`)
      .join("");
    tzSel.value = memberTz || local;

    const daysEl = el("member-days");
    daysEl.innerHTML = "";
    DAYS.forEach((d) => {
      const w = avail[d.value];
      const checked = memberDays.includes(d.value);
      const startVal = w ? w.start : 18;
      const endVal = w ? w.end : 22;
      const row = document.createElement("div");
      row.className = "member-day-row";
      row.innerHTML = `
        <label class="day-toggle">
          <input type="checkbox" data-day="${d.value}"${checked ? " checked" : ""} />
          <span>${d.label}</span>
        </label>
        <div class="member-day-hours${checked ? "" : " is-hidden"}">
          <select class="input input--sm" data-day-start="${d.value}" aria-label="${d.label} start hour">${hourOptionsHtml(startVal)}</select>
          <span class="member-day-sep">–</span>
          <select class="input input--sm" data-day-end="${d.value}" aria-label="${d.label} end hour">${endHourOptionsHtml(endVal)}</select>
        </div>`;
      const cb = row.querySelector("input[type=checkbox]");
      const hours = row.querySelector(".member-day-hours");
      cb.addEventListener("change", () => hours.classList.toggle("is-hidden", !cb.checked));
      daysEl.appendChild(row);
    });

    const rolesEl = el("member-roles");
    rolesEl.innerHTML = "";
    MEMBER_ROLES.forEach((role) => {
      const roleChecked = memberRoles.includes(role.value);
      const picked = classesByRole[role.value] || [];
      const row = document.createElement("div");
      row.className = "member-role-add";
      const classBoxes = CLASSES.filter((c) => c.value)
        .map(
          (c) =>
            `<label class="class-toggle"><input type="checkbox" data-class="${role.value}:${c.value}"${picked.includes(c.value) ? " checked" : ""} /><span>${c.label}</span></label>`
        )
        .join("");
      row.innerHTML = `
        <label class="day-toggle">
          <input type="checkbox" data-role="${role.value}"${roleChecked ? " checked" : ""} />
          <span>${role.label}</span>
        </label>
        <div class="member-role-classes-add${roleChecked ? "" : " is-hidden"}">${classBoxes}</div>`;
      const cb = row.querySelector("input[data-role]");
      const classes = row.querySelector(".member-role-classes-add");
      cb.addEventListener("change", () => classes.classList.toggle("is-hidden", !cb.checked));
      rolesEl.appendChild(row);
    });
  }

  memberAddForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    if (!currentTeam) return;
    const displayName = el("member-name").value.trim();
    if (!displayName) {
      showMessage("Display name is required");
      return;
    }

    const days = [];
    const availability = {};
    DAYS.forEach((d) => {
      const cb = memberAddForm.querySelector(`input[data-day="${d.value}"]`);
      if (cb && cb.checked) {
        days.push(d.value);
        const start = Number(memberAddForm.querySelector(`[data-day-start="${d.value}"]`).value);
        const end = Number(memberAddForm.querySelector(`[data-day-end="${d.value}"]`).value);
        availability[d.value] = { start, end };
      }
    });

    const roles = [];
    const classesByRole = {};
    MEMBER_ROLES.forEach((role) => {
      const cb = memberAddForm.querySelector(`input[data-role="${role.value}"]`);
      if (cb && cb.checked) {
        roles.push(role.value);
        const picked = Array.from(
          memberAddForm.querySelectorAll(`input[data-class^="${role.value}:"]`)
        )
          .filter((x) => x.checked)
          .map((x) => x.getAttribute("data-class").split(":")[1]);
        if (picked.length) classesByRole[role.value] = picked;
      }
    });

    const body = {
      display_name: displayName,
      discord_username: el("member-discord").value.trim(),
      timezone: el("member-tz").value,
      days,
      availability,
      roles,
      classes_by_role: classesByRole,
    };

    try {
      if (editingMemberId != null) {
        const updated = await api.updateRosterMember(currentTeam.id, editingMemberId, body);
        currentRosterMembers = currentRosterMembers.map((x) =>
          x.id === updated.id ? updated : x
        );
        closeMemberForm();
        renderRosterMembers();
        showMessage("Member updated", "success");
      } else {
        const created = await api.createRosterMember(currentTeam.id, body);
        currentRosterMembers.unshift(created);
        closeMemberForm();
        renderRosterMembers();
        showMessage("Member added", "success");
      }
    } catch (err) {
      handleError(err);
    }
  });

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

    const postFooterInput = el("post-footer-input");
    if (postFooterInput) {
      postFooterInput.value = currentTeam.post_footer || "";
      postFooterInput.readOnly = !editable;
    }

    const dmFooterInput = el("dm-footer-input");
    if (dmFooterInput) {
      dmFooterInput.value = currentTeam.dm_footer || "";
      dmFooterInput.readOnly = !editable;
    }

    const signupPostInput = el("signup-post-input");
    if (signupPostInput) {
      signupPostInput.value = currentTeam.signup_post || "";
      signupPostInput.readOnly = !editable;
    }

    renderSchedule(editable);
    renderEncountersBar();
    renderEncounterControls();
    applyEncountersMode();
    renderRoster();
    renderGroupings();
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

  // Members page: open from the team detail toolbar; back returns to the team.
  el("members-btn").addEventListener("click", () => {
    if (currentTeam) openMembers(currentTeam.id);
  });
  el("members-back-btn").addEventListener("click", () => {
    if (currentTeam) {
      showView("detail");
    } else {
      showView("teams");
      loadTeams();
    }
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
      encounters_enabled: encountersEnabled(),
      post_footer: el("post-footer-input") ? el("post-footer-input").value : "",
      dm_footer: el("dm-footer-input") ? el("dm-footer-input").value : "",
      signup_post: el("signup-post-input") ? el("signup-post-input").value : "",
      players,
    };
    setSaveStatus("team", "saving");
    try {
      currentTeam = await api.saveTeam(currentTeam.id, payload);
      setSaveStatus("team", "saved");
      // Roster name/role edits change the labels groupings show for each slot;
      // refresh them now that currentTeam reflects the saved roster — but not
      // while the user is mid-interaction in the groupings section, since the
      // rebuild would close an open dropdown.
      if (!isGroupingsInteracting()) renderGroupings();
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
    // The "Copy to…" control performs its own save; ignore it here.
    if (e.target.closest("[data-copy]")) return;
    // Groupings manage their own per-grouping state + autosave.
    if (e.target.closest("#groupings-card")) return;
    if (e.target.matches("input, select, textarea")) {
      scheduleAutosave("team");
      // Build/role/class/race changes can change buff + crit coverage; repaint.
      refreshBuffCoverage();
      refreshCritCoverage();
      refreshPenCoverage();
      // Name/role edits change the jump-nav labels.
      if (e.target.matches('[data-field="name"], [data-field="role"]')) {
        renderPlayerNav();
      }
    }
  });

  // Quick, fixed-duration smooth scroll. The native `behavior: "smooth"` is
  // distance-based and feels sluggish for long jumps (e.g. slot 1 → slot 12), so
  // we animate ourselves over a short constant duration. Honors reduced-motion.
  const NAV_SCROLL_DURATION = 220; // ms
  function smoothScrollTo(targetY) {
    const maxY = Math.max(
      0,
      document.documentElement.scrollHeight - window.innerHeight
    );
    const dest = Math.max(0, Math.min(targetY, maxY));
    const startY = window.scrollY;
    const delta = dest - startY;
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduce || Math.abs(delta) < 2) {
      window.scrollTo(0, dest);
      return;
    }
    const start = performance.now();
    // easeOutCubic: fast start, gentle settle.
    const ease = (t) => 1 - Math.pow(1 - t, 3);
    function step(now) {
      const t = Math.min(1, (now - start) / NAV_SCROLL_DURATION);
      window.scrollTo(0, startY + delta * ease(t));
      if (t < 1) requestAnimationFrame(step);
    }
    requestAnimationFrame(step);
  }

  // Scroll an element's top just below the sticky chrome, honoring its CSS
  // scroll-margin-top (the same offset native scrollIntoView would respect).
  function smoothScrollToEl(elm) {
    const marginTop = parseFloat(getComputedStyle(elm).scrollMarginTop) || 0;
    const top = elm.getBoundingClientRect().top + window.scrollY - marginTop;
    smoothScrollTo(top);
  }

  // Floating jump-nav: scroll to the top, the Group Buffs card, the Groupings
  // card, or a player slot. Delegated so it works for the dynamically rebuilt
  // per-player links.
  el("player-nav").addEventListener("click", (e) => {
    const link = e.target.closest("[data-nav]");
    if (!link) return;
    e.preventDefault();
    const kind = link.dataset.nav;
    if (kind === "top") {
      smoothScrollTo(0);
    } else if (kind === "buffs") {
      const card = el("group-stats-card") || document.querySelector(".buffs-card");
      if (card) {
        expandAncestors(card);
        smoothScrollToEl(card);
      }
    } else if (kind === "groupings") {
      const card = el("groupings-card");
      if (card) {
        expandAncestors(card);
        smoothScrollToEl(card);
      }
    } else if (kind === "player") {
      const slotEl = el("roster").querySelector(
        `.player-slot[data-slot="${link.dataset.slot}"]`
      );
      if (slotEl) {
        expandAncestors(slotEl);
        smoothScrollToEl(slotEl);
      }
    }
  });

  // --- Collapsible sections ---
  // Every collapsible region is a `.collapsible` whose own header is a direct
  // child `.collapsible-head` and whose hideable content is a direct child
  // `.collapsible-body`. Collapsing toggles `.is-collapsed` (CSS hides the body)
  // and flips the header chevron via its toggle's aria-expanded. The `>` child
  // combinator in the CSS keeps nested collapsibles (player slots inside the
  // roster) independent of their parent.
  function ownCollapseToggle(root) {
    return root.querySelector(":scope > .collapsible-head .collapse-toggle");
  }
  function setCollapsed(root, collapsed) {
    if (!root) return;
    root.classList.toggle("is-collapsed", collapsed);
    const toggle = ownCollapseToggle(root);
    if (toggle) {
      toggle.setAttribute("aria-expanded", String(!collapsed));
      toggle.setAttribute("aria-label", collapsed ? "Expand section" : "Collapse section");
    }
  }
  // Expand the element and every collapsible ancestor so a jumped-to target is
  // actually visible (e.g. clicking a player in the side nav while the roster is
  // collapsed).
  function expandAncestors(elm) {
    let node = elm;
    while (node && node !== detailView) {
      if (node.classList && node.classList.contains("collapsible")) {
        setCollapsed(node, false);
      }
      node = node.parentElement;
    }
  }
  function sectionRoots() {
    return detailView.querySelectorAll(".section-collapsible");
  }
  function playerRoots() {
    return el("roster").querySelectorAll(".player-slot.collapsible");
  }

  // Click on a chevron toggles its section; click anywhere on a player header
  // (a non-interactive bar) toggles that player.
  detailView.addEventListener("click", (e) => {
    const toggle = e.target.closest(".collapse-toggle");
    if (toggle) {
      const root = toggle.closest(".collapsible");
      if (root) setCollapsed(root, !root.classList.contains("is-collapsed"));
      return;
    }
    const head = e.target.closest(".player-head");
    if (head) {
      const root = head.closest(".collapsible");
      if (root) setCollapsed(root, !root.classList.contains("is-collapsed"));
    }
  });

  el("collapse-all-btn").addEventListener("click", () => {
    sectionRoots().forEach((r) => setCollapsed(r, true));
  });
  el("expand-all-btn").addEventListener("click", () => {
    sectionRoots().forEach((r) => setCollapsed(r, false));
  });
  el("players-collapse-all-btn").addEventListener("click", () => {
    playerRoots().forEach((r) => setCollapsed(r, true));
  });
  el("players-expand-all-btn").addEventListener("click", () => {
    playerRoots().forEach((r) => setCollapsed(r, false));
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

  // Delete a team from the teams list (owner only). Confirms, deletes, and
  // refreshes the list in place.
  async function deleteTeam(team) {
    if (!confirm(`Delete team “${team.name}”? This cannot be undone.`)) {
      return;
    }
    try {
      await api.deleteTeam(team.id);
      showMessage("Team deleted", "success");
      loadTeams();
    } catch (err) {
      handleError(err);
    }
  }

  // --- Groupings ---
  // A grouping splits the roster into a set of numbered groups (e.g. ice cages
  // or slayer stacks). Each grouping is edited locally and autosaved on its own
  // debounce; structural edits (add/remove player, change group count) re-render
  // while text edits (names) save without re-rendering to preserve focus.

  // The display name for a player slot: their roster name, else "Slot N".
  function playerSlotLabel(slot) {
    const p = (currentTeam.players || []).find((x) => x.slot === slot);
    const name = p && p.name ? p.name.trim() : "";
    return name || `Slot ${slot}`;
  }

  // Slots already assigned to some group within this grouping (a player may be
  // in only one group per grouping).
  function assignedSlots(grouping) {
    const set = new Set();
    grouping.groups.forEach((g) => g.slots.forEach((s) => set.add(s)));
    return set;
  }

  // Resize a grouping's group list to `count`, preserving existing groups and
  // dropping any beyond the new count (their members become unassigned).
  function setGroupCount(grouping, count) {
    count = Math.max(1, Math.min(MAX_GROUPS_PER_GROUPING, count));
    const groups = [];
    for (let n = 1; n <= count; n++) {
      const existing = grouping.groups.find((g) => g.group_number === n);
      groups.push(existing || { group_number: n, name: "", slots: [] });
    }
    grouping.group_count = count;
    grouping.groups = groups;
  }

  function setGroupingSaveStatus(groupingId, state) {
    const node = el(`grouping-status-${groupingId}`);
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

  function scheduleGroupingSave(groupingId) {
    if (!canEdit()) return;
    setGroupingSaveStatus(groupingId, "saving");
    clearTimeout(groupingSaveTimers[groupingId]);
    groupingSaveTimers[groupingId] = setTimeout(
      () => saveGroupingNow(groupingId),
      GROUPING_SAVE_DELAY
    );
  }

  async function saveGroupingNow(groupingId) {
    clearTimeout(groupingSaveTimers[groupingId]);
    delete groupingSaveTimers[groupingId];
    const grouping = currentGroupings.find((g) => g.id === groupingId);
    if (!grouping || !currentTeam || !canEdit()) return;
    const payload = {
      name: (grouping.name || "").trim() || "Grouping",
      group_count: grouping.group_count,
      groups: grouping.groups.map((g) => ({
        group_number: g.group_number,
        name: (g.name || "").trim(),
        slots: g.slots.slice(),
      })),
    };
    setGroupingSaveStatus(groupingId, "saving");
    try {
      const updated = await api.saveGrouping(currentTeam.id, groupingId, payload);
      // Reconcile server-canonical state, but not while the user is still
      // interacting here: swapping the object would orphan the live DOM
      // closures (losing an in-flight edit) and could close an open dropdown.
      // Local state already mirrors what we just sent, so it's safe to skip.
      const idx = currentGroupings.findIndex((g) => g.id === groupingId);
      if (idx !== -1 && !isGroupingsInteracting()) currentGroupings[idx] = updated;
      setGroupingSaveStatus(groupingId, "saved");
    } catch (err) {
      setGroupingSaveStatus(groupingId, "error");
      handleError(err);
    }
  }

  // True while the user is actively interacting with a control in the groupings
  // section (e.g. an open native <select> popup, which keeps focus on the
  // <select>). Re-rendering the groupings DOM then would detach that control and
  // close the popup, so save side-effects defer their refresh while this holds.
  function isGroupingsInteracting() {
    const card = el("groupings-card");
    return !!card && card.contains(document.activeElement);
  }

  function renderGroupings() {
    const list = el("groupings-list");
    if (!list) return;
    const editable = canEdit();
    el("add-grouping-btn").classList.toggle(
      "is-hidden",
      !editable || currentGroupings.length >= MAX_GROUPINGS
    );
    el("groupings-empty").classList.toggle("is-hidden", currentGroupings.length > 0);
    list.innerHTML = "";
    currentGroupings.forEach((grouping) => {
      list.appendChild(renderGroupingCard(grouping, editable));
    });
  }

  function renderGroupingCard(grouping, editable) {
    const card = document.createElement("div");
    card.className = "grouping";

    const head = document.createElement("div");
    head.className = "grouping-head";

    const nameInput = document.createElement("input");
    nameInput.className = "input grouping-name";
    nameInput.type = "text";
    nameInput.maxLength = 100;
    nameInput.placeholder = "Grouping name";
    nameInput.value = grouping.name || "";
    nameInput.readOnly = !editable;
    nameInput.addEventListener("input", () => {
      grouping.name = nameInput.value;
      scheduleGroupingSave(grouping.id);
    });
    head.appendChild(nameInput);

    const countField = document.createElement("label");
    countField.className = "grouping-count-field";
    countField.append("Groups");
    const countInput = document.createElement("input");
    countInput.className = "input grouping-count";
    countInput.type = "number";
    countInput.min = "1";
    countInput.max = String(MAX_GROUPS_PER_GROUPING);
    countInput.value = String(grouping.group_count);
    countInput.disabled = !editable;
    countInput.addEventListener("change", () => {
      const n = parseInt(countInput.value, 10);
      setGroupCount(grouping, Number.isNaN(n) ? grouping.group_count : n);
      renderGroupings();
      scheduleGroupingSave(grouping.id);
    });
    countField.appendChild(countInput);
    head.appendChild(countField);

    const actions = document.createElement("div");
    actions.className = "form-actions grouping-actions";
    const status = document.createElement("span");
    status.className = "save-status";
    status.id = `grouping-status-${grouping.id}`;
    actions.appendChild(status);
    if (editable) {
      const del = document.createElement("button");
      del.type = "button";
      del.className = "btn btn--ghost btn--sm btn--danger";
      del.textContent = "Delete";
      del.addEventListener("click", () => deleteGrouping(grouping));
      actions.appendChild(del);
    }
    head.appendChild(actions);
    card.appendChild(head);

    const groupsWrap = document.createElement("div");
    groupsWrap.className = "grouping-groups";
    const taken = assignedSlots(grouping);
    grouping.groups.forEach((group) => {
      groupsWrap.appendChild(renderGroup(grouping, group, taken, editable));
    });
    card.appendChild(groupsWrap);
    return card;
  }

  function renderGroup(grouping, group, taken, editable) {
    const box = document.createElement("div");
    box.className = "grouping-group";

    const nameInput = document.createElement("input");
    nameInput.className = "input grouping-group-name";
    nameInput.type = "text";
    nameInput.maxLength = 50;
    nameInput.placeholder = `Group ${group.group_number}`;
    nameInput.value = group.name || "";
    nameInput.readOnly = !editable;
    nameInput.addEventListener("input", () => {
      group.name = nameInput.value;
      scheduleGroupingSave(grouping.id);
    });
    box.appendChild(nameInput);

    const chips = document.createElement("div");
    chips.className = "chip-list";
    group.slots
      .slice()
      .sort((a, b) => a - b)
      .forEach((slot) => {
        const chip = document.createElement("span");
        chip.className = "chip";
        chip.append(playerSlotLabel(slot));
        if (editable) {
          const rm = document.createElement("button");
          rm.type = "button";
          rm.className = "chip-remove";
          rm.setAttribute("aria-label", `Remove ${playerSlotLabel(slot)}`);
          rm.textContent = "×";
          rm.addEventListener("click", () => {
            group.slots = group.slots.filter((s) => s !== slot);
            renderGroupings();
            scheduleGroupingSave(grouping.id);
          });
          chip.appendChild(rm);
        }
        chips.appendChild(chip);
      });
    box.appendChild(chips);

    if (editable) {
      // Only players not yet assigned anywhere in this grouping can be added.
      const available = (currentTeam.players || [])
        .map((p) => p.slot)
        .filter((slot) => !taken.has(slot))
        .sort((a, b) => a - b);
      const select = document.createElement("select");
      select.className = "input grouping-add-player";
      const placeholder = document.createElement("option");
      placeholder.value = "";
      placeholder.textContent = available.length ? "+ Add player…" : "All players assigned";
      select.appendChild(placeholder);
      available.forEach((slot) => {
        const opt = document.createElement("option");
        opt.value = String(slot);
        opt.textContent = `${slot}. ${playerSlotLabel(slot)}`;
        select.appendChild(opt);
      });
      select.disabled = available.length === 0;
      select.addEventListener("change", () => {
        const slot = parseInt(select.value, 10);
        if (Number.isNaN(slot)) return;
        group.slots.push(slot);
        renderGroupings();
        scheduleGroupingSave(grouping.id);
      });
      box.appendChild(select);
    }
    return box;
  }

  async function addGrouping() {
    if (!currentTeam || !canEdit()) return;
    if (currentGroupings.length >= MAX_GROUPINGS) {
      showMessage(`You can have at most ${MAX_GROUPINGS} groupings per team`);
      return;
    }
    try {
      const grouping = await api.createGrouping(
        currentTeam.id,
        `Grouping ${currentGroupings.length + 1}`,
        2
      );
      currentGroupings.push(grouping);
      expandAncestors(el("groupings-card"));
      renderGroupings();
    } catch (err) {
      handleError(err);
    }
  }

  async function deleteGrouping(grouping) {
    if (!confirm(`Delete grouping “${grouping.name || "Grouping"}”?`)) return;
    try {
      await api.deleteGrouping(currentTeam.id, grouping.id);
      currentGroupings = currentGroupings.filter((g) => g.id !== grouping.id);
      renderGroupings();
    } catch (err) {
      handleError(err);
    }
  }

  el("add-grouping-btn").addEventListener("click", addGrouping);

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
      slot.className = "player-slot collapsible";
      slot.dataset.slot = player.slot;
      // Drives the role-based background color (see .player-slot[data-role] CSS).
      slot.dataset.role = player.role;
      const copyControl = editable
        ? `<div class="slot-actions" data-copy>
            <select class="input slot-copy" data-copy-select aria-label="Copy another player's build and loadout into this slot">
              <option value="">Copy from…</option>
            </select>
            <button type="button" class="btn btn--ghost btn--sm slot-clear" data-clear-slot aria-label="Clear this player's build and loadout">Clear</button>
          </div>`
        : "";
      slot.innerHTML = `
        <div class="player-head collapsible-head">
          <button class="collapse-toggle" type="button" aria-expanded="true" aria-label="Collapse player"></button>
          <span class="slot-number">${player.slot}</span>
          <span class="player-head-name" data-slot-summary></span>
        </div>
        <div class="player-body collapsible-body">
          ${copyControl}
          <div class="player-fields">
            <div class="form-group">
              <label>Name</label>
              <input class="input" data-field="name" maxlength="100" />
            </div>
            <div class="form-group">
              <label>Discord handle</label>
              <div data-discord-combo></div>
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
              <div class="loadout-col" data-type="crit_dmg">
                <label>Crit Dmg sources</label>
                <div class="chip-list" data-list></div>
              </div>
              <div class="loadout-col" data-type="pen_extra">
                <label>Pen sources</label>
                <div class="chip-list" data-list></div>
              </div>
              <div class="loadout-col is-hidden" data-type="scribed_buffs" data-scribed-col>
                <label>Scribed buffs</label>
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
              <div class="crit-field crit-catalyst is-hidden" data-catalyst-field>
                <label>Catalyst dmg types</label>
                <select class="input" data-crit-field="catalyst_elements" aria-label="Elemental Catalyst damage types applied">
                  <option value="3">3 — Flame/Frost/Shock (15%)</option>
                  <option value="2">2 elements (10%)</option>
                  <option value="1">1 element (5%)</option>
                </select>
              </div>
              <div class="crit-field crit-weapon-dmg is-hidden" data-weapon-dmg-field>
                <label>Weapon damage</label>
                <input class="input" type="number" min="0" max="20000" step="1" data-crit-field="weapon_damage" aria-label="Higher of Weapon or Spell Damage (for Anthelmir's Construct penetration)" />
              </div>
              <div class="crit-field crit-splintered is-hidden" data-splintered-field>
                <label>Splintered skills</label>
                <select class="input" data-crit-field="splintered_secrets_skills" aria-label="Herald of the Tome abilities slotted for Splintered Secrets penetration">
                  <option value="5">5 skills (6200)</option>
                  <option value="4">4 skills (4960)</option>
                  <option value="3">3 skills (3720)</option>
                  <option value="2">2 skills (2480)</option>
                  <option value="1">1 skill (1240)</option>
                  <option value="0">0 skills (0)</option>
                </select>
              </div>
              <div class="crit-field crit-force-nature is-hidden" data-force-nature-field>
                <label>Status effects</label>
                <select class="input" data-crit-field="force_of_nature_status" aria-label="Negative status effects on the enemy for Force of Nature penetration">
                  <option value="5">5 effects (3300)</option>
                  <option value="4">4 effects (2640)</option>
                  <option value="3">3 effects (1980)</option>
                  <option value="2">2 effects (1320)</option>
                  <option value="1">1 effect (660)</option>
                  <option value="0">0 effects (0)</option>
                </select>
              </div>
              <div class="crit-field crit-banner is-hidden" data-banner-focus-field>
                <label>Banner focus</label>
                <select class="input" data-crit-field="banner_bearer_focus" aria-label="Banner Bearer focus script">
                  <option value="">—</option>
                  ${optionsHtml(BANNER_BEARER_FOCUS, "")}
                </select>
              </div>
              <div class="crit-results">
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
          </div>
        </div>`;

      slot.querySelector('[data-field="name"]').value = player.name;
      slot.querySelector('[data-field="subclassed"]').checked = player.subclassed;

      // Discord handle: an open combobox whose suggestions are the team's member
      // pool (free-form text still allowed). The inner input keeps data-field so
      // it participates in the roster autosave + collectPlayers() like before.
      const comboHost = slot.querySelector("[data-discord-combo]");
      const { root: comboRoot } = createComboBox({
        value: player.discord_handle || "",
        options: memberHandleOptions(),
        placeholder: "@handle or name",
        inputAttrs: { "data-field": "discord_handle", maxlength: "100" },
        // Picking a known member auto-fills the player's name, but only when the
        // name is still empty so an existing name is never overwritten.
        onChoose: (handle) => {
          if (!editable) return;
          const nameField = slot.querySelector('[data-field="name"]');
          if (!nameField || nameField.value.trim()) return;
          const member = currentRosterMembers.find(
            (m) => (m.discord_username || "").trim().toLowerCase() === handle.trim().toLowerCase()
          );
          const fill = member && (member.display_name || "").trim();
          if (!fill) return;
          nameField.value = fill;
          updateSlotSummary(slot);
          renderPlayerNav();
          scheduleAutosave("team");
        },
      });
      comboHost.replaceWith(comboRoot);

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
        updateSlotSummary(slot);
      });

      // Keep the collapsed-header summary (slot number, name, role) in sync as
      // the player's name is typed.
      const nameInput = slot.querySelector('[data-field="name"]');
      nameInput.addEventListener("input", () => updateSlotSummary(slot));
      updateSlotSummary(slot);

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

      // "Copy from…" pulls another player's build + loadout INTO this slot
      // (everything except name and discord handle). Options are rebuilt from
      // the live roster on open so slot names stay current.
      const copySel = slot.querySelector("[data-copy-select]");
      if (copySel) {
        copySel.addEventListener("mousedown", () =>
          populateCopyOptions(copySel, Number(slot.dataset.slot))
        );
        copySel.addEventListener("focus", () =>
          populateCopyOptions(copySel, Number(slot.dataset.slot))
        );
        copySel.addEventListener("change", () => {
          const source = Number(copySel.value);
          copySel.value = "";
          if (!source) return;
          const src = el("roster").querySelector(`.player-slot[data-slot="${source}"]`);
          if (src) copyPlayerToSlot(src, slot);
        });
      }

      // "Clear" wipes this player's class/race/build, loadout, and crit/pen
      // setup for the current encounter. Name, Discord handle, and role are kept.
      const clearBtn = slot.querySelector("[data-clear-slot]");
      if (clearBtn) {
        clearBtn.addEventListener("click", () => {
          const nameEl = slot.querySelector('[data-field="name"]');
          const who =
            nameEl && nameEl.value.trim()
              ? nameEl.value.trim()
              : `Slot ${slot.dataset.slot}`;
          if (!confirm(`Clear ${who}'s name, handle, build, and loadout? Only the role is kept.`))
            return;
          clearPlayerSlot(slot);
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
    renderPlayerNav();
  }

  // Fill a player slot's collapsed-header summary with its number, current name
  // (falling back to "Slot N"), and role label so it stays useful when the slot
  // is collapsed.
  function updateSlotSummary(slot) {
    const target = slot.querySelector("[data-slot-summary]");
    if (!target) return;
    const num = slot.dataset.slot;
    const nameEl = slot.querySelector('[data-field="name"]');
    const roleEl = slot.querySelector('[data-field="role"]');
    const name = nameEl && nameEl.value.trim() ? nameEl.value.trim() : `Slot ${num}`;
    const role = roleEl ? labelFor(ROLES, roleEl.value) : "";
    target.textContent = role ? `${num}. ${name} — ${role}` : `${num}. ${name}`;
  }

  // Build the desktop floating jump-nav list from the live roster (name + role),
  // so it reflects unsaved name/role edits. The Top/Group Buffs anchors are
  // static in the markup; only the per-player list is regenerated here.
  function renderPlayerNav() {
    const list = el("player-nav-list");
    if (!list) return;
    list.innerHTML = "";
    el("roster")
      .querySelectorAll(".player-slot")
      .forEach((slot) => {
        const num = Number(slot.dataset.slot);
        const nameEl = slot.querySelector('[data-field="name"]');
        const roleEl = slot.querySelector('[data-field="role"]');
        const role = roleEl ? roleEl.value : "";
        const name = nameEl && nameEl.value.trim() ? nameEl.value.trim() : `Slot ${num}`;
        const link = document.createElement("a");
        link.className = "player-nav-link";
        link.href = "#";
        link.dataset.nav = "player";
        link.dataset.slot = String(num);
        link.dataset.role = role;
        link.innerHTML = `<span class="player-nav-name">${num}. ${escapeAttr(
          name
        )}</span><span class="player-nav-role">${escapeAttr(labelFor(ROLES, role))}</span>`;
        list.appendChild(link);
      });
  }

  // Rebuild a slot's "Copy from…" options from the live roster (every slot except
  // its own), labelled with the current name when present.
  function populateCopyOptions(selectEl, ownSlot) {
    const opts = ['<option value="">Copy from…</option>'];
    el("roster")
      .querySelectorAll(".player-slot")
      .forEach((s) => {
        const num = Number(s.dataset.slot);
        if (num === ownSlot) return;
        const nameEl = s.querySelector('[data-field="name"]');
        const name = nameEl && nameEl.value.trim() ? ` — ${nameEl.value.trim()}` : "";
        opts.push(`<option value="${num}">Slot ${num}${escapeAttr(name)}</option>`);
      });
    selectEl.innerHTML = opts.join("");
    selectEl.value = "";
  }

  // Copy everything from one roster slot to another EXCEPT name + discord handle:
  // role/class/race/subclass + the active build (skill lines or masteries) and
  // the full per-encounter loadout (gear/skills/potions/CP/crit dmg/pen sources,
  // mundus, armor counts). Operates on the live DOM (so unsaved edits are
  // included), then persists both the team and the encounter.
  function copyPlayerToSlot(srcSlot, dstSlot) {
    const field = (slotEl, f) => {
      const e = slotEl.querySelector(`[data-field="${f}"]`);
      return e ? e.value : "";
    };

    ["role", "class", "race"].forEach((f) => {
      const s = srcSlot.querySelector(`[data-field="${f}"]`);
      const d = dstSlot.querySelector(`[data-field="${f}"]`);
      if (s && d) d.value = s.value;
    });
    dstSlot.dataset.role = field(dstSlot, "role");

    const srcSub = srcSlot.querySelector('[data-field="subclassed"]');
    const dstSub = dstSlot.querySelector('[data-field="subclassed"]');
    if (srcSub && dstSub) dstSub.checked = srcSub.checked;

    // Re-render the target's conditional build with the source's selections.
    renderBuild(dstSlot, {
      skill_line_1: field(srcSlot, "skill_line_1"),
      skill_line_2: field(srcSlot, "skill_line_2"),
      skill_line_3: field(srcSlot, "skill_line_3"),
      mastery_1: field(srcSlot, "mastery_1"),
      mastery_2: field(srcSlot, "mastery_2"),
    });

    // Loadout chip columns: clear the target list and copy the source's chips.
    dstSlot.querySelectorAll("[data-loadout] .loadout-col").forEach((dstCol) => {
      const type = dstCol.dataset.type;
      const dstList = dstCol.querySelector("[data-list]");
      if (!dstList) return;
      dstList.innerHTML = "";
      const srcChips = srcSlot.querySelectorAll(
        `[data-loadout] .loadout-col[data-type="${type}"] .chip`
      );
      srcChips.forEach((chip) =>
        addChip(dstList, type, chip.dataset.value, true, Number(chip.dataset.count) || 1)
      );
    });

    // Crit/pen setup fields (mundus + armor counts + catalyst element count + weapon damage).
    ["mundus", "armor_heavy", "armor_medium", "armor_light", "catalyst_elements", "weapon_damage", "splintered_secrets_skills", "force_of_nature_status", "banner_bearer_focus"].forEach((f) => {
      const s = srcSlot.querySelector(`[data-crit-field="${f}"]`);
      const d = dstSlot.querySelector(`[data-crit-field="${f}"]`);
      if (s && d) d.value = s.value;
    });

    refreshBuffCoverage();
    refreshCritCoverage();
    refreshPenCoverage();

    // Programmatic value changes don't fire input/change, so persist explicitly.
    setSaveStatus("team", "saving");
    setSaveStatus("encounter", "saving");
    Promise.resolve(saveAll()).then(() => saveLoadouts());
  }

  // Reset a roster slot to an empty build: clears name, Discord handle,
  // class/race/subclass + build, the full per-encounter loadout
  // (gear/skills/potions/CP/crit dmg/pen sources), and the crit/pen setup
  // fields. Only the role is preserved (it has no empty value and defines the
  // slot). Operates on the live DOM, then persists both the team and encounter.
  function clearPlayerSlot(slot) {
    ["name", "discord_handle", "class", "race"].forEach((f) => {
      const d = slot.querySelector(`[data-field="${f}"]`);
      if (d) d.value = "";
    });

    const sub = slot.querySelector('[data-field="subclassed"]');
    if (sub) sub.checked = false;

    // Re-render the build area now that class is empty and subclass is off.
    renderBuild(slot, {});

    // Empty every loadout chip column.
    slot.querySelectorAll("[data-loadout] .loadout-col [data-list]").forEach((list) => {
      list.innerHTML = "";
    });

    // Reset crit/pen setup fields to their neutral defaults.
    const critEl = slot.querySelector("[data-crit]");
    if (critEl) {
      const mundus = critEl.querySelector('[data-crit-field="mundus"]');
      if (mundus) mundus.value = "";
      ["armor_heavy", "armor_medium", "armor_light", "weapon_damage"].forEach((f) => {
        const input = critEl.querySelector(`[data-crit-field="${f}"]`);
        if (input) input.value = 0;
      });
      ["catalyst_elements", "splintered_secrets_skills", "force_of_nature_status"].forEach((f) => {
        const selEl = critEl.querySelector(`[data-crit-field="${f}"]`);
        if (selEl) selEl.value = "0";
      });
      const bannerSel = critEl.querySelector('[data-crit-field="banner_bearer_focus"]');
      if (bannerSel) bannerSel.value = "";
    }

    refreshBuffCoverage();
    refreshCritCoverage();
    refreshPenCoverage();
    updateSlotSummary(slot);
    renderPlayerNav();

    // Programmatic value changes don't fire input/change, so persist explicitly.
    setSaveStatus("team", "saving");
    setSaveStatus("encounter", "saving");
    Promise.resolve(saveAll()).then(() => saveLoadouts());
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
        const catSel = critEl.querySelector('[data-crit-field="catalyst_elements"]');
        if (catSel) catSel.value = String(clampCatalystElements(lo.catalyst_elements));
        const wdInput = critEl.querySelector('[data-crit-field="weapon_damage"]');
        if (wdInput) wdInput.value = Number(lo.weapon_damage) || 0;
        const ssSel = critEl.querySelector('[data-crit-field="splintered_secrets_skills"]');
        if (ssSel) ssSel.value = String(clampSplinteredSecretsSkills(lo.splintered_secrets_skills));
        const fonSel = critEl.querySelector('[data-crit-field="force_of_nature_status"]');
        if (fonSel) fonSel.value = String(clampForceOfNatureStatus(lo.force_of_nature_status));
        const bbSel = critEl.querySelector('[data-crit-field="banner_bearer_focus"]');
        if (bbSel) bbSel.value = lo.banner_bearer_focus || "";
      }
    });

    updateScribedColumns();
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

  // Whether the open team has the encounters feature enabled. Disabled by
  // default: only an explicit true enables the encounters section.
  function encountersEnabled() {
    return !!currentTeam && currentTeam.encounters_enabled === true;
  }

  // Show or hide the entire encounters section based on the team's toggle. When
  // disabled, the encounters management card and the encounter chip switcher are
  // hidden and the roster shows only the first encounter. The toggle lives in the
  // main team info panel, which stays visible so an editor can re-enable it. Run
  // after the bar and controls render so it has the final say on visibility.
  function applyEncountersMode() {
    const enabled = encountersEnabled();
    const editable = canEdit();

    const toggle = el("encounters-enabled-toggle");
    if (toggle) {
      toggle.checked = enabled;
      toggle.disabled = !editable;
    }

    el("encounters-manage-card").classList.toggle("is-hidden", !enabled);
    el("encounters-panel").classList.toggle("is-hidden", !enabled);
    el("encounters-sentinel").classList.toggle("is-hidden", !enabled);
  }

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
    expandAncestors(addEncounterForm);
    addEncounterForm.classList.remove("is-hidden");
  });
  el("add-encounter-cancel").addEventListener("click", () => {
    addEncounterForm.classList.add("is-hidden");
  });

  // Toggle whether the team uses multiple encounters. Turning it off hides the
  // encounters section and snaps the roster back to the first encounter; turning
  // it on restores the switcher/management controls. The generic detail-view
  // change handler persists the new flag via the team autosave.
  el("encounters-enabled-toggle").addEventListener("change", async (e) => {
    if (!canEdit()) {
      e.target.checked = encountersEnabled();
      return;
    }
    const enabled = e.target.checked;
    currentTeam.encounters_enabled = enabled;
    // When disabling, ensure the single shown encounter is the first one.
    if (
      !enabled &&
      currentEncounters.length &&
      currentEncounter &&
      currentEncounter.id !== currentEncounters[0].id
    ) {
      await selectEncounter(currentEncounters[0].id);
    }
    renderEncountersBar();
    renderEncounterControls();
    applyEncountersMode();
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

  // Maximum number of times a loadout item may be stacked in one list. Only
  // certain pen sources (set-piece bonuses) are stackable; everything else is 1.
  function chipMaxStack(type, key) {
    if (type === "pen_extra") return penExtraMaxStack(key);
    return 1;
  }

  // Sync a chip's visible label and tooltip with its current stack count,
  // appending "×N" once it stacks beyond one.
  function updateChipCountLabel(chip, type) {
    const cfg = LOADOUT_TYPES[type];
    const key = chip.dataset.value;
    const count = Math.max(1, Number(chip.dataset.count) || 1);
    const labelEl = chip.querySelector(".chip-label");
    if (labelEl) labelEl.textContent = count > 1 ? `${cfg.label(key)} ×${count}` : cfg.label(key);
  }

  // Create a removable chip for one loadout item (stackable items pass a count).
  function addChip(listEl, type, key, editable, count) {
    if (!key) return;
    const maxStack = chipMaxStack(type, key);
    const existing = listEl.querySelector(`.chip[data-value="${escapeAttr(key)}"]`);
    // Duplicates either increment a stackable chip (up to its cap) or are ignored.
    if (existing) {
      if (maxStack > 1) {
        const cur = Math.max(1, Number(existing.dataset.count) || 1);
        const next = Math.min(maxStack, cur + Math.max(1, count || 1));
        existing.dataset.count = String(next);
        updateChipCountLabel(existing, type);
      }
      return;
    }

    const cfg = LOADOUT_TYPES[type];
    const chip = document.createElement("span");
    chip.className = "chip";
    chip.dataset.value = key;
    chip.dataset.count = String(Math.min(maxStack, Math.max(1, count || 1)));
    // Show the gear set description on hover (same floating tooltip the picker
    // options use; see initTooltips in components.js).
    const desc = cfg.desc(key);
    if (desc) chip.dataset.tip = desc;
    chip.innerHTML = `<span class="chip-label"></span>`;
    updateChipCountLabel(chip, type);
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
      // Stackable chips (e.g. set-piece pen bonuses) are persisted as their key
      // repeated once per stack, so the calculator can sum them.
      const read = (type) =>
        Array.from(
          slot
            .querySelector(`[data-loadout] .loadout-col[data-type="${type}"] .chip-list`)
            .querySelectorAll(".chip")
        ).flatMap((c) => {
          const count = Math.max(1, Number(c.dataset.count) || 1);
          return Array.from({ length: count }, () => c.dataset.value);
        });
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
        crit_dmg: read("crit_dmg"),
        pen_extra: read("pen_extra"),
        scribed_buffs: read("scribed_buffs"),
        mundus: critVal("mundus"),
        armor_heavy: armor("armor_heavy"),
        armor_medium: armor("armor_medium"),
        armor_light: armor("armor_light"),
        catalyst_elements: clampCatalystElements(critVal("catalyst_elements")),
        weapon_damage: (() => {
          const v = parseInt(critVal("weapon_damage"), 10);
          if (!Number.isFinite(v)) return 0;
          return Math.max(0, Math.min(20000, v));
        })(),
        splintered_secrets_skills: clampSplinteredSecretsSkills(critVal("splintered_secrets_skills")),
        force_of_nature_status: clampForceOfNatureStatus(critVal("force_of_nature_status")),
        banner_bearer_focus: critVal("banner_bearer_focus"),
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

  // Show each slot's "Scribed buffs" column only when that player has a scribed
  // (grimoire) skill slotted; hide it otherwise. Selections are left in the DOM
  // when hidden so toggling the grimoire back restores them (coverage ignores
  // them while no grimoire is slotted; see playerBuffContributions).
  function updateScribedColumns() {
    el("roster")
      .querySelectorAll(".player-slot")
      .forEach((slot) => {
        const col = slot.querySelector("[data-scribed-col]");
        if (!col) return;
        const chips = slot.querySelectorAll(
          '[data-loadout] .loadout-col[data-type="skills"] .chip'
        );
        const hasGrimoire = Array.from(chips).some((c) =>
          isGrimoireSkill(c.dataset.value)
        );
        col.classList.toggle("is-hidden", !hasGrimoire);
      });
  }

  // Recompute and repaint the summary card (count + pip bar). Cheap; safe to
  // call on every roster/loadout change. Keeps an open modal in sync.
  function refreshBuffCoverage() {
    const countEl = el("buffs-count");
    if (!countEl || !currentTeam || detailView.classList.contains("is-hidden")) return;
    if (!el("roster").querySelector(".player-slot")) return;

    updateScribedColumns();

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
  // selected encounter's gear/skills/CP/crit dmg/mundus/armor) so it survives
  // autosaves. The card shows group/target/solo-required; each roster slot gets a
  // crit-damage label + met/unmet indicator against the cap.
  let lastCritCoverage = null;

  function refreshCritCoverage() {
    const groupEl = el("crit-group");
    if (!groupEl || !currentTeam || detailView.classList.contains("is-hidden")) return;
    if (!el("roster").querySelector(".player-slot")) return;

    const cov = computeCritCoverage(collectPlayers(), currentLoadoutBySlot());
    lastCritCoverage = cov;

    el("crit-cap").textContent = `${cov.cap}%`;
    el("crit-group").textContent = `${cov.group}%`;
    el("crit-required").textContent = `${cov.soloRequired}%`;

    const bySlot = {};
    cov.players.forEach((p) => {
      bySlot[p.slot] = p;
    });
    el("roster").querySelectorAll(".player-slot").forEach((slot) => {
      // The catalyst element selector only matters when Elemental Catalyst is
      // equipped; show it only then so it doesn't clutter every slot.
      const catField = slot.querySelector("[data-catalyst-field]");
      if (catField) {
        const hasCatalyst = !!slot.querySelector(
          '[data-loadout] .loadout-col[data-type="gear"] .chip[data-value="elemental_catalyst"]'
        );
        catField.classList.toggle("is-hidden", !hasCatalyst);
      }

      // The Banner focus dropdown only matters when the Banner Bearer grimoire is
      // slotted; show it only then. Informational (records the banner morph), so
      // it doesn't affect crit/pen — it just lives in this conditional setup row.
      const bannerField = slot.querySelector("[data-banner-focus-field]");
      if (bannerField) {
        const hasBannerBearer = !!slot.querySelector(
          `[data-loadout] .loadout-col[data-type="skills"] .chip[data-value="${BANNER_BEARER_SKILL}"]`
        );
        bannerField.classList.toggle("is-hidden", !hasBannerBearer);
      }

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
      const breakdown = `self ${r.self}% + group ${cov.group}%`;
      const cap = r.cap != null ? r.cap : cov.cap;
      label.dataset.tip = r.met
        ? `Meets the ${cap}% cap (${breakdown}).`
        : `${r.deficit}% under the ${cap}% cap (${breakdown}).`;
    });

    if (!el("crit-modal").classList.contains("is-hidden")) renderCritModal();
  }

  // Render the group source breakdown + per-player breakdown into the crit
  // details modal.
  function renderCritModal() {
    const cov =
      lastCritCoverage || computeCritCoverage(collectPlayers(), currentLoadoutBySlot());
    el("crit-modal-sub").textContent =
      `Cap ${cov.cap}% · Group ${cov.group}% · Each player needs ${cov.soloRequired}% of their own` +
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
      const capNote = p.cap != null && p.cap !== cov.cap ? ` / ${p.cap}% cap` : "";
      row.innerHTML = `
        <div class="buff-row-head">
          <span class="buff-status" aria-hidden="true">${p.met ? "✓" : "✗"}</span>
          <span class="buff-name">P${p.slot} — ${p.total}%${capNote}${p.met ? "" : ` (−${p.deficit}%)`}</span>
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

  // Whether a roster slot has the Arcanist Herald of the Tome skill line: a
  // subclassed player with it slotted, or a pure Arcanist. Mirrors the
  // splintered_secrets pen source detection (see playerSelfPen in data.js).
  function slotHasHeraldOfTome(slot) {
    const sub = slot.querySelector('[data-field="subclassed"]');
    if (sub && sub.checked) {
      return [1, 2, 3].some((n) => {
        const s = slot.querySelector(`[data-field="skill_line_${n}"]`);
        return s && s.value === "herald_of_the_tome";
      });
    }
    const cls = slot.querySelector('[data-field="class"]');
    return !!cls && cls.value === "arcanist";
  }

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
      // The weapon-damage input only matters for Anthelmir's Construct's pen
      // scaling; show it only when that set is equipped.
      const wdField = slot.querySelector("[data-weapon-dmg-field]");
      if (wdField) {
        const hasAnthelmir = !!slot.querySelector(
          '[data-loadout] .loadout-col[data-type="gear"] .chip[data-value="anthelmirs_construct"]'
        );
        wdField.classList.toggle("is-hidden", !hasAnthelmir);
      }

      // The Splintered Secrets skill count only matters when the player has the
      // Herald of the Tome skill line (subclassed) or is an Arcanist; show it
      // only then, mirroring the splintered_secrets pen source's detection.
      const ssField = slot.querySelector("[data-splintered-field]");
      if (ssField) {
        ssField.classList.toggle("is-hidden", !slotHasHeraldOfTome(slot));
      }

      // The Force of Nature status-effect count only matters when that Warfare
      // CP star is slotted; show it only when the cp_blue chip is present.
      const fonField = slot.querySelector("[data-force-nature-field]");
      if (fonField) {
        const hasForceOfNature = !!slot.querySelector(
          '[data-loadout] .loadout-col[data-type="cp_blue"] .chip[data-value="force_of_nature"]'
        );
        fonField.classList.toggle("is-hidden", !hasForceOfNature);
      }

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

  // --- Admin: user management (admins only) ---
  // The topbar "Manage Users" button (shown only to admins) opens a modal to
  // list/add/remove users, toggle admin, and enable/disable self-registration.
  function showAdminButton() {
    el("manage-users-btn").classList.toggle(
      "is-hidden",
      !(currentUser && currentUser.is_admin)
    );
  }

  async function openAdminModal() {
    el("admin-modal").classList.remove("is-hidden");
    try {
      const s = await api.getAdminSettings();
      el("admin-registration-toggle").checked = !!s.registration_enabled;
    } catch (err) {
      handleError(err);
    }
    renderAdminUsers();
  }

  function closeAdminModal() {
    el("admin-modal").classList.add("is-hidden");
  }

  async function renderAdminUsers() {
    const list = el("admin-users-list");
    list.innerHTML = '<p class="text-muted">Loading…</p>';
    let users = [];
    try {
      const data = await api.listUsers();
      users = data.users || [];
    } catch (err) {
      list.innerHTML = "";
      handleError(err);
      return;
    }
    list.innerHTML = "";
    users.forEach((u) => {
      const isSelf = currentUser && u.id === currentUser.id;
      const row = document.createElement("div");
      row.className = "admin-user-row";
      row.innerHTML = `
        <div class="admin-user-main">
          <span class="admin-user-name">${escapeAttr(u.username)}${
        isSelf ? " (you)" : ""
      }</span>
          <span class="admin-user-email text-muted">${escapeAttr(u.email)}</span>
        </div>
        <label class="toggle admin-user-admin">
          <input type="checkbox" data-admin-toggle ${u.is_admin ? "checked" : ""} /> Admin
        </label>
        <button class="btn btn--danger btn--sm" type="button" data-admin-delete ${
          isSelf ? "disabled" : ""
        }>Remove</button>`;

      const adminCb = row.querySelector("[data-admin-toggle]");
      adminCb.addEventListener("change", async () => {
        try {
          await api.setUserAdmin(u.id, adminCb.checked);
          showMessage(`Updated ${u.username}`, "success");
          if (isSelf) {
            currentUser.is_admin = adminCb.checked;
            showAdminButton();
          }
          renderAdminUsers();
        } catch (err) {
          adminCb.checked = !adminCb.checked;
          handleError(err);
        }
      });

      const delBtn = row.querySelector("[data-admin-delete]");
      if (delBtn && !isSelf) {
        delBtn.addEventListener("click", async () => {
          if (
            !confirm(
              `Remove user “${u.username}”? This deletes their account and any teams they own.`
            )
          ) {
            return;
          }
          try {
            await api.deleteUser(u.id);
            showMessage(`Removed ${u.username}`, "success");
            renderAdminUsers();
          } catch (err) {
            handleError(err);
          }
        });
      }
      list.appendChild(row);
    });
  }

  el("manage-users-btn").addEventListener("click", openAdminModal);
  el("admin-modal-close").addEventListener("click", closeAdminModal);
  el("admin-modal").addEventListener("click", (e) => {
    if (e.target === el("admin-modal")) closeAdminModal();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !el("admin-modal").classList.contains("is-hidden")) {
      closeAdminModal();
    }
  });

  // --- Discord account linking ---
  // The topbar "Link Discord" button opens a modal that shows the current link
  // status and lets the user generate a one-time code to type into Discord via
  // /coreteam link (matching the bot's account-link flow).
  async function openDiscordModal() {
    el("discord-modal").classList.remove("is-hidden");
    el("discord-code").textContent = "—";
    el("discord-command-hint").textContent = "";
    await refreshDiscordStatus();
  }

  function closeDiscordModal() {
    el("discord-modal").classList.add("is-hidden");
  }

  async function refreshDiscordStatus() {
    const statusEl = el("discord-status");
    statusEl.textContent = "Checking your link status…";
    try {
      const link = await api.discordLink();
      if (link && link.linked) {
        el("discord-link-section").classList.add("is-hidden");
        el("discord-linked-section").classList.remove("is-hidden");
        el("discord-linked-name").textContent = link.discord_username || "your Discord account";
        statusEl.textContent = "Your account is linked to Discord.";
      } else {
        el("discord-link-section").classList.remove("is-hidden");
        el("discord-linked-section").classList.add("is-hidden");
        statusEl.textContent = "Not linked yet.";
      }
    } catch (err) {
      statusEl.textContent = "";
      handleError(err);
    }
  }

  el("link-discord-btn").addEventListener("click", openDiscordModal);
  el("discord-modal-close").addEventListener("click", closeDiscordModal);
  el("discord-modal").addEventListener("click", (e) => {
    if (e.target === el("discord-modal")) closeDiscordModal();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !el("discord-modal").classList.contains("is-hidden")) {
      closeDiscordModal();
    }
  });

  el("discord-generate-btn").addEventListener("click", async () => {
    try {
      const res = await api.discordLinkCode();
      el("discord-code").textContent = res.code;
      el("discord-command-hint").textContent =
        `Run ${res.command} in your Discord server. The code expires shortly.`;
    } catch (err) {
      handleError(err);
    }
  });

  el("discord-unlink-btn").addEventListener("click", async () => {
    if (!confirm("Unlink your Discord account?")) return;
    try {
      await api.discordUnlink();
      showMessage("Discord account unlinked", "success");
      await refreshDiscordStatus();
    } catch (err) {
      handleError(err);
    }
  });

  el("admin-registration-toggle").addEventListener("change", async (e) => {
    try {
      await api.setRegistrationEnabled(e.target.checked);
      showMessage(
        `Self-registration ${e.target.checked ? "enabled" : "disabled"}`,
        "success"
      );
    } catch (err) {
      e.target.checked = !e.target.checked;
      handleError(err);
    }
  });

  el("admin-add-user-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const username = el("admin-new-username").value.trim();
    const email = el("admin-new-email").value.trim();
    const password = el("admin-new-password").value;
    const isAdmin = el("admin-new-admin").checked;
    if (!username || !email || !password) return;
    try {
      await api.createUser(username, email, password, isAdmin);
      el("admin-add-user-form").reset();
      showMessage(`Created ${username}`, "success");
      renderAdminUsers();
    } catch (err) {
      handleError(err);
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
      showAdminButton();
      await loadTeams();
    } catch (err) {
      handleError(err);
    }
  }

  setupEncounterStickiness();
  init();
})();
