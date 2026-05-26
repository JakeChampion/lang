// web/wasi-http-shim.js — a hand-written host for the
// wasi:http/incoming-handler@0.2.0 core module the Fern wasm backend
// emits, so the playground can run a user-written HTTP handler in the
// browser with no jco / Component Model transpile.
//
// Browsers can only instantiate *core* modules, and our handler core
// (fernCompileHttpHandlerCore) exports
// `wasi:http/incoming-handler@0.2.0#handle(incoming-request,
// response-outparam)` plus `memory` + `cabi_realloc`, and imports 22
// Canonical-ABI functions across wasi:http/types and wasi:io/streams.
// We implement exactly those, mint opaque i32 resource handles, and
// marshal the request/response across linear memory.
//
// The big simplification over a general Component-Model runtime: we
// own the emitter, so the guest's call sequence is fixed and known.
// Resources are just entries in a Map, `[resource-drop]` imports are
// no-ops, and we never have to enforce the child-before-parent drop
// discipline that wasmtime does — the guest already emits the calls
// in a valid order.
//
// Memory discipline: cabi_realloc (and the guest allocator behind it)
// may grow linear memory, which detaches existing typed-array views.
// So every read/write fetches a fresh view, and any host bytes we
// stage are copied out of guest memory before the next alloc.

const encoder = new TextEncoder();
const decoder = new TextDecoder();

function toBytes(v) {
  if (v == null) return new Uint8Array(0);
  if (typeof v === "string") return encoder.encode(v);
  return v;
}

function concatChunks(chunks) {
  let total = 0;
  for (const c of chunks) total += c.length;
  const out = new Uint8Array(total);
  let off = 0;
  for (const c of chunks) {
    out.set(c, off);
    off += c.length;
  }
  return out;
}

