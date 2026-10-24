// The host supplies only the two WASI imports that this numeric core requires.
export async function instantiate(bytes) {
  let instance;
  const decoder = new TextDecoder();
  const wasi = {
    proc_exit(code) {
      throw new Error(`Fern kernel exited with code ${code}`);
    },
    fd_write(fd, vectors, count, written) {
      const view = new DataView(instance.exports.memory.buffer);
      let text = "",
        length = 0;
      for (let i = 0; i < count; i++) {
        const at = view.getUint32(vectors + i * 8, true),
          size = view.getUint32(vectors + i * 8 + 4, true);
        text += decoder.decode(new Uint8Array(view.buffer, at, size));
        length += size;
      }
      view.setUint32(written, length, true);
      console.error(`Fern ${fd}: ${text}`);
      return 0;
    },
  };
  ({ instance } = await WebAssembly.instantiate(bytes, {
    wasi_snapshot_preview1: wasi,
  }));
  return {
    memory: instance.exports.memory,
    particle: instance.exports["fern:showcase/swarm#particle"],
    sample: instance.exports["fern:showcase/swarm#sample"],
  };
}
export async function load() {
  const response = await fetch(new URL("./kernel.wasm", import.meta.url));
  if (!response.ok)
    throw new Error(`Kernel download failed (${response.status})`);
  const bytes = await response.arrayBuffer();
  return { ...(await instantiate(bytes)), bytes: bytes.byteLength };
}
