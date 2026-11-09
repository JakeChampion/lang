// Unit tests for the build mode of scripts/check-links.mjs.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { fileFor, linksIn } from "../scripts/check-links.mjs";

test("reads href and src values without the fragment or query", () => {
  assert.deepEqual(
    linksIn('<a href="/lang/why/#costs">x</a> <img src="/lang/og.png?v=2"> <a href="#top">'),
    ["/lang/why/", "/lang/og.png"],
  );
});

test("resolves a site path to a page, an index or a file in the build", () => {
  const dist = mkdtempSync(join(tmpdir(), "fern-links-"));
  mkdirSync(join(dist, "why"));
  writeFileSync(join(dist, "why", "index.html"), "");
  writeFileSync(join(dist, "install.sh"), "");
  assert.equal(fileFor(dist, "/lang", "/lang/why/"), join(dist, "why", "index.html"));
  assert.equal(fileFor(dist, "/lang", "/lang/install.sh"), join(dist, "install.sh"));
  assert.equal(fileFor(dist, "/lang", "/lang/favicon.svg"), null);
  // A path outside the base is not this site's to check.
  assert.equal(fileFor(dist, "/lang", "/other/"), undefined);
});
