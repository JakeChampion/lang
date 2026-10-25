import { instantiateCore } from "../shared/wasm.mjs";

export async function instantiate(bytes) {
  const exports = await instantiateCore(bytes);
  return {
    buffer: bytes,
    memory: exports.memory,
    particle: exports["fern:showcase/swarm#particle"],
    sample: exports["fern:showcase/swarm#sample"],
  };
}
export async function load() {
  const response = await fetch(new URL("./kernel.wasm", import.meta.url));
  if (!response.ok)
    throw new Error(`Kernel download failed (${response.status})`);
  const bytes = await response.arrayBuffer();
  return { ...(await instantiate(bytes)), buffer: bytes };
}
