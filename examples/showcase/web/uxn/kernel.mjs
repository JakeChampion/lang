import { instantiateCore } from "../shared/wasm.mjs";

export const layout = Object.freeze({
  wst: 65536,
  rst: 65792,
  dev: 66048,
  meta: 66304,
  console: 66336,
  screen: 74528,
  size: 336672,
});
export const word = (s, at) => s[at] * 256 + s[at + 1];
export function putword(s, at, n) {
  s[at] = n >>> 8;
  s[at + 1] = n;
}

// A fresh instance per operation bounds the canonical ABI's temporary arena.
// The compiled module is shared; persistent machine state is a byte image.
export function bind(module) {
  async function call(name, ...args) {
    const e = await instantiateCore(module);
    const lowered = [];
    for (const arg of args) {
      if (typeof arg === "number") {
        lowered.push(arg);
        continue;
      }
      const bytes =
        typeof arg === "string" ? new TextEncoder().encode(arg) : arg;
      const at = e.cabi_realloc(0, 0, 1, bytes.length);
      new Uint8Array(e.memory.buffer).set(bytes, at);
      lowered.push(at, bytes.length);
    }
    const result = e[`fern:showcase/uxn#${name}`](...lowered);
    const [at, length] = new Uint32Array(e.memory.buffer, result, 2);
    return new Uint8Array(e.memory.buffer, at, length).slice();
  }
  return {
    async assemble(source) {
      const result = await call("assemble", source);
      if (result[0] !== 0)
        throw new Error(new TextDecoder().decode(result.slice(1)));
      return result.slice(1);
    },
    boot: (rom) => call("boot", rom),
    run: (state, budget, entry = -1) => call("run", state, budget, entry),
    raw: call,
  };
}
export async function load() {
  const response = await fetch(new URL("./kernel.wasm", import.meta.url));
  if (!response.ok)
    throw new Error(`Kernel download failed (${response.status})`);
  return bind(await WebAssembly.compile(await response.arrayBuffer()));
}
