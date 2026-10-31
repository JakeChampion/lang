import { load } from "./kernel.mjs";
const ready = load();
self.onmessage = async ({
  data: { id, action, source, rom, state, budget, entry },
}) => {
  try {
    const api = await ready;
    let result;
    if (action === "assemble") result = await api.assemble(source);
    else if (action === "boot") result = await api.boot(rom);
    else if (action === "run") result = await api.run(state, budget, entry);
    else throw new Error("Unknown machine operation");
    self.postMessage({ id, result }, [result.buffer]);
  } catch (error) {
    self.postMessage({ id, error: error.message });
  }
};
