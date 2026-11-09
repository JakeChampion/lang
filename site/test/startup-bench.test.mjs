// Unit tests for the startup bench log parser.

import assert from "node:assert/strict";
import { test } from "node:test";

import { loadStartup } from "../src/lib/startup-bench.mjs";

const log = [
  "# Startup bench, 2026-11-15, Linux x86-64",
  "",
  "startup-bench: Linux/x86_64, hyperfine 1.18.0, 300 runs after 30 warm-up runs, no shell. Not measured: none.",
  "",
  "| language | version | mean (ms) | σ (ms) | size (bytes) |",
  "|---|---|---|---|---|",
  "| Fern | fern 0123abcd | 0.41 | 0.05 | 9408 |",
  "| Node.js | v24.18.0 | 31.27 | 2.73 | interpreter |",
].join("\n");

test("reads the newest log's method and rows", () => {
  const b = loadStartup({ "../a/2026-11-08-bench.md": log.replace("0.41", "9.99"), "../a/2026-11-15-bench.md": log });
  assert.equal(b.date, "2026-11-15");
  assert.equal(b.platform, "Linux x86-64");
  assert.match(b.method, /^Linux\/x86_64, hyperfine/);
  assert.deepEqual(b.rows[0], { language: "Fern", version: "fern 0123abcd", mean: 0.41, stddev: 0.05, bytes: 9408 });
  assert.equal(b.rows[1].bytes, null);
});

test("gives null when there is no log", () => {
  assert.equal(loadStartup({}), null);
});
