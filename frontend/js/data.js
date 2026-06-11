/*
 * data.js — ESO reference data + display helpers shared across page scripts.
 *
 * This module holds all the domain/reference data the UI renders (and the small
 * presentation helpers that operate on it). Page scripts (e.g. app.js) consume
 * these globals; api.js stays a pure network client. Keys mirror the backend
 * allow-lists in internal/models (eso.go and encounter.go) — keep them in sync.
 *
 * Contents:
 *   - Roles, classes, share roles, days (+ label/format helpers).
 *   - Timezone helpers.
 *   - Subclassing: skill lines and class masteries (+ option/lookup helpers).
 *   - Encounters SEED data: ENCOUNTER_NAME_GROUPS (valid names grouped by
 *     trial), GEAR_SET_GROUPS (gear grouped by set type: 5pc, monster, arena,
 *     mythic) / SKILL_GROUPS (searchable loadout options). Loadouts store the
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

// Gear sets (seed), grouped by set type (the kind of gear, not the source
// zone). This is the source of truth; the flat GEAR_SETS list below is derived
// from it for lookups. The `group` is used to separate sets in the searchable
// dropdown. `desc` is the set's headline bonus, shown as a tooltip.
const GEAR_SET_GROUPS = [
  { group: "5-Piece Sets", sets: [
    { value: "perfected_relequen", label: "Perfected Relequen", desc: "5pc: Light/Heavy attacks apply a stack of Tempest, dealing increasing damage over time, up to 10 stacks. (Cloudrest, Perfected)" },
    { value: "relequen", label: "Relequen", desc: "5pc: Light/Heavy attacks apply a stack of Tempest, dealing increasing damage over time, up to 10 stacks. (Cloudrest)" },
    { value: "pillar_of_nirn", label: "Pillar of Nirn", desc: "5pc: Dealing direct damage has a chance to create a fissure that deals Bleed Damage over 10 seconds. (Craftable)" },
    { value: "sul_xans_torment", label: "Sul-Xan's Torment", desc: "5pc: Damaging an enemy spawns a soul that explodes for Magic Damage; collected souls grant Weapon and Spell Damage. (Sanity's Edge)" },
    { value: "whorl_of_the_depths", label: "Whorl of the Depths", desc: "5pc: Direct damage has a chance to spawn a vortex that deals Frost Damage over time. (Dreadsail Reef)" },
    { value: "coral_riptide", label: "Coral Riptide", desc: "5pc: Increases Weapon and Spell Damage, scaling higher the lower your Stamina. (Dreadsail Reef)" },
    { value: "deadly_strike", label: "Deadly Strike", desc: "5pc: Increases the damage of your Light/Heavy attacks and damage-over-time abilities by 15%. (Cyrodiil)" },
    { value: "tzogvins_warband", label: "Tzogvin's Warband", desc: "5pc: Critical hits grant a stack of Precision, increasing Weapon Critical (up to 10). (Frostvault)" },
    { value: "kinras_wrath", label: "Kinras's Wrath", desc: "5pc: Light/Heavy attacks grant stacking Weapon and Spell Damage; at max stacks emit a fiery aura. (Black Drake Villa)" },
    { value: "bahseis_mania", label: "Bahsei's Mania", desc: "5pc: Increases damage done by up to 11% based on your missing Magicka. (Rockgrove)" },
    { value: "ansuuls_torment", label: "Ansuul's Torment", desc: "5pc: Light/Heavy attacking a low-Health enemy increases your damage done. (Sanity's Edge)" },
    { value: "highland_sentinel", label: "Highland Sentinel", desc: "5pc: Critical damage grants you and nearby allies a stacking Critical Damage buff (Aim). (Lucent Citadel)" },
    { value: "aegis_caller", label: "Aegis Caller", desc: "5pc: Damaging an enemy summons a Lava Whip; stronger against 3 or more enemies. (Depths of Malatar)" },
    { value: "perfected_olorime", label: "Perfected Vestment of Olorime", desc: "5pc: Casting an ability that leaves an effect on the ground grants you and allies Major Courage. (Cloudrest, Perfected)" },
    { value: "spell_power_cure", label: "Spell Power Cure", desc: "5pc: Healing an ally already at full Health grants them Major Courage, increasing Weapon and Spell Damage. (White-Gold Tower)" },
    { value: "master_architect", label: "Master Architect", desc: "5pc: Casting an Ultimate grants you and allies Major Slayer, increasing damage to Dungeon/Trial monsters. (Halls of Fabrication)" },
    { value: "roaring_opportunist", label: "Roaring Opportunist", desc: "5pc: Casting an Ultimate grants you and allies Major Slayer. (Sunspire)" },
    { value: "saxhleel_champion", label: "Saxhleel Champion", desc: "5pc: Casting an Ultimate grants you and allies Major Force based on the Ultimate's cost. (Rockgrove)" },
    { value: "pearlescent_ward", label: "Pearlescent Ward", desc: "5pc: Grants you and group members increasing Weapon and Spell Damage based on how many wear 5pc Trial sets. (Dreadsail Reef)" },
    { value: "lucent_echoes", label: "Lucent Echoes", desc: "5pc: Taking damage grants nearby group members increased Critical Damage. (Lucent Citadel)" },
    { value: "powerful_assault", label: "Powerful Assault", desc: "5pc: Using an Assault skill grants you and nearby allies Weapon and Spell Damage. (Cyrodiil)" },
    { value: "pillagers_profit", label: "Pillager's Profit", desc: "5pc: When a nearby ally uses an Ultimate, grant Magicka and Stamina recovery. (Imperial City)" },
    { value: "z_ens_redress", label: "Z'en's Redress", desc: "5pc: Increases your damage to an enemy by 1% per damage-over-time effect you have on them, up to 5%. (Vvardenfell)" },
    { value: "crimson_oaths_rive", label: "Crimson Oath's Rive", desc: "5pc: Dealing damage releases a cleave that deals Physical Damage and reduces enemy damage done. (Markarth)" },
    { value: "plaguebreak", label: "Plaguebreak", desc: "5pc: Damaging a cursed enemy builds up an explosion that bursts for AoE damage. (Cyrodiil)" },
  ] },
  { group: "Monster Sets", sets: [
    { value: "slimecraw", label: "Slimecraw", desc: "1pc: Grants Minor Berserk, increasing your damage done by 5%. (Monster set)" },
    { value: "zaan", label: "Zaan", desc: "2pc: Critical hits beam the closest enemy, dealing increasing Flame Damage over time. (Monster set)" },
    { value: "nazaray", label: "Nazaray", desc: "2pc: Activating an Ultimate deals Magic Damage and grants you Weapon and Spell Damage. (Monster set)" },
    { value: "selene", label: "Selene", desc: "2pc: Dealing melee damage summons a primal beast that mauls your enemy for Physical Damage. (Monster set)" },
    { value: "encratiss_behemoth", label: "Encratis's Behemoth", desc: "2pc: Damaging a Flame-debuffed enemy reduces their Flame Damage and increases yours. (Monster set)" },
    { value: "baron_zaudrus", label: "Baron Zaudrus", desc: "2pc: Dealing damage builds stacks; at 3 stacks deal Magic Damage and grant Minor Brittle. (Monster set)" },
  ] },
  { group: "Arena Weapons", sets: [
    { value: "merciless_charge", label: "Maelstrom (Merciless Charge)", desc: "2pc: Each Heavy Weapon ability that hits enemies grants stacks; at 5 stacks your next Whirlwind deals extra Bleed Damage. (Maelstrom Arena)" },
    { value: "crushing_wall", label: "Maelstrom (Crushing Wall)", desc: "2pc: Increases the damage of your Wall of Elements heavy/area damage to enemies. (Maelstrom Arena)" },
    { value: "wrath_of_elements", label: "Vateshran (Wrath of Elements)", desc: "2pc: Casting an Elemental Weapon or applying a charged status effect builds a Charged Conduit that deals damage. (Vateshran Hollows)" },
    { value: "frenzied_momentum", label: "Vateshran (Frenzied Momentum)", desc: "2pc: Casting Momentum/Rally adds a damage-over-time channel that scales with your Weapon and Spell Damage. (Vateshran Hollows)" },
    { value: "masters_destruction_staff", label: "Master's Destruction Staff (Tri Focus)", desc: "2pc: Increases the heavy-attack splash and status effect potency of your Destruction Staff. (Blackrose Prison)" },
  ] },
  { group: "Mythic Items", sets: [
    { value: "velothi_ur_mages_amulet", label: "Velothi Ur-Mage's Amulet", desc: "1pc: Grants Minor Force and increases damage of your damage-over-time abilities, at the cost of Light/Heavy attack damage. (Mythic)" },
    { value: "oakensoul_ring", label: "Oakensoul Ring", desc: "1pc: Grants many major/minor buffs while locking you to a single weapon bar. (Mythic)" },
    { value: "sea_serpents_coil", label: "Sea-Serpent's Coil", desc: "1pc: Grants Weapon and Spell Damage; taking heavy damage triggers a powerful heal on a cooldown. (Mythic)" },
    { value: "spaulder_of_ruin", label: "Spaulder of Ruin", desc: "1pc: Creates an aura granting nearby allies Minor Heroism and Minor Courage. (Mythic)" },
    { value: "death_dealers_fete", label: "Death Dealer's Fete", desc: "1pc: Grants stacking Max Stamina/Magicka/Health that builds while in combat. (Mythic)" },
  ] },
];

// Flat gear set list (derived) for label/value lookups. Each entry carries the
// `group` (set type) it belongs to.
const GEAR_SETS = GEAR_SET_GROUPS.flatMap((g) =>
  g.sets.map((s) => ({ ...s, group: g.group }))
);

// Skills (seed), grouped by skill line. This is the source of truth; the flat
// SKILLS list below is derived from it for lookups. The `group` is the skill
// line, used to separate skills in the searchable dropdown.
const SKILL_GROUPS = [
  { group: "Arcanist · Herald of the Tome", skills: [
    { value: "pragmatic_fatecarver", label: "Pragmatic Fatecarver", desc: "Channeled beam dealing Magic and Physical Damage; active Crux extends its range. A core Arcanist spammable." },
    { value: "cephaliarchs_flail", label: "Cephaliarch's Flail", desc: "Melee attack dealing Frost Damage and healing you for each enemy hit; generates a Crux." },
    { value: "runeblades", label: "Runeblades", desc: "Ranged attack dealing Magic Damage; generates a Crux. Basic Arcanist spammable." },
    { value: "fulminating_rune", label: "Fulminating Rune", desc: "Places a rune that detonates after a short delay for Magic Damage; generates a Crux on cast." },
    { value: "tentacular_dread", label: "Tentacular Dread", desc: "Frontal attack that pulls in and stuns enemies for Frost Damage, spending Crux to increase the damage." },
  ] },
  { group: "Arcanist · Soldier of Apocrypha", skills: [
    { value: "the_languid_eye", label: "The Languid Eye", desc: "Soldier of Apocrypha (defensive) skill-line ability. (Effect unverified — confirm against current patch.)" },
  ] },
  { group: "Dragonknight · Ardent Flame", skills: [
    { value: "molten_whip", label: "Molten Whip", desc: "Melee Flame Damage attack, empowered by Seething Fury stacks from other Ardent Flame abilities. DK spammable." },
    { value: "venomous_claw", label: "Venomous Claw", desc: "Applies a Poison Damage over time that ramps up in intensity over its duration." },
    { value: "noxious_breath", label: "Noxious Breath", desc: "Frontal cone dealing Poison Damage over time and applying Major Breach to reduce enemy resistances." },
    { value: "engulfing_flames", label: "Engulfing Flames", desc: "Flame attack dealing damage over time and increasing the Flame Damage the target takes." },
  ] },
  { group: "Dragonknight · Earthen Heart", skills: [
    { value: "igneous_shield", label: "Igneous Shield", desc: "Grants you and nearby allies a damage shield and Major Mending (increased healing done)." },
  ] },
  { group: "Necromancer · Grave Lord", skills: [
    { value: "blighted_blastbones", label: "Blighted Blastbones", desc: "Summons a skeleton that charges the enemy and explodes for Disease Damage, applying Major Defile." },
    { value: "stalking_blastbones", label: "Stalking Blastbones", desc: "Summons a skeleton that stalks the enemy and explodes for high Flame Damage." },
    { value: "skeletal_archer", label: "Skeletal Archer", desc: "Summons a skeletal archer that attacks your enemy over time and leaves a corpse on death." },
  ] },
  { group: "Nightblade · Assassination", skills: [
    { value: "merciless_resolve", label: "Merciless Resolve", desc: "Builds stacks as you attack; once charged, your next cast fires a spectral bow for heavy damage. Grants Minor Berserk." },
    { value: "relentless_focus", label: "Relentless Focus", desc: "Like Merciless Resolve but restores Stamina; charges a spectral bow proc and grants Minor Berserk." },
    { value: "soul_harvest", label: "Soul Harvest", desc: "Execute-scaling melee ultimate that deals more damage to low-Health enemies and grants Ultimate when it kills." },
  ] },
  { group: "Sorcerer · Dark Magic", skills: [
    { value: "crystal_weapon", label: "Crystal Weapon", desc: "Charges your weapon to deal Physical Damage over your next two attacks and reduce the enemy's resistances." },
  ] },
  { group: "Sorcerer · Daedric Summoning", skills: [
    { value: "daedric_prey", label: "Daedric Prey", desc: "Curses an enemy to explode for AoE Magic Damage and commands your pets to deal bonus damage to it." },
    { value: "bound_armaments", label: "Bound Armaments", desc: "Summons Daedric weapons granting Max Stamina; builds stacks to unleash a flurry heavy attack." },
    { value: "hardened_ward", label: "Hardened Ward", desc: "Damage shield scaling with your Max Magicka and pets, with overflow protection against larger hits." },
  ] },
  { group: "Sorcerer · Storm Calling", skills: [
    { value: "hurricane", label: "Hurricane", desc: "Surrounds you in a storm dealing increasing Physical Damage over time; grants Major Brutality and Sorcery." },
    { value: "boundless_storm", label: "Boundless Storm", desc: "Storm aura dealing Shock Damage over time and granting Major Resolve plus a brief Major Expedition." },
  ] },
  { group: "Templar · Aedric Spear", skills: [
    { value: "puncturing_sweeps", label: "Puncturing Sweeps", desc: "Sweeping melee attack dealing Magic Damage and healing you. Core Templar spammable." },
    { value: "blazing_spear", label: "Blazing Spear", desc: "Throws a spear dealing AoE Magic Damage over time and leaving a Holy Shock synergy for allies." },
  ] },
  { group: "Templar · Dawn's Wrath", skills: [
    { value: "power_of_the_light", label: "Power of the Light", desc: "Applies Major Breach and stores a portion of damage dealt, detonating it for Magic Damage." },
  ] },
  { group: "Templar · Restoring Light", skills: [
    { value: "combat_prayer", label: "Combat Prayer", desc: "Burst heal to allies in front of you, granting Minor Berserk and Minor Resolve." },
  ] },
  { group: "Warden · Animal Companions", skills: [
    { value: "cutting_dive", label: "Cutting Dive", desc: "Swooping attack dealing Bleed Damage; applies Minor Maim if the enemy is already bleeding." },
    { value: "subterranean_assault", label: "Subterranean Assault", desc: "Summons shalk that burst from the ground after a delay for Poison Damage, applying Major Breach." },
    { value: "deep_fissure", label: "Deep Fissure", desc: "Summons shalk that strike after a delay for AoE Magic Damage, applying Major and Minor Breach." },
  ] },
  { group: "Two Handed", skills: [
    { value: "stampede", label: "Stampede", desc: "Charge dealing Physical Damage and leaving a damage-over-time field; hits harder while moving." },
    { value: "carve", label: "Carve", desc: "Cleave dealing Physical Damage and Bleed over time; grants Ultimate per enemy hit." },
    { value: "dizzying_swing", label: "Dizzying Swing", desc: "Heavy melee Physical Damage that off-balances and briefly stuns the enemy." },
  ] },
  { group: "Bow", skills: [
    { value: "endless_hail", label: "Endless Hail", desc: "Rains arrows over an area dealing Physical Damage over time. Strong sustained AoE." },
    { value: "arrow_barrage", label: "Arrow Barrage", desc: "Volley dealing Physical Damage over a wider area over time." },
  ] },
  { group: "Destruction Staff", skills: [
    { value: "elemental_blockade", label: "Elemental Blockade", desc: "Creates a wall of elements dealing damage over time and applying the staff's status effect." },
    { value: "force_pulse", label: "Force Pulse", desc: "Ranged attack dealing elemental damage, with bonus splash damage to nearby enemies." },
    { value: "crushing_shock", label: "Crushing Shock", desc: "Ranged elemental attack that interrupts the enemy and deals splash damage." },
    { value: "unstable_wall_of_elements", label: "Unstable Wall of Elements", desc: "Wall of elements that detonates for extra damage when it expires." },
  ] },
  { group: "Restoration Staff", skills: [
    { value: "radiating_regeneration", label: "Radiating Regeneration", desc: "Heal over time that bounces to additional injured allies." },
    { value: "illustrious_healing", label: "Illustrious Healing", desc: "Ground-targeted heal over time for allies standing in the area." },
    { value: "healing_springs", label: "Healing Springs", desc: "Channeled area heal that also restores Magicka for each ally healed." },
  ] },
  { group: "One Hand and Shield", skills: [
    { value: "pierce_armor", label: "Pierce Armor", desc: "Taunt that reduces the enemy's Physical and Spell Resistance (Major Breach and Fracture)." },
    { value: "heroic_slash", label: "Heroic Slash", desc: "Strike applying Minor Maim to the enemy and granting you Minor Heroism (Ultimate generation)." },
  ] },
  { group: "Mages Guild", skills: [
    { value: "structured_entropy", label: "Structured Entropy", desc: "Magic Damage over time that heals you and increases your Max Health while active." },
    { value: "degeneration", label: "Degeneration", desc: "Magic Damage over time that grants Major Brutality and Sorcery (increased Weapon and Spell Damage)." },
    { value: "inner_light", label: "Inner Light", desc: "Grants Major Prophecy (spell crit) and increased Max Magicka while slotted; reveals stealthed enemies." },
  ] },
  { group: "Fighters Guild", skills: [
    { value: "camouflaged_hunter", label: "Camouflaged Hunter", desc: "Grants Minor Berserk while slotted; activating deals damage and grants Major Savagery (weapon crit)." },
    { value: "barbed_trap", label: "Barbed Trap", desc: "Places a trap dealing Physical Damage over time and granting Minor Force (crit damage); offers a synergy." },
  ] },
  { group: "Undaunted", skills: [
    { value: "inner_rage", label: "Inner Rage", desc: "Ranged taunt dealing Magic Damage and offering a pull synergy for allies." },
    { value: "energy_orb", label: "Energy Orb", desc: "Sends a slow orb that heals allies in its path; offers a synergy to heal or restore resources." },
  ] },
  { group: "Assault", skills: [
    { value: "resolving_vigor", label: "Resolving Vigor", desc: "Strong burst-then-over-time self heal. A survivability staple." },
  ] },
];

// Flat skill list (derived) for label/value lookups. Each entry carries the
// `group` (skill line) it belongs to.
const SKILLS = SKILL_GROUPS.flatMap((g) =>
  g.skills.map((s) => ({ ...s, group: g.group }))
);

// --- Lookup helpers ---
const GEAR_BY_KEY = Object.fromEntries(GEAR_SETS.map((g) => [g.value, g]));
const GEAR_BY_LABEL = Object.fromEntries(GEAR_SETS.map((g) => [g.label.toLowerCase(), g]));
const SKILL_BY_KEY = Object.fromEntries(SKILLS.map((s) => [s.value, s]));
const SKILL_BY_LABEL = Object.fromEntries(SKILLS.map((s) => [s.label.toLowerCase(), s]));

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

// Grouped option data for the searchable-select component:
//   [{ group: string|null, items: [{ value, label, desc? }] }]
// Gear is grouped by set type (5pc, monster, arena, mythic); skills by skill line.
const GEAR_GROUPS = GEAR_SET_GROUPS.map((g) => ({ group: g.group, items: g.sets }));
const SKILL_SELECT_GROUPS = SKILL_GROUPS.map((g) => ({ group: g.group, items: g.skills }));

// Master tables keyed by loadout type, so UI code can stay generic.
const LOADOUT_TYPES = {
  gear: { items: GEAR_SETS, groups: GEAR_GROUPS, byLabel: gearByLabel, label: gearLabel, desc: gearDesc, addPlaceholder: "Search gear set…" },
  skills: { items: SKILLS, groups: SKILL_SELECT_GROUPS, byLabel: skillByLabel, label: skillLabel, desc: skillDesc, addPlaceholder: "Search skill…" },
};
