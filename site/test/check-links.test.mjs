// Unit tests for scripts/check-links.mjs.

import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";

import { fetchRetry, fileFor, linksIn, pagePathOf, sitePath } from "../scripts/check-links.mjs";

test("reads href and src values without the fragment or query", () => {
  assert.deepEqual(
    linksIn('<a href="/why/#costs">x</a> <img src="/og.png?v=2"> <a href="#top">'),
    ["/why/", "/og.png"],
  );
});

test("resolves a site path to a page, an index or a file in the build", () => {
  const dist = mkdtempSync(join(tmpdir(), "fern-links-"));
  mkdirSync(join(dist, "why"));
  writeFileSync(join(dist, "why", "index.html"), "");
  writeFileSync(join(dist, "install.sh"), "");
  assert.equal(fileFor(dist, "/why/"), join(dist, "why", "index.html"));
  assert.equal(fileFor(dist, "/install.sh"), join(dist, "install.sh"));
  assert.equal(fileFor(dist, "/favicon.svg"), null);
});

test("resolves a relative link against its page, and skips other sites", () => {
  assert.equal(sitePath("/errors/e008/", "../../docs/X.md"), "/docs/X.md");
  assert.equal(sitePath("/errors/e008/", "e009/"), "/errors/e008/e009/");
  assert.equal(sitePath("/errors/", "/why/"), "/why/");
  assert.equal(sitePath("/", "https://example.org/"), null);
  assert.equal(sitePath("/", "//cdn.example.org/x.js"), null);
  assert.equal(sitePath("/", "mailto:a@b"), null);
});

test("gives the path a build file serves", () => {
  assert.equal(pagePathOf("/d", "/d/errors/e008/index.html"), "/errors/e008/");
  assert.equal(pagePathOf("/d", "/d/index.html"), "/");
  assert.equal(pagePathOf("/d", "/d/404.html"), "/404.html");
});

test("does not read links out of a script", () => {
  assert.deepEqual(linksIn('<a href="/a/">a</a><script>x = \'<a href="\' + url + \'">\';</script>'), ["/a/"]);
});

test("tries a request again after a network error", async () => {
  let calls = 0;
  const flaky = async () => {
    calls++;
    if (calls < 3) throw new TypeError("fetch failed");
    return { status: 200 };
  };
  assert.equal((await fetchRetry("u", {}, 3, flaky)).status, 200);
  assert.equal(calls, 3);
});

test("stops after the last attempt, and does not try an HTTP status again", async () => {
  let calls = 0;
  const down = async () => {
    calls++;
    throw new TypeError("fetch failed");
  };
  await assert.rejects(fetchRetry("u", {}, 2, down), /fetch failed/);
  assert.equal(calls, 2);
  calls = 0;
  const missing = async () => {
    calls++;
    return { status: 404 };
  };
  assert.equal((await fetchRetry("u", {}, 3, missing)).status, 404);
  assert.equal(calls, 1);
});