// runHttpHandler instantiates a wasi:http handler core module and
// drives one request through it.
//
//   request: { method, path, headers: [[name, value], ...], body }
//   returns: { status, headers: [[name, value], ...], body }
export async function runHttpHandler(bytes, request) {
  const method = request.method || "GET";
  const path = request.path || "/";
  const reqHeaders = request.headers || [];
  const reqBody = toBytes(request.body);

  let instance = null;
  const u8 = () => new Uint8Array(instance.exports.memory.buffer);
  const dv = () => new DataView(instance.exports.memory.buffer);
  const w8 = (ptr, off, v) => {
    u8()[ptr + off] = v & 0xff;
  };
  const w32 = (ptr, off, v) => {
    dv().setUint32(ptr + off, v >>> 0, true);
  };
  // Copy `len` bytes out of guest memory, bounds-checked. wasmtime
  // traps an out-of-bounds canonical-ABI string/list with "pointer/
  // length out of bounds"; we mirror that with a thrown error rather
  // than silently returning clamped garbage, so the playground shows
  // an honest failure for a guest that hands us a bad (ptr, len).
  const readBytes = (ptr, len) => {
    const mem = u8();
    if (ptr < 0 || len < 0 || ptr + len > mem.length) {
      throw new Error("string pointer/length out of bounds of memory");
    }
    return mem.slice(ptr, ptr + len);
  };

  // Stage `src` bytes into fresh guest memory via the guest's own
  // canonical-ABI allocator and return the pointer. Re-fetches the
  // view after cabi_realloc, which may have grown the buffer.
  function guestAlloc(src) {
    const ptr = instance.exports.cabi_realloc(0, 0, 1, src.length);
    if (src.length > 0) u8().set(src, ptr);
    return ptr;
  }

  // Opaque-handle table. The guest only ever passes back handles we
  // minted (or the two we hand to the entry point), so a plain
  // counter + Map is enough.
  let nextHandle = 1;
  const handles = new Map();
  const mint = (obj) => {
    const h = nextHandle++;
    handles.set(h, obj);
    return h;
  };

  let captured = null;

  const reqHandle = mint({ kind: "incoming-request" });
  const outparamHandle = mint({ kind: "response-outparam" });

  const httpTypes = {
    // result/variant returns land at retptr; see the layout notes on
    // each import in internal/codegen/wasmbin/wasi.go.
    "[method]incoming-request.method": (self, retptr) => {
      // Always use the `other(string)` arm (disc 9) carrying the
      // method text. The guest round-trips it to a Fern string for
      // both canonical verbs and custom methods, so `req.method ==
      // "GET"` works either way.
      w8(retptr, 0, 9);
      const b = encoder.encode(method);
      const p = guestAlloc(b);
      w32(retptr, 4, p);
      w32(retptr, 8, b.length);
    },
    "[method]incoming-request.path-with-query": (self, retptr) => {
      w8(retptr, 0, 1); // option<string>: some = discriminant 1
      const b = encoder.encode(path);
      const p = guestAlloc(b);
      w32(retptr, 4, p);
      w32(retptr, 8, b.length);
    },
    "[method]incoming-request.headers": () =>
      mint({
        kind: "fields",
        entries: reqHeaders.map(([n, v]) => [n, toBytes(v)]),
      }),
    "[method]incoming-request.consume": (self, retptr) => {
      w8(retptr, 0, 0); // Ok
      const h = mint({ kind: "incoming-body", body: reqBody, cursor: 0 });
      w32(retptr, 4, h);
    },
    "[resource-drop]incoming-request": () => {},
    "[method]incoming-body.stream": (self, retptr) => {
      w8(retptr, 0, 0); // Ok
      const h = mint({ kind: "input-stream", body: handles.get(self) });
      w32(retptr, 4, h);
    },
    "[static]incoming-body.finish": () => mint({ kind: "future-trailers" }),
    "[resource-drop]future-trailers": () => {},
    "[constructor]fields": () => mint({ kind: "fields", entries: [] }),
    "[method]fields.entries": (self, retptr) => {
      const f = handles.get(self);
      const n = f.entries.length;
      const arrPtr = instance.exports.cabi_realloc(0, 0, 4, n * 16);
      for (let i = 0; i < n; i++) {
        const [name, valBytes] = f.entries[i];
        const nameBytes = encoder.encode(name);
        const np = guestAlloc(nameBytes);
        const vp = guestAlloc(valBytes);
        const e = arrPtr + i * 16;
        w32(e, 0, np);
        w32(e, 4, nameBytes.length);
        w32(e, 8, vp);
        w32(e, 12, valBytes.length);
      }
      w32(retptr, 0, arrPtr);
      w32(retptr, 4, n);
    },
    "[method]fields.append": (self, namePtr, nameLen, valPtr, valLen, retptr) => {
      const name = decoder.decode(readBytes(namePtr, nameLen));
      const value = readBytes(valPtr, valLen);
      handles.get(self).entries.push([name, value]);
      w8(retptr, 0, 0); // Ok
    },
    "[resource-drop]fields": () => {},
    "[constructor]outgoing-response": (headersHandle) =>
      mint({
        kind: "outgoing-response",
        status: 200,
        headers: handles.get(headersHandle),
        body: null,
      }),
    "[method]outgoing-response.set-status-code": (self, status) => {
      handles.get(self).status = status;
      return 0; // Ok discriminant
    },
    "[method]outgoing-response.body": (self, retptr) => {
      w8(retptr, 0, 0); // Ok
      const body = { kind: "outgoing-body", chunks: [] };
      handles.get(self).body = body;
      w32(retptr, 4, mint(body));
    },
    "[method]outgoing-body.write": (self, retptr) => {
      w8(retptr, 0, 0); // Ok
      const h = mint({ kind: "output-stream", body: handles.get(self) });
      w32(retptr, 4, h);
    },
    "[static]outgoing-body.finish": (self, optDisc, optVal, retptr) => {
      w8(retptr, 0, 0); // Ok
    },
    "[static]response-outparam.set": (outparam, disc, respHandle) => {
      if (disc === 0) {
        const resp = handles.get(respHandle);
        const bodyBytes = resp.body
          ? concatChunks(resp.body.chunks)
          : new Uint8Array(0);
        captured = {
          status: resp.status,
          headers: resp.headers
            ? resp.headers.entries.map(([n, v]) => [n, decoder.decode(v)])
            : [],
          body: decoder.decode(bodyBytes),
        };
      } else {
        captured = { status: 500, headers: [], body: "[response error]" };
      }
    },
  };

  const ioStreams = {
    "[method]input-stream.blocking-read": (self, len, retptr) => {
      const body = handles.get(self).body; // incoming-body
      const remaining = body.body.length - body.cursor;
      if (remaining <= 0) {
        w8(retptr, 0, 1); // Err(closed) → guest treats as end-of-stream
        return;
      }
      const n = Math.min(Number(len), remaining);
      const chunk = body.body.subarray(body.cursor, body.cursor + n);
      body.cursor += n;
      const p = guestAlloc(chunk);
      w8(retptr, 0, 0); // Ok
      w32(retptr, 4, p);
      w32(retptr, 8, n);
    },
    "[method]output-stream.blocking-write-and-flush": (self, ptr, len, retptr) => {
      handles.get(self).body.chunks.push(readBytes(ptr, len));
      w8(retptr, 0, 0); // Ok
    },
    "[resource-drop]input-stream": () => {},
    "[resource-drop]output-stream": () => {},
  };

  const result = await WebAssembly.instantiate(bytes, {
    "wasi:http/types@0.2.0": httpTypes,
    "wasi:io/streams@0.2.0": ioStreams,
  });
  instance = result.instance;

  const entry = instance.exports["wasi:http/incoming-handler@0.2.0#handle"];
  if (typeof entry !== "function") {
    throw new Error("module has no wasi:http/incoming-handler#handle export");
  }
  entry(reqHandle, outparamHandle);

  return (
    captured || { status: 0, headers: [], body: "(handler set no response)" }
  );
}
