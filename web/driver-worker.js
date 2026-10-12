// The self-host compiler (web/playground.wasm), run in a worker so a compile
// never blocks the page: compiling anything that imports std/platform takes
// seconds in wasm.
//
// The page posts { id, module, src, args }: module the compiled
// WebAssembly.Module, src the program on stdin, args its full argv. The reply
// is { id, result } with runCoreWasm's result, or { id, error } when the run
// threw.
import { runCoreWasm } from "./wasi-shim.js";

onmessage = async (e) => {
  const { id, module, src, args } = e.data;
  try {
    const result = await runCoreWasm(module, { stdin: src, args });
    postMessage({ id, result }, [result.stdoutBytes.buffer]);
  } catch (err) {
    postMessage({ id, error: String(err) });
  }
};
