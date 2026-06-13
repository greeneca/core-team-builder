/*
 * gear-skills.js — ESO gear-set + skill (ability) catalogs.
 *
 * Extracted from data.js so these frequently-updated tables are easy to edit in
 * isolation. This file holds the SOURCE-OF-TRUTH catalogs plus their derived
 * flat lists; the lookups/helpers that consume them stay in data.js.
 *
 * Load order: this file MUST be included BEFORE data.js (and before
 * components.js / app.js), since data.js references these globals
 * (GEAR_SET_GROUPS, GEAR_SETS, SKILL_GROUPS, SKILLS, GEAR_ABBREVIATIONS) at
 * load time. See the <script> tags in index.html.
 *
 * Loadouts store the item `value` (key); labels/tooltips are looked up from
 * these tables. To add a gear set: add an entry under the relevant GEAR_SET_GROUPS
 * group (and, optionally, a short abbreviation in GEAR_ABBREVIATIONS). To add a
 * skill: add an entry under the relevant SKILL_GROUPS skill line.
 */

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
    { value: "roar_of_alkosh", label: "Roar of Alkosh", desc: "5pc: Using a synergy deals damage and applies Major Vulnerability to nearby enemies. (Maw of Lorkhaj)" },
    { value: "touch_of_zen", label: "Touch of Z'en", desc: "5pc: Increases your damage to an enemy by 5% for every damage-over-time effect you have on them, up to 25%. (Halls of Fabrication)" },
    { value: "way_of_martial_knowledge", label: "Way of Martial Knowledge", desc: "5pc: Damaging an enemy increases their damage taken from your next direct attack. (Maelstrom Arena)" },
    { value: "elemental_catalyst", label: "Elemental Catalyst", desc: "5pc: Applying Flame, Frost, and Shock status effects grants the target stacks of increased Critical Damage taken. (Vateshran Hollows)" },
  ] },
  { group: "Monster Sets", sets: [
    { value: "slimecraw", label: "Slimecraw", desc: "1pc: Grants Minor Berserk, increasing your damage done by 5%. (Monster set)" },
    { value: "zaan", label: "Zaan", desc: "2pc: Critical hits beam the closest enemy, dealing increasing Flame Damage over time. (Monster set)" },
    { value: "nazaray", label: "Nazaray", desc: "2pc: Activating an Ultimate deals Magic Damage and grants you Weapon and Spell Damage. (Monster set)" },
    { value: "selene", label: "Selene", desc: "2pc: Dealing melee damage summons a primal beast that mauls your enemy for Physical Damage. (Monster set)" },
    { value: "encratiss_behemoth", label: "Encratis's Behemoth", desc: "2pc: Damaging a Flame-debuffed enemy reduces their Flame Damage and increases yours. (Monster set)" },
    { value: "baron_zaudrus", label: "Baron Zaudrus", desc: "2pc: Dealing damage builds stacks; at 3 stacks deal Magic Damage and grant Minor Brittle. (Monster set)" },
    { value: "nunatak", label: "Nunatak", desc: "2pc: Damaging an enemy with a Frost ability applies Major Brittle, increasing their Critical Damage taken. (Monster set)" },
    { value: "symphony_of_blades", label: "Symphony of Blades", desc: "2pc: Healing a low-resource ally grants them Magicka and Stamina restoration over time. (Monster set)" },
    { value: "ozezan_the_inferno", label: "Ozezan the Inferno", desc: "2pc: Taking or healing critical damage grants you and your healer Minor Courage and damage mitigation. (Monster set)" },
    { value: "tremorscale", label: "Tremorscale", desc: "2pc: Taunting an enemy with a taunt ability deals Physical Damage and reduces the target's Physical and Spell Resistance (penetration debuff). (Monster set)" },
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
    { value: "harpooners_wading_kilt", label: "Harpooner's Wading Kilt", desc: "1pc: Dealing direct damage grants a stack of Hunter's Focus (up to 5), each increasing your Critical Damage; stacks reset when you take damage. (Mythic)" },
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
    { value: "rune_of_the_colorless_pool", label: "Rune of the Colorless Pool", desc: "Paralyzes and stuns the enemy, applying Minor Vulnerability (+5% damage taken) and Minor Brittle (+10% Critical Damage taken) for 20 seconds." },
  ] },
  { group: "Dragonknight · Ardent Flame", skills: [
    { value: "molten_whip", label: "Molten Whip", desc: "Melee Flame Damage attack, empowered by Seething Fury stacks from other Ardent Flame abilities. DK spammable." },
    { value: "venomous_claw", label: "Venomous Claw", desc: "Applies a Poison Damage over time that ramps up in intensity over its duration." },
    { value: "noxious_breath", label: "Noxious Breath", desc: "Frontal cone dealing Poison Damage over time and applying Major Breach to reduce enemy resistances." },
    { value: "engulfing_flames", label: "Engulfing Flames", desc: "Flame attack dealing damage over time and increasing the Flame Damage the target takes." },
  ] },
  { group: "Dragonknight · Earthen Heart", skills: [
    { value: "igneous_shield", label: "Igneous Shield", desc: "Grants you and nearby allies a damage shield and Major Mending (increased healing done)." },
    { value: "magma_fist", label: "Magma Fist", desc: "Earthen Heart attack dealing Flame Damage; the Magma Shell-style morph applies Heat Shock, increasing the target's damage taken." },
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
    { value: "pierce_armor", label: "Pierce Armor", desc: "Taunt that reduces the enemy's Physical and Spell Resistance (Major Breach, Minor Breach, and Major Fracture)." },
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
    { value: "aggressive_horn", label: "Aggressive Horn", desc: "Warhorn ultimate that grants you and nearby allies Major Force, increasing Critical Damage. (\"Horn\")" },
  ] },
];

