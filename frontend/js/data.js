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
 *     trial), GEAR_SETS / SKILLS (searchable loadout options). Loadouts store
 *     the item `value` (key); labels/tooltips are looked up from these tables.
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
    { value: "abyssal_pact", label: "Abyssal Pact", desc: "You dig ever deeper into the unknown for power, and you find it. Activating an Arcanist Ultimate immediately generates maximum Crux and grants you 666 Weapon and Spell Damage for 15 seconds." },
    { value: "mind_over_matter", label: "Mind Over Matter", desc: "Pain is a teacher, and you have mastered it. Upgrades Fatewoven Armor to also grant 300 Magicka and Stamina when you take damage, increasing by 100 per Crux you have active. This effect can occur once every 2 seconds." },
    { value: "manifest_destiny", label: "Manifest Destiny", desc: "Your mastery over fate comes so naturally that it feels like destiny. Upgrades rank 2 of Fated Fortune to also increase your damage done by 33% for the duration." },
    { value: "fleshborne_fate", label: "Fleshborne Fate", desc: "Small plucks on threads in the tapestry build to new formations. New fate. While in combat, when you heal or overheal a total of 75000 Health, you generate a Crux." },
    { value: "self_perpetuated_fate", label: "Self-Perpetuated Fate", desc: "You've taken being the master of your own fate literally, ensuring your thread never ends. Upgrades rank 2 of Implaceable Outcome to generate maximum Crux, up to once every 15 seconds." },
  ],
  dragonknight: [
    { value: "booming_voice", label: "Booming Voice", desc: "Your voice echoes as you speak. Activating rank 2 of The Storm Voice now also grants 5 Health, Magicka, and Stamina Recovery for every Ultimate spent for 10 seconds." },
    { value: "immovable_mountain", label: "Immovable Mountain", desc: "You are as unyielding as a mountain. Each second you remain Bracing, you increase the amount of damage you block by 6%, up to 5 times. Blocking damage has a 20% chance to restore 500 Stamina. This effect can occur once every block cost." },
    { value: "unstoppable_force", label: "Unstoppable Force", desc: "Each action you take feels as if it were flung from the top of a mountain. Improves your Landslide passive to increase the potency of your damage done, healing, and damage shield strength by 1% per stack." },
    { value: "rousing_roar", label: "Rousing Roar", desc: "Your will is so strong that it rouses others to take action. Activating rank 2 of The Storm Voice grants you and group members within 28 meters of you Major Berserk, Heroism, and Protection for 1 second per 10 Ultimate spent, increasing damage done and reducing damage taken by 10%, and granting 3 Ultimate every 1.5 seconds while in combat." },
    { value: "recursive_flame", label: "Recursive Flame", desc: "Your flames can never truly be extinguished, capable of birthing again from mere embers. When your Dragonknight damage over time effects end, you apply Recursive Flame to the target, dealing 1904 Flame Damage over 12 seconds. This effect stacks up to 12 times and increases in damage by 25% per stack." },
  ],
  necromancer: [
    { value: "cycle_of_death", label: "Cycle of Death", desc: "Your mastery over death has allowed you to supplant life with it. Upgrades rank 2 of Corpse Consumption to mark the closest enemy to you with death's touch for 6 seconds, allowing you to use a corpse-consuming ability on them. This effect can occur once every 2 seconds." },
    { value: "at_the_precipice", label: "At the Precipice", desc: "When the living stand at the precipice of the thereafter, you've learned to coax them back with some of death's lingering essence intact. While you are in combat, healing a target below 50% Health relinquishes them from death's clutches and allows you to use a corpse consuming ability against them within 10 seconds, up to once every second." },
    { value: "lord_of_the_cycle", label: "Lord of the Cycle", desc: "The cycle of life and death continues; you will live - others will die. Upgrades rank 2 of Reusable Parts to grant Lord of the Cycle for 25 seconds, increasing your damage done by 1% for every 1% Health you have more than your target. The Damage bonus caps at 50% or 25% while Battle Spirit is active." },
    { value: "pound_of_flesh", label: "Pound of Flesh", desc: "When death knocks at the door demanding recompense, you collect. When you take damage, you have a 1% chance to heal 1600 Health and restore 5% of your missing Stamina, up to once per second. This chance increases by 1% for every missing Health percent you have and the healing is based off your Max Health." },
    { value: "nothing_wasted", label: "Nothing Wasted", desc: "You leave no trace of power behind when drawing upon the remnants of life. Upgrades rank 2 of Corpse Consumption to also grant a stack of Nothing Wasted for 20 seconds, which increases your Max Health by 2% and Weapon and Spell Damage by 2% per stack, up to 10 times. Stacks decay one at a time instead of all at once." },
  ],
  nightblade: [
    { value: "critical_motivation", label: "Critical Motivation", desc: "Nocturnal watches over her children like a mother, pushing them to grow even greater. Upgrades rank 2 of Hemorrhage to have a chance to generate 2 Ultimate. This effect can occur once every 0.3 seconds, and the chance is based off your Weapon Critical." },
    { value: "evasive_trance", label: "Evasive Trance", desc: "While in battle, your instincts take over, allowing you to effortlessly dodge attacks in sync with your actions for but a moment. Activating Nightblade ability while bracing causes you to dodge attacks for 0.3 seconds." },
    { value: "detect_weakness", label: "Detect Weakness", desc: "The closer to the edge someone stands, the stronger you pull or shove. Increases your Weapon and Spell Damage by up to 1500, based on your target's missing Health. Reduces your damage taken by up to 15%, based on your attacker's missing Health." },
    { value: "share_the_spoils", label: "Share the Spoils", desc: "You pilfer so proliferously you've become philanthropic. Upgrades rank 2 of Transfer to grant the closest group member 500 Magicka and Stamina and doubles the Ultimate you gain when it activates." },
    { value: "above_and_beyond", label: "Above and Beyond", desc: "A job well done is the only job you'll do. Increases your Critical Damage and Healing by 25%. Increases your maximum Critical Damage and Healing by 35%." },
  ],
  sorcerer: [
    { value: "conservation_of_energy", label: "Conservation of Energy", desc: "Your tireless hours spent researching and learning the inner machinations of reality have given you insight into conserving your energy. Upgrades rank 2 of Blood Magic to work with any ability with a cost, and restores 1000 Magicka and Stamina when it activates." },
    { value: "efficient_defense", label: "Efficient Defense", desc: "You leave nothing to chance, creating contingencies in every plan you enact. While beginning to use a Sorcerer ability or an ability with a cast time, you gain a damage shield for 0.3 seconds that can absorb 8000 damage. This effect is based off your Max Health." },
    { value: "implosion", label: "Implosion", desc: "The way thunder follows lightning, so too do your blows echo and strike again. When you deal damage, you have a 1% chance for every 1% missing Health the target has to deal 314 Shock Damage, up to once every 0.2 seconds. This chance is divided by one plus every permanent pet you have active." },
    { value: "font_of_power", label: "Font of Power", desc: "Your thirst for knowledge knows no end. The more you quench it, the deeper it gets. Upgrades rank 2 of Exploitation to work with any Sorcerer ability and increases your Weapon and Spell Damage by 9% for 10 seconds. The Weapon and Spell Damage increases by 1% for every 1500 Max Magicka or Stamina you have, whichever is higher." },
    { value: "parallel_protection", label: "Parallel Protection", desc: "You haven't survived your foray into the forbidden on luck alone - your defensive spells are cast with such efficiency that they seem to duplicate. Casting a damage shield on yourself or an ally grants an additional shield for 3 seconds that absorbs up to 4000 damage. This effect scales off the highest of your Max Health or Max Magicka, and shield is capped at 25% of the target's Max Health." },
  ],
  templar: [
    { value: "hold_the_line", label: "Hold the Line", desc: "In the light, you find the will to stand your ground with unyielding zeal. While Sacred Ground is active, you gain a damage shield for 6 seconds, up to once every 6 seconds. The shield absorbs up to 3200 damage and provides 300 Health, Magicka, and Stamina Recovery while active. If the shield breaks, you gain 10 Ultimate." },
    { value: "missionary_of_light", label: "Missionary of Light", desc: "You carry with you the light in every step, and the light provides you succor. Sacred Ground is now applied while you are in your own Nova and Spear Shards, and while Radical Sweep and Solar Barrage are active. While Sacred Ground is active, you heal for 1279 Health every 1 second. If you are at full Health after being healed from this effect while in combat, you also gain 2 Ultimate." },
    { value: "sacred_anchor", label: "Sacred Anchor", desc: "Your crusade seems like a heavy burden to others, but to you, it is an anchor. Sacred Ground now activates and refreshes itself while Bracing. Increases the amount of damage you can block by 20% while stationary." },
    { value: "illuminary_of_bravery", label: "Illuminary of Bravery", desc: "Like a torch lit in the dark, you instil hope when it is needed most. Upgrades rank 2 of Illuminate to also grant Lustrous Bravery for the duration, which grants 300 Weapon and Spell Damage to allies and doubles for you." },
    { value: "in_radiance_judgement", label: "In Radiance, Judgement", desc: "Judgement follows in your wake like the burning light of the sun. When rank 2 of Burning Light deals damage, your Templar abilities gain 2000 damage done for 3.1 seconds." },
  ],
  warden: [
    { value: "hypothermia", label: "Hypothermia", desc: "You've harnessed the dangers of the tundra. Applying Chill to an enemy also applies Major Brittle for 2 seconds, increasing their Critical Damage taken by 20%." },
    { value: "wild_adaptation", label: "Wild Adaptation", desc: "Your abilities adapt to the elemental effects on your allies and enemies. Gain 333 Weapon and Spell Damage for each status effect on your target, up to a maximum of 1665." },
    { value: "thick_hide", label: "Thick Hide", desc: "Just like animals in the wild, your skin has grown tough against those touched by the elements. Reduce your damage taken by 10% against targets with at least one status effect." },
    { value: "one_with_winter", label: "One with Winter", desc: "Winter comes to your call just as nature does. Upgrades Bond with Nature to also activate off Winter's Embrace abilities, granting 15% Weapon and Spell Damage for 10 seconds if you are at full Health after the Heal." },
    { value: "natures_bounty", label: "Nature's Bounty", desc: "A bountiful harvest should be enjoyed by all. Upgrades rank 2 of Nature's Gift to also restore 500 Magicka or 500 Stamina to your ally, whichever resource pool is lower." },
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

// Gear sets (seed). `desc` is the set's headline bonus, shown as a tooltip.
const GEAR_SETS = [
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
  // Monster / 1-2pc sets
  { value: "slimecraw", label: "Slimecraw", desc: "1pc: Grants Minor Berserk, increasing your damage done by 5%. (Monster set)" },
  { value: "zaan", label: "Zaan", desc: "2pc: Critical hits beam the closest enemy, dealing increasing Flame Damage over time. (Monster set)" },
  { value: "nazaray", label: "Nazaray", desc: "2pc: Activating an Ultimate deals Magic Damage and grants you Weapon and Spell Damage. (Monster set)" },
  { value: "selene", label: "Selene", desc: "2pc: Dealing melee damage summons a primal beast that mauls your enemy for Physical Damage. (Monster set)" },
  { value: "encratiss_behemoth", label: "Encratis's Behemoth", desc: "2pc: Damaging a Flame-debuffed enemy reduces their Flame Damage and increases yours. (Monster set)" },
  { value: "baron_zaudrus", label: "Baron Zaudrus", desc: "2pc: Dealing damage builds stacks; at 3 stacks deal Magic Damage and grant Minor Brittle. (Monster set)" },
];

// Skills (seed), grouped by skill line. This is the source of truth; the flat
// SKILLS list below is derived from it for lookups. The `group` is the skill
// line, used to separate skills in the searchable dropdown.
const SKILL_GROUPS = [
  { group: "Arcanist · Herald of the Tome", skills: [
    { value: "pragmatic_fatecarver", label: "Pragmatic Fatecarver" },
    { value: "cephaliarchs_flail", label: "Cephaliarch's Flail" },
    { value: "runeblades", label: "Runeblades" },
    { value: "fulminating_rune", label: "Fulminating Rune" },
    { value: "tentacular_dread", label: "Tentacular Dread" },
  ] },
  { group: "Arcanist · Soldier of Apocrypha", skills: [
    { value: "the_languid_eye", label: "The Languid Eye" },
  ] },
  { group: "Dragonknight · Ardent Flame", skills: [
    { value: "molten_whip", label: "Molten Whip" },
    { value: "venomous_claw", label: "Venomous Claw" },
    { value: "noxious_breath", label: "Noxious Breath" },
    { value: "engulfing_flames", label: "Engulfing Flames" },
  ] },
  { group: "Dragonknight · Earthen Heart", skills: [
    { value: "igneous_shield", label: "Igneous Shield" },
  ] },
  { group: "Necromancer · Grave Lord", skills: [
    { value: "blighted_blastbones", label: "Blighted Blastbones" },
    { value: "stalking_blastbones", label: "Stalking Blastbones" },
    { value: "skeletal_archer", label: "Skeletal Archer" },
  ] },
  { group: "Nightblade · Assassination", skills: [
    { value: "merciless_resolve", label: "Merciless Resolve" },
    { value: "relentless_focus", label: "Relentless Focus" },
    { value: "soul_harvest", label: "Soul Harvest" },
  ] },
  { group: "Sorcerer · Dark Magic", skills: [
    { value: "crystal_weapon", label: "Crystal Weapon" },
  ] },
  { group: "Sorcerer · Daedric Summoning", skills: [
    { value: "daedric_prey", label: "Daedric Prey" },
    { value: "bound_armaments", label: "Bound Armaments" },
    { value: "hardened_ward", label: "Hardened Ward" },
  ] },
  { group: "Sorcerer · Storm Calling", skills: [
    { value: "hurricane", label: "Hurricane" },
    { value: "boundless_storm", label: "Boundless Storm" },
  ] },
  { group: "Templar · Aedric Spear", skills: [
    { value: "puncturing_sweeps", label: "Puncturing Sweeps" },
    { value: "blazing_spear", label: "Blazing Spear" },
  ] },
  { group: "Templar · Dawn's Wrath", skills: [
    { value: "power_of_the_light", label: "Power of the Light" },
  ] },
  { group: "Templar · Restoring Light", skills: [
    { value: "combat_prayer", label: "Combat Prayer" },
  ] },
  { group: "Warden · Animal Companions", skills: [
    { value: "cutting_dive", label: "Cutting Dive" },
    { value: "subterranean_assault", label: "Subterranean Assault" },
    { value: "deep_fissure", label: "Deep Fissure" },
  ] },
  { group: "Two Handed", skills: [
    { value: "stampede", label: "Stampede" },
    { value: "carve", label: "Carve" },
    { value: "dizzying_swing", label: "Dizzying Swing" },
  ] },
  { group: "Bow", skills: [
    { value: "endless_hail", label: "Endless Hail" },
    { value: "arrow_barrage", label: "Arrow Barrage" },
  ] },
  { group: "Destruction Staff", skills: [
    { value: "elemental_blockade", label: "Elemental Blockade" },
    { value: "force_pulse", label: "Force Pulse" },
    { value: "crushing_shock", label: "Crushing Shock" },
    { value: "unstable_wall_of_elements", label: "Unstable Wall of Elements" },
  ] },
  { group: "Restoration Staff", skills: [
    { value: "radiating_regeneration", label: "Radiating Regeneration" },
    { value: "illustrious_healing", label: "Illustrious Healing" },
    { value: "healing_springs", label: "Healing Springs" },
  ] },
  { group: "One Hand and Shield", skills: [
    { value: "pierce_armor", label: "Pierce Armor" },
    { value: "heroic_slash", label: "Heroic Slash" },
  ] },
  { group: "Mages Guild", skills: [
    { value: "structured_entropy", label: "Structured Entropy" },
    { value: "degeneration", label: "Degeneration" },
    { value: "inner_light", label: "Inner Light" },
  ] },
  { group: "Fighters Guild", skills: [
    { value: "camouflaged_hunter", label: "Camouflaged Hunter" },
    { value: "barbed_trap", label: "Barbed Trap" },
  ] },
  { group: "Undaunted", skills: [
    { value: "inner_rage", label: "Inner Rage" },
    { value: "energy_orb", label: "Energy Orb" },
  ] },
  { group: "Assault", skills: [
    { value: "resolving_vigor", label: "Resolving Vigor" },
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
function skillByLabel(label) {
  return SKILL_BY_LABEL[String(label || "").trim().toLowerCase()] || null;
}

// Grouped option data for the searchable-select component:
//   [{ group: string|null, items: [{ value, label, desc? }] }]
// Gear is a single headerless group; skills are grouped by skill line.
const GEAR_GROUPS = [{ group: null, items: GEAR_SETS }];
const SKILL_SELECT_GROUPS = SKILL_GROUPS.map((g) => ({ group: g.group, items: g.skills }));

// Master tables keyed by loadout type, so UI code can stay generic.
const LOADOUT_TYPES = {
  gear: { items: GEAR_SETS, groups: GEAR_GROUPS, byLabel: gearByLabel, label: gearLabel, desc: gearDesc, addPlaceholder: "Search gear set…" },
  skills: { items: SKILLS, groups: SKILL_SELECT_GROUPS, byLabel: skillByLabel, label: skillLabel, desc: () => "", addPlaceholder: "Search skill…" },
};
