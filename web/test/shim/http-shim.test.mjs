// web/wasi-http-shim.js implements exactly the host functions the handler
// core imports, and that core's imports are the `@import` declarations of
// internal/stdlib/std/wasi_http.fern, the entry every wasi:http handler is
// compiled with. Two files in two languages state one list; this pins them
// equal, so an import added to the entry without its host function fails
// here by name instead of as a LinkError in the browser, and a host function
// nothing imports is noticed too.

import { readFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { describe, it } from "node:test";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const shim = readFileSync(join(repoRoot, "web/wasi-http-shim.js"), "utf8");
const entry = readFileSync(join(repoRoot, "internal/stdlib/std/wasi_http.fern"), "utf8");

// shimKeys lists the function names one of the shim's import objects
// (`const NAME = { "[method]…": … };`) provides.
function shimKeys(objectName) {
  const start = shim.indexOf(`const ${objectName} = {`);
  assert.notEqual(start, -1, `the shim has no ${objectName} object`);
  const end = shim.indexOf("\n  };", start);
  assert.notEqual(end, -1, `the shim's ${objectName} object does not close`);
  return [...shim.slice(start, end).matchAll(/^\s{4}"([^"]+)":/gm)].map((m) => m[1]).sort();
}

// declaredImports groups the entry's `@import("interface", "name")` lines by
// interface.
function declaredImports() {
  const byInterface = {};
  for (const m of entry.matchAll(/^@import\("([^"]+)", "([^"]+)"\)/gm)) {
    (byInterface[m[1]] ??= []).push(m[2]);
  }
  for (const names of Object.values(byInterface)) names.sort();
  return byInterface;
}

describe("the browser's wasi:http host", () => {
  const declared = declaredImports();

  it("provides every wasi:http/types function the entry imports, and no other", () => {
    assert.deepEqual(shimKeys("httpTypes"), declared["wasi:http/types@0.2.0"]);
  });

  it("provides every wasi:io/streams function the entry imports, and no other", () => {
    assert.deepEqual(shimKeys("ioStreams"), declared["wasi:io/streams@0.2.0"]);
  });

  it("is asked for nothing outside those two interfaces", () => {
    assert.deepEqual(Object.keys(declared).sort(), ["wasi:http/types@0.2.0", "wasi:io/streams@0.2.0"]);
  });
});
