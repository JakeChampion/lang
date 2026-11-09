// Tests for public/install.sh against a fake release on a file:// URL.

import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { test } from "node:test";

const script = fileURLToPath(new URL("../public/install.sh", import.meta.url));
const assets = ["fern-linux-x86_64", "fern-linux-arm64", "fern-darwin-arm64"];

// A release with an archive for each platform. Each archive holds a `fern`
// that prints a version line, as the real one does.
function fakeRelease({ badHash = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), "fern-install-"));
  const release = join(root, "release");
  const stage = join(root, "stage");
  mkdirSync(release);
  mkdirSync(stage);
  writeFileSync(join(stage, "fern"), "#!/bin/sh\necho 'fern test'\necho 'built for test'\n", { mode: 0o755 });
  for (const a of assets) {
    const archive = join(release, `${a}.tar.gz`);
    execFileSync("tar", ["-czf", archive, "-C", stage, "."]);
    const sum = createHash("sha256").update(readFileSync(archive)).digest("hex");
    writeFileSync(`${archive}.sha256`, `${badHash ? "0".repeat(64) : sum}  ${a}.tar.gz\n`);
  }
  return { release, dir: join(root, "bin") };
}

function install({ release, dir }) {
  return spawnSync("sh", [script], {
    encoding: "utf8",
    env: {
      ...process.env,
      FERN_RELEASE_URL: `file://${release}`,
      FERN_INSTALL_DIR: dir,
      FERN_SKIP_ATTESTATION: "1",
    },
  });
}

test("installs the archive for this computer", () => {
  const r = fakeRelease();
  const run = install(r);
  assert.equal(run.status, 0, run.stderr);
  assert.match(run.stdout, /SHA-256 matches/);
  assert.match(run.stdout, /installed fern test at /);
  assert.equal(execFileSync(join(r.dir, "fern"), { encoding: "utf8" }), "fern test\nbuilt for test\n");
});

test("refuses an archive whose SHA-256 does not match", () => {
  const r = fakeRelease({ badHash: true });
  const run = install(r);
  assert.equal(run.status, 1);
  assert.match(run.stderr, /SHA-256 .* is [0-9a-f]{64}, not 0{64}\. Nothing was installed\./);
  assert.equal(existsSync(join(r.dir, "fern")), false);
});

test("reports a missing release", () => {
  const r = fakeRelease();
  const run = install({ ...r, release: join(r.release, "missing") });
  assert.equal(run.status, 1);
  assert.match(run.stderr, /cannot download file:\/\//);
});
