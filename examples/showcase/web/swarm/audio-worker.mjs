import { instantiate, load } from "./kernel.mjs";
const original = load();
// The page sends the module bytes after a visitor edits the kernel.
onmessage = async ({ data: { tone, id, bytes } }) => {
  try {
    const { sample } = await (bytes ? instantiate(bytes) : original),
      rate = 24000,
      pcm = new Float32Array(rate * 8);
    for (let i = 0; i < pcm.length; i++) pcm[i] = sample(i, rate, tone);
    postMessage({ id, pcm, rate }, [pcm.buffer]);
  } catch (error) {
    postMessage({ id, error: error.message });
  }
};
