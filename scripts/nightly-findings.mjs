#!/usr/bin/env node
// nightly-findings DIR — group the nightly differential legs' findings into
// the markdown table the nightly report posts.
//
// Each leg appends one JSON line per failed seed to $FERN_FINDINGS
// (internal/testing/e2e/diff_oracle_findings_test.go): the leg, the failure's
// class, and the pass the decision bisect named. A fault that fails two
// hundred seeds is one row with a count rather than two hundred failures, and
// the same row on consecutive nights is the same bug. Reads every *.jsonl
// under DIR; prints nothing when there are none.
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
    if (!groups.has(key)) groups.set(key, { test: f.test, cls: f.class, pass: f.pass, seeds: [] });
    groups.get(key).seeds.push(f.seed);
  }
  if (groups.size === 0) return "";
  const rows = [...groups.values()].sort(
    (a, b) => b.seeds.length - a.seeds.length || a.test.localeCompare(b.test) || a.cls.localeCompare(b.cls),
  );
  const out = ["| seeds | leg | failure | pass | first seeds |", "| --- | --- | --- | --- | --- |"];
  for (const g of rows) {
    const first = g.seeds.sort((a, b) => a - b).slice(0, 3).join(", ");
    out.push(`| ${g.seeds.length} | ${cell(g.test)} | ${cell(g.cls)} | ${cell(g.pass)} | ${first} |`);
  }
  return out.join("\n") + "\n";
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
