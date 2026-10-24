import { load } from "./kernel.mjs";
const kernel = load();
onmessage = async ({ data: { tone, id } }) => {
  try {
    const { sample } = await kernel,
      rate = 24000,
      pcm = new Float32Array(rate * 8);
    for (let i = 0; i < pcm.length; i++) pcm[i] = sample(i, rate, tone);
    postMessage({ id, pcm, rate }, [pcm.buffer]);
  } catch (error) {
    postMessage({ id, error: error.message });
  }
};
