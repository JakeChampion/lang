// Compile the shared module outside the page thread.
onmessage = async (e) => {
  try {
    const module = await WebAssembly.compileStreaming(fetch(e.data));
    postMessage({ module });
  } catch (err) {
    postMessage({ error: String(err) });
  }
};
