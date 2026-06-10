/*
 * data.js — ESO master data for the encounters feature.
 *
 * This is SEED data: a representative subset meant to be expanded over time.
 *   - ENCOUNTER_NAME_GROUPS: valid encounter names (grouped by trial). The
 *     name strings here must match the backend allow-list in
 *     internal/models/encounter.go (ValidEncounterNames).
 *   - GEAR_SETS / SKILLS: searchable loadout options. Loadouts store the item
 *     `value` (key); labels/tooltips are looked up from these tables.
 */

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
