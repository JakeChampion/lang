export async function instantiateCore(bytes) {
  let instance;
  const decoder = new TextDecoder();
  const result = await WebAssembly.instantiate(bytes, {
    wasi_snapshot_preview1: {
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
    },
  });
  instance = result instanceof WebAssembly.Instance ? result : result.instance;
  return instance.exports;
}
