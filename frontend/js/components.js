/*
 * components.js — reusable, framework-free UI components.
 *
 * createSearchableSelect: a dropdown that combines full free-text search with
 * optional group headers (an optgroup-style experience that native <select> /
 * <datalist> cannot provide together). Used for the encounter loadout pickers
 * (skills grouped by skill line; gear as a single headerless group).
 *
 * Tooltips: any element with a non-empty `data-tip` attribute shows a floating
 * tooltip on hover/focus (see initTooltips). This replaces the native `title`
 * attribute, which is unreliable (slow, can't be styled, and inconsistent on
 * small elements like chips). Used for gear set descriptions on both the picker
 * options and the selected gear chips.
 */

// Lightweight, app-wide hover/focus tooltip. A single tooltip element is reused
// and appended to <body> so it is never clipped by overflow or stacking
// contexts (unlike a CSS ::after tooltip). Driven by delegated events, so it
// works for elements added dynamically after load (e.g. loadout chips).
//
// Tooltips can be turned off via setTooltipsEnabled(false); the preference is
// persisted in localStorage so it survives reloads.

const TOOLTIPS_PREF_KEY = "ctb_tooltips_disabled";
let tooltipsOn = localStorage.getItem(TOOLTIPS_PREF_KEY) !== "1";
let tooltipEl = null;
let tooltipTarget = null;

// tooltipsEnabled() -> boolean: whether tooltips are currently shown.
function tooltipsEnabled() {
  return tooltipsOn;
}

// setTooltipsEnabled(on): enable/disable tooltips app-wide and persist the
// choice. Disabling also hides any tooltip currently on screen.
function setTooltipsEnabled(on) {
  tooltipsOn = !!on;
  if (tooltipsOn) {
    localStorage.removeItem(TOOLTIPS_PREF_KEY);
  } else {
    localStorage.setItem(TOOLTIPS_PREF_KEY, "1");
    hideTooltip();
  }
}

function hideTooltip() {
  tooltipTarget = null;
  if (tooltipEl) tooltipEl.classList.add("is-hidden");
}

function initTooltips() {
  function ensureEl() {
    if (!tooltipEl) {
      tooltipEl = document.createElement("div");
      tooltipEl.className = "tooltip is-hidden";
      tooltipEl.setAttribute("role", "tooltip");
      document.body.appendChild(tooltipEl);
    }
    return tooltipEl;
  }

  function position(target) {
    const el = ensureEl();
    const r = target.getBoundingClientRect();
    const tr = el.getBoundingClientRect();
    const margin = 8;
    let left = r.left + r.width / 2 - tr.width / 2;
    left = Math.max(margin, Math.min(left, window.innerWidth - tr.width - margin));
    // Prefer above the target; flip below when there is no room.
    let top = r.top - tr.height - margin;
    if (top < margin) top = r.bottom + margin;
    el.style.left = `${left}px`;
    el.style.top = `${top}px`;
  }

  function show(target) {
    if (!tooltipsOn) return;
    const text = target.getAttribute("data-tip");
    if (!text) return;
    const el = ensureEl();
    el.textContent = text;
    el.classList.remove("is-hidden");
    tooltipTarget = target;
    position(target);
  }

  document.addEventListener("mouseover", (e) => {
    const t = e.target.closest ? e.target.closest("[data-tip]") : null;
    if (t) {
      if (t !== tooltipTarget) show(t);
    } else if (tooltipTarget) {
      hideTooltip();
    }
  });
  document.addEventListener("focusin", (e) => {
    const t = e.target.closest ? e.target.closest("[data-tip]") : null;
    if (t) show(t);
  });
  document.addEventListener("focusout", hideTooltip);
  // Reposition is cheap to skip — just hide while the page moves under it.
  window.addEventListener("scroll", hideTooltip, true);
}

initTooltips();

