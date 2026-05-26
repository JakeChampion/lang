// component-runner.js — run a Fern-built WebAssembly Component in the
// browser via Bytecode Alliance jco + the preview2 WASI shim.
//
// EXPERIMENTAL. This is the in-browser execution half of the
// playground's "Build component" feature. It pulls jco and the WASI
// shim from a CDN on first use, transpiles the component to JS + core
// modules, and instantiates it with browser-side WASI.
//
//   runComponent(bytes, "wasm")      → runs the wasi:cli/run export,
//                                      returns { stdout }
//   runComponent(bytes, "wasi-http") → not yet wired in-browser (the
//                                      synthetic IncomingRequest /
//                                      ResponseOutparam plumbing is
//                                      involved); throws so the caller
//                                      falls back to the download +
//                                      `wasmtime serve` path.
//
// Loaded lazily via dynamic import() from index.html so the playground
// boots (and works offline for Run / View assembly) even when this
// module's CDN dependencies aren't reachable. The caller wraps the
// whole thing in try/catch and degrades to the download path on any
// failure.

const JCO = "https://esm.sh/@bytecodealliance/jco@1";
const SHIM = "https://esm.sh/@bytecodealliance/preview2-shim@0";

// runComponent transpiles `bytes` (a Component Model binary) and runs
// it. `world` selects the export shape we drive.
export async function runComponent(bytes, world) {
  if (world !== "wasm") {
    throw new Error(
      "in-browser run currently supports the wasi:cli/run world only; " +
        "use the download to run a " + world + " component locally"
    );
  }

  const { transpile } = await import(JCO);
  const shim = await import(SHIM);

  // `instantiation: "async"` makes jco emit an `instantiate(getCore,
  // imports)` function instead of top-level await + fetch, so we
  // control core-module loading without a filesystem — the shape jco
  // documents for browser hosts.
  const name = "fern_component";
  const { files } = await transpile(bytes, {
    name,
    instantiation: "async",
    // Route every WASI import at the package level to the shim; the
    // shim ships browser-capable implementations of the wasi:* worlds.
    map: Object.entries({
      "wasi:cli/*": "@bytecodealliance/preview2-shim/cli#*",
      "wasi:clocks/*": "@bytecodealliance/preview2-shim/clocks#*",
      "wasi:filesystem/*": "@bytecodealliance/preview2-shim/filesystem#*",
      "wasi:io/*": "@bytecodealliance/preview2-shim/io#*",
      "wasi:random/*": "@bytecodealliance/preview2-shim/random#*",
      "wasi:sockets/*": "@bytecodealliance/preview2-shim/sockets#*",
    }),
  });

  // The transpiled output is a set of files keyed by name. The entry
  // module is `${name}.js`; the rest are core wasm modules it asks for
  // by name through the getCoreModule callback.
  const text = new TextDecoder();
  const jsSource = text.decode(fileBytes(files, name + ".js"));

  // Compile the core modules up front so getCoreModule is synchronous.
  const cores = new Map();
  for (const [fname, data] of fileEntries(files)) {
    if (fname.endsWith(".wasm")) {
      cores.set(fname, await WebAssembly.compile(asUint8(data)));
    }
  }
  const getCoreModule = (path) => {
    const mod = cores.get(path) || cores.get(path.replace(/^\.\//, ""));
    if (!mod) throw new Error("missing core module: " + path);
    return mod;
  };

  // Load the emitted ESM from a blob URL so its relative imports of the
  // shim resolve through esm.sh (absolute specifiers in `map` above).
  const blobUrl = URL.createObjectURL(
    new Blob([jsSource], { type: "text/javascript" })
  );
  let mod;
  try {
    mod = await import(blobUrl);
  } finally {
    URL.revokeObjectURL(blobUrl);
  }

  const imports = defaultImports(shim);
  const instance = await mod.instantiate(getCoreModule, imports);

  // Capture anything the program writes to stdout/stderr. The shim's
  // browser stdout implementation forwards decoded text to console, so
  // we tap console for the duration of the run. (Best-effort: it also
  // catches stray console.log from the shim itself, which is fine for
  // a playground.)
  const captured = [];
  const origLog = console.log;
  const origErr = console.error;
  console.log = (...a) => captured.push(a.join(" "));
  console.error = (...a) => captured.push(a.join(" "));
  try {
    // wasi:cli/run exports `run.run() -> result`. A thrown/false
    // result maps to a non-zero exit; we surface stdout either way.
    if (instance.run && typeof instance.run.run === "function") {
      instance.run.run();
    } else if (typeof instance.run === "function") {
      instance.run();
    } else {
      throw new Error("component has no wasi:cli/run export");
    }
  } finally {
    console.log = origLog;
    console.error = origErr;
  }

  return { stdout: captured.join("\n") };
}

// fileEntries normalises jco's `files` (an array of [name, bytes] or a
// plain object) into [name, bytes] pairs.
function fileEntries(files) {
  return Array.isArray(files) ? files : Object.entries(files);
}

function fileBytes(files, name) {
  for (const [fname, data] of fileEntries(files)) {
    if (fname === name) return data;
  }
  throw new Error("transpile output missing " + name);
}

function asUint8(data) {
  return data instanceof Uint8Array ? data : new Uint8Array(data);
}

// defaultImports assembles the WASI import object the transpiled module
// expects from the shim's per-world namespaces.
function defaultImports(shim) {
  const imports = {};
  for (const world of [
    "cli",
    "clocks",
    "filesystem",
    "io",
    "random",
    "sockets",
  ]) {
    if (shim[world]) imports[world] = shim[world];
  }
  return imports;
}
