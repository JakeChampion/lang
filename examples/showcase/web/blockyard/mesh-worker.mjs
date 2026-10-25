import { instantiate } from "./protocol.mjs";
import { meshChunk } from "./mesh.mjs";
let kernel,
  epoch = 0,
  scheduled = false;
const chunks = new Map(),
  pending = new Map(),
  overlay = new Map(),
  dirty = new Set();
const key = (x, z) => z * 32 + x,
  blockKey = (x, y, z) => (y * 512 + z) * 512 + x;
function mark(x, z) {
  for (const [cx, cz] of [
    [x, z],
    [x - 1, z],
    [x + 1, z],
    [x, z - 1],
    [x, z + 1],
  ])
    if (chunks.has(key(cx, cz))) dirty.add(key(cx, cz));
  if (!scheduled) {
    scheduled = true;
    setTimeout(flush, 0);
  }
}
function get(x, y, z) {
  if (x < 0 || z < 0 || x >= 512 || z >= 512 || y < 0 || y >= 48)
    return kernel.terrain(x, y, z);
  const at = blockKey(x, y, z);
  if (overlay.has(at)) return overlay.get(at);
  const chunk = chunks.get(key(x >> 4, z >> 4));
  return chunk
    ? chunk.data[(y * 16 + (z & 15)) * 16 + (x & 15)]
    : kernel.terrain(x, y, z);
}
function flush() {
  const next = dirty.values().next();
  if (next.done) {
    scheduled = false;
    return;
  }
  const at = next.value;
  dirty.delete(at);
  const chunk = chunks.get(at);
  if (chunk) {
    let data = chunk.data;
    if (overlay.size) {
      data = data.slice();
      for (const change of pending.values())
        if (change.x >> 4 === chunk.x && change.z >> 4 === chunk.z)
          data[(change.y * 16 + (change.z & 15)) * 16 + (change.x & 15)] =
            change.material;
    }
    const mesh = meshChunk(data, chunk.x, chunk.z, get);
    const copy = chunk.data.slice();
    postMessage(
      {
        kind: "mesh",
        epoch,
        key: at,
        x: chunk.x,
        z: chunk.z,
        revision: chunk.revision,
        voxels: copy,
        opaque: mesh.opaque,
        alpha: mesh.alpha,
      },
      [copy.buffer, mesh.opaque.buffer, mesh.alpha.buffer],
    );
  }
  setTimeout(flush, 0);
}
function resolve(nonce) {
  const old = pending.get(nonce);
  if (!old) return;
  pending.delete(nonce);
  const at = blockKey(old.x, old.y, old.z);
  overlay.delete(at);
  for (const value of pending.values())
    if (blockKey(value.x, value.y, value.z) === at)
      overlay.set(at, value.material);
  mark(old.x >> 4, old.z >> 4);
}
onmessage = async ({ data: message }) => {
  try {
    if (message.kind === "init") {
      kernel = await instantiate(message.bytes);
      postMessage({ kind: "ready" });
      return;
    }
    if (message.kind === "reset") {
      epoch = message.epoch;
      chunks.clear();
      pending.clear();
      overlay.clear();
      dirty.clear();
      return;
    }
    if (message.epoch !== epoch) return;
    if (message.kind === "chunk") {
      const data = new Uint8Array(16 * 48 * 16);
      for (let y = 0; y < 48; y++)
        for (let z = 0; z < 16; z++)
          for (let x = 0; x < 16; x++)
            data[(y * 16 + z) * 16 + x] = kernel.terrain(
              message.x * 16 + x,
              y,
              message.z * 16 + z,
            );
      for (const block of message.blocks) data[block.at] = block.material;
      chunks.set(key(message.x, message.z), {
        x: message.x,
        z: message.z,
        revision: message.revision,
        data,
      });
      mark(message.x, message.z);
    } else if (message.kind === "edits") {
      for (const change of message.blocks) {
        const chunk = chunks.get(key(change.x >> 4, change.z >> 4));
        if (chunk)
          chunk.data[(change.y * 16 + (change.z & 15)) * 16 + (change.x & 15)] =
            change.material;
        mark(change.x >> 4, change.z >> 4);
      }
    } else if (message.kind === "optimistic") {
      pending.set(message.nonce, message.change);
      overlay.set(
        blockKey(message.change.x, message.change.y, message.change.z),
        message.change.material,
      );
      mark(message.change.x >> 4, message.change.z >> 4);
    } else if (message.kind === "resolve") resolve(message.nonce);
    else if (message.kind === "forget") {
      chunks.delete(message.key);
      dirty.delete(message.key);
    }
  } catch (error) {
    postMessage({ kind: "error", message: error.message });
  }
};
