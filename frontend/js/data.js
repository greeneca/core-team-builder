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
    { value: "abyssal_emergence", label: "Abyssal Emergence", desc: "Dive deep into the abyss where power is yours to claim. Activating an Arcanist Ultimate immediately generates maximum Crux (3) and grants you 666 Weapon and Spell Damage for 15 seconds." },
    { value: "fate_realigned", label: "Fate Realigned", desc: "Be the hand that guides the weave of fate. Upgrades rank 2 of Implacable Outcome to generate maximum Crux (3), up to once every 15 seconds. After this effect triggers you gain 300 Weapon and Spell Damage for 25 seconds." },
    { value: "unbound_potential", label: "Unbound Potential", desc: "Surpass your limits in the fieldwork of battle. Upgrades rank 2 of Fated Fortune to also increase your damage done by 30% (halved against players) for the duration." },
    { value: "erudites_rigor", label: "Erudite's Rigor", desc: "Master the archaic and forge new runes of conflict. Upgrades Fatewoven Armor to also grant 300 Magicka and Stamina when you take damage, increasing by 100 per Crux you have active. When this effect activates you also gain 1 Ultimate. This effect can occur once every 2 seconds." },
    { value: "ink_scribes_verve", label: "Ink-Scribe's Verve", desc: "Illuminate body and mind with glimmering ink. While in combat, when you heal or overheal a total of 75,000 Health, you generate a Crux." },
  ],
  dragonknight: [
    { value: "lead_from_the_front", label: "Lead From the Front", desc: "Let others be swept along in the wake of your flame. Activating rank 2 of The Storm Voice grants you and group members within 28 meters of you Major Berserk and Major Protection for 1 second per 15 Ultimate spent, increasing damage done and reducing damage taken by 10%." },
    { value: "resolute_defense", label: "Resolute Defense", desc: "You are as unyielding as a mountain. Each second you remain Bracing (holding block), you increase the amount of damage you block by 6%, up to 5 times (30% total). Blocking damage has a 20% chance to restore 500 Stamina. This effect can occur once every block cost." },
    { value: "wildfire_embers", label: "Wildfire Embers", desc: "Fire forged within can never be extinguished. Fan the flames. When your Dragonknight damage over time effects end, you apply Wildfire Embers to the target, dealing 2499 Flame Damage over 12 seconds. This effect stacks up to 12 times and increases in damage by 25% per stack." },
    { value: "booming_voice", label: "Booming Voice", desc: "Let your voice echo across the peaks. Activating rank 2 of The Storm Voice now also grants 5 Health, Magicka and Stamina Recovery for every Ultimate spent for 10 seconds." },
    { value: "inexorable_descent", label: "Inexorable Descent", desc: "Be as the landslide. Let nothing stop you. Improves your Landslide passive to increase the potency of your damage done, healing done and damage shield strength by 1% per stack." },
  ],
  necromancer: [
    { value: "nothing_wasted", label: "Nothing Wasted", desc: "As an abattoir of death, no remnants of life are wasted. Upgrades rank 2 of Corpse Consumption to also grant a stack of Nothing Wasted for 10 seconds, which increases your Max Health by 2% and Weapon and Spell Damage by 2% per stack, up to 10 times. Stacks decay three at a time instead of all at once." },
    { value: "malevolent_promise", label: "Malevolent Promise", desc: "Death's knell is a familiar song. Share it with your enemies. Also upgrades rank 2 of Corpse Consumption to mark the closest non-player enemy to you with death's touch for 6 seconds, allowing you to use a corpse consuming ability on them. Consuming or replacing this effect triggers rank 2 of Death Gleaning. This effect can occur once every 2 seconds." },
    { value: "cycle_unending", label: "Cycle Unending", desc: "The cycle of life and death continues: you will live, others will die. Upgrades rank 2 of Reusable Parts to grant Cycle Unending for 25 seconds, increasing your damage done by 1% for every 1% Health you have more than your target. The damage bonus caps at 25% (12% against players)." },
    { value: "pound_of_flesh", label: "Pound of Flesh", desc: "Knock upon death's door and demand your due. When you take damage you have a 1% chance to heal 2745 Health and restore 5% of your missing Stamina, up to once per second. This chance increases by 1% for every missing Health percent you have and the healing is based off your Max Health." },
    { value: "veils_forfeit", label: "Veil's Forfeit", desc: "Call souls back from an early grave. Only you may grant them release. While you are in combat, directly healing a target below 50% Health relinquishes them from death's clutches and allows you to use a corpse consuming ability against them within 10 seconds, up to once every second. Consuming or replacing this effect grants the target 2 Ultimate." },
  ],
  nightblade: [
    { value: "nocturnal_inspiration", label: "Nocturnal Inspiration", desc: "The Night Mistress rewards those who strike without reservation. Upgrades rank 2 of Hemorrhage to have a chance to generate 2 Ultimate. This effect can occur once every 0.3 seconds and the chance is equal to your Weapon Critical chance." },
    { value: "an_eye_for_exploitation", label: "An Eye for Exploitation", desc: "Every strike, every stab, is a step towards execution. Increases your Weapon and Spell Damage by up to 1250, based on your target's missing Health. Also reduces your damage taken by up to 12%, based on your attacker's missing Health." },
    { value: "above_and_beyond", label: "Above and Beyond", desc: "An assassin is only as good as their reputation. Increases your Critical Damage and Healing by 15% (halved against players). Also increases your maximum Critical Damage and Healing cap by 25%, raising it from 125% to 150%." },
    { value: "cutthroats_focus", label: "Cutthroat's Focus", desc: "This duel will be their last. For you, it's just another dance. Activating a Nightblade ability while Bracing (holding block) causes you to dodge attacks for 0.3 seconds. Whenever you dodge an attack, your attacker's psyche suffers, increasing their damage taken by 5% for 5 seconds." },
    { value: "share_the_spoils", label: "Share the Spoils", desc: "Secrets and coin are tools as sharp as the daggers you wield. Upgrades rank 2 of Transfer to grant the closest 4 group members 250 Magicka and Stamina, 1 Ultimate and doubles the Ultimate you gain when it activates (4, up from 2)." },
  ],
  sorcerer: [
    { value: "conservation_of_energy", label: "Conservation of Energy", desc: "Spellcasting is second nature to you. A skill honed by tireless study. Upgrades rank 2 of Blood Magic to work with any ability that has a cost, and restores 400 Magicka and 389 Stamina when it activates. Scales off your Max Magicka and Stamina. Cost-per-tick abilities are excluded." },
    { value: "font_of_power", label: "Font of Power", desc: "Within you is a wellspring of near unlimited power. Upgrades rank 2 of Exploitation to work with any Sorcerer ability and increases your Weapon and Spell Damage by 11% for 10 seconds. Increases by 1% for every 1750 Max Magicka or Stamina you have, whichever is higher." },
    { value: "static_reverberation", label: "Static Reverberation", desc: "The battlefield crackles with untapped potential. Make use of it. When you deal damage, you have a 1% chance for every 1% missing Health the target has to deal 747 Shock Damage, up to once every 0.2 seconds. The chance is divided by one plus every permanent pet you have active." },
    { value: "calculated_defense", label: "Calculated Defense", desc: "Success is a certainty when nothing is left to chance. While beginning to use a Sorcerer ability or an ability with a cast time, you gain a damage shield for 0.5 seconds that can absorb 7127 damage. This effect is based off your Max Health. If the shield does not break, you and nearby group members gain 3% Weapon and Spell Damage for 20 seconds." },
    { value: "sphere_of_influence", label: "Sphere of Influence", desc: "Magicka extends your reach, a benefit to those who fight beside you. Casting a damage shield on yourself or an ally grants 150 Health, Magicka and Stamina Recovery and an additional shield for 4 seconds that absorbs up to 3239 damage. The shield scales off the higher of your Max Health or Max Magicka and is capped at 25% of the target's Max Health." },
  ],
  templar: [
    { value: "bastion_of_light", label: "Bastion of Light", desc: "Light consecrates the ground on which you tread. Sacred Ground is now also applied while you are in your own Nova and Spear Shards, and while Radial Sweep and Solar Barrage are active. While Sacred Ground is active, you heal for 1498 Health every 1 second. If you are at full Health after being healed from this effect while in combat, you also gain 2 Ultimate." },
    { value: "devout_guardian", label: "Devout Guardian", desc: "Radiant is the shield that pushes back the dark. While Sacred Ground is active, you gain a damage shield for 6 seconds, up to once every 6 seconds. The shield absorbs up to 3747 damage and provides 300 Health, Magicka and Stamina Recovery while active. If the shield breaks, you gain 10 Ultimate." },
    { value: "bright_harbinger", label: "Bright Harbinger", desc: "Light's banner instills hope. You are its bearer. Upgrades rank 2 of Illuminate to also grant Bright Harbinger for the duration, which grants 300 Weapon and Spell Damage to allies and doubles for you (600)." },
    { value: "judgments_brand", label: "Judgment's Brand", desc: "Judgement follows in the wake of your radiance. When rank 2 of Burning Light deals damage, your Templar abilities gain 1250 damage done for 3.1 seconds. Halved against players (625)." },
    { value: "steadfast_candescence", label: "Steadfast Candescence", desc: "Your conviction is unwavering. Light gives no quarter. Sacred Ground now activates and refreshes itself while Bracing (holding block). Increases the amount of damage you can block by 20% while stationary." },
  ],
  warden: [
    { value: "tundras_maw", label: "Tundra's Maw", desc: "Expose your enemies to unrelenting cold. Applying Chill to an enemy also applies Major Brittle for 2 seconds, increasing their Critical Damage taken by 20%." },
    { value: "wild_adaptation", label: "Wild Adaptation", desc: "The battlefield is your grove. Tend to it. Gain 333 Weapon and Spell Damage for each status effect on your target, up to a maximum of 1665 (5 status effects). This can also apply when targeting friendly players." },
    { value: "glacial_obstinance", label: "Glacial Obstinance", desc: "Tap into the wellspring of permafrost's resolve. Upgrades Bond with Nature to also activate when casting Winter's Embrace abilities and to grant 15% Weapon and Spell Damage for 10 seconds if you are at full Health after the heal." },
    { value: "green_keepers_hide", label: "Green-Keeper's Hide", desc: "The twisting of seasons has tempered your will. Reduce your damage taken by 3% for every status effect active on your attacker, up to a maximum of 15% (5 status effects)." },
    { value: "bountiful_harvest", label: "Bountiful Harvest", desc: "Share in the spoils of nature's abundance. Upgrades rank 2 of Nature's Gift to grant the healed target Major Heroism for 4.5 seconds and an additional 250 Magicka and Stamina." },
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

// --- Crit-damage inputs: weapon lines, mundus, blue (Warfare) CP ---
//
// These are the per-encounter, per-player inputs the crit calculator needs that
// aren't otherwise tracked. `critPct` (when present) is the Critical Damage the
// source contributes; the crit model below reads it.

// Weapon lines a player can run (one per bar). Only Dual Wield and Two-Handed
// carry a Critical Damage passive; the rest are listed so the picker is useful.
const WEAPON_LINES = [
  { value: "dual_wield", label: "Dual Wield", critPct: 12, desc: "Twin Blade and Blunt passive: increases Critical Damage by 12% while a Dual Wield weapon is equipped." },
  { value: "two_handed", label: "Two-Handed", critPct: 16, desc: "Heavy Weapons passive: increases Critical Damage by 16% while a Two-Handed weapon is equipped." },
  { value: "one_hand_and_shield", label: "One Hand and Shield", critPct: 0, desc: "Defensive weapon line. No Critical Damage passive." },
  { value: "bow", label: "Bow", critPct: 0, desc: "Ranged weapon line. No Critical Damage passive." },
  { value: "destruction_staff", label: "Destruction Staff", critPct: 0, desc: "Magicka damage weapon line. No Critical Damage passive." },
  { value: "restoration_staff", label: "Restoration Staff", critPct: 0, desc: "Healing weapon line. No Critical Damage passive." },
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
];

// Flat lists / lookups for the new inputs.
const WEAPON_BY_KEY = Object.fromEntries(WEAPON_LINES.map((w) => [w.value, w]));
const MUNDUS_BY_KEY = Object.fromEntries(MUNDUS_STONES.map((m) => [m.value, m]));
const BLUE_CP_BY_KEY = Object.fromEntries(BLUE_CP.map((c) => [c.value, c]));
const WEAPON_BY_LABEL = Object.fromEntries(WEAPON_LINES.map((w) => [w.label.toLowerCase(), w]));
const BLUE_CP_BY_LABEL = Object.fromEntries(BLUE_CP.map((c) => [c.label.toLowerCase(), c]));

function weaponLabel(key) { const w = WEAPON_BY_KEY[key]; return w ? w.label : key; }
function weaponDesc(key) { const w = WEAPON_BY_KEY[key]; return w && w.desc ? w.desc : ""; }
function weaponByLabel(label) { return WEAPON_BY_LABEL[String(label || "").trim().toLowerCase()] || null; }
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
  { value: "sharpened", label: "Sharpened (weapon trait)", pen: 1638, bucket: "self", desc: "Sharpened weapon trait: increases your Offensive Penetration (both weapons, gold quality)." },
  { value: "mace_maul", label: "Mace / Maul", pen: 1487, bucket: "self", desc: "Mace (1H) or Maul (2H) weapon: ignores a portion of the target's Resistance." },
  { value: "set_piece_bonuses", label: "Set-piece bonuses", pen: 1487, bucket: "self", desc: "Flat Offensive Penetration from set 2–4 piece bonuses not otherwise modeled." },
];

