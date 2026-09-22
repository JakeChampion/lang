#!/usr/bin/env node
// Run scripts/ci-check-perf against synthetic baseline/report pairs.
//
// WHY THIS EXISTS. The comparator decides whether a perf regression is
// reported and whether it is fatal, and nothing tested it. That mattered:
// #9963 found the alloc baseline 82% behind main for six days, the comparator
// warning correctly on every run into a channel nobody was required to read.
// Making the alloc lane strict rests entirely on FERN_CI_PERF_GATE_STRICT
// actually escalating, which was a documented lever that had never been pulled
// by a test.
//
// The script is invoked as a subprocess, so what is checked is its real exit
// status and its real stdout — not a reimplementation of its awk.
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

const repo = new URL("..", import.meta.url).pathname;
const script = path.join(repo, "scripts", "ci-check-perf");
assert.ok(fs.existsSync(script), "scripts/ci-check-perf must exist");

const dir = fs.mkdtempSync(path.join(os.tmpdir(), "perf-gate-"));
let n = 0;

// baseline / report are `metric<TAB>value [tolerance]` bodies; env is merged
// over the child's environment. Returns the exit status and combined output.
function check(baseline, report, env = {}) {
  const b = path.join(dir, `b${n}.txt`);
  const r = path.join(dir, `r${n}.txt`);
  n++;
  fs.writeFileSync(b, baseline);
  fs.writeFileSync(r, report);
  const out = spawnSync(script, [b, r], {
    encoding: "utf8",
    env: { ...process.env, ...env },
  });
  assert.equal(out.error, undefined, `ci-check-perf failed to run: ${out.error}`);
  return { status: out.status, text: `${out.stdout}${out.stderr}` };
}

let cases = 0;

// A metric on its baseline is no finding, and the summary says so rather than
// staying silent — a silent pass is indistinguishable from a pass that
// measured nothing.
{
  const r = check("m/a\t1000\n", "m/a\t1000\n");
  assert.equal(r.status, 0, "an exact match must not fail");
  assert.match(r.text, /within tolerance/, "an exact match must say every metric is within tolerance");
  assert.doesNotMatch(r.text, /SLOWER|FASTER/, "an exact match must report no drift");
  cases++;
}

// Drift beyond the tolerance is reported in both directions. The FASTER half
// is not cosmetic: a win left unbanked is the ceiling nobody holds, which is
// how #9963's 82% accumulated.
{
  const up = check("m/a\t1000\n", "m/a\t1100\n");
  assert.equal(up.status, 0, "a finding is advisory without the strict lever");
  assert.match(up.text, /SLOWER: m\/a/, "a regression must be named SLOWER");
  assert.match(up.text, /\+10\.00%/, "the delta must be reported");
  assert.match(up.text, /m\/a 1100/, "the corrected baseline must be pasteable");
  cases++;

  const down = check("m/a\t1000\n", "m/a\t900\n");
  assert.equal(down.status, 0, "a win is advisory too");
  assert.match(down.text, /FASTER: m\/a/, "an improvement must be named FASTER");
  assert.match(down.text, /new ceiling/, "an improvement must ask for the baseline to move");
  cases++;
}

// Inside the tolerance is not a finding, and the per-entry third column
// overrides the default — the map benchmarks carry a wider one for their
// per-process hash seed.
{
  const inside = check("m/a\t1000\n", "m/a\t1005\n");
  assert.equal(inside.status, 0);
  assert.doesNotMatch(inside.text, /SLOWER/, "0.5% must be inside the 1% default");
  cases++;

  const tight = check("m/a\t1000\t0.1\n", "m/a\t1005\n");
  assert.match(tight.text, /SLOWER: m\/a/, "a per-entry tolerance of 0.1% must catch 0.5%");
  cases++;

  const wide = check("m/a\t1000\t20\n", "m/a\t1100\n");
  assert.doesNotMatch(wide.text, /SLOWER/, "a per-entry tolerance of 20% must admit 10%");
  cases++;
}

// A baselined metric the report never carried is its own finding: a benchmark
// that was dropped, renamed or failed to build otherwise leaves a row nobody
// notices is absent.
{
  const r = check("m/a\t1000\nm/b\t2000\n", "m/a\t1000\n");
  assert.match(r.text, /MISSING: m\/b/, "a baselined metric absent from the report must be reported");
  cases++;
}

// A measured metric with no baseline is recorded and not compared, and says
// which — otherwise a new benchmark reads as passing a gate it has no entry in.
{
  const r = check("m/a\t1000\n", "m/a\t1000\nm/new\t5\n");
  assert.match(r.text, /UNBASELINED|no baseline/, "an unbaselined metric must be called out");
  assert.match(r.text, /m\/new 5/, "an unbaselined metric must be pasteable into the baseline");
  cases++;
}

