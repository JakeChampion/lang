#!/usr/bin/env node
import fs from "node:fs";
import { spawnSync } from "node:child_process";

const plan = JSON.parse(process.env.FERN_CI_SELECTION || '{"full":true}');
function run(command, args) {
  const result = spawnSync(command, args, { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status || 1);
}
if (plan.full || plan.lint?.compiler) {
  run("make", ["fmt-check"]);
} else {
  if (plan.version !== 1 || !Array.isArray(plan.changed)) throw new Error("Invalid format selection.");
  const files = [...new Set(plan.changed)].filter((file) =>
    (file.endsWith(".fern") || file.endsWith(".fern.md")) &&
    !file.startsWith("conformance/") && !file.includes("/testdata/") && fs.existsSync(file));
  if (files.length) {
    run("make", ["build"]);
    run("./bin/fern", ["-fmt", "-d", ...files]);
  }
}
