#!/usr/bin/env node
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

const root = path.resolve(path.dirname(new URL(import.meta.url).pathname), "..");
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), "fern-consumer-tests-"));
try {
  const bin = path.join(temporary, "bin");
  fs.mkdirSync(bin);
  fs.writeFileSync(path.join(bin, "go"), '#!/bin/sh\nprintf "%s\\n" "${CI_TEST_AVAILABLE:-TestOne}"\n', { mode: 0o755 });
  fs.writeFileSync(path.join(bin, "gotestsum"), '#!/bin/sh\nprintf "%s\\n" "$*" >> "$CI_TEST_CALLS"\nexit "${CI_TEST_RESULT:-0}"\n', { mode: 0o755 });
  const pkg = "internal/testing/e2e";
  fs.mkdirSync(path.join(temporary, pkg), { recursive: true });
  fs.writeFileSync(path.join(temporary, pkg, "cases_test.go"), "package e2e\nfunc TestOne() {}\nfunc TestOtherPlatform() {}\n");
  const calls = path.join(temporary, "calls");
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, CI_TEST_CALLS: calls, RUNNER_TEMP: temporary };
  const request = { version: 1, full: false, tests: { [pkg]: ["TestOne"] } };
  const run = (selection, extra = {}) => spawnSync(process.execPath, [path.join(root, "scripts/ci-run-affected.mjs")], {
    cwd: temporary, encoding: "utf8", env: { ...env, FERN_CI_SELECTION: JSON.stringify(selection), ...extra },
  });
  assert.equal(run(request).status, 0);
  const command = fs.readFileSync(calls, "utf8");
  assert.match(command, /-count=1/);
  assert.match(command, /\^\(TestOne\)\$/);
  assert.notEqual(run(request, { CI_TEST_RESULT: "3" }).status, 0);
  assert.notEqual(run({ ...request, full: true }).status, 0);
  assert.notEqual(run({ ...request, tests: {} }).status, 0);
  assert.notEqual(run({ ...request, tests: { [pkg]: ["TestMissing"] } }).status, 0);
  assert.notEqual(run({ ...request, tests: { "other/package": ["TestOne"] } }).status, 0);
  const platform = run({ ...request, tests: { [pkg]: ["TestOne", "TestOtherPlatform"] } });
  assert.equal(platform.status, 0);
  assert.match(platform.stderr, /1 tests do not compile for this host/);
  const pattern = (selection) => spawnSync(process.execPath, [path.join(root, "scripts/ci-test-pattern.mjs"), pkg, "^TestFull"], {
    cwd: temporary, encoding: "utf8", env: { ...env, FERN_CI_SELECTION: JSON.stringify(selection) },
  });
  assert.equal(pattern({ full: true }).stdout.trim(), "^TestFull");
  assert.equal(pattern({ macosFull: false, darwinTests: { [pkg]: ["TestOne"] } }).stdout.trim(), "^(TestOne)$");
  assert.equal(pattern({ macosFull: false, darwinTests: {} }).stdout.trim(), "^$");
  assert.notEqual(pattern({ macosFull: false, darwinTests: { [pkg]: ["TestMissing"] } }).status, 0);
  fs.writeFileSync(path.join(bin, "make"), '#!/bin/sh\nprintf "%s\\n" "$@" >> "$CI_TEST_CALLS"\n', { mode: 0o755 });
  fs.writeFileSync(path.join(bin, "fern"), '#!/bin/sh\nprintf "%s\\n" "$@" >> "$CI_TEST_CALLS"\nexit "${CI_TEST_RESULT:-0}"\n', { mode: 0o755 });
  fs.mkdirSync(path.join(temporary, "examples"));
  fs.writeFileSync(path.join(temporary, "examples", "with space.fern"), "");
  const format = (selection, extra = {}) => spawnSync(process.execPath, [path.join(root, "scripts/ci-format.mjs")], {
    cwd: temporary, encoding: "utf8", env: { ...env, FERN_CI_SELECTION: JSON.stringify(selection), ...extra },
  });
  fs.writeFileSync(calls, "");
  assert.equal(format({ version: 1, full: false, changed: ["examples/with space.fern", "examples/deleted.fern", "docs/readme.md"] }).status, 0);
  assert.equal(fs.readFileSync(calls, "utf8"), "build\n-fmt\n-d\nexamples/with space.fern\n");
  fs.writeFileSync(calls, "");
  assert.equal(format({ full: true }).status, 0);
  assert.equal(fs.readFileSync(calls, "utf8"), "fmt-check\n");
  assert.notEqual(format({ version: 1, full: false, changed: ["examples/with space.fern"] }, { CI_TEST_RESULT: "4" }).status, 0);
  assert.notEqual(format({ full: false }).status, 0);
  console.log("ci-run-affected-selftest: consumer selection, failures, platform filters, native patterns, and format checks pass");
} finally {
  fs.rmSync(temporary, { recursive: true, force: true });
}