const PEN_EXTRA_BY_KEY = Object.fromEntries(PEN_EXTRA_SOURCES.map((p) => [p.value, p]));
const PEN_EXTRA_BY_LABEL = Object.fromEntries(PEN_EXTRA_SOURCES.map((p) => [p.label.toLowerCase(), p]));
function penExtraLabel(key) { const p = PEN_EXTRA_BY_KEY[key]; return p ? p.label : key; }
function penExtraDesc(key) { const p = PEN_EXTRA_BY_KEY[key]; return p && p.desc ? p.desc : ""; }
function penExtraByLabel(label) { return PEN_EXTRA_BY_LABEL[String(label || "").trim().toLowerCase()] || null; }

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
const WEAPON_SELECT_GROUPS = [{ group: null, items: WEAPON_LINES }];
const BLUE_CP_SELECT_GROUPS = [{ group: null, items: BLUE_CP }];
const PEN_EXTRA_SELECT_GROUPS = [{ group: null, items: PEN_EXTRA_SOURCES }];

// Master tables keyed by loadout type, so UI code can stay generic. cp_blue and
// weapons reuse the same chip/searchable-select machinery as gear/skills.
const LOADOUT_TYPES = {
  gear: { items: GEAR_SETS, groups: GEAR_GROUPS, byLabel: gearByLabel, label: gearLabel, desc: gearDesc, addPlaceholder: "Search gear set…" },
  skills: { items: SKILLS, groups: SKILL_SELECT_GROUPS, byLabel: skillByLabel, label: skillLabel, desc: skillDesc, addPlaceholder: "Search skill…" },
  potions: { items: POTIONS, groups: POTION_SELECT_GROUPS, byLabel: potionByLabel, label: potionLabel, desc: potionDesc, addPlaceholder: "Search potion…" },
  cp_blue: { items: BLUE_CP, groups: BLUE_CP_SELECT_GROUPS, byLabel: cpBlueByLabel, label: cpBlueLabel, desc: cpBlueDesc, addPlaceholder: "Search blue CP star…" },
  weapons: { items: WEAPON_LINES, groups: WEAPON_SELECT_GROUPS, byLabel: weaponByLabel, label: weaponLabel, desc: weaponDesc, addPlaceholder: "Add weapon line…" },
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
// of its sources. The source keys below are SENSIBLE PLACEHOLDERS — adjust them
// to match the exact ESO sources as needed (the structure stays the same).
const BUFFS = [
  { value: "major_resolve", label: "Major Resolve", desc: "Increases Physical and Spell Resistance.",
    sources: { skills: ["boundless_storm"] } },
  { value: "minor_toughness", label: "Minor Toughness", desc: "Increases Max Health (Undaunted abilities slotted).",
    sources: { skills: ["inner_rage", "energy_orb"] } },
  { value: "major_brutality_sorcery", label: "Major Brutality and Sorcery", desc: "Increases Weapon and Spell Damage.",
    sources: { skills: ["hurricane", "degeneration"], potions: ["weapon_power_potion", "spell_power_potion"] } },
  { value: "minor_brutality_sorcery", label: "Minor Brutality or Minor Sorcery", desc: "Increases Weapon or Spell Damage.",
    sources: { potions: ["weapon_power_potion", "spell_power_potion"] } },
  { value: "minor_savagery_prophecy", label: "Minor Savagery or Prophecy", desc: "Increases Weapon or Spell Critical.",
    sources: { skills: ["camouflaged_hunter", "inner_light", "barbed_trap"], potions: ["weapon_power_potion", "spell_power_potion"] } },
  { value: "major_berserk", label: "Major Berserk", desc: "Increases damage done by 10%.",
    sources: { masteries: ["lead_from_the_front"] } },
  { value: "major_slayer", label: "Major Slayer", desc: "Increases damage to Dungeon/Trial monsters.",
    sources: { gear: ["master_architect", "roaring_opportunist"] } },
  { value: "major_courage", label: "Major Courage", desc: "Increases Weapon and Spell Damage.",
    sources: { gear: ["perfected_olorime", "spell_power_cure"], masteries: ["bright_harbinger"] } },
  { value: "minor_courage", label: "Minor Courage", desc: "Increases Weapon and Spell Damage.",
    sources: { gear: ["spaulder_of_ruin", "ozezan_the_inferno"] } },
  { value: "major_vulnerability", label: "Major Vulnerability", desc: "Increases the damage enemies take.",
    sources: { gear: ["roar_of_alkosh"] } },
  { value: "heat_shock", label: "Heat Shock", desc: "Increases the target's damage taken (from Magma Fist).",
    sources: { skills: ["magma_fist"] } },
  { value: "minor_fei", label: "Minor Fortitude, Endurance, Intellect", desc: "Increases Health, Stamina, and Magicka Recovery.",
    sources: { potions: ["tri_restoration_potion"] } },
  { value: "minor_evasion", label: "Minor Evasion", desc: "Reduces damage taken from area attacks.",
    sources: { skills: ["elude"] } },
  { value: "powerful_assault", label: "Powerful Assault", desc: "Set: grants Weapon and Spell Damage to nearby allies.",
    sources: { gear: ["powerful_assault"] } },
  { value: "pillagers_profit", label: "Pillager's Profit", desc: "Set: grants resource recovery when an ally uses an Ultimate.",
    sources: { gear: ["pillagers_profit"] } },
  { value: "pearlescent_ward", label: "Pearlescent Ward", desc: "Set: scaling Weapon and Spell Damage from worn Trial sets.",
    sources: { gear: ["pearlescent_ward"] } },
  { value: "touch_of_zen", label: "Touch of Z'en", desc: "Set: increases damage per damage-over-time effect on the target.",
    sources: { gear: ["touch_of_zen"] } },
  { value: "way_of_martial_knowledge", label: "Way of Martial Knowledge", desc: "Set: increases the target's damage taken from your next direct attack.",
    sources: { gear: ["way_of_martial_knowledge"] } },
  { value: "encratiss_behemoth", label: "Encratis's Behemoth", desc: "Monster set: Flame Damage debuff/buff swing.",
    sources: { gear: ["encratiss_behemoth"] } },
  { value: "symphony_of_blades", label: "Symphony of Blades", desc: "Monster set: resource sustain for low-resource allies.",
    sources: { gear: ["symphony_of_blades"] } },
  { value: "ozezan_the_inferno", label: "Ozezan the Inferno", desc: "Monster set: Minor Courage + mitigation for you and your healer.",
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
// modelled as a group source. Crit comes from three buckets:
//   - group  : a buff that benefits the whole group (any one player provides it)
//   - target : a debuff on the boss (any one player applies it → all benefit)
//   - self   : only that player (gear, mundus, CP, armor, weapon line, race,
//              class passive)
// Per player: effective crit = group + target + self; they "meet" the cap when
// that reaches CRIT_CAP. "Solo required" = CRIT_CAP - group - target (what each
// player must supply from their own sources). Source keys map to the existing
// master data; a few (Minor Force, Minor Brittle) are best-guess placeholders
// and are one-line edits here.
const CRIT_CAP = 125;
const CRIT_BASE = 50;

// Group sources (detected anywhere on the team).
const CRIT_GROUP_SOURCES = [
  { value: "major_force", label: "Major Force", pct: 20, detect: { gear: ["saxhleel_champion"], skills: ["aggressive_horn"] } },
  { value: "lucent_echoes", label: "Lucent Echoes", pct: 11, detect: { gear: ["lucent_echoes"] } },
];

// Target sources (debuffs on the boss; detected anywhere on the team).
const CRIT_TARGET_SOURCES = [
  { value: "minor_brittle", label: "Minor Brittle", pct: 10, detect: { gear: ["baron_zaudrus"], skills: ["rune_of_the_colorless_pool"] } },
  { value: "major_brittle", label: "Major Brittle", pct: 20, detect: { gear: ["nunatak"], masteries: ["tundras_maw"] } },
  { value: "elemental_catalyst", label: "Elemental Catalyst", pct: 15, detect: { gear: ["elemental_catalyst"] } },
];

// Self sources (per player). `type` selects the detection rule:
//   mundus | cp | gear | race | classPassive (class for non-subclassed; the
//   linked skill line for subclassed). Medium-armor Dexterity and the weapon-
//   line passive are handled specially in playerSelfCrit (per-piece / MAX).
const CRIT_SELF_SOURCES = [
  { value: "minor_force", label: "Minor Force (Velothi)", pct: 10, type: "gear", key: "velothi_ur_mages_amulet" },
  { value: "the_shadow", label: "The Shadow Mundus", pct: 18, type: "mundus", key: "the_shadow" },
  { value: "backstabber", label: "Backstabber", pct: 15, type: "cp", key: "backstabber" },
  { value: "fighting_finesse", label: "Fighting Finesse", pct: 10, type: "cp", key: "fighting_finesse" },
  { value: "harpooners_wading_kilt", label: "Harpooner's Wading Kilt", pct: 10, type: "gear", key: "harpooners_wading_kilt" },
  { value: "sul_xans_torment", label: "Sul-Xan's Torment", pct: 12, type: "gear", key: "sul_xans_torment" },
  { value: "feline_ambush", label: "Feline Ambush (Khajiit)", pct: 12, type: "race", key: "khajiit" },
  { value: "hemorrhage", label: "Hemorrhage (Nightblade)", pct: 10, type: "classPassive", class: "nightblade", line: "assassination" },
  { value: "piercing_spear", label: "Piercing Spear (Templar)", pct: 10, type: "classPassive", class: "templar", line: "aedric_spear" },
  { value: "glacial_presence", label: "Glacial Presence (Warden)", pct: 10, type: "classPassive", class: "warden", line: "winters_embrace" },
  { value: "fated_fortune", label: "Fated Fortune (Arcanist)", pct: 12, type: "classPassive", class: "arcanist", line: "herald_of_the_tome" },
];

// CRIT_MEDIUM_PER_PIECE: Critical Damage from the Medium Armor Dexterity passive
// per equipped medium piece (6 pieces = 12%, matching the reference table).
const CRIT_MEDIUM_PER_PIECE = 2;

// playerCritContext(player, loadout): the per-player inputs the calc reads.
// Build keys honor subclassing (subclassed → skill lines; otherwise masteries).
function playerCritContext(player, loadout) {
  const lo = loadout || {};
  const ctx = {
    slot: player.slot,
    gear: new Set(lo.gear || []),
    skills: new Set(lo.skills || []),
    cpBlue: new Set(lo.cp_blue || []),
    weapons: new Set(lo.weapons || []),
    mundus: lo.mundus || "",
    armorMedium: Number(lo.armor_medium) || 0,
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

// critSourceProviders(contexts, detect): which players satisfy a group/target
// source. `detect` maps a category (gear/skills/masteries/skillLines/classes)
// to candidate keys.
function critSourceProviders(contexts, detect) {
  const providers = [];
  contexts.forEach((c) => {
    for (const [cat, keys] of Object.entries(detect)) {
      let have;
      if (cat === "gear") have = c.gear;
      else if (cat === "skills") have = c.skills;
      else if (cat === "masteries") have = c.masteries;
      else if (cat === "skillLines") have = c.skillLines;
      else if (cat === "classes") have = new Set(c.class ? [c.class] : []);
      else continue;
      const hit = keys.find((k) => have.has(k));
      if (hit) {
        providers.push({ slot: c.slot, category: cat, key: hit });
        break;
      }
    }
  });
  return providers;
}

// playerSelfCrit(ctx): the Critical Damage a single player supplies, with a
// labelled breakdown. Weapon line uses MAX (only the active bar applies).
function playerSelfCrit(ctx) {
  const sources = [];
  let total = 0;
  CRIT_SELF_SOURCES.forEach((s) => {
    let present = false;
    if (s.type === "mundus") present = ctx.mundus === s.key;
    else if (s.type === "cp") present = ctx.cpBlue.has(s.key);
    else if (s.type === "gear") present = ctx.gear.has(s.key);
    else if (s.type === "race") present = ctx.race === s.key;
    else if (s.type === "classPassive") {
      present = ctx.isSubclassed ? ctx.skillLines.has(s.line) : ctx.class === s.class;
    }
    if (present) {
      sources.push({ label: s.label, pct: s.pct });
      total += s.pct;
    }
  });

  if (ctx.armorMedium > 0) {
    const pct = ctx.armorMedium * CRIT_MEDIUM_PER_PIECE;
    sources.push({ label: `Dexterity (${ctx.armorMedium}x medium)`, pct });
    total += pct;
  }

  let weaponMax = 0;
  let weaponName = "";
  ctx.weapons.forEach((w) => {
    const cfg = WEAPON_BY_KEY[w];
    if (cfg && (cfg.critPct || 0) > weaponMax) {
      weaponMax = cfg.critPct;
      weaponName = cfg.label;
    }
  });
  if (weaponMax > 0) {
    sources.push({ label: `${weaponName} passive`, pct: weaponMax });
    total += weaponMax;
  }

  return { total, sources };
}

// computeCritCoverage(players, loadoutBySlot): evaluate the roster + the
// selected encounter. Returns the group/target totals, the solo requirement,
// the detected group/target sources (with providers), and per-player results
// ({ slot, self, total, met, deficit, sources }).
function computeCritCoverage(players, loadoutBySlot) {
  const contexts = (players || []).map((p) =>
    playerCritContext(p, (loadoutBySlot && loadoutBySlot[p.slot]) || {})
  );

  let group = CRIT_BASE;
  const groupSources = [];
  CRIT_GROUP_SOURCES.forEach((s) => {
    const providers = critSourceProviders(contexts, s.detect);
    if (providers.length) {
      group += s.pct;
      groupSources.push({ value: s.value, label: s.label, pct: s.pct, providers });
    }
  });

  let target = 0;
  const targetSources = [];
  CRIT_TARGET_SOURCES.forEach((s) => {
    const providers = critSourceProviders(contexts, s.detect);
    if (providers.length) {
      target += s.pct;
      targetSources.push({ value: s.value, label: s.label, pct: s.pct, providers });
    }
  });

  const soloRequired = Math.max(0, CRIT_CAP - group - target);

  const playerResults = contexts.map((ctx) => {
    const self = playerSelfCrit(ctx);
    const total = group + target + self.total;
    const met = total >= CRIT_CAP;
    return {
      slot: ctx.slot,
      self: self.total,
      sources: self.sources,
      total,
      met,
      deficit: met ? 0 : CRIT_CAP - total,
    };
  });

  return {
    cap: CRIT_CAP,
    base: CRIT_BASE,
    group,
    target,
    soloRequired,
    groupSources,
    targetSources,
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
// "Self required" = PEN_TARGET - group. Several group keys (Breaches, Runic
// Sunder, Dismember) are best-guess placeholders — one-line edits here.
const PEN_TARGET = 18200;
const PEN_LIGHT_PER_PIECE = 939; // Light Armor (Concentration) penetration per light piece.
const PEN_ARENA_ONE_PIECE = 1190; // 1pc bonus from an arena weapon.

// Group/target sources (detected anywhere on the team). `detect` maps a category
// (gear/skills/masteries/skillLines/classes) to candidate keys.
const PEN_GROUP_SOURCES = [
  { value: "major_breach", label: "Major Breach", pen: 5948, detect: { skills: ["pierce_armor"] } },
  { value: "minor_breach", label: "Minor Breach", pen: 2974, detect: { skills: ["elemental_drain", "pierce_armor"] } },
  { value: "alkosh", label: "Roar of Alkosh", pen: 6000, detect: { gear: ["roar_of_alkosh"] } },
  { value: "crimson_oath", label: "Crimson Oath's Rive", pen: 3541, detect: { gear: ["crimson_oaths_rive"] } },
  { value: "tremorscale", label: "Tremorscale", pen: 2640, detect: { gear: ["tremorscale"] } },
  { value: "runic_sunder", label: "Runic Sunder", pen: 2200, detect: { skills: ["runic_sunder"] } },
  { value: "crystal_weapon", label: "Crystal Weapon", pen: 1000, detect: { skills: ["crystal_weapon"] } },
  { value: "dismember", label: "Dismember", pen: 3271, detect: { skills: ["dismember"] } },
];

// Self sources (per player). `type` ∈ cp | gear | mundus | race | classPassive
// (class for non-subclassed; the linked skill line for subclassed). Light armor,
// arena 1pc, and self-bucket `pen_extra` are handled specially in playerSelfPen.
const PEN_SELF_SOURCES = [
  { value: "piercing", label: "Piercing (CP)", pen: 700, type: "cp", key: "piercing" },
  { value: "velothi", label: "Velothi Ur-Mage's Amulet", pen: 1650, type: "gear", key: "velothi_ur_mages_amulet" },
  { value: "the_lover", label: "The Lover Mundus", pen: 2744, type: "mundus", key: "the_lover" },
  { value: "splintered_secrets", label: "Splintered Secrets (Arcanist)", pen: 2480, type: "classPassive", class: "arcanist", line: "herald_of_the_tome" },
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
    mundus: lo.mundus || "",
    armorLight: Number(lo.armor_light) || 0,
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
    if (present) {
      sources.push({ label: s.label, pen: s.pen });
      total += s.pen;
    }
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
    if (s.bucket === "self" && ctx.penExtra.has(s.value)) {
      sources.push({ label: s.label, pen: s.pen });
      total += s.pen;
    }
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
    if (providers.length) {
      group += s.pen;
      groupSources.push({ value: s.value, label: s.label, pen: s.pen, providers });
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