// createSearchableSelect({ groups, placeholder, onSelect }) -> HTMLElement
//
//   groups:      [{ group: string|null, items: [{ value, label, desc? }] }]
//                A null/empty `group` renders its items without a header.
//   placeholder: input placeholder text.
//   onSelect:    (value) => void, called when the user picks an item.
//
// Returns the root element to insert into the DOM.
function createSearchableSelect({ groups, placeholder, onSelect }) {
  const root = document.createElement("div");
  root.className = "ss";

  const input = document.createElement("input");
  input.className = "input input--sm ss-input";
  input.type = "text";
  input.placeholder = placeholder || "Search…";
  input.autocomplete = "off";
  input.setAttribute("role", "combobox");
  input.setAttribute("aria-autocomplete", "list");
  input.setAttribute("aria-expanded", "false");

  const panel = document.createElement("div");
  panel.className = "ss-panel is-hidden";
  panel.setAttribute("role", "listbox");

  root.appendChild(input);
  root.appendChild(panel);

  // Options currently visible (after filtering), for keyboard navigation.
  let currentOptions = [];
  let activeIndex = -1;

  function isOpen() {
    return !panel.classList.contains("is-hidden");
  }

  function open() {
    if (!isOpen()) {
      render();
      panel.classList.remove("is-hidden");
      input.setAttribute("aria-expanded", "true");
    }
  }

  function close() {
    panel.classList.add("is-hidden");
    input.setAttribute("aria-expanded", "false");
    activeIndex = -1;
  }

  function choose(value) {
    onSelect(value);
    input.value = "";
    close();
  }

  function setActive(i) {
    activeIndex = i;
    highlight();
  }

  function highlight() {
    currentOptions.forEach((o, i) => o.el.classList.toggle("is-active", i === activeIndex));
    const active = currentOptions[activeIndex];
    if (active) active.el.scrollIntoView({ block: "nearest" });
  }

  // Rebuild the panel filtered by the current query.
  function render() {
    const q = input.value.trim().toLowerCase();
    panel.innerHTML = "";
    currentOptions = [];

    groups.forEach((grp) => {
      const matches = (grp.items || []).filter((it) =>
        it.label.toLowerCase().includes(q)
      );
      if (matches.length === 0) return;

      if (grp.group) {
        const header = document.createElement("div");
        header.className = "ss-group";
        header.textContent = grp.group;
        panel.appendChild(header);
      }

      matches.forEach((it) => {
        const opt = document.createElement("div");
        opt.className = "ss-option";
        opt.setAttribute("role", "option");
        opt.dataset.value = it.value;
        opt.textContent = it.label;
        if (it.desc) opt.dataset.tip = it.desc;

        const idx = currentOptions.length;
        // mousedown (not click) fires before the input loses focus.
        opt.addEventListener("mousedown", (e) => {
          e.preventDefault();
          choose(it.value);
        });
        opt.addEventListener("mousemove", () => setActive(idx));

        currentOptions.push({ value: it.value, el: opt });
        panel.appendChild(opt);
      });
    });

    if (currentOptions.length === 0) {
      const empty = document.createElement("div");
      empty.className = "ss-empty";
      empty.textContent = "No matches";
      panel.appendChild(empty);
    }

    activeIndex = currentOptions.length ? 0 : -1;
    highlight();
  }

  input.addEventListener("focus", open);
  input.addEventListener("input", () => {
    open();
    render();
  });

  input.addEventListener("keydown", (e) => {
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (!isOpen()) {
          open();
        } else if (currentOptions.length) {
          setActive((activeIndex + 1) % currentOptions.length);
        }
        break;
      case "ArrowUp":
        e.preventDefault();
        if (isOpen() && currentOptions.length) {
          setActive((activeIndex - 1 + currentOptions.length) % currentOptions.length);
        }
        break;
      case "Enter":
        if (isOpen() && currentOptions[activeIndex]) {
          e.preventDefault();
          choose(currentOptions[activeIndex].value);
        }
        break;
      case "Escape":
        if (isOpen()) {
          e.preventDefault();
          close();
        }
        break;
      default:
        break;
    }
  });

  // Close when focus leaves the component entirely.
  root.addEventListener("focusout", (e) => {
    if (!root.contains(e.relatedTarget)) close();
  });

  return root;
}
