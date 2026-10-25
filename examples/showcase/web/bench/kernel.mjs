// Calls a Fern core-module kernel through the canonical ABI. A string or a
// byte list goes into the kernel's memory through cabi_realloc, and a list
// result comes back through its [pointer, length] return area.
import { instantiateCore } from "/assets/wasm.mjs";

const encoder = new TextEncoder();

export async function kernel(url, prefix) {
  const response = await fetch(url);
  if (!response.ok) throw Error(`Kernel download failed (${response.status})`);
  const module = await WebAssembly.compile(await response.arrayBuffer());
  // Each run uses a fresh instance, so argument copies never accumulate.
  return async () => bind(await instantiateCore(module), prefix);
}

function bind(exports, prefix) {
  const memory = () => new Uint8Array(exports.memory.buffer);
  const lower = (value) => {
    if (typeof value === "number") return [value];
    const data = typeof value === "string" ? encoder.encode(value) : value;
    const at = exports.cabi_realloc(0, 0, 1, data.length);
    memory().set(data, at);
    return [at, data.length];
  };
  return {
    call: (name, ...args) => exports[`${prefix}#${name}`](...args.flatMap(lower)),
    list(result) {
      const [at, length] = new Uint32Array(exports.memory.buffer, result, 2);
      return memory().slice(at, at + length);
    },
  };
}

export const hex = (bytes) =>
  Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
export const unhex = (text) =>
  Uint8Array.from(text.match(/../g) ?? [], (pair) => parseInt(pair, 16));
export const mib = (bytes, ms) => bytes / 1048576 / (ms / 1000);
export const size = (bytes) =>
  bytes >= 1048576
    ? `${(bytes / 1048576).toFixed(2)} MiB`
    : `${(bytes / 1024).toFixed(1)} KiB`;
export const pause = () => new Promise((resolve) => setTimeout(resolve, 30));
