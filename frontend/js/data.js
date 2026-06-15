/*
 * data.js — ESO reference data + display helpers shared across page scripts.
 *
 * This module holds all the domain/reference data the UI renders (and the small
 * presentation helpers that operate on it). Page scripts (e.g. app.js) consume
 * these globals; api.js stays a pure network client. Keys mirror the backend
 * allow-lists in internal/models (eso.go and encounter.go) — keep them in sync.
 *
 * The gear-set + skill (ability) catalogs (GEAR_SET_GROUPS / GEAR_SETS /
 * SKILL_GROUPS / SKILLS / GEAR_ABBREVIATIONS) were split out into gear-skills.js
 * for ease of updating; that file MUST load before this one. The lookups and
 * helpers here consume those globals.
 *
 * Contents:
 *   - Roles, classes, share roles, days (+ label/format helpers).
 *   - Timezone helpers.
 *   - Subclassing: skill lines and class masteries (+ option/lookup helpers).
 *   - Encounters SEED data: ENCOUNTER_NAME_GROUPS (valid names grouped by
 *     trial). Gear sets / skills live in gear-skills.js. Loadouts store the
 *     item `value` (key); labels/tooltips are looked up from these tables.
 */

// Escape a string for safe use inside an HTML attribute value.
function escapeAttr(s) {
  return String(s || "")
    .replace(/&/g, "&amp;")
    .replace(/"/g, "&quot;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
}

// --- Roles, classes, sharing, schedule ---

// Canonical role/class values shared with the backend, plus display labels.
// Every player always has a concrete role (the backend defaults new slots to
// tanks/healers/dps), so the picker intentionally omits an "unset" option.
const ROLES = [
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

// Playable ESO races. Keys mirror the backend allow-list (eso.go). Race feeds
// the crit calculator (only Khajiit's "Feline Ambush" gives crit damage today).
const RACES = [
  { value: "", label: "—" },
  { value: "altmer", label: "Altmer (High Elf)" },
  { value: "argonian", label: "Argonian" },
  { value: "bosmer", label: "Bosmer (Wood Elf)" },
  { value: "breton", label: "Breton" },
  { value: "dunmer", label: "Dunmer (Dark Elf)" },
  { value: "imperial", label: "Imperial" },
  { value: "khajiit", label: "Khajiit" },
  { value: "nord", label: "Nord" },
  { value: "orc", label: "Orc (Orsimer)" },
  { value: "redguard", label: "Redguard" },
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

// A curated set of popular IANA timezones — one representative zone per UTC
// offset — to keep the team-timezone picker short and unambiguous (rather than
// the browser's full ~400-entry list).
const POPULAR_TIMEZONES = [
  "Pacific/Honolulu", // UTC-10
  "America/Anchorage", // UTC-9
  "America/Los_Angeles", // UTC-8
  "America/Denver", // UTC-7
  "America/Chicago", // UTC-6
  "America/New_York", // UTC-5
  "America/Halifax", // UTC-4
  "America/Sao_Paulo", // UTC-3
  "Atlantic/Azores", // UTC-1
  "Europe/London", // UTC+0
  "Europe/Paris", // UTC+1
  "Europe/Athens", // UTC+2
  "Europe/Moscow", // UTC+3
  "Asia/Dubai", // UTC+4
  "Asia/Karachi", // UTC+5
  "Asia/Dhaka", // UTC+6
  "Asia/Bangkok", // UTC+7
  "Asia/Shanghai", // UTC+8
  "Asia/Tokyo", // UTC+9
  "Australia/Sydney", // UTC+10
  "Pacific/Auckland", // UTC+12
];

// The popular timezone list. (Kept as a function so callers stay unchanged.)
function timezoneList() {
  return POPULAR_TIMEZONES.slice();
}

// tzOffsetMinutes(timeZone, date): the zone's UTC offset in minutes at the
// given instant (positive = ahead of UTC). Derived by reading the wall-clock
// fields the zone shows for that instant via Intl.
function tzOffsetMinutes(timeZone, date) {
  try {
    const dtf = new Intl.DateTimeFormat("en-US", {
      timeZone,
      hourCycle: "h23",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
    const p = {};
    for (const part of dtf.formatToParts(date)) p[part.type] = part.value;
    const asUTC = Date.UTC(
      Number(p.year),
      Number(p.month) - 1,
      Number(p.day),
      Number(p.hour),
      Number(p.minute),
      Number(p.second)
    );
    return Math.round((asUTC - date.getTime()) / 60000);
  } catch {
    return 0;
  }
}

// convertWallTime(hhmm, fromZone, toZone): convert a recurring "HH:MM" wall
// time from one IANA zone to another, anchored on today's date for DST.
// Returns "HH:MM" (24h). Empty/invalid input or matching zones pass through.
// Note: recurring times have no real date, so conversions near a DST boundary
// can be off by an hour — acceptable for a weekly schedule display.
function convertWallTime(hhmm, fromZone, toZone) {
  if (!hhmm || !fromZone || !toZone || fromZone === toZone) return hhmm || "";
  const m = /^(\d{1,2}):(\d{2})$/.exec(hhmm);
  if (!m) return hhmm;
  const hour = Number(m[1]);
  const minute = Number(m[2]);
  try {
    // Anchor on today's date as seen in the source zone.
    const dp = {};
    for (const part of new Intl.DateTimeFormat("en-US", {
      timeZone: fromZone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    }).formatToParts(new Date())) {
      dp[part.type] = part.value;
    }
    // Find the UTC instant whose wall time in fromZone is the given HH:MM.
    const guess = Date.UTC(Number(dp.year), Number(dp.month) - 1, Number(dp.day), hour, minute, 0);
    const instant = new Date(guess - tzOffsetMinutes(fromZone, new Date(guess)) * 60000);
    // Read that instant's wall time in the target zone.
    const op = {};
    for (const part of new Intl.DateTimeFormat("en-US", {
      timeZone: toZone,
      hourCycle: "h23",
      hour: "2-digit",
      minute: "2-digit",
    }).formatToParts(instant)) {
      op[part.type] = part.value;
    }
    return `${op.hour}:${op.minute}`;
  } catch {
    return hhmm;
  }
}

// tzLabel(timeZone): the IANA name plus its current UTC offset, e.g.
// "America/New_York (UTC-5)" or "Asia/Kolkata (UTC+5:30)". Used to label the
// team-timezone picker options and chips so the offset is clear at a glance.
function tzLabel(timeZone) {
  const offset = tzOffsetMinutes(timeZone, new Date());
  const sign = offset < 0 ? "-" : "+";
  const abs = Math.abs(offset);
  const hours = Math.floor(abs / 60);
  const mins = abs % 60;
  const offsetText = mins ? `${hours}:${String(mins).padStart(2, "0")}` : `${hours}`;
  return `${timeZone} (UTC${sign}${offsetText})`;
}

// shortZoneName(timeZone): a compact zone label (e.g. "EST", "GMT+2") for the
// zone right now, falling back to the IANA name.
function shortZoneName(timeZone) {
  try {
    const parts = new Intl.DateTimeFormat("en-US", {
      timeZone,
      timeZoneName: "short",
    }).formatToParts(new Date());
    const tzPart = parts.find((p) => p.type === "timeZoneName");
    return tzPart ? tzPart.value : timeZone;
  } catch {
    return timeZone;
  }
}

// Build a short, human-readable schedule string in the **viewer's** current
// timezone, e.g. "Mon, Wed · 17:00 PST". The stored `time` is in UTC and is
// converted to the viewer's zone for display.
function formatSchedule(days, time) {
  const labels = (days || []).map((d) => labelFor(DAYS, d));
  const dayText = labels.length ? labels.join(", ") : "";
  const localZone = localTimezone();
  const localTime = time ? convertWallTime(time, "UTC", localZone) : "";
  let core = "";
  if (dayText && localTime) core = `${dayText} · ${localTime}`;
  else if (dayText) core = dayText;
  else if (localTime) core = localTime;
  if (!core) return "No schedule set";
  return localTime ? `${core} ${shortZoneName(localZone)}` : core;
}

// --- Subclassing: skill lines + class masteries ---
//
// `value` keys mirror the backend allow-lists in internal/models/eso.go.

// The 21 ESO class skill lines, grouped by class for optgroup rendering. Every
// subclass dropdown lists all of them.
const SKILL_LINE_GROUPS = [
  { class: "Arcanist", lines: [
    { value: "herald_of_the_tome", label: "Herald of the Tome" },
    { value: "soldier_of_apocrypha", label: "Soldier of Apocrypha" },
    { value: "curative_runeforms", label: "Curative Runeforms" },
  ] },
  { class: "Dragonknight", lines: [
    { value: "ardent_flame", label: "Ardent Flame" },
    { value: "draconic_power", label: "Draconic Power" },
    { value: "earthen_heart", label: "Earthen Heart" },
  ] },
  { class: "Necromancer", lines: [
    { value: "grave_lord", label: "Grave Lord" },
    { value: "bone_tyrant", label: "Bone Tyrant" },
    { value: "living_death", label: "Living Death" },
  ] },
  { class: "Nightblade", lines: [
    { value: "assassination", label: "Assassination" },
    { value: "shadow", label: "Shadow" },
    { value: "siphoning", label: "Siphoning" },
  ] },
  { class: "Sorcerer", lines: [
    { value: "dark_magic", label: "Dark Magic" },
    { value: "daedric_summoning", label: "Daedric Summoning" },
    { value: "storm_calling", label: "Storm Calling" },
  ] },
  { class: "Templar", lines: [
    { value: "aedric_spear", label: "Aedric Spear" },
    { value: "dawns_wrath", label: "Dawn's Wrath" },
    { value: "restoring_light", label: "Restoring Light" },
  ] },
  { class: "Warden", lines: [
    { value: "animal_companions", label: "Animal Companions" },
    { value: "green_balance", label: "Green Balance" },
    { value: "winters_embrace", label: "Winter's Embrace" },
  ] },
];

// Flat skill-line list (with leading "—") for label lookups.
const SKILL_LINES = [{ value: "", label: "—" }].concat(
  SKILL_LINE_GROUPS.flatMap((g) => g.lines)
);

// The 5 class masteries per class. A non-subclassed player picks up to 2 from
// their own class.
const MASTERIES_BY_CLASS = {
  arcanist: [
    { value: "abyssal_emergence", label: "Abyssal Emergence", desc: "Dive deep into the abyss where power is yours to claim. Activating an Arcanist Ultimate immediately generates maximum Crux and grants you 666 Weapon and Spell Damage for 15 seconds." },
    { value: "fate_realigned", label: "Fate Realigned", desc: "Be the hand that guides the weave of fate. Upgrades rank 2 of Implacable Outcome to generate maximum Crux, up to once every 15 seconds. After this effect triggers you gain 300 Weapon and Spell Damage for 25 seconds." },
    { value: "unbound_potential", label: "Unbound Potential", desc: "Surpass your limits in the fieldwork of battle. Upgrades rank 2 of Fated Fortune to also increase your damage done by 30%, halving against players, for the duration." },
    { value: "erudites_rigor", label: "Erudite's Rigor", desc: "Master the archaic and forge new runes of conflict. Upgrades Fatewoven Armor to inflict Minor Cowardice on your attacker while granting Major Vitality to you and your group for 4 seconds. You gain 1 Ultimate for every Crux you have. These effects can occur once every 2 seconds." },
    { value: "ink_scribes_verve", label: "Ink-Scribe's Verve", desc: "Illuminate body and mind with glimmering ink. While in combat, when you heal or overheal a total of 100000 Health, you generate a Crux and grant you and your group members Major Force for 10 seconds, increasing Critical Damage by 20%." },
  ],
  dragonknight: [
    { value: "lead_from_the_front", label: "Lead From the Front", desc: "Let others be swept along in the wake of your flame. Activating rank 2 of The Storm Voice grants you and group members within 28 meters of you Major Berserk and Protection for 1 second per 15 Ultimate spent, increasing damage done and reducing damage taken by 10%." },
    { value: "resolute_defense", label: "Resolute Defense", desc: "You are as unyielding as a mountain. Each second you remain Bracing, you increase the amount of damage you block by 6%, up to 5 times. Blocking damage has a 20% chance to restore 500 Stamina. This effect can occur once every block cost." },
    { value: "wildfire_embers", label: "Wildfire Embers", desc: "Fire forged within can never be extinguished. Fan the flames. When your Dragonknight damage over time effects end, you apply Wildfire Embers to the target, dealing 1565 Flame Damage over 12 seconds. This effect stacks up to 12 times and increases in damage by 25% per stack." },
    { value: "booming_voice", label: "Booming Voice", desc: "Let your voice echo across the peaks. Activating rank 2 of The Storm Voice now also grants 5 Health, Magicka, and Stamina Recovery for every Ultimate spent for 10 seconds after a delay of 15 seconds." },
    { value: "inexorable_descent", label: "Inexorable Descent", desc: "Be as the landslide. Let nothing stop you. Improves your Landslide passive to increase the potency of your damage done, healing done, and damage shield strength by 1% per stack." },
  ],
  necromancer: [
    { value: "nothing_wasted", label: "Nothing Wasted", desc: "As an abattoir of death, no remnants of life are wasted. Upgrades rank 2 of Corpse Consumption to also grant a stack of Nothing Wasted for 15 seconds, which increases your Max Health by 2% and Weapon and Spell Damage by 2% per stack, up to 10 times. Stacks decay three at a time instead of all at once." },
    { value: "malevolent_promise", label: "Malevolent Promise", desc: "Death's knell is a familiar song. Share it with your enemies. Upgrades rank 2 of Corpse Consumption to mark the closest non-player enemy to you with death's touch for 6 seconds, allowing you to use a corpse consuming ability on them. Consuming or replacing this effect forcibly triggers rank 1 of Corpse Consumption's Ultimate generation and triggers rank 2 of Death Gleaning. This effect can occur once every 2 seconds." },
    { value: "cycle_unending", label: "Cycle Unending", desc: "The cycle of life and death continues: you will live, others will die. Upgrades rank 2 of Reusable Parts to grant Cycle Unending for 25 seconds, increasing the potency of your damage done by 1% for every 1% Health you have more than your target. The damage bonus caps at 25%, or 10% against players." },
    { value: "pound_of_flesh", label: "Pound of Flesh", desc: "Knock upon death's door and demand your due. When you take damage you have a 10% chance to heal 1600 Health and restore 5% of your missing Stamina, up to once per second. This chance increases by 1% for every missing Health percent you have and the healing is based off your Max Health." },
    { value: "veils_forfeit", label: "Veil's Forfeit", desc: "Call souls back from an early grave. Only you may grant them release. While you are in combat, directly healing a target below 66% Health relinquishes them from death's clutches and allows you to use a corpse consuming ability against them within 10 seconds, up to once every second. Extends the duration of Major Vulnerability you apply by 50%." },
  ],
  nightblade: [
    { value: "nocturnal_inspiration", label: "Nocturnal Inspiration", desc: "The Night Mistress rewards those who strike without reservation. Upgrades rank 2 of Hemorrhage to have a chance to generate 2 Ultimate. This effect can occur once every 0.3 seconds and the chance is equal to your Weapon Critical chance." },
    { value: "an_eye_for_exploitation", label: "An Eye for Exploitation", desc: "Every strike, every stab, is a step towards execution. Increases your Weapon and Spell Damage by up to 2000, based on your target's missing Health. Reduces your damage taken by up to 20%, based on your attacker's missing Health. Both effects are halved against targets with Battle Spirit." },
    { value: "above_and_beyond", label: "Above and Beyond", desc: "An assassin is only as good as their reputation. Increases your Critical Damage and Healing by 25%. This effect is reduced to 5% against targets with Battle Spirit. Increases your maximum Critical Damage and Healing by 30%." },
    { value: "cutthroats_focus", label: "Cutthroat's Focus", desc: "This duel will be their last. For you, it's just another dance. Activating a Nightblade ability while bracing causes you to dodge attacks for 0.3 seconds. Whenever you dodge an attack, your attacker's psyche suffers, increasing their damage taken by 5% for 5 seconds, increasing to 20 seconds against monsters." },
    { value: "share_the_spoils", label: "Share the Spoils", desc: "Secrets and coin are tools as sharp as the daggers you wield. Upgrades rank 2 of Transfer to grant group members 250 Magicka and Stamina, 2 Ultimate, and doubles the Ultimate you gain when it activates." },
  ],
  sorcerer: [
    { value: "conservation_of_energy", label: "Conservation of Energy", desc: "Spellcasting is second nature to you. A skill honed by tireless study. Upgrades rank 2 of Blood Magic to work with all abilities with a cost, excluding cost per tick abilities, and restores 239 Magicka and 239 Stamina when it activates. This effect scales off your Max Magicka and Stamina." },
    { value: "font_of_power", label: "Font of Power", desc: "Within you is a wellspring of near unlimited power. Upgrades rank 2 of Exploitation to work with any Sorcerer ability and increases your Weapon and Spell Damage by 6% for 10 seconds. The Weapon and Spell Damage increases by 1% for every 1750 Max Magicka or Stamina you have, whichever is higher." },
    { value: "static_reverberation", label: "Static Reverberation", desc: "The battlefield crackles with untapped potential. Make use of it. When you deal damage, you have a 5% chance to deal 315 Shock Damage, up to once every 0.3 seconds. The chance increases by 1% for every 1% missing Health the target has. The chance is divided by 1 plus every permanent pet you have active." },
    { value: "calculated_defense", label: "Calculated Defense", desc: "Success is a certainty when nothing is left to chance. While beginning to use a Sorcerer ability or an ability with a cast time, you gain a damage shield for 0.5 seconds that can absorb 5280 damage. This effect is based off your Max Health. If the shield does not break, you and nearby group members gain 6% Weapon and Spell Damage for 20 seconds." },
    { value: "sphere_of_influence", label: "Sphere of Influence", desc: "Magicka extends your reach, a benefit to those who fight beside you. Casting a damage shield on yourself or an ally grants an additional shield that absorbs up to 2400 damage for 4 seconds and 225 Health, Magicka, and Stamina Recovery for 12 seconds. The shield scales off the higher of your Max Health or Max Magicka, and is capped at 25% of the target's Max Health." },
  ],
  templar: [
    { value: "bastion_of_light", label: "Bastion of Light", desc: "Light consecrates the ground on which you tread. Sacred Ground is now applied while you are in your own Nova and Spear Shards, and while Radial Sweep and Solar Barrage are active. While Sacred Ground is active, you heal for 1280 Health every 1 second. If you are at full Health after being healed from this effect while in combat, you also gain 2 Ultimate." },
    { value: "devout_guardian", label: "Devout Guardian", desc: "Radiant is the shield that pushes back the dark. While Sacred Ground is active, you gain a damage shield for 6 seconds, up to once every 6 seconds. The shield absorbs up to 3200 damage and provides 300 Health, Magicka, and Stamina Recovery while active. If the shield breaks, you gain 10 Ultimate." },
    { value: "bright_harbinger", label: "Bright Harbinger", desc: "Light's banner instills hope. You are its bearer. Upgrades rank 2 of Illuminate to also grant Bright Harbinger for the duration, which grants 300 Weapon and Spell Damage to allies and doubles for you." },
    { value: "judgments_brand", label: "Judgment's Brand", desc: "Judgement follows in the wake of your radiance. When rank 2 of Burning Light deals damage, your Templar abilities gain 1400 damage done for 3.1 seconds. This bonus is halved against players." },
    { value: "steadfast_candescence", label: "Steadfast Candescence", desc: "Your conviction is unwavering. Light gives no quarter. Sacred Ground now activates and refreshes itself while Bracing. Increases the amount of damage you can block by 20% while stationary." },
  ],
  warden: [
    { value: "tundras_maw", label: "Tundra's Maw", desc: "Expose your enemies to unrelenting cold. Applying Chill to an enemy also applies Major Brittle for 2 seconds, increasing their Critical Damage taken by 20%." },
    { value: "wild_adaptation", label: "Wild Adaptation", desc: "The battlefield is your grove. Tend to it. Gain 333 Weapon and Spell Damage for each status effect on your target, up to a maximum of 1665." },
    { value: "glacial_obstinance", label: "Glacial Obstinance", desc: "Tap into the wellspring of permafrost's resolve. Upgrades Bond with Nature to also activate when casting Winter's Embrace abilities and to grant 15% Weapon and Spell Damage for 10 seconds if you are at full Health after the heal." },
    { value: "green_keepers_hide", label: "Green-Keeper's Hide", desc: "The twisting of seasons has tempered your will. Reduce your damage taken by 3% for every status effect active on your attacker, up to a maximum of 15%." },
    { value: "bountiful_harvest", label: "Bountiful Harvest", desc: "Share in the spoils of nature's abundance. Upgrades rank 2 of Nature's Gift to grant you and your group members Major Heroism for 3 seconds and an additional 125 Magicka and Stamina, up to once every 2 seconds." },
  ],
};

// Flat mastery list (with leading "—") across all classes, for label lookups.
const MASTERIES = [{ value: "", label: "—" }].concat(
  Object.values(MASTERIES_BY_CLASS).flat()
);

// Build the <option>/<optgroup> markup for a skill-line dropdown, selecting
// `selected` if present.
function skillLineOptionsHtml(selected) {
  let html = `<option value="" ${selected ? "" : "selected"}>—</option>`;
  for (const group of SKILL_LINE_GROUPS) {
    html += `<optgroup label="${group.class}">`;
    for (const l of group.lines) {
      html += `<option value="${l.value}" ${l.value === selected ? "selected" : ""}>${l.label}</option>`;
    }
    html += `</optgroup>`;
  }
  return html;
}

// Return the class (lowercase value, e.g. "arcanist") a skill line belongs to,
// or "" if unknown.
function skillLineClass(value) {
  if (!value) return "";
  for (const g of SKILL_LINE_GROUPS) {
    if (g.lines.some((l) => l.value === value)) return g.class.toLowerCase();
  }
  return "";
}

// Build the <option> markup for a class-mastery dropdown. Each option carries a
// `title` (its description) so hovering shows a tooltip; `selected` is preselected.
function masteryOptionsHtml(masteries, selected) {
  let html = `<option value="" ${selected ? "" : "selected"}>—</option>`;
  for (const m of masteries || []) {
    html += `<option value="${m.value}" title="${escapeAttr(m.desc)}" ${m.value === selected ? "selected" : ""}>${m.label}</option>`;
  }
  return html;
}

// Return the description for a mastery value within a class, or "" if unknown.
function masteryDesc(cls, value) {
  if (!value) return "";
  const m = (MASTERIES_BY_CLASS[cls] || []).find((x) => x.value === value);
  return m ? m.desc : "";
}

// --- Encounters: names, gear sets, skills ---

// Encounter names, grouped for an <optgroup> picker. Each `names` entry's
// value === label (encounters are stored by their display name).
const ENCOUNTER_NAME_GROUPS = [
  { group: "General", names: ["Default", "Trash"] },
  { group: "Aetherian Archive", names: ["Lightning Storm Atronach", "Foundation Stone Atronach", "Varlariel", "The Celestial Mage"] },
  { group: "Hel Ra Citadel", names: ["Ra Kotu", "Yokeda Kai and Yokeda Rok'dun", "The Celestial Warrior"] },
  { group: "Sanctum Ophidia", names: ["Possessed Mantikora", "Stonebreaker", "Ozara", "The Celestial Serpent"] },
  { group: "Maw of Lorkhaj", names: ["Zhaj'hassa the Forgotten", "The Twins", "Rakkhat"] },
  { group: "Halls of Fabrication", names: ["Hunter-Killer Fabricants", "Pinnacle Factotum", "Archcustodian", "Refabrication Committee", "The Assembly General"] },
  { group: "Asylum Sanctorium", names: ["Saint Llothis the Pious", "Saint Felms the Bold", "Saint Olms the Just"] },
  { group: "Cloudrest", names: ["Shade of Galenwe", "Shade of Siroria", "Shade of Relequen", "Z'Maja"] },
  { group: "Sunspire", names: ["Lokkestiiz", "Yolnahkriin", "Nahviintaas"] },
  { group: "Kyne's Aegis", names: ["Yandir the Butcher", "Captain Vrol", "Lord Falgravn"] },
  { group: "Rockgrove", names: ["Oaxiltso", "Flame-Herald Bahsei", "Xalvakka"] },
  { group: "Dreadsail Reef", names: ["Lylanar and Turlassil", "Reef Guardian", "Tideborn Taleria"] },
  { group: "Sanity's Edge", names: ["Exarchanic Yaseyla", "Archwizard Twelvane and Chimera", "Ansuul the Tormentor"] },
  { group: "Lucent Citadel", names: ["Count Ryelaz and Zilyesset", "Orphic Shattered Shard", "Xoryn"] },
  { group: "Ossein Cage", names: ["Shapers of Flesh", "Jynorah and Skorkhif", "Overfiend Kazpian"] },
];

// Flat set of valid encounter names for quick validation/lookup.
const ENCOUNTER_NAMES = new Set(ENCOUNTER_NAME_GROUPS.flatMap((g) => g.names));

// The group holding non-trial encounters (Default, Trash). General encounters
// may always coexist with a single trial's bosses.
const GENERAL_ENCOUNTER_GROUP = "General";

// Map each encounter name to its group/trial, for the single-trial rule.
const ENCOUNTER_TRIAL_BY_NAME = {};
ENCOUNTER_NAME_GROUPS.forEach((g) => {
  g.names.forEach((n) => {
    ENCOUNTER_TRIAL_BY_NAME[n] = g.group;
  });
});

// encounterTrial(name) -> the group/trial a name belongs to, or "" if unknown.
function encounterTrial(name) {
  return ENCOUNTER_TRIAL_BY_NAME[name] || "";
}

// validEncounterGroups(existingNames, keepName?) returns ENCOUNTER_NAME_GROUPS
// filtered to the only valid choices for a team that already uses
// `existingNames`:
//   - already-used names are dropped (encounters are unique per team);
//   - trial bosses are limited to the team's locked-in trial (the trial of any
//     existing non-general encounter); if no trial is locked, all are allowed;
//   - General (Default/Trash) is always offered.
// `keepName` (the encounter being renamed) is always retained and does not lock
// or count against the rules.
function validEncounterGroups(existingNames, keepName) {
  const used = new Set(existingNames || []);
  if (keepName) used.delete(keepName);

  let lockedTrial = "";
  for (const n of used) {
    const t = encounterTrial(n);
    if (t && t !== GENERAL_ENCOUNTER_GROUP) {
      lockedTrial = t;
      break;
    }
  }

  const groups = [];
  for (const g of ENCOUNTER_NAME_GROUPS) {
    if (g.group !== GENERAL_ENCOUNTER_GROUP && lockedTrial && g.group !== lockedTrial) {
      continue;
    }
    const names = g.names.filter((n) => !used.has(n) || n === keepName);
    if (names.length) groups.push({ group: g.group, names });
  }
  return groups;
}

// Gear sets + their derived flat list (GEAR_SET_GROUPS / GEAR_SETS) now live in
// gear-skills.js, loaded before this file. The lookups/helpers below consume the
// GEAR_SETS global defined there.

// Skills + their derived flat list (SKILL_GROUPS / SKILLS) now live in
// gear-skills.js, loaded before this file. The lookups/helpers below consume the
// SKILLS global defined there.

// Potions (seed), grouped by purpose. A third per-encounter loadout type
// alongside gear/skills; potions are tracked as a buff source. `desc` is shown
// as a tooltip on the picker options and selected chips.
const POTION_GROUPS = [
  { group: "Trial Potions", potions: [
    { value: "spell_power_potion", label: "Essence of Spell Power", desc: "Grants Major Sorcery and Major Prophecy (increased Spell Damage and Spell Critical) and restores Magicka. The standard magicka-DPS \"spell power\" potion (Lady's Smock, Corn Flower, Water Hyacinth)." },
    { value: "weapon_power_potion", label: "Essence of Weapon Power", desc: "Grants Major Brutality and Major Savagery (increased Weapon Damage and Weapon Critical) and restores Stamina. The standard stamina-DPS \"weapon power\" potion (Blessed Thistle, Dragonthorn, Water Hyacinth)." },
    { value: "tri_restoration_potion", label: "Tri-Restoration Potion", desc: "Restores Health, Magicka, and Stamina and grants Minor Fortitude, Minor Intellect, and Minor Endurance (increased recoveries). The \"tri-stat\" potion (Bugloss, Columbine, Mountain Flower)." },
  ] },
];

// Flat potion list (derived) for label/value lookups.
const POTIONS = POTION_GROUPS.flatMap((g) =>
  g.potions.map((p) => ({ ...p, group: g.group }))
);

// --- Crit-damage inputs: crit-damage sources, mundus, blue (Warfare) CP ---
//
// These are the per-encounter, per-player inputs the crit calculator needs that
// aren't otherwise tracked. `critPct` is the Critical Damage the source
// contributes; the crit model below reads it.

// Crit-damage sources a player can run. Currently the two weapon-line passives
// that grant Critical Damage; only the highest applies (the active damage bar).
// Stored per encounter per slot under the `crit_dmg` loadout key.
const CRIT_DMG_SOURCES = [
  { value: "two_handed", label: "Heavy Weapons (Two-Handed)", critPct: 16, desc: "Two-Handed \"Heavy Weapons\" passive: increases Critical Damage by 16% while a Two-Handed weapon is equipped." },
  { value: "dual_wield_single", label: "Twin Blade and Blunt (Single Axe)", critPct: 6, desc: "Dual Wield \"Twin Blade and Blunt\" passive: increases Critical Damage by 6% with one one-handed weapon equipped." },
  { value: "dual_wield_double", label: "Twin Blade and Blunt (Double Axe)", critPct: 12, desc: "Dual Wield \"Twin Blade and Blunt\" passive: increases Critical Damage by 12% with two one-handed weapons equipped." },
];

// Mundus stones (single-select per player per encounter). Only The Shadow
// grants Critical Damage.
const MUNDUS_STONES = [
  { value: "", label: "—", critPct: 0, desc: "No mundus selected." },
  { value: "the_shadow", label: "The Shadow", critPct: 18, desc: "Increases Critical Damage by 18% (the standard DPS mundus)." },
  { value: "the_thief", label: "The Thief", critPct: 0, desc: "Increases Critical Chance." },
  { value: "the_warrior", label: "The Warrior", critPct: 0, desc: "Increases Weapon Damage." },
  { value: "the_mage", label: "The Mage", critPct: 0, desc: "Increases Maximum Magicka." },
  { value: "the_apprentice", label: "The Apprentice", critPct: 0, desc: "Increases Spell Damage." },
  { value: "the_atronach", label: "The Atronach", critPct: 0, desc: "Increases Magicka Recovery." },
  { value: "the_ritual", label: "The Ritual", critPct: 0, desc: "Increases Healing Done." },
  { value: "the_lover", label: "The Lover", critPct: 0, desc: "Increases Physical and Spell Penetration." },
  { value: "the_lord", label: "The Lord", critPct: 0, desc: "Increases Maximum Health." },
  { value: "the_steed", label: "The Steed", critPct: 0, desc: "Increases Health Recovery and movement speed." },
  { value: "the_lady", label: "The Lady", critPct: 0, desc: "Increases Physical and Spell Resistance." },
  { value: "the_tower", label: "The Tower", critPct: 0, desc: "Increases Maximum Stamina." },
  { value: "the_serpent", label: "The Serpent", critPct: 0, desc: "Increases Stamina Recovery." },
];

// Slottable blue (Warfare) champion-point stars. Only the two crit stars carry
// `critPct`; the others are listed so the picker reflects a real CP bar (slot
// up to 4). Stored free-form per encounter per slot.
const BLUE_CP = [
  { value: "backstabber", label: "Backstabber", critPct: 15, desc: "Increases your Critical Damage by 15% against enemies you flank (attack from behind)." },
  { value: "fighting_finesse", label: "Fighting Finesse", critPct: 10, desc: "Increases your Critical Damage by 10%." },
  { value: "master_at_arms", label: "Master-at-Arms", desc: "Increases your direct damage." },
  { value: "deadly_aim", label: "Deadly Aim", desc: "Increases your damage with single-target damage-over-time abilities." },
  { value: "biting_aura", label: "Biting Aura", desc: "Increases your damage with area-of-effect abilities." },
  { value: "thaumaturge", label: "Thaumaturge", desc: "Increases your damage-over-time damage." },
  { value: "wrathful_strikes", label: "Wrathful Strikes", desc: "Increases your Weapon and Spell Damage with direct-damage attacks." },
  { value: "piercing", label: "Piercing", desc: "Increases your Offensive Penetration." },
  { value: "force_of_nature", label: "Force of Nature", desc: "Increases your Offensive Penetration by 660 for each negative status effect on the enemy, up to a maximum of 5 (3300)." },
];

// Flat lists / lookups for the new inputs.
const CRIT_DMG_BY_KEY = Object.fromEntries(CRIT_DMG_SOURCES.map((w) => [w.value, w]));
const MUNDUS_BY_KEY = Object.fromEntries(MUNDUS_STONES.map((m) => [m.value, m]));
const BLUE_CP_BY_KEY = Object.fromEntries(BLUE_CP.map((c) => [c.value, c]));
const CRIT_DMG_BY_LABEL = Object.fromEntries(CRIT_DMG_SOURCES.map((w) => [w.label.toLowerCase(), w]));
const BLUE_CP_BY_LABEL = Object.fromEntries(BLUE_CP.map((c) => [c.label.toLowerCase(), c]));

function critDmgLabel(key) { const w = CRIT_DMG_BY_KEY[key]; return w ? w.label : key; }
function critDmgDesc(key) { const w = CRIT_DMG_BY_KEY[key]; return w && w.desc ? w.desc : ""; }
function critDmgByLabel(label) { return CRIT_DMG_BY_LABEL[String(label || "").trim().toLowerCase()] || null; }
function cpBlueLabel(key) { const c = BLUE_CP_BY_KEY[key]; return c ? c.label : key; }
function cpBlueDesc(key) { const c = BLUE_CP_BY_KEY[key]; return c && c.desc ? c.desc : ""; }
function cpBlueByLabel(label) { return BLUE_CP_BY_LABEL[String(label || "").trim().toLowerCase()] || null; }
function mundusLabel(key) { const m = MUNDUS_BY_KEY[key]; return m ? m.label : key; }
function mundusDesc(key) { const m = MUNDUS_BY_KEY[key]; return m && m.desc ? m.desc : ""; }

// Extra penetration sources that aren't derivable from otherwise-tracked data
// (weapon trait/type, enchants, generic set-piece bonuses). `pen` is the flat
// Offensive Penetration; `bucket` is "self" (only that player) or "group" (a
// target debuff that benefits the whole team when any one player runs it). Used
// by the penetration calculator and stored per encounter per slot (`pen_extra`).
const PEN_EXTRA_SOURCES = [
  { value: "crusher", label: "Crusher (enchant)", pen: 2108, bucket: "group", desc: "Crusher weapon enchant: applies a stacking Physical & Spell Resistance debuff to the target. Benefits the whole group." },
  { value: "sharpened", label: "Sharpened (single)", pen: 1638, bucket: "self", desc: "One Sharpened weapon (gold quality): increases your Offensive Penetration by 1638." },
  { value: "sharpened_double", label: "Sharpened (double)", pen: 3276, bucket: "self", desc: "Two Sharpened weapons (gold quality), e.g. dual wield or both bars: increases your Offensive Penetration by 3276." },
  { value: "arena_one_piece", label: "Arena weapon (1pc)", pen: 1190, bucket: "self", desc: "Flat Offensive Penetration from an arena weapon's 1pc bonus. Only add this if the arena weapon isn't already selected in gear (an equipped Arena Weapons set is auto-counted)." },
  { value: "mace_maul", label: "Mace (single)", pen: 1487, bucket: "self", desc: "One Mace (1H) or Maul (2H) weapon: ignores a portion of the target's Resistance." },
  { value: "mace_maul_double", label: "Mace / Maul (double)", pen: 2974, bucket: "self", desc: "Two Maces (1H) or Mauls (2H), e.g. dual wield or both bars: ignores twice the Resistance of a single weapon." },
  // `maxStack` lets a self source be added more than once (each stack adds `pen`).
  // Set-piece flat-pen bonuses commonly come from several different sets at once,
  // so this can be stacked up to 5 times (stored as repeated keys in `pen_extra`).
  { value: "set_piece_bonuses", label: "Set-piece bonuses", pen: 1487, bucket: "self", maxStack: 5, desc: "Flat Offensive Penetration from set 2–4 piece bonuses not otherwise modeled. Add up to 5 times to stack multiple sets." },
];

const PEN_EXTRA_BY_KEY = Object.fromEntries(PEN_EXTRA_SOURCES.map((p) => [p.value, p]));
const PEN_EXTRA_BY_LABEL = Object.fromEntries(PEN_EXTRA_SOURCES.map((p) => [p.label.toLowerCase(), p]));
function penExtraLabel(key) { const p = PEN_EXTRA_BY_KEY[key]; return p ? p.label : key; }
function penExtraDesc(key) { const p = PEN_EXTRA_BY_KEY[key]; return p && p.desc ? p.desc : ""; }
function penExtraByLabel(label) { return PEN_EXTRA_BY_LABEL[String(label || "").trim().toLowerCase()] || null; }
function penExtraMaxStack(key) { const p = PEN_EXTRA_BY_KEY[key]; return p && p.maxStack ? p.maxStack : 1; }

// Count occurrences of each key in an array (used for stackable pen sources that
// are stored as repeated keys, e.g. set_piece_bonuses).
function countKeys(arr) {
  return (arr || []).reduce((acc, k) => {
    acc[k] = (acc[k] || 0) + 1;
    return acc;
  }, {});
}

// --- Lookup helpers ---
const GEAR_BY_KEY = Object.fromEntries(GEAR_SETS.map((g) => [g.value, g]));
const GEAR_BY_LABEL = Object.fromEntries(GEAR_SETS.map((g) => [g.label.toLowerCase(), g]));
const SKILL_BY_KEY = Object.fromEntries(SKILLS.map((s) => [s.value, s]));
const SKILL_BY_LABEL = Object.fromEntries(SKILLS.map((s) => [s.label.toLowerCase(), s]));
const POTION_BY_KEY = Object.fromEntries(POTIONS.map((p) => [p.value, p]));
const POTION_BY_LABEL = Object.fromEntries(POTIONS.map((p) => [p.label.toLowerCase(), p]));

function gearLabel(key) {
  const g = GEAR_BY_KEY[key];
  return g ? g.label : key;
}
function gearDesc(key) {
  const g = GEAR_BY_KEY[key];
  return g ? g.desc : "";
}
function gearByLabel(label) {
  return GEAR_BY_LABEL[String(label || "").trim().toLowerCase()] || null;
}

// Gear-set abbreviations (GEAR_ABBREVIATIONS) now live in gear-skills.js,
// loaded before this file; gearAbbrev below consumes that global.

// gearAbbrev(key): the curated abbreviation for a gear set, or an auto-generated
// acronym from its label as a fallback (e.g. "Pillar of Nirn" -> "PN"). Used by
// the condensed Discord export.
function gearAbbrev(key) {
  if (GEAR_ABBREVIATIONS[key]) return GEAR_ABBREVIATIONS[key];
  const label = gearLabel(key).replace(/\(.*?\)/g, "").trim();
  const words = label.split(/\s+/).filter((w) => w && !/^(of|the|and)$/i.test(w));
  if (words.length <= 1) return words[0] || label;
  return words.map((w) => w[0].toUpperCase()).join("");
}
function skillLabel(key) {
  const s = SKILL_BY_KEY[key];
  return s ? s.label : key;
}
function skillDesc(key) {
  const s = SKILL_BY_KEY[key];
  return s && s.desc ? s.desc : "";
}
function skillByLabel(label) {
  return SKILL_BY_LABEL[String(label || "").trim().toLowerCase()] || null;
}
function potionLabel(key) {
  const p = POTION_BY_KEY[key];
  return p ? p.label : key;
}
function potionDesc(key) {
  const p = POTION_BY_KEY[key];
  return p && p.desc ? p.desc : "";
}
function potionByLabel(label) {
  return POTION_BY_LABEL[String(label || "").trim().toLowerCase()] || null;
}

// Grouped option data for the searchable-select component:
//   [{ group: string|null, items: [{ value, label, desc? }] }]
// Gear is grouped by set type (5pc, monster, arena, mythic); skills by skill line.
const GEAR_GROUPS = GEAR_SET_GROUPS.map((g) => ({ group: g.group, items: g.sets }));
const SKILL_SELECT_GROUPS = SKILL_GROUPS.map((g) => ({ group: g.group, items: g.skills }));
const POTION_SELECT_GROUPS = POTION_GROUPS.map((g) => ({ group: g.group, items: g.potions }));
const CRIT_DMG_SELECT_GROUPS = [{ group: null, items: CRIT_DMG_SOURCES }];
const BLUE_CP_SELECT_GROUPS = [{ group: null, items: BLUE_CP }];
const PEN_EXTRA_SELECT_GROUPS = [{ group: null, items: PEN_EXTRA_SOURCES }];

// Master tables keyed by loadout type, so UI code can stay generic. cp_blue and
// the crit-damage sources reuse the same chip/searchable-select machinery as
// gear/skills. The crit-damage sources are stored under the `crit_dmg` key.
const LOADOUT_TYPES = {
  gear: { items: GEAR_SETS, groups: GEAR_GROUPS, byLabel: gearByLabel, label: gearLabel, desc: gearDesc, addPlaceholder: "Search gear set…" },
  skills: { items: SKILLS, groups: SKILL_SELECT_GROUPS, byLabel: skillByLabel, label: skillLabel, desc: skillDesc, addPlaceholder: "Search skill…" },
  potions: { items: POTIONS, groups: POTION_SELECT_GROUPS, byLabel: potionByLabel, label: potionLabel, desc: potionDesc, addPlaceholder: "Search potion…" },
  cp_blue: { items: BLUE_CP, groups: BLUE_CP_SELECT_GROUPS, byLabel: cpBlueByLabel, label: cpBlueLabel, desc: cpBlueDesc, addPlaceholder: "Search blue CP star…" },
  crit_dmg: { items: CRIT_DMG_SOURCES, groups: CRIT_DMG_SELECT_GROUPS, byLabel: critDmgByLabel, label: critDmgLabel, desc: critDmgDesc, addPlaceholder: "Add crit damage source…" },
  pen_extra: { items: PEN_EXTRA_SOURCES, groups: PEN_EXTRA_SELECT_GROUPS, byLabel: penExtraByLabel, label: penExtraLabel, desc: penExtraDesc, addPlaceholder: "Add penetration source…" },
};

// --- Buffs: coverage tracking ---
//
// A team wants to cover a list of buffs. Each buff can be provided by any of
// several SOURCES across categories that already exist in this app:
//   - gear        : equipped gear-set keys (per-encounter loadout)
//   - skills      : slotted skill keys (per-encounter loadout)
//   - potions     : potion keys (per-encounter loadout)
//   - masteries   : a non-subclassed player's class-mastery keys (roster build)
//   - classes     : a non-subclassed player's class (roster build)
//   - skillLines  : a subclassed player's skill-line keys (roster build)
//
// A buff is "met" for an encounter if at least one player provides at least one
// of its sources.
//
// GROUP-WIDE RULE: every source listed here must actually apply the buff to the
// whole group (the source's tooltip grants it to "you and your group members /
// allies"), OR it must be an enemy DEBUFF (Breach, Brittle, Vulnerability, …),
// which benefits the whole team no matter who applies it. Self-only buffs — the
// ones each player maintains for themselves (e.g. personal Major Resolve from
// Boundless Storm, a personal weapon/spell-power potion) — are intentionally NOT
// tracked here, because slotting them on one player does not cover the group.
//
// CLASS-PASSIVE SOURCES: a few skill lines passively share a buff with the
// group, so we treat the buff as covered if any player runs that line. These are
// detected via `skillLines` (a subclassed player who slotted the line) AND
// `classes` (a base, non-subclassed player of that class always has the line):
//   - Green Balance  (Warden)        → group Minor Toughness
//   - Draconic Power (Dragonknight)  → group Minor Brutality
//   - Assassination  (Nightblade)    → group Minor Savagery
const BUFFS = [
  // Major Resolve has true group-wide providers (the Warden Frost Cloak line and
  // the Mighty Glacier set apply it to "you and your grouped allies").
  { value: "major_resolve", label: "Major Resolve", desc: "Increases group Physical and Spell Resistance.",
    sources: { skills: ["frost_cloak", "expansive_frost_cloak", "ice_fortress", "mend_spirit"], gear: ["mighty_glacier"] } },
  // Minor Toughness shared to the group by the Warden Green Balance passive.
  { value: "minor_toughness", label: "Minor Toughness", desc: "Increases group Max Health by 10%.",
    sources: { skillLines: ["green_balance"], classes: ["warden"] } },
  // Major Brutality & Sorcery: only the Dragonknight Igneous Weapons line shares
  // it with grouped allies; personal potions/Degeneration/Surge are self-only.
  { value: "major_brutality_sorcery", label: "Major Brutality and Sorcery", desc: "Increases group Weapon and Spell Damage.",
    sources: { skills: ["igneous_weapons", "molten_armaments", "molten_weapons"] } },
  // Minor Brutality shared to the group by the Dragonknight Draconic Power passive.
  { value: "minor_brutality_sorcery", label: "Minor Brutality", desc: "Increases group Weapon Damage.",
    sources: { skillLines: ["draconic_power"], classes: ["dragonknight"] } },
  // Minor Savagery shared to the group by the Nightblade Assassination passive.
  { value: "minor_savagery_prophecy", label: "Minor Savagery", desc: "Increases group Weapon Critical.",
    sources: { skillLines: ["assassination"], classes: ["nightblade"] } },
  { value: "major_berserk", label: "Major Berserk", desc: "Increases damage done by 10%.",
    sources: { masteries: ["lead_from_the_front"], skills: ["summon_storm_atronach", "summon_charged_atronach", "greater_storm_atronach"] } },
  { value: "minor_berserk", label: "Minor Berserk", desc: "Increases group damage done by 5%.",
    sources: { skills: ["combat_prayer"], gear: ["kinras_wrath"] } },
  { value: "major_slayer", label: "Major Slayer", desc: "Increases damage to Dungeon/Trial monsters.",
    sources: { gear: ["master_architect", "roaring_opportunist", "perfected_roaring_opportunist", "war_machine"] } },
  { value: "major_courage", label: "Major Courage", desc: "Increases Weapon and Spell Damage.",
    sources: { gear: ["perfected_olorime", "vestment_of_olorime", "spell_power_cure"], skills: ["ferocious_roar"] } },
  { value: "minor_courage", label: "Minor Courage", desc: "Increases Weapon and Spell Damage.",
    sources: { gear: ["claw_of_yolnahkriin", "perfected_claw_of_yolnahkriin", "magma_incarnate", "pangrit_denmother", "phoenix_moth_theurge", "crusader", "fledglings_nest"], skills: ["arcanists_domain", "reconstructive_domain", "zenas_empowering_disc"] } },
  // Enemy debuff: applied to the boss by a Necromancer Colossus ultimate or the
  // Umbral Edge set, so the whole group benefits.
  { value: "major_vulnerability", label: "Major Vulnerability", desc: "Increases the damage the enemy takes by 10%.",
    sources: { skills: ["glacial_colossus", "frozen_colossus", "pestilent_colossus"], gear: ["umbral_edge"] } },
  // Enemy debuff: the Nightblade Cutthroat's Focus mastery makes a dodged
  // attacker take 5% more damage (20s vs monsters), benefiting the whole group.
  { value: "cutthroats_focus", label: "Cutthroat's Focus", desc: "Increases the enemy's damage taken by 5%.",
    sources: { masteries: ["cutthroats_focus"] } },
  { value: "heat_shock", label: "Heat Shock", desc: "Increases the target's damage taken (from Magma Fist).",
    sources: { skills: ["magma_fist"] } },
  // Minor Fortitude/Intellect/Endurance shared to allies by the Arcanist domains
  // and the Templar Radiant Aura.
  { value: "minor_fei", label: "Minor Fortitude, Endurance, Intellect", desc: "Increases group Health, Stamina, and Magicka Recovery.",
    sources: { skills: ["arcanists_domain", "reconstructive_domain", "zenas_empowering_disc", "radiant_aura"] } },
  { value: "minor_evasion", label: "Minor Evasion", desc: "Reduces group damage taken from area attacks.",
    sources: { gear: ["abyssal_brace"] } },
  // --- Group survival/support buffs (several only come from class masteries) ---
  // Major Protection (−10% group damage taken): DK Lead From the Front mastery,
  // the Warden Sleet Storm ultimate line, and supporting sets.
  { value: "major_protection", label: "Major Protection", desc: "Reduces group damage taken by 10%.",
    sources: { masteries: ["lead_from_the_front"], skills: ["sleet_storm", "permafrost", "glyphic_of_the_tides"], gear: ["hagravens_garden"] } },
  // Major Vitality (+12% group healing received / shield strength): Arcanist
  // Erudite's Rigor mastery and the Nightblade Soul Siphon ultimate.
  { value: "major_vitality", label: "Major Vitality", desc: "Increases group healing received and shield strength.",
    sources: { masteries: ["erudites_rigor"], skills: ["soul_siphon"] } },
  // Major Heroism (group Ultimate generation): Warden Bountiful Harvest mastery
  // plus the usual support sets.
  { value: "major_heroism", label: "Major Heroism", desc: "Grants the group 3 Ultimate every 1.5s.",
    sources: { masteries: ["bountiful_harvest"], gear: ["drakes_rush", "transformative_hope", "perfected_transformative_hope", "heroic_unity"] } },
  // Bright Harbinger: unique +300 Weapon/Spell Damage to allies, only from the
  // Templar Bright Harbinger mastery (upgrades Illuminate).
  { value: "bright_harbinger", label: "Bright Harbinger", desc: "Grants allies +300 Weapon and Spell Damage.",
    sources: { masteries: ["bright_harbinger"] } },
  // Calculated Defense: unique +6% Weapon/Spell Damage to nearby group members,
  // only from the Sorcerer Calculated Defense mastery (when its shield holds).
  { value: "calculated_defense", label: "Calculated Defense", desc: "Grants nearby group members +6% Weapon and Spell Damage.",
    sources: { masteries: ["calculated_defense"] } },
  { value: "powerful_assault", label: "Powerful Assault", desc: "Set: grants Weapon and Spell Damage to nearby allies.",
    sources: { gear: ["powerful_assault"] } },
  { value: "pillagers_profit", label: "Pillager's Profit", desc: "Set: grants resource recovery when an ally uses an Ultimate.",
    sources: { gear: ["pillagers_profit"] } },
  { value: "pearlescent_ward", label: "Pearlescent Ward", desc: "Set: scaling Weapon and Spell Damage from worn Trial sets.",
    sources: { gear: ["pearlescent_ward"] } },
  { value: "touch_of_zen", label: "Touch of Z'en", desc: "Set: increases damage per damage-over-time effect on the target.",
    sources: { gear: ["z_ens_redress"] } },
  { value: "way_of_martial_knowledge", label: "Way of Martial Knowledge", desc: "Set: increases the target's damage taken from your next direct attack.",
    sources: { gear: ["way_of_martial_knowledge"] } },
  { value: "encratiss_behemoth", label: "Encratis's Behemoth", desc: "Monster set: Flame Damage debuff/buff swing.",
    sources: { gear: ["encratiss_behemoth"] } },
  { value: "symphony_of_blades", label: "Symphony of Blades", desc: "Monster set: resource sustain for low-resource allies.",
    sources: { gear: ["symphony_of_blades"] } },
  { value: "ozezan_the_inferno", label: "Ozezan the Inferno", desc: "Monster set: grants healed allies Minor Vitality and Armor.",
    sources: { gear: ["ozezan_the_inferno"] } },
];

// Human labels for buff source categories (shown in the details modal).
const BUFF_CATEGORY_LABELS = {
  gear: "Gear",
  skills: "Skill",
  potions: "Potion",
  masteries: "Mastery",
  classes: "Class",
  skillLines: "Skill line",
};

// buffSourceLabel(category, key): the display label for one source key.
function buffSourceLabel(category, key) {
  switch (category) {
    case "gear": return gearLabel(key);
    case "skills": return skillLabel(key);
    case "potions": return potionLabel(key);
    case "masteries": return labelFor(MASTERIES, key);
    case "classes": return labelFor(CLASSES, key);
    case "skillLines": return labelFor(SKILL_LINES, key);
    default: return key;
  }
}

// buffKnownSources(buff): the full list of a buff's possible providers as
// "Category: Label" strings (every key across every source category), regardless
// of whether the current roster provides them. Used for the details tooltip.
function buffKnownSources(buff) {
  const parts = [];
  for (const [category, keys] of Object.entries((buff && buff.sources) || {})) {
    (keys || []).forEach((key) => {
      parts.push(`${BUFF_CATEGORY_LABELS[category] || category}: ${buffSourceLabel(category, key)}`);
    });
  }
  return parts;
}

// playerBuffContributions(player, loadout): the set of values one player
// contributes per category. Build sources honor the subclassing rule — a
// subclassed player contributes skill lines; a non-subclassed player
// contributes their class and class masteries. Loadout sources (gear/skills/
// potions) always count.
function playerBuffContributions(player, loadout) {
  const lo = loadout || {};
  const sets = {
    gear: new Set(lo.gear || []),
    skills: new Set(lo.skills || []),
    potions: new Set(lo.potions || []),
    masteries: new Set(),
    classes: new Set(),
    skillLines: new Set(),
  };
  if (player.subclassed) {
    [player.skill_line_1, player.skill_line_2, player.skill_line_3].forEach((v) => {
      if (v) sets.skillLines.add(v);
    });
  } else {
    if (player.class) sets.classes.add(player.class);
    [player.mastery_1, player.mastery_2].forEach((v) => {
      if (v) sets.masteries.add(v);
    });
  }
  return sets;
}

// computeBuffCoverage(players, loadoutBySlot): evaluate every buff against the
// roster + the selected encounter's loadouts. Returns:
//   { total, met, items: [{ buff, met, providers: [{slot, category, key}] }] }
function computeBuffCoverage(players, loadoutBySlot) {
  const contribs = (players || []).map((p) => ({
    player: p,
    sets: playerBuffContributions(p, (loadoutBySlot && loadoutBySlot[p.slot]) || {}),
  }));

  const items = BUFFS.map((buff) => {
    const providers = [];
    for (const { player, sets } of contribs) {
      for (const [category, keys] of Object.entries(buff.sources || {})) {
        const have = sets[category];
        if (!have) continue;
        for (const key of keys) {
          if (have.has(key)) providers.push({ slot: player.slot, category, key });
        }
      }
    }
    return { buff, met: providers.length > 0, providers };
  });

  const met = items.filter((i) => i.met).length;
  return { total: items.length, met, items };
}

// --- Crit damage: coverage calculator ---
//
// ESO critical damage caps at CRIT_CAP% total. Base is CRIT_BASE% (everyone),
// modelled as a group source. Crit comes from two buckets:
//   - group : anything that benefits the whole team — raid buffs and boss
//             debuffs alike (any one player provides/applies it → all benefit)
//   - self  : only that player (gear, mundus, CP, armor, crit-damage source,
//             race, class passive)
// Per player: effective crit = group + self; they "meet" the cap when that
// reaches CRIT_CAP. "Solo required" = CRIT_CAP - group (what each player must
// supply from their own sources). Source keys map to the existing master data;
// a few (Minor Force, Minor Brittle) are best-guess placeholders and are
// one-line edits here.
const CRIT_CAP = 125;
const CRIT_BASE = 50;

// Group sources: anything provided to the whole team (raid buffs as well as
// boss debuffs like Brittle/Catalyst). Detected anywhere on the team.
const CRIT_GROUP_SOURCES = [
  // Major Force (+20% crit dmg) shared with the whole group. Saxhleel Champion
  // and Grisly Gourmet grant it to allies; Aggressive Horn and Light's Champion
  // (skills) buff the group.
  { value: "major_force", label: "Major Force", pct: 20, detect: { gear: ["saxhleel_champion", "perfected_saxhleel_champion", "grisly_gourmet"], skills: ["aggressive_horn", "lights_champion"], masteries: ["ink_scribes_verve"] } },
  // Minor Force (+10% crit dmg) applied to group members by these sets (the
  // wearer of Grave Inevitability gets Major Force instead, but its grouped
  // allies get Minor Force). Velothi's self-only Minor Force stays in the self
  // list and is deduped per-player when the team already provides this.
  { value: "minor_force", label: "Minor Force", pct: 10, detect: { gear: ["twilight_remedy", "phoenix_moth_theurge", "grave_inevitability"] } },
  { value: "lucent_echoes", label: "Lucent Echoes", pct: 11, detect: { gear: ["lucent_echoes", "perfected_lucent_echoes"] } },
  // Minor Brittle (+10% crit dmg taken) is an enemy debuff: Rune of the
  // Colorless Pool and Glittering Goad both apply it to the target. (Baron
  // Zaudrus was previously listed here but only grants Ultimate, not Brittle.)
  { value: "minor_brittle", label: "Minor Brittle", pct: 10, detect: { gear: ["glittering_goad"], skills: ["rune_of_the_colorless_pool"] } },
  { value: "major_brittle", label: "Major Brittle", pct: 20, detect: { gear: ["nunatak"], masteries: ["tundras_maw"] } },
  // Elemental Catalyst applies 5% Critical Damage taken per distinct elemental
  // damage type (Flame/Frost/Shock) the wearer deals, up to 15% for all three.
  // `perElement` tells computeCritCoverage to scale the pct by the wearer's
  // catalystElements count (1-3) instead of using a flat value, so a build that
  // only runs 1 or 2 damage types is modelled correctly.
  { value: "elemental_catalyst", label: "Elemental Catalyst", perElement: true, detect: { gear: ["elemental_catalyst"] } },
];

// Critical Damage taken granted by each Elemental Catalyst weakness stack.
const ELEMENTAL_CATALYST_PER_ELEMENT = 5;

// clampCatalystElements(v): normalize a stored catalyst_elements value to the
// valid 1-3 range. Anything missing/invalid (including 0) falls back to 3 so a
// loadout without the field set behaves like the previous flat 15% bonus.
function clampCatalystElements(v) {
  const n = parseInt(v, 10);
  if (!Number.isFinite(n) || n < 1) return 3;
  return Math.min(3, n);
}

// Offensive Penetration the Arcanist "Splintered Secrets" passive grants per
// slotted Herald of the Tome ability (caps at 5 abilities = 6200).
const SPLINTERED_SECRETS_PER_SKILL = 1240;
const SPLINTERED_SECRETS_MAX_SKILLS = 5;
const SPLINTERED_SECRETS_DEFAULT_SKILLS = 2;

// clampSplinteredSecretsSkills(v): normalize a stored splintered_secrets_skills
// value to 0-5. Anything missing/invalid/negative falls back to 2 so a loadout
// without the field set behaves like the previous flat 2480 bonus.
function clampSplinteredSecretsSkills(v) {
  const n = parseInt(v, 10);
  if (!Number.isFinite(n) || n < 0) return SPLINTERED_SECRETS_DEFAULT_SKILLS;
  return Math.min(SPLINTERED_SECRETS_MAX_SKILLS, n);
}

// Offensive Penetration the "Force of Nature" Warfare CP star grants per
// negative status effect on the enemy (caps at 5 effects = 3300).
const FORCE_OF_NATURE_PER_STATUS = 660;
const FORCE_OF_NATURE_MAX_STATUS = 5;

// clampForceOfNatureStatus(v): normalize a stored force_of_nature_status value
// to 0-5. Anything missing/invalid/negative falls back to 5 (the full bonus).
function clampForceOfNatureStatus(v) {
  const n = parseInt(v, 10);
  if (!Number.isFinite(n) || n < 0) return FORCE_OF_NATURE_MAX_STATUS;
  return Math.min(FORCE_OF_NATURE_MAX_STATUS, n);
}

// Self sources (per player). Like group/target sources, each entry has a
// `detect` map of category → candidate keys, so a single buff can be detected
// multiple ways (e.g. gear OR a class passive). Supported categories:
//   gear | skills | masteries | skillLines | cp | mundus | race | classes |
//   classPassive (an array of { class, line }: matches `class` for a
//   non-subclassed player, or `line` for a subclassed one).
// A buff may appear in both the group source list and here; if the team already
// provides it, the self copy is skipped so it isn't double-counted (dedup is by
// `value`). Medium-armor Dexterity, the crit-damage source passives, and the
// Warden's Advanced Species passive are handled specially in playerSelfCrit
// (per-piece / MAX / per-slotted-Animal-Companion-skill).
const CRIT_SELF_SOURCES = [
  { value: "minor_force", label: "Minor Force (Velothi)", pct: 10, detect: { gear: ["velothi_ur_mages_amulet"] } },
  { value: "the_shadow", label: "The Shadow Mundus", pct: 18, detect: { mundus: ["the_shadow"] } },
  { value: "backstabber", label: "Backstabber", pct: 15, detect: { cp: ["backstabber"] } },
  { value: "fighting_finesse", label: "Fighting Finesse", pct: 10, detect: { cp: ["fighting_finesse"] } },
  { value: "harpooners_wading_kilt", label: "Harpooner's Wading Kilt", pct: 10, detect: { gear: ["harpooners_wading_kilt"] } },
  { value: "sul_xans_torment", label: "Sul-Xan's Torment", pct: 12, detect: { gear: ["sul_xans_torment"] } },
  { value: "feline_ambush", label: "Feline Ambush (Khajiit)", pct: 12, detect: { race: ["khajiit"] } },
  { value: "hemorrhage", label: "Hemorrhage (Nightblade)", pct: 10, detect: { classPassive: [{ class: "nightblade", line: "assassination" }] } },
  { value: "piercing_spear", label: "Piercing Spear (Templar)", pct: 10, detect: { classPassive: [{ class: "templar", line: "aedric_spear" }] } },
  { value: "glacial_presence", label: "Glacial Presence (Warden)", pct: 10, detect: { classPassive: [{ class: "warden", line: "winters_embrace" }] } },
  { value: "fated_fortune", label: "Fated Fortune (Arcanist)", pct: 12, detect: { classPassive: [{ class: "arcanist", line: "herald_of_the_tome" }] } },
  // Above and Beyond is a non-subclassed Nightblade class mastery. The PvE value
  // (25%) applies in trials; the "reduced to 5% against Battle Spirit" clause is
  // PvP-only. It also raises the player's max crit cap (see CRIT_CAP_BONUS_SOURCES).
  { value: "above_and_beyond", label: "Above and Beyond (Nightblade)", pct: 25, detect: { masteries: ["above_and_beyond"] } },
  // Crit-damage gear sets. Order's Wrath and Back-Alley Gourmand give a flat
  // bonus (Back-Alley requires an active food buff — effectively always up in
  // PvE). True-Sworn Fury and Mora Scribe's Thesis scale with combat/buff state;
  // we model their baselines (True-Sworn's unscaled 4%; Mora Scribe's at its 12%
  // Minor-Buff cap). Both regular and Perfected Mora Scribe's Thesis grant the
  // same crit bonus. Malacath's Band of Brutality is a net negative — it trades
  // 50% Critical Damage for raw damage — so its pct is negative and lowers the
  // player's total. All five are one-line value edits if the assumptions change.
  { value: "orders_wrath", label: "Order's Wrath", pct: 8, detect: { gear: ["orders_wrath"] } },
  { value: "back_alley_gourmand", label: "Back-Alley Gourmand (food)", pct: 13, detect: { gear: ["back_alley_gourmand"] } },
  { value: "true_sworn_fury", label: "True-Sworn Fury", pct: 4, detect: { gear: ["true_sworn_fury"] } },
  { value: "mora_scribes_thesis", label: "Mora Scribe's Thesis (12 Minor Buffs)", pct: 12, detect: { gear: ["mora_scribes_thesis", "perfected_mora_scribes_thesis"] } },
  { value: "malacaths_band_of_brutality", label: "Malacath's Band of Brutality", pct: -50, detect: { gear: ["malacaths_band_of_brutality"] } },
];

// Crit-cap bonuses: sources that raise a single player's maximum Critical Damage
// above CRIT_CAP. Above and Beyond grants +30% to the personal cap (125 → 155),
// detected the same way as its self source. Applied in playerCritCap.
const CRIT_CAP_BONUS_SOURCES = [
  { value: "above_and_beyond", label: "Above and Beyond (Nightblade)", pct: 30, detect: { masteries: ["above_and_beyond"] } },
];

// CRIT_MEDIUM_PER_PIECE: Critical Damage from the Medium Armor Dexterity passive
// per equipped medium piece (6 pieces = 12%, matching the reference table).
const CRIT_MEDIUM_PER_PIECE = 2;

// Warden's Advanced Species (Animal Companions passive): +5% Critical Damage for
// each slotted Animal Companion ability. This scales with the player's bar, so
// it can't be a flat CRIT_SELF_SOURCES entry; it's counted in playerSelfCrit by
// matching slotted skills against the Animal Companions line. Slotting those
// skills already requires the line (base Warden or subclassed), so the count
// itself is the gate — no separate class/line check is needed.
const ADVANCED_SPECIES_PER_SKILL = 5;
const ANIMAL_COMPANION_SKILLS = new Set(
  SKILLS.filter((s) => s.group === "Warden · Animal Companions").map((s) => s.value)
);

// playerCritContext(player, loadout): the per-player inputs the calc reads.
// Build keys honor subclassing (subclassed → skill lines; otherwise masteries).
function playerCritContext(player, loadout) {
  const lo = loadout || {};
  const ctx = {
    slot: player.slot,
    gear: new Set(lo.gear || []),
    skills: new Set(lo.skills || []),
    cpBlue: new Set(lo.cp_blue || []),
    critDmg: new Set(lo.crit_dmg || []),
    mundus: lo.mundus || "",
    armorMedium: Number(lo.armor_medium) || 0,
    // Elemental Catalyst element count (1-3); defaults to 3 (full 15%) when unset
    // so existing loadouts keep the previous behavior. Clamped to a valid count.
    catalystElements: clampCatalystElements(lo.catalyst_elements),
    race: player.race || "",
    isSubclassed: !!player.subclassed,
    class: player.class || "",
    masteries: new Set(),
    skillLines: new Set(),
  };
  if (player.subclassed) {
    [player.skill_line_1, player.skill_line_2, player.skill_line_3].forEach((v) => {
      if (v) ctx.skillLines.add(v);
    });
  } else {
    [player.mastery_1, player.mastery_2].forEach((v) => {
      if (v) ctx.masteries.add(v);
    });
  }
  return ctx;
}

// ctxDetectHit(c, cat, keys): does a single player's context satisfy one
// detection category? Returns the matched key (truthy) or null. Shared by the
// group/target (team-wide) and self (per-player) detection so a `detect` map
// behaves identically everywhere. Categories:
//   gear/skills/masteries/skillLines/cp → Set membership;
//   classes/race/mundus → scalar equality against the candidate list;
//   classPassive → array of { class, line }: `line` for a subclassed player,
//   otherwise `class`.
function ctxDetectHit(c, cat, keys) {
  if (cat === "classes") return c.class && keys.includes(c.class) ? c.class : null;
  if (cat === "race") return c.race && keys.includes(c.race) ? c.race : null;
  if (cat === "mundus") return c.mundus && keys.includes(c.mundus) ? c.mundus : null;
  if (cat === "classPassive") {
    for (const k of keys) {
      const ok = c.isSubclassed ? c.skillLines.has(k.line) : c.class === k.class;
      if (ok) return c.isSubclassed ? k.line : k.class;
    }
    return null;
  }
  let have;
  if (cat === "gear") have = c.gear;
  else if (cat === "skills") have = c.skills;
  else if (cat === "masteries") have = c.masteries;
  else if (cat === "skillLines") have = c.skillLines;
  else if (cat === "cp") have = c.cpBlue;
  else return null;
  return keys.find((k) => have.has(k)) || null;
}

// ctxMatchesDetect(c, detect): true if the player satisfies the source any way
// it can be applied (any category, any candidate key).
function ctxMatchesDetect(c, detect) {
  for (const [cat, keys] of Object.entries(detect || {})) {
    if (ctxDetectHit(c, cat, keys)) return true;
  }
  return false;
}

// critSourceProviders(contexts, detect): which players satisfy a group/target
// source. `detect` maps a category to candidate keys (see ctxDetectHit).
function critSourceProviders(contexts, detect) {
  const providers = [];
  contexts.forEach((c) => {
    for (const [cat, keys] of Object.entries(detect)) {
      const hit = ctxDetectHit(c, cat, keys);
      if (hit) {
        providers.push({ slot: c.slot, category: cat, key: hit });
        break;
      }
    }
  });
  return providers;
}

// playerSelfCrit(ctx, provided): the Critical Damage a single player supplies,
// with a labelled breakdown. `provided` is a Set of buff `value`s already
// counted team-wide (group/target); self sources sharing one of those values
// are skipped so the same buff isn't double-counted. Duplicate self entries for
// one buff are also collapsed. Weapon line uses MAX (only the active bar applies).
function playerSelfCrit(ctx, provided) {
  const skip = provided || new Set();
  const counted = new Set();
  const sources = [];
  let total = 0;
  CRIT_SELF_SOURCES.forEach((s) => {
    if (skip.has(s.value) || counted.has(s.value)) return;
    if (ctxMatchesDetect(ctx, s.detect)) {
      counted.add(s.value);
      sources.push({ label: s.label, pct: s.pct });
      total += s.pct;
    }
  });

  if (ctx.armorMedium > 0) {
    const pct = ctx.armorMedium * CRIT_MEDIUM_PER_PIECE;
    sources.push({ label: `Dexterity (${ctx.armorMedium}x medium)`, pct });
    total += pct;
  }

  let animalCompanions = 0;
  ctx.skills.forEach((k) => {
    if (ANIMAL_COMPANION_SKILLS.has(k)) animalCompanions += 1;
  });
  if (animalCompanions > 0) {
    const pct = animalCompanions * ADVANCED_SPECIES_PER_SKILL;
    sources.push({ label: `Advanced Species (${animalCompanions}x Animal Companion)`, pct });
    total += pct;
  }

  let critDmgMax = 0;
  let critDmgName = "";
  ctx.critDmg.forEach((w) => {
    const cfg = CRIT_DMG_BY_KEY[w];
    if (cfg && (cfg.critPct || 0) > critDmgMax) {
      critDmgMax = cfg.critPct;
      critDmgName = cfg.label;
    }
  });
  if (critDmgMax > 0) {
    sources.push({ label: critDmgName, pct: critDmgMax });
    total += critDmgMax;
  }

  return { total, sources };
}

// playerCritCap(ctx): the player's maximum Critical Damage. Defaults to
// CRIT_CAP; cap-raising sources (e.g. Above and Beyond's +30%) add on top.
function playerCritCap(ctx) {
  let cap = CRIT_CAP;
  CRIT_CAP_BONUS_SOURCES.forEach((s) => {
    if (ctxMatchesDetect(ctx, s.detect)) cap += s.pct;
  });
  return cap;
}

// computeCritCoverage(players, loadoutBySlot): evaluate the roster + the
// selected encounter. Returns the group total (team buffs + boss debuffs), the
// solo requirement, the detected group sources (with providers), and per-player
// results ({ slot, self, total, met, deficit, sources }).
function computeCritCoverage(players, loadoutBySlot) {
  const contexts = (players || []).map((p) =>
    playerCritContext(p, (loadoutBySlot && loadoutBySlot[p.slot]) || {})
  );

  // Buff values provided team-wide. A self source sharing one of these is
  // skipped per player so a buff present in both buckets isn't double-counted.
  const provided = new Set();

  let group = CRIT_BASE;
  const groupSources = [];
  CRIT_GROUP_SOURCES.forEach((s) => {
    const providers = critSourceProviders(contexts, s.detect);
    if (!providers.length) return;
    let pct = s.pct;
    let label = s.label;
    if (s.perElement) {
      // Elemental Catalyst scales with how many elemental damage types the
      // wearer applies. With multiple wearers, the highest count wins (the boss
      // accrues the union of their weakness stacks).
      const elements = Math.max(
        ...providers.map((p) => {
          const ctx = contexts.find((c) => c.slot === p.slot);
          return ctx ? ctx.catalystElements : 3;
        })
      );
      pct = elements * ELEMENTAL_CATALYST_PER_ELEMENT;
      label = `${s.label} (${elements}x element${elements === 1 ? "" : "s"})`;
    }
    group += pct;
    provided.add(s.value);
    groupSources.push({ value: s.value, label, pct, providers });
  });

  const soloRequired = Math.max(0, CRIT_CAP - group);

  const playerResults = contexts.map((ctx) => {
    const self = playerSelfCrit(ctx, provided);
    const total = group + self.total;
    const cap = playerCritCap(ctx);
    const met = total >= cap;
    return {
      slot: ctx.slot,
      self: self.total,
      sources: self.sources,
      total,
      cap,
      met,
      deficit: met ? 0 : cap - total,
    };
  });

  return {
    cap: CRIT_CAP,
    base: CRIT_BASE,
    group,
    soloRequired,
    groupSources,
    players: playerResults,
  };
}

// --- Penetration: coverage calculator ---
//
// Mirrors the crit model. A player wants their Offensive Penetration to reach
// the target's Resistance (`PEN_TARGET`, the standard trial value). Penetration
// comes from two buckets:
//   - group : team-wide debuffs/buffs (Breaches, Alkosh, Crusher, …) — any one
//             player providing it benefits everyone
//   - self  : only that player (CP, light armor, mundus, race, class passive,
//             sets, and the free-form `pen_extra` flat sources)
// Per player: total = group + self; they "meet" it when total >= PEN_TARGET.
// "Self required" = PEN_TARGET - group. Several keys (Breaches, Runic Sunder)
// are best-guess placeholders — one-line edits here.
const PEN_TARGET = 18200;
const PEN_LIGHT_PER_PIECE = 939; // Light Armor (Concentration) penetration per light piece.
const PEN_ARENA_ONE_PIECE = 1190; // 1pc bonus from an arena weapon.

// Group/target sources (detected anywhere on the team). `detect` maps a category
// (gear/skills/masteries/skillLines/classes) to candidate keys.
const PEN_GROUP_SOURCES = [
  // Major Breach (−5948 enemy Resistance) — applied to the target, so the whole
  // group benefits. Provided by the Puncture line (Pierce Armor/Puncture/Ransack),
  // the Elemental Drain line (Elemental Drain/Weakness to Elements/Elemental
  // Susceptibility), and Crushing Weapon.
  { value: "major_breach", label: "Major Breach", pen: 5948, detect: { skills: ["pierce_armor", "puncture", "ransack", "elemental_drain", "weakness_to_elements", "elemental_susceptibility", "crushing_weapon"] } },
  // Minor Breach (−2974 enemy Resistance). Only Pierce Armor (of the Puncture
  // morphs) also applies Minor Breach; Deep Fissure and Sunderflame apply it too.
  // (Elemental Drain was previously here but grants MAJOR Breach, not Minor.)
  { value: "minor_breach", label: "Minor Breach", pen: 2974, detect: { skills: ["pierce_armor", "deep_fissure", "sunderflame"] } },
  { value: "alkosh", label: "Roar of Alkosh", pen: 6000, detect: { gear: ["roar_of_alkosh"] } },
  { value: "crimson_oath", label: "Crimson Oath's Rive", pen: 3541, detect: { gear: ["crimson_oaths_rive"] } },
  { value: "tremorscale", label: "Tremorscale", pen: 2640, detect: { gear: ["tremorscale"] } },
  { value: "runic_sunder", label: "Runic Sunder", pen: 2200, detect: { skills: ["runic_sunder"] } },
  { value: "crystal_weapon", label: "Crystal Weapon", pen: 1000, detect: { skills: ["crystal_weapon"] } },
  // Anthelmir's Construct reduces the target's Armor (a team-wide pen debuff)
  // by an amount that scales off the wearer's higher Weapon/Spell Damage.
  // `perWeaponDamage` tells computePenCoverage to derive the value from the
  // wearer's entered weapon damage (× ANTHELMIR_PEN_PER_WD) instead of a flat
  // `pen`. With multiple wearers the highest contribution wins.
  { value: "anthelmirs_construct", label: "Anthelmir's Construct", perWeaponDamage: true, detect: { gear: ["anthelmirs_construct"] } },
];

// Anthelmir's Construct armor reduction per point of (higher) Weapon/Spell
// Damage. The set's base tooltip is 400 Armor and explicitly scales off Weapon/
// Spell Damage, but ZOS doesn't publish the coefficient — this is a best-guess
// placeholder (≈0.5 → ~2000 pen at 4000 WD). One-line edit to tune once the
// exact scaling is confirmed.
const ANTHELMIR_PEN_PER_WD = 0.5;

// Self sources (per player). `type` ∈ cp | gear | mundus | race | classPassive
// (class for non-subclassed; the linked skill line for subclassed). Light armor,
// arena 1pc, and self-bucket `pen_extra` are handled specially in playerSelfPen.
// A source with `scaled` contributes `scaled.per × count`, where `count` is the
// per-loadout value in `ctx[scaled.ctxKey]` (clamped on input). `scaled.unit`
// labels the count in the breakdown. Sources without `scaled` add a flat `pen`.
const PEN_SELF_SOURCES = [
  { value: "piercing", label: "Piercing (CP)", pen: 700, type: "cp", key: "piercing" },
  { value: "force_of_nature", label: "Force of Nature (CP)", type: "cp", key: "force_of_nature", scaled: { per: FORCE_OF_NATURE_PER_STATUS, ctxKey: "forceOfNatureStatus", unit: "status effect" } },
  { value: "velothi", label: "Velothi Ur-Mage's Amulet", pen: 1650, type: "gear", key: "velothi_ur_mages_amulet" },
  { value: "the_lover", label: "The Lover Mundus", pen: 2744, type: "mundus", key: "the_lover" },
  // Splintered Secrets scales with the number of slotted Herald of the Tome
  // abilities (1240 each), read from the loadout.
  { value: "splintered_secrets", label: "Splintered Secrets (Arcanist)", type: "classPassive", class: "arcanist", line: "herald_of_the_tome", scaled: { per: SPLINTERED_SECRETS_PER_SKILL, ctxKey: "splinteredSecretsSkills", unit: "skill" } },
  // Dismember is a Necromancer Grave Lord passive, so it's a self source: it
  // applies to a pure (non-subclassed) Necromancer, or to a subclassed player
  // who has slotted the Grave Lord skill line.
  { value: "dismember", label: "Dismember (Necromancer)", pen: 3271, type: "classPassive", class: "necromancer", line: "grave_lord" },
  { value: "wood_elf", label: "Hunter's Eye (Wood Elf)", pen: 950, type: "race", key: "bosmer" },
];

// playerPenContext(player, loadout): the per-player inputs the pen calc reads.
function playerPenContext(player, loadout) {
  const lo = loadout || {};
  const ctx = {
    slot: player.slot,
    gear: new Set(lo.gear || []),
    skills: new Set(lo.skills || []),
    cpBlue: new Set(lo.cp_blue || []),
    penExtra: new Set(lo.pen_extra || []),
    penExtraCounts: countKeys(lo.pen_extra || []),
    mundus: lo.mundus || "",
    armorLight: Number(lo.armor_light) || 0,
    // Higher of Weapon/Spell Damage; drives Anthelmir's Construct's pen scaling.
    weaponDamage: Math.max(0, Number(lo.weapon_damage) || 0),
    // Slotted Herald of the Tome abilities; scales Splintered Secrets pen.
    splinteredSecretsSkills: clampSplinteredSecretsSkills(lo.splintered_secrets_skills),
    // Negative status effects on the enemy; scales Force of Nature CP pen.
    forceOfNatureStatus: clampForceOfNatureStatus(lo.force_of_nature_status),
    race: player.race || "",
    isSubclassed: !!player.subclassed,
    class: player.class || "",
    masteries: new Set(),
    skillLines: new Set(),
  };
  if (player.subclassed) {
    [player.skill_line_1, player.skill_line_2, player.skill_line_3].forEach((v) => {
      if (v) ctx.skillLines.add(v);
    });
  } else {
    [player.mastery_1, player.mastery_2].forEach((v) => {
      if (v) ctx.masteries.add(v);
    });
  }
  return ctx;
}

// playerSelfPen(ctx): the penetration a single player supplies, with a labelled
// breakdown.
function playerSelfPen(ctx) {
  const sources = [];
  let total = 0;
  PEN_SELF_SOURCES.forEach((s) => {
    let present = false;
    if (s.type === "cp") present = ctx.cpBlue.has(s.key);
    else if (s.type === "gear") present = ctx.gear.has(s.key);
    else if (s.type === "mundus") present = ctx.mundus === s.key;
    else if (s.type === "race") present = ctx.race === s.key;
    else if (s.type === "classPassive") {
      present = ctx.isSubclassed ? ctx.skillLines.has(s.line) : ctx.class === s.class;
    }
    if (!present) return;
    // `scaled` sources (Splintered Secrets, Force of Nature) multiply a per-unit
    // pen by a per-loadout count; everything else is a flat contribution.
    if (s.scaled) {
      const n = ctx[s.scaled.ctxKey] || 0;
      if (n <= 0) return;
      const pen = s.scaled.per * n;
      sources.push({ label: `${s.label} (${n} ${s.scaled.unit}${n === 1 ? "" : "s"})`, pen });
      total += pen;
      return;
    }
    sources.push({ label: s.label, pen: s.pen });
    total += s.pen;
  });

  if (ctx.armorLight > 0) {
    const pen = ctx.armorLight * PEN_LIGHT_PER_PIECE;
    sources.push({ label: `Light Armor (${ctx.armorLight}x light)`, pen });
    total += pen;
  }

  const hasArena = [...ctx.gear].some((k) => (GEAR_BY_KEY[k] || {}).group === "Arena Weapons");
  if (hasArena) {
    sources.push({ label: "Arena weapon (1pc)", pen: PEN_ARENA_ONE_PIECE });
    total += PEN_ARENA_ONE_PIECE;
  }

  PEN_EXTRA_SOURCES.forEach((s) => {
    if (s.bucket !== "self") return;
    const cap = s.maxStack || 1;
    const stacks = Math.min(ctx.penExtraCounts[s.value] || 0, cap);
    if (stacks <= 0) return;
    const pen = s.pen * stacks;
    sources.push({ label: stacks > 1 ? `${s.label} ×${stacks}` : s.label, pen });
    total += pen;
  });

  return { total, sources };
}

// computePenCoverage(players, loadoutBySlot): evaluate the roster + the selected
// encounter. Returns the group total, the self requirement, the detected group
// sources (with providers), and per-player results
// ({ slot, self, total, met, deficit, sources }).
function computePenCoverage(players, loadoutBySlot) {
  const contexts = (players || []).map((p) =>
    playerPenContext(p, (loadoutBySlot && loadoutBySlot[p.slot]) || {})
  );

  let group = 0;
  const groupSources = [];
  PEN_GROUP_SOURCES.forEach((s) => {
    const providers = critSourceProviders(contexts, s.detect);
    if (!providers.length) return;
    let pen = s.pen;
    let label = s.label;
    if (s.perWeaponDamage) {
      // Scale off the highest weapon damage among wearers (the strongest axe
      // debuff is what lands on the boss).
      const wd = Math.max(
        ...providers.map((p) => {
          const ctx = contexts.find((c) => c.slot === p.slot);
          return ctx ? ctx.weaponDamage : 0;
        })
      );
      pen = Math.round(wd * ANTHELMIR_PEN_PER_WD);
      label = `${s.label} (${pen} from ${wd} WD)`;
    }
    if (pen > 0) {
      group += pen;
      groupSources.push({ value: s.value, label, pen, providers });
    }
  });
  // Group-bucket pen_extra (e.g. Crusher): counts once if any player runs it.
  PEN_EXTRA_SOURCES.filter((s) => s.bucket === "group").forEach((s) => {
    const providers = contexts
      .filter((c) => c.penExtra.has(s.value))
      .map((c) => ({ slot: c.slot, category: "pen_extra", key: s.value }));
    if (providers.length) {
      group += s.pen;
      groupSources.push({ value: s.value, label: s.label, pen: s.pen, providers });
    }
  });

  const selfRequired = Math.max(0, PEN_TARGET - group);

  const playerResults = contexts.map((ctx) => {
    const self = playerSelfPen(ctx);
    const total = group + self.total;
    const met = total >= PEN_TARGET;
    return {
      slot: ctx.slot,
      self: self.total,
      sources: self.sources,
      total,
      met,
      deficit: met ? 0 : PEN_TARGET - total,
    };
  });

  return {
    target: PEN_TARGET,
    group,
    selfRequired,
    groupSources,
    players: playerResults,
  };
}
