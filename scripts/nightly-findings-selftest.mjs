#!/usr/bin/env node
// Pin scripts/nightly-findings.mjs: findings group by signature, the largest
// group first, each group shows its smallest repro, and neither a cell nor a
// repro can break the table or lose text to GitHub's tag stripping.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { summarize } from "./nightly-findings.mjs";

const line = (test, seed, cls, pass, repro) => JSON.stringify({ test, seed, class: cls, pass, repro });

assert.equal(summarize([]), "");
assert.equal(summarize(["not json"]), "");

const table = summarize([
  line("TestDifferential_SelfHostWasm", 9, "self-host wasm exit", "none"),
  line("TestDifferential_SelfHostX86_64", 30, "refused: copy or phi type", "inline"),
  line("TestDifferential_SelfHostX86_64", 3, "refused: copy or phi type", "inline"),
  line("TestDifferential_SelfHostX86_64", 12, "refused: copy or phi type", "inline"),
  line("TestDifferential_SelfHostX86_64", 7, "refused: copy or phi type", "inline"),
  line("TestDifferential_SelfHostArm64", 5, "a|b <T> c", "reuse"),
]).split("\n");
assert.equal(table[0], "| seeds | leg | failure | pass | first seeds |");
assert.equal(table[2], "| 4 | TestDifferential_SelfHostX86_64 | refused: copy or phi type | inline | 3, 7, 12 |");
assert.equal(table[3], "| 1 | TestDifferential_SelfHostArm64 | a\\|b &lt;T> c | reuse | 5 |");
assert.equal(table[4], "| 1 | TestDifferential_SelfHostWasm | self-host wasm exit | none | 9 |");
assert.ok(!table.some((l) => l.startsWith("<details>")), "a group with no repro shows none");

const repros = summarize([
  line("T", 4, "refused: malformed IR", "inline", "function main(): i32 { return 1 << 2; }"),
  line("T", 8, "refused: malformed IR", "inline", "function main(): i32 { return 0; }"),
  line("T", 9, "refused: malformed IR", "inline"),
]).split("\n");
assert.equal(repros[2], "| 3 | T | refused: malformed IR | inline | 4, 8, 9 |");
assert.equal(repros[4], "<details><summary>T: refused: malformed IR, seed 8 shrunk to 34 bytes</summary>");
assert.equal(repros[6], "<pre>function main(): i32 { return 0; }</pre>");
const escaped = summarize([line("T", 4, "c", "p", "a << b && c > d")]);
assert.match(escaped, /<pre>a &lt;&lt; b &amp;&amp; c &gt; d<\/pre>/);

// The command line reads every .jsonl under the directory, as the nightly
// report's downloaded artifacts lay them out, and prints nothing for none.
const dir = fs.mkdtempSync(path.join(os.tmpdir(), "findings-"));
const script = new URL("./nightly-findings.mjs", import.meta.url).pathname;
assert.equal(execFileSync("node", [script, dir], { encoding: "utf8" }), "");
fs.mkdirSync(path.join(dir, "findings-a"));
fs.mkdirSync(path.join(dir, "findings-b"));
fs.writeFileSync(path.join(dir, "findings-a", "findings.jsonl"), line("T", 1, "c", "p") + "\n");
fs.writeFileSync(path.join(dir, "findings-b", "findings.jsonl"), line("T", 2, "c", "p") + "\n\n");
const out = execFileSync("node", [script, dir], { encoding: "utf8" });
assert.match(out, /\| 2 \| T \| c \| p \| 1, 2 \|/);
fs.rmSync(dir, { recursive: true });

console.log("nightly-findings selftest: ok");
