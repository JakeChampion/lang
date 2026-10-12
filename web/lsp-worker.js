// The playground's language server: fern.wasm, the in-tree LSP that
// `cmd/fern-lsp` runs over stdio, hosted in a worker so a check never blocks
// the page. A check type-checks every module the program imports, which takes
// seconds in wasm for anything importing std/platform.
//
// The page posts { id?, msg } with msg a JSON-RPC message; a request (one with
// an id) is answered with { id, resp }, resp the server's JSON response or "".
// Server notifications arrive as { method, params }. { ready } or { failed }
// reports the boot.
importScripts("wasm_exec.js");

const go = new Go();
WebAssembly.instantiateStreaming(fetch("fern.wasm"), go.importObject)
  .then((result) => {
    go.run(result.instance);
    globalThis.fernLspOnNotify((method, params) => postMessage({ method, params }));
    postMessage({ ready: true });
  })
  .catch((err) => postMessage({ failed: String(err) }));

onmessage = (e) => {
  const resp = globalThis.fernLsp(e.data.msg);
  if (e.data.id !== undefined) postMessage({ id: e.data.id, resp });
};
