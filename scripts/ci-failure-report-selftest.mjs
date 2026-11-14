#!/usr/bin/env node
// Tests of the parts of ci-failure-report.mjs that make the report from a log.
// The log lines are from job 132508689286 of this repository.
import assert from "node:assert/strict";
import { MARKER, body, errorExcerpt, failedTests, render } from "./ci-failure-report.mjs";

const ts = "2026-11-14T16:42:40.2274379Z ";
const goLog = [
  "##[endgroup]",
  "shard 0/4: 556 tests",
  "=== RUN   TestOther",
  "--- PASS: TestOther (0.01s)",
  "=== RUN   TestInterpScriptRfc3339/date_parse_iso_rejects_malformed_input",
  '    interp_script_test.go:1148: stdout = "rejected 2026-13-01\\n",',
  '         want "ACCEPTED 2026-13-01\\n" for a <date>',
  "        stderr: ",
  "--- FAIL: TestInterpScriptRfc3339/date_parse_iso_rejects_malformed_input (0.21s)",
  "=== RUN   TestInterpScriptRfc3339",
  "--- FAIL: TestInterpScriptRfc3339 (1.27s)",
  "\u001b[32m✓\u001b[0m  worker-1 (10m22.253s)",
  "=== \u001b[31mFAIL\u001b[0m: worker-0 TestInterpScriptRfc3339 (1.27s)",
  "##[error]Process completed with exit code 1.",
].map((l) => ts + l).join("\n");

let cases = 0;

// A failed subtest gives the reason, so its parent is left out. The excerpt
// starts at the RUN line of the test and has no timestamps.
{
  const tests = failedTests(goLog);
  assert.deepEqual(tests.map((t) => t.name), ["TestInterpScriptRfc3339/date_parse_iso_rejects_malformed_input"]);
  assert.equal(tests[0].lines[0], "=== RUN   TestInterpScriptRfc3339/date_parse_iso_rejects_malformed_input");
  assert.equal(tests[0].lines.at(-1), "--- FAIL: TestInterpScriptRfc3339/date_parse_iso_rejects_malformed_input (0.21s)");
  assert.ok(!tests[0].lines.some((l) => l.includes("TestOther") || l.includes("2026-11-14T")));
  cases++;
}

// A long test output keeps its first and its last lines, and a long line is
// cut.
{
  const noisy = ["=== RUN   TestLong", "    long_test.go:7: the assertion", ...Array.from({ length: 60 }, (_, i) => `    line ${i}`), `    ${"x".repeat(900)}`, "--- FAIL: TestLong (1.00s)"].join("\n");
  const [t] = failedTests(noisy);
  assert.equal(t.lines.length, 25);
  assert.equal(t.lines[1], "    long_test.go:7: the assertion");
  assert.equal(t.lines[8], "... 40 lines are left out ...");
  assert.equal(t.lines.at(-1), "--- FAIL: TestLong (1.00s)");
  assert.ok(t.lines.at(-2).length < 420 && t.lines.at(-2).endsWith(" ..."));
  cases++;
}

// A log with no Go test failure gives the lines before the first error, with
// no group markers and no colour codes.
{
  const log = ["##[group]Run make check-sources", "\u001b[36;1mmake check-sources\u001b[0m", "##[endgroup]",
    "compiler/checker.fern:10:3: error[E009]: no such name", "make: *** [Makefile:196: check-sources] Error 1",
    "##[error]Process completed with exit code 2.", "Post job cleanup."].map((l) => ts + l).join("\n");
  assert.deepEqual(failedTests(log), []);
  assert.deepEqual(errorExcerpt(log), ["make check-sources", "compiler/checker.fern:10:3: error[E009]: no such name",
    "make: *** [Makefile:196: check-sources] Error 1", "##[error]Process completed with exit code 2."]);
  cases++;
}

// The report names each job, its failed step and its tests. The excerpt is
// escaped, because GitHub removes a span that reads as an HTML tag.
{
  const text = render([
    { name: "Full suite / Test e2e other / shard0", url: "https://example.test/job/1", step: "Run the tests", log: goLog },
    { name: "Lint / lint", url: "https://example.test/job/2", step: "", log: null },
  ]);
  assert.match(text, /### \[Full suite \/ Test e2e other \/ shard0\]\(https:\/\/example\.test\/job\/1\)/);
  assert.match(text, /Failed step: `Run the tests`/);
  assert.match(text, /- `TestInterpScriptRfc3339\/date_parse_iso_rejects_malformed_input`/);
  assert.match(text, /for a &lt;date&gt;/);
  assert.doesNotMatch(text, /<date>/);
  assert.match(text, /The log of this job could not be read\./);
  cases++;
}

// More jobs than the report shows are named in one line.
{
  const jobs = Array.from({ length: 8 }, (_, i) => ({ name: `job${i}`, url: `https://example.test/job/${i}`, step: "", log: "" }));
  const text = render(jobs);
  assert.match(text, /2 more job\(s\) failed: \[job6\]\(https:\/\/example\.test\/job\/6\), \[job7\]/);
  assert.doesNotMatch(text, /### \[job6\]/);
  cases++;
}

// The comment starts with the marker that finds it again, and a run that
// passes replaces the report.
{
  const sha = "34d740f3f6f21553b59b972964a09da225f48299";
  const red = body({ conclusion: "failure", sha, runUrl: "https://example.test/run", jobs: [{ name: "j", url: "u", step: "s", log: goLog }] });
  assert.ok(red.startsWith(`${MARKER}\n## CI failed on \`34d740f3\``));
  assert.match(red, /1 job\(s\) failed/);
  const none = body({ conclusion: "failure", sha, runUrl: "https://example.test/run", jobs: [] });
  assert.match(none, /No job has the conclusion `failure`/);
  const green = body({ conclusion: "success", sha, runUrl: "https://example.test/run", jobs: [] });
  assert.ok(green.startsWith(`${MARKER}\n## CI passed on \`34d740f3\``));
  cases++;
}

console.log(`ci-failure-report-selftest: ${cases} cases pass`);
