#!/usr/bin/env node
// nightly-findings DIR — group the nightly differential legs' findings into
// the markdown table the nightly report posts.
//
// Each leg appends one JSON line per failed seed to $FERN_FINDINGS
// (internal/testing/e2e/diff_oracle_findings_test.go): the leg, the failure's
// class, and the pass the decision bisect named. A fault that fails two
// hundred seeds is one row with a count rather than two hundred failures, and
// the same row on consecutive nights is the same bug. The rows are in
// Furthest-Point-First order (Chen et al., PLDI 2013): the largest group
// first, then each time the row least like every row above it, so distinct
// bugs come first. A row near an earlier one says so. Under the table, each
// row's smallest shrunk program. Reads every *.jsonl under DIR; prints nothing
// when there are none.
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

export function summarize(lines) {
  const groups = new Map();
  for (const line of lines) {
    let f;
    try {
      f = JSON.parse(line);
    } catch {
      continue;
    }
    const key = [f.test, f.class, f.pass].join("\u0000");
    if (!groups.has(key)) groups.set(key, { test: f.test, cls: f.class, pass: f.pass, seeds: [], repro: "" });
    const g = groups.get(key);
    g.seeds.push(f.seed);
    if (f.repro && (!g.repro || f.repro.length < g.repro.length)) {
      g.repro = f.repro;
      g.reproSeed = f.seed;
    }
  }
  if (groups.size === 0) return "";
  const rows = rank(
    [...groups.values()].sort(
      (a, b) => b.seeds.length - a.seeds.length || a.test.localeCompare(b.test) || a.cls.localeCompare(b.cls),
    ),
  );
  const out = ["| # | seeds | leg | failure | pass | first seeds | like |", "| --- | --- | --- | --- | --- | --- | --- |"];
  rows.forEach((g, i) => {
    const first = [...g.seeds].sort((a, b) => a - b).slice(0, 3).join(", ");
    const like = g.near >= 0 && g.dist < LIKE ? `#${g.near + 1}` : "";
    out.push(`| ${i + 1} | ${g.seeds.length} | ${cell(g.test)} | ${cell(g.cls)} | ${cell(g.pass)} | ${first} | ${like} |`);
  });
  rows.forEach((g, i) => {
    if (!g.repro) return;
    out.push(
      "",
      `<details><summary>#${i + 1} ${html(g.test)}: ${html(g.cls)}, seed ${g.reproSeed} shrunk to ${g.repro.length} bytes</summary>`,
      "",
      `<pre>${html(g.repro)}</pre>`,
      "</details>",
    );
  });
  return out.join("\n") + "\n";
}

// A row closer than this to an earlier row is marked like it.
const LIKE = 1 / 3;

// rank orders rows Furthest-Point-First from the first row, and records for
// each row the earlier row nearest to it.
export function rank(rows) {
  const left = rows.map((g) => ({ ...g, grams: grams(g.repro), near: -1, dist: Infinity }));
  const out = [];
  while (left.length > 0) {
    let pick = 0;
    for (let i = 1; i < left.length; i++) if (left[i].dist > left[pick].dist) pick = i;
    const g = left.splice(pick, 1)[0];
    out.push(g);
    for (const r of left) {
      const d = distance(g, r);
      if (d < r.dist) [r.dist, r.near] = [d, out.length - 1];
    }
  }
  return out;
}

// distance is the mean of up to four parts, each from 0 to 1: the classes
// differ, the passes differ, the Jaccard distance of the seed sets, and that of
// the token pairs of the two repros when both rows have one. The leg is not a
// part, because one fault often fails the same seeds on more than one leg.
export function distance(a, b) {
  const parts = [a.cls === b.cls ? 0 : 1, a.pass === b.pass ? 0 : 1, jaccard(new Set(a.seeds), new Set(b.seeds))];
  if (a.repro && b.repro) parts.push(jaccard(a.grams ?? grams(a.repro), b.grams ?? grams(b.repro)));
  return parts.reduce((x, y) => x + y) / parts.length;
}

function grams(src) {
  const toks = String(src ?? "").match(/[A-Za-z_][A-Za-z0-9_]*|[0-9]+|\S/g) ?? [];
  const out = new Set();
  for (let i = 1; i < toks.length; i++) out.add(toks[i - 1] + " " + toks[i]);
  return out;
}

function jaccard(a, b) {
  if (a.size === 0 && b.size === 0) return 0;
  let both = 0;
  for (const x of a) if (b.has(x)) both++;
  return 1 - both / (a.size + b.size - both);
}

function html(s) {
  return String(s).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

// GitHub drops anything that reads as an HTML tag from a comment, so `<` is
// escaped along with the table's own separator.
function cell(s) {
  return String(s).replaceAll("|", "\\|").replaceAll("<", "&lt;");
}

export function readFindings(dir) {
  const lines = [];
  for (const entry of fs.readdirSync(dir, { withFileTypes: true, recursive: true })) {
    if (entry.isFile() && entry.name.endsWith(".jsonl")) {
      const file = path.join(entry.parentPath ?? entry.path, entry.name);
      lines.push(...fs.readFileSync(file, "utf8").split("\n").filter((l) => l.trim() !== ""));
    }
  }
  return lines;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const dir = process.argv[2];
  if (!dir) {
    console.error("usage: nightly-findings DIR");
    process.exit(2);
  }
  process.stdout.write(fs.existsSync(dir) ? summarize(readFindings(dir)) : "");
}
