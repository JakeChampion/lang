#!/usr/bin/env node
// Pin scripts/nightly-findings.mjs: findings group by signature, the largest
// group first, and a cell cannot break the table or lose text to GitHub's
// tag stripping.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { summarize } from "./nightly-findings.mjs";

const line = (test, seed, cls, pass) => JSON.stringify({ test, seed, class: cls, pass });

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