// THE LEVER. This is what making a lane strict depends on, and it is the pair
// that had never been exercised: identical inputs, one variable, opposite exit
// statuses.
{
  const advisory = check("m/a\t1000\n", "m/a\t1100\n");
  assert.equal(advisory.status, 0, "without the lever a finding must exit 0");
  cases++;

  const strict = check("m/a\t1000\n", "m/a\t1100\n", { FERN_CI_PERF_GATE_STRICT: "1" });
  assert.equal(strict.status, 1, "with FERN_CI_PERF_GATE_STRICT=1 a finding must exit non-zero");
  assert.match(strict.text, /finding\(s\) and FERN_CI_PERF_GATE_STRICT=1/, "the strict failure must say why");
  cases++;

  // Strict must not fail a clean run: a lane that goes red on no finding is a
  // lane that gets reverted.
  const clean = check("m/a\t1000\n", "m/a\t1000\n", { FERN_CI_PERF_GATE_STRICT: "1" });
  assert.equal(clean.status, 0, "strict must exit 0 when nothing drifted");
  cases++;

  // And it must escalate the other findings too, not only SLOWER: a MISSING
  // metric under strict is how a dropped benchmark stops being a warning.
  const missing = check("m/a\t1000\nm/b\t2000\n", "m/a\t1000\n", { FERN_CI_PERF_GATE_STRICT: "1" });
  assert.equal(missing.status, 1, "strict must escalate a MISSING metric");
  cases++;
}

// THE CEILING, which is what perf.yml actually sets on the alloc lane. It is
// deliberately NOT the tolerance: the tolerance is a significance threshold at
// 1%, and the alloc pair drifts a few tenths of a percent from unrelated merges
// in a couple of hours, so a fatal 1% would fail PRs for drift they did not
// cause. These cases pin that the two are independent in both directions.
{
  // Under the ceiling: reported as a finding, but not fatal. This is the
  // everyday case the design exists to keep quiet.
  const under = check("m/a\t1000\n", "m/a\t1050\n", { FERN_CI_PERF_GATE_MAX_DRIFT: "10" });
  assert.equal(under.status, 0, "5% must not breach a 10% ceiling");
  assert.match(under.text, /SLOWER: m\/a/, "it is still a finding: the signal does not go away");
  cases++;

  // Over the ceiling: fatal, and the message names the metric, the drift and
  // the remedy rather than only the variable.
  const over = check("m/a\t1000\n", "m/a\t1200\n", { FERN_CI_PERF_GATE_MAX_DRIFT: "10" });
  assert.equal(over.status, 1, "20% must breach a 10% ceiling");
  assert.match(over.text, /m\/a drifted/, "the ceiling failure must name the metric");
  assert.match(over.text, /ceiling of 10%/, "the ceiling failure must name the ceiling");
  assert.match(over.text, /re-bank/, "the ceiling failure must say what to do");
  cases++;

  // A WIN past the ceiling is fatal too. #9963 is the case where a number left
  // high became a ceiling nobody held; a large improvement has to be banked for
  // the same reason a regression does.
  const win = check("m/a\t1000\n", "m/a\t800\n", { FERN_CI_PERF_GATE_MAX_DRIFT: "10" });
  assert.equal(win.status, 1, "a 20% improvement must breach the ceiling too");
  assert.match(win.text, /drifted/, "the ceiling reads absolute drift, not signed");
  cases++;

  // Unset means no ceiling, which is every other perf lane.
  const none = check("m/a\t1000\n", "m/a\t1200\n");
  assert.equal(none.status, 0, "without the ceiling variable a 20% move stays advisory");
  cases++;

  // The ceiling is independent of the tolerance: a per-entry tolerance wide
  // enough to silence the finding must not also silence the ceiling, or a lane
  // could be made unbreachable by widening one number.
  const wideTol = check("m/a\t1000\t50\n", "m/a\t1200\n", { FERN_CI_PERF_GATE_MAX_DRIFT: "10" });
  assert.equal(wideTol.status, 1, "a 50% tolerance must not raise the 10% ceiling");
  assert.doesNotMatch(wideTol.text, /SLOWER/, "and the finding is genuinely silenced, so the ceiling is what fired");
  cases++;

  // Nothing measured must not read as 0% drift and sail through the ceiling.
  // It does not, and by an earlier guard than the one I expected: a wholly
  // empty report never reaches the comparison at all, and the script says so
  // instead of computing a drift from nothing. Asserted because that guard is
  // the difference between "no regression" and "no measurement", which is the
  // same vacuity trap this whole selftest exists for.
  const empty = check("m/a\t1000\n", "", { FERN_CI_PERF_GATE_MAX_DRIFT: "10" });
  assert.match(empty.text, /nothing was measured, so nothing was compared/, "an empty report must say nothing was measured");
  assert.match(empty.text, /ran blind/, "and must warn rather than pass quietly");
  assert.doesNotMatch(empty.text, /drifted/, "the ceiling must not fire on a drift computed from nothing");
  cases++;
}

fs.rmSync(dir, { recursive: true, force: true });
console.log(`perf-gate-selftest: ${cases} cases pass against scripts/ci-check-perf`);
