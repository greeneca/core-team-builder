/*
 * components.js — reusable, framework-free UI components.
 *
 * createSearchableSelect: a dropdown that combines full free-text search with
 * optional group headers (an optgroup-style experience that native <select> /
 * <datalist> cannot provide together). Used for the encounter loadout pickers
 * (skills grouped by skill line; gear as a single headerless group).
 */

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
        if (it.desc) opt.title = it.desc;

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
