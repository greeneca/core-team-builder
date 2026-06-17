#!/usr/bin/env node
/*
 * gen.js - generate backend/internal/esoref/data_gen.go from the frontend's
 * single-source ESO reference data.
 *
 * The Discord bot (Go) needs the same human-readable labels and gear
 * abbreviations the web UI uses, but those tables live only in the frontend
 * (frontend/js/gear-skills.js + frontend/js/data.js). Rather than maintain a
 * second copy by hand, this script loads those files in a sandbox, reads the
 * resulting tables, and emits a Go file of key->label maps. Run it whenever the
 * frontend data changes:
 *
 *     node tools/gen-esoref/gen.js
 *
 * The generated file is committed so the Docker build (which has no Node) just
 * compiles it.
 */

"use strict";

const fs = require("fs");
const path = require("path");
const vm = require("vm");
const { execFileSync } = require("child_process");

const repoRoot = path.resolve(__dirname, "..", "..");
const gearSkillsPath = path.join(repoRoot, "frontend", "js", "gear-skills.js");
const dataPath = path.join(repoRoot, "frontend", "js", "data.js");
const outPath = path.join(repoRoot, "backend", "internal", "esoref", "data_gen.go");

// Minimal sandbox: the data files only run top-level array/object builders at
// load time (no DOM), but we stub window/document defensively in case a helper
// definition references them.
const sandbox = {
  Intl, Date, Math, Object, Array, JSON, console,
  String, Number, Boolean, RegExp, Symbol, Error,
  Set, Map, WeakMap, WeakSet,
  parseInt, parseFloat, isNaN, isFinite,
};
sandbox.globalThis = sandbox;
sandbox.window = sandbox;
sandbox.document = { createElement: () => ({ style: {} }) };
vm.createContext(sandbox);

const gearSkillsSrc = fs.readFileSync(gearSkillsPath, "utf8");
const dataSrc = fs.readFileSync(dataPath, "utf8");

// Concatenate both files plus an epilogue into ONE script: top-level `const`
// bindings from earlier in the same script are in scope for the epilogue, so we
// can capture them onto the sandbox (they are NOT exposed as context properties
// otherwise).
const epilogue = `
globalThis.__esoref = {
  GEAR_SETS, GEAR_ABBREVIATIONS, SKILLS, MASTERIES, SKILL_LINES, POTIONS,
  MUNDUS_STONES, BLUE_CP, CRIT_DMG_SOURCES, PEN_EXTRA_SOURCES,
  SCRIBED_BUFFS, GRIMOIRE_SKILLS, BANNER_BEARER_FOCUS,
  ROLES, CLASSES, RACES, DAYS,
  // Crit/penetration coverage data + constants, so the bot can compute the
  // team's "self required" pen and crit damage the same way the web UI does.
  CRIT_GROUP_SOURCES, PEN_GROUP_SOURCES,
  CRIT_CAP, CRIT_BASE, PEN_TARGET, ELEMENTAL_CATALYST_PER_ELEMENT, ANTHELMIR_PEN_PER_WD,
  // Tracked group buffs (incl. the selfBuff flag) so the bot can list which
  // self-buffs the team is missing.
  BUFFS,
};
`;

vm.runInContext(gearSkillsSrc + "\n" + dataSrc + "\n" + epilogue, sandbox, {
  filename: "frontend-data-combined.js",
});

const ref = sandbox.__esoref;

// listToMap: turn [{value,label}] into { value: label } (dropping the blank
// "" -> "—" placeholder, which the Go fallback handles).
function listToMap(list) {
  const out = {};
  for (const item of list || []) {
    if (!item || item.value === "" || item.value == null) continue;
    out[item.value] = item.label;
  }
  return out;
}

function goStringLit(s) {
  return JSON.stringify(String(s));
}

// Emit a Go map literal with keys sorted for stable diffs.
function emitMap(name, obj) {
  const keys = Object.keys(obj).sort();
  let out = `var ${name} = map[string]string{\n`;
  for (const k of keys) {
    out += `\t${goStringLit(k)}: ${goStringLit(obj[k])},\n`;
  }
  out += "}\n";
  return out;
}

const maps = {
  gearLabels: listToMap(ref.GEAR_SETS),
  gearAbbrevs: { ...(ref.GEAR_ABBREVIATIONS || {}) },
  skillLabels: listToMap(ref.SKILLS),
  masteryLabels: listToMap(ref.MASTERIES),
  skillLineLabels: listToMap(ref.SKILL_LINES),
  potionLabels: listToMap(ref.POTIONS),
  mundusLabels: listToMap(ref.MUNDUS_STONES),
  cpBlueLabels: listToMap(ref.BLUE_CP),
  critDmgLabels: listToMap(ref.CRIT_DMG_SOURCES),
  penExtraLabels: listToMap(ref.PEN_EXTRA_SOURCES),
  scribedBuffLabels: listToMap(ref.SCRIBED_BUFFS),
  bannerBearerFocusLabels: listToMap(ref.BANNER_BEARER_FOCUS),
  roleLabels: listToMap(ref.ROLES),
  classLabels: listToMap(ref.CLASSES),
  raceLabels: listToMap(ref.RACES),
  dayLabels: listToMap(ref.DAYS),
};

let body = "";
for (const [name, obj] of Object.entries(maps)) {
  body += emitMap(name, obj) + "\n";
}

