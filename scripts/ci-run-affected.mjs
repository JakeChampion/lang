#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import path from "node:path";
import { liveTests } from "./ci-test-selection.mjs";

const selection = JSON.parse(process.env.FERN_CI_SELECTION || "null");
if (!selection || selection.version !== 1 || selection.full || !selection.tests) {
  throw new Error("Affected tests require a selective dependency plan.");
}
const packages = new Set(["internal/testing/e2e", "internal/testing/e2ecompiler"]);
let count = 0;
let failed = false;
for (const [pkg, tests] of Object.entries(selection.tests)) {
  if (!packages.has(pkg) || !Array.isArray(tests) || !tests.length || tests.some((name) => !/^Test[A-Za-z0-9_]+$/.test(name))) {
    throw new Error(`Invalid consumer selection for ${pkg}`);
  }
  const selected = liveTests(pkg, tests);
  count += tests.length;
  if (!selected.length) continue;
  const pattern = `^(${selected.join("|")})$`;
  const filename = path.join(process.env.RUNNER_TEMP || "/tmp", `affected-${path.basename(pkg)}.jsonl`);
  console.log(`${pkg}: ${selected.length} affected tests`);
  const result = spawnSync("gotestsum", ["--format", "standard-verbose", "--", "-count=1", "-timeout", "35m", "-run", pattern, `./${pkg}/`], {
    stdio: "inherit",
    env: { ...process.env, GOTESTSUM_JSONFILE: filename },
  });
  if (result.error) throw result.error;
  failed ||= result.status !== 0;
}
if (!count) throw new Error("The consumer lane selected no tests.");
process.exit(failed ? 1 : 0);
