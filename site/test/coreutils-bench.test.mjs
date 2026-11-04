// Unit tests for the coreutils page's bench-log parser.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

import { loadBench } from "../src/lib/coreutils-bench.mjs";

const logDir = new URL("../../docs/coreutils-log/", import.meta.url);

// The first line and the table shape scripts/coreutils-bench writes.
const scriptLog = (columns, rows) =>
  [
    "# coreutils bench, whole catalogue, 2026-11-08, Linux x86-64",
    "",
    "coreutils-bench: 2026-11-08 Linux/x86_64, fern abc1234 (-O, x86-64-linux), yes (GNU coreutils) 9.12, uutils 0.0.24. mean ± σ over ≥20 runs.",
    "",
    `| ${columns.join(" | ")} |`,
    `|${columns.map(() => "---").join("|")}|`,
    ...rows.map((r) => `| ${r.join(" | ")} |`),
  ].join("\n");

test("the committed log gives the numbers docs/COREUTILS.md quotes", () => {
  const file = "2026-10-12-bench.md";
  const b = loadBench({ [`/x/${file}`]: readFileSync(new URL(file, logDir), "utf8") });
  assert.equal(b.gnuVersion, "9.12");
  assert.equal(b.uutilsVersion, "0.0.24");
  assert.deepEqual(b.summary, {
    rows: 669,
    utilities: 106,
    fasterThanGnu: 530,
    comparedGnu: 669,
    fasterThanUutils: 568,
    comparedUutils: 623,
    medianGnu: 1.71,
    medianUutils: 3.24,
    everyRowBeatsGnu: 67,
  });
});

test("the newest dated log wins", () => {
  const cols = ["utility", "workload", "fern (ms)", "gnu (ms)", "gnu / fern"];
  const b = loadBench({
    "/x/2026-10-12-bench.md": scriptLog(cols, [["`yes`", "old", "1.00 ± 0.10", "2.00 ± 0.10", "2.00×"]]),
    "/x/2026-11-08-bench.md": scriptLog(cols, [["`yes`", "new", "1.00 ± 0.10", "3.00 ± 0.10", "3.00×"]]),
    "/x/2026-10-13-listing.md": "not a bench log",
  });
  assert.equal(b.file, "2026-11-08-bench.md");
  assert.equal(b.rows[0].workload, "new");
});

test("a run without uutils has no uutils columns", () => {
  const b = loadBench({
    "/x/2026-11-08-bench.md": scriptLog(
      ["utility", "workload", "fern (ms)", "gnu (ms)", "gnu / fern"],
      [["`yes`", "yes \\| head -c 1G", "300.00 ± 10.00", "380.00 ± 12.00", "1.27×"]],
    ),
  });
  assert.equal(b.hasUutils, false);
  assert.equal(b.uutilsVersion, null);
  assert.equal(b.summary.medianUutils, null);
  assert.equal(b.rows[0].workload, "yes | head -c 1G");
  assert.equal(b.rows[0].vsGnu, 1.27);
});

test("cells the script writes for a failed or short run", () => {
  const cols = ["utility", "workload", "fern (ms)", "gnu (ms)", "uutils (ms)", "gnu / fern", "uutils / fern"];
  const b = loadBench({
    "/x/2026-11-08-bench.md": scriptLog(cols, [
      ["`tac`", "slow", "did not finish", "500.00 ± 9.00", "exit 1", "—", "—"],
      ["`sort`", "big", "4100.00 ± 30.00 (5 runs)", "5000.00 ± 40.00", "6000.00 ± 50.00", "1.22×", "1.46×"],
    ]),
  });
  assert.equal(b.rows[0].fern, null);
  assert.equal(b.rows[0].uutils, null);
  assert.equal(b.rows[0].vsGnu, null);
  assert.equal(b.rows[1].fern, 4100);
  assert.equal(b.summary.comparedGnu, 1);
});

test("a row with the wrong number of cells is an error", () => {
  const cols = ["utility", "workload", "fern (ms)", "gnu (ms)", "gnu / fern"];
  assert.throws(
    () => loadBench({ "/x/2026-11-08-bench.md": scriptLog(cols, [["`yes`", "short", "1.00 ± 0.10"]]) }),
    /bad row/,
  );
});