// --- Crit / penetration coverage data (structured) ---
// These mirror the JS computeCritCoverage / computePenCoverage GROUP-source
// detection so the bot can derive the team's "self required" pen and crit.

function emitStrSlice(a) {
  return `[]string{${(a || []).map(goStringLit).join(", ")}}`;
}

// emitDetect serializes a JS `detect` object into a Go DetectMap literal.
function emitDetect(d) {
  d = d || {};
  const parts = [];
  const arr = (field, key) => {
    const v = d[key];
    if (Array.isArray(v) && v.length) parts.push(`${field}: ${emitStrSlice(v)}`);
  };
  arr("Gear", "gear");
  arr("Skills", "skills");
  arr("Potions", "potions");
  arr("Masteries", "masteries");
  arr("SkillLines", "skillLines");
  arr("CP", "cp");
  arr("Classes", "classes");
  arr("Race", "race");
  arr("Mundus", "mundus");
  arr("Scribed", "scribed");
  if (Array.isArray(d.classPassive) && d.classPassive.length) {
    const cps = d.classPassive
      .map((c) => `{Class: ${goStringLit(c.class)}, Line: ${goStringLit(c.line)}}`)
      .join(", ");
    parts.push(`ClassPassive: []ClassPassive{${cps}}`);
  }
  return `DetectMap{${parts.join(", ")}}`;
}

function emitCritGroup(list) {
  let out = "var CritGroupSources = []CritGroupSource{\n";
  for (const s of list || []) {
    out += `\t{Value: ${goStringLit(s.value)}, Label: ${goStringLit(s.label)}, Pct: ${s.pct || 0}, PerElement: ${!!s.perElement}, Detect: ${emitDetect(s.detect)}},\n`;
  }
  out += "}\n";
  return out;
}

function emitPenGroup(list) {
  let out = "var PenGroupSources = []PenGroupSource{\n";
  for (const s of list || []) {
    out += `\t{Value: ${goStringLit(s.value)}, Label: ${goStringLit(s.label)}, Pen: ${s.pen || 0}, PerWeaponDamage: ${!!s.perWeaponDamage}, Detect: ${emitDetect(s.detect)}},\n`;
  }
  out += "}\n";
  return out;
}

function emitPenExtra(list) {
  let out = "var PenExtraSources = []PenExtraSource{\n";
  for (const s of list || []) {
    out += `\t{Value: ${goStringLit(s.value)}, Label: ${goStringLit(s.label)}, Pen: ${s.pen || 0}, Bucket: ${goStringLit(s.bucket || "")}, MaxStack: ${s.maxStack || 0}},\n`;
  }
  out += "}\n";
  return out;
}

// emitBuffs serializes the tracked group buffs (value/label, the selfBuff flag,
// and their group-source detection) so the bot can list missing self-buffs.
function emitBuffs(list) {
  let out = "var Buffs = []Buff{\n";
  for (const b of list || []) {
    out += `\t{Value: ${goStringLit(b.value)}, Label: ${goStringLit(b.label)}, SelfBuff: ${!!b.selfBuff}, Detect: ${emitDetect(b.sources)}},\n`;
  }
  out += "}\n";
  return out;
}

body += `// Coverage constants (mirror data.js).
const (
	CritCap                     = ${ref.CRIT_CAP}
	CritBase                    = ${ref.CRIT_BASE}
	PenTarget                   = ${ref.PEN_TARGET}
	ElementalCatalystPerElement = ${ref.ELEMENTAL_CATALYST_PER_ELEMENT}
)

// AnthelmirPenPerWD is the Armor reduction per point of (higher) Weapon/Spell
// Damage from Anthelmir's Construct.
const AnthelmirPenPerWD = ${ref.ANTHELMIR_PEN_PER_WD}

`;
// emitStrSet serializes a JS Set/array of keys into a Go map[string]bool literal
// with keys sorted for stable diffs.
function emitStrSet(name, set) {
  const keys = Array.from(set || []).sort();
  let out = `var ${name} = map[string]bool{\n`;
  for (const k of keys) {
    out += `\t${goStringLit(k)}: true,\n`;
  }
  out += "}\n";
  return out;
}

body += emitCritGroup(ref.CRIT_GROUP_SOURCES) + "\n";
body += emitPenGroup(ref.PEN_GROUP_SOURCES) + "\n";
body += emitPenExtra(ref.PEN_EXTRA_SOURCES) + "\n";
body += emitBuffs(ref.BUFFS) + "\n";
body += emitStrSet("grimoireSkills", ref.GRIMOIRE_SKILLS) + "\n";

const header = `// Code generated by tools/gen-esoref/gen.js; DO NOT EDIT.
//
// Source of truth: frontend/js/gear-skills.js and frontend/js/data.js.
// Regenerate with:  node tools/gen-esoref/gen.js

package esoref

`;

fs.mkdirSync(path.dirname(outPath), { recursive: true });
fs.writeFileSync(outPath, header + body);

// Run gofmt so the generated file matches what `gofmt`/CI expects (it aligns the
// map literal columns). Best-effort: warn but don't fail if gofmt is absent.
try {
  execFileSync("gofmt", ["-w", outPath], { stdio: "inherit" });
} catch (err) {
  console.warn(`warning: gofmt failed (${err.message}); run "gofmt -w ${outPath}" manually`);
}

const counts = Object.entries(maps)
  .map(([n, o]) => `${n}=${Object.keys(o).length}`)
  .join(", ");
console.log(`wrote ${path.relative(repoRoot, outPath)} (${counts})`);
