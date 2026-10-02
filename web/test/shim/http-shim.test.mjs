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
import { describe, it, mock } from "node:test";

import { runHttpHandler } from "../../wasi-http-shim.js";

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

describe("HTTP response streaming", () => {
  for (const early of [true, false]) {
    it(`collects body writes ${early ? "after" : "before"} response handoff`, async () => {
      const memory = new WebAssembly.Memory({ initial: 1 });
      const view = new DataView(memory.buffer);
      const bytes = new Uint8Array(memory.buffer);
      const encoder = new TextEncoder();
      let cursor = 1024;
      const realloc = (_ptr, _old, _align, len) => {
        const ptr = cursor;
        cursor += len;
        return ptr;
      };
      const stage = (text) => {
        const data = encoder.encode(text);
        const ptr = realloc(0, 0, 1, data.length);
        bytes.set(data, ptr);
        return [ptr, data.length];
      };
      const replacement = mock.method(WebAssembly, "instantiate", async (_module, imports) => ({
        instance: { exports: {
          memory,
          cabi_realloc: realloc,
          "wasi:http/incoming-handler@0.2.0#handle": (_req, out) => {
            const http = imports["wasi:http/types@0.2.0"];
            const streams = imports["wasi:io/streams@0.2.0"];
            const fields = http["[constructor]fields"]();
            http["[method]fields.append"](fields, ...stage("x-test"), ...stage("streamed"), 0);
            const resp = http["[constructor]outgoing-response"](fields);
            http["[method]outgoing-response.set-status-code"](resp, 201);
            http["[method]outgoing-response.body"](resp, 0);
            const body = view.getUint32(4, true);
            const handoff = () => http["[static]response-outparam.set"](out, 0, resp);
            if (early) handoff();
            http["[method]outgoing-body.write"](body, 0);
            const stream = view.getUint32(4, true);
            // A UTF-8 character crosses the host's 4096-byte chunk boundary.
            const [ptr, len] = stage("x".repeat(4095) + "é");
            streams["[method]output-stream.blocking-write-and-flush"](stream, ptr, 4096, 0);
            streams["[method]output-stream.blocking-write-and-flush"](stream, ptr + 4096, len - 4096, 0);
            streams["[resource-drop]output-stream"](stream);
            http["[static]outgoing-body.finish"](body, 0, 0, 0);
            if (!early) handoff();
          },
        } },
      }));
      try {
        assert.deepEqual(await runHttpHandler(new Uint8Array(), {}), {
          status: 201,
          headers: [["x-test", "streamed"]],
          body: "x".repeat(4095) + "é",
        });
      } finally {
        replacement.mock.restore();
      }
    });
  }
});