// Flat skill list (derived) for label/value lookups. Each entry carries the
// `group` (skill line) it belongs to.
const SKILLS = SKILL_GROUPS.flatMap((g) =>
  g.skills.map((s) => ({ ...s, group: g.group }))
);

// Short, Discord-friendly gear-set abbreviations, keyed by set value. These are
// sensible defaults meant to be hand-tuned later — edit freely. Any set not
// listed here falls back to an auto-generated acronym (see gearAbbrev in data.js).
const GEAR_ABBREVIATIONS = {
  // 5-piece sets
  perfected_relequen: "pRele",
  relequen: "Rele",
  pillar_of_nirn: "Pillar",
  sul_xans_torment: "Sul-Xan",
  whorl_of_the_depths: "Whorl",
  coral_riptide: "Coral",
  deadly_strike: "Deadly",
  tzogvins_warband: "Tzogvin",
  kinras_wrath: "Kinras",
  bahseis_mania: "Bahsei",
  ansuuls_torment: "Ansuul",
  highland_sentinel: "Highland",
  aegis_caller: "Aegis",
  perfected_olorime: "pOlo",
  spell_power_cure: "SPC",
  master_architect: "MA",
  roaring_opportunist: "RO",
  saxhleel_champion: "Sax",
  pearlescent_ward: "PW",
  lucent_echoes: "LE",
  powerful_assault: "PA",
  pillagers_profit: "PP",
  z_ens_redress: "Redress",
  crimson_oaths_rive: "Crimson",
  plaguebreak: "Plague",
  roar_of_alkosh: "Alkosh",
  touch_of_zen: "ToZ",
  way_of_martial_knowledge: "WMK",
  elemental_catalyst: "Catalyst",
  // Monster sets
  slimecraw: "Slime",
  zaan: "Zaan",
  nazaray: "Nazaray",
  selene: "Selene",
  encratiss_behemoth: "Encratis",
  baron_zaudrus: "Zaudrus",
  nunatak: "Nunatak",
  symphony_of_blades: "Symphony",
  ozezan_the_inferno: "Ozezan",
  tremorscale: "Tremor",
  // Arena weapons
  merciless_charge: "Merciless",
  crushing_wall: "Crushing",
  wrath_of_elements: "Wrath",
  frenzied_momentum: "Frenzied",
  masters_destruction_staff: "Master DS",
  // Mythic items
  velothi_ur_mages_amulet: "Velothi",
  oakensoul_ring: "Oakensoul",
  sea_serpents_coil: "Sea-Serpent",
  spaulder_of_ruin: "Spaulder",
  death_dealers_fete: "Death Dealer",
  harpooners_wading_kilt: "Kilt",
};
