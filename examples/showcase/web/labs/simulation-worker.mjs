import { instantiateCore } from "/assets/wasm.mjs";
let api,
  app,
  cells,
  values,
  next,
  incoming,
  outgoing,
  capacity,
  width = 256,
  count = 65536,
  tick = 0,
  running = true,
  timer,
  measured = [],
  last = performance.now(),
  steps = 0,
  consumed = 0,
  produced = 0,
  generation = 0;
const fn = (name) => api[`fern:showcase/${app}#${name}`];
function reset() {
  values = new Int32Array(count);
  next = new Int32Array(count);
  incoming = new Int32Array(count);
  outgoing = new Int32Array(count);
  capacity = new Int32Array(count);
  tick = 0;
  steps = 0;
  consumed = 0;
  produced = 0;
  measured = [];
  generation++;
}
function step() {
  if (!running || !cells) return;
  const at = performance.now();
  if (app === "foundry") {
    incoming.fill(0);
    outgoing.fill(0);
    for (let i = 0; i < count; i++) capacity[i] = 15 - values[i];
    for (let i = 0; i < count; i++) {
      const kind = cells[i] || 0;
      if (!kind || kind === 6 || !values[i]) continue;
      const target = fn("output")(kind, i, width, count);
      if (target !== i && (cells[target] || 0) > 0 && capacity[target] > 0) {
        outgoing[i] = 1;
        incoming[target]++;
        capacity[target]--;
      }
    }
    for (let i = 0; i < count; i++) {
      const kind = cells[i] || 0;
      if (kind === 6) consumed += incoming[i];
      const value = fn("cell")(kind, values[i], incoming[i], outgoing[i], tick);
      if (kind === 1 && tick % 8 === 0 && values[i] < 15) produced++;
      next[i] = value;
    }
  } else {
    for (let i = 0; i < count; i++) {
      const base = i * 3,
        kind = base + 2 < cells.length ? cells[base] : 3 + (i % 4);
      const a = base + 2 < cells.length ? cells[base + 1] : Math.max(0, i - 1),
        b = base + 2 < cells.length ? cells[base + 2] : Math.max(0, i - 2);
      next[i] = fn("gate")(kind, values[a] || 0, values[b] || 0, values[i]);
    }
  }
  [values, next] = [next, values];
  tick++;
  steps++;
  const ms = performance.now() - at;
  measured.push(ms);
  if (measured.length > 256) measured.shift();
  const now = performance.now();
  if (now - last >= 100) {
    const sorted = [...measured].sort((a, b) => a - b),
      percent = (q) =>
        sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))] || 0;
    const display = values.slice(0, Math.min(count, 65536));
    postMessage(
      {
        kind: "frame",
        generation,
        tick,
        width,
        count,
        values: display,
        produced,
        consumed,
        steps,
        p50: percent(0.5),
        p95: percent(0.95),
        p99: percent(0.99),
        memory: api.memory.buffer.byteLength,
        hostBytes: values.byteLength * 5 + cells.byteLength,
      },
      [display.buffer],
    );
    last = now;
  }
}
onmessage = async ({ data }) => {
  try {
    if (data.kind === "init") {
      app = data.app;
      const response = await fetch(`/labs/${app}.wasm`);
      if (!response.ok) throw Error("Cannot load Fern simulation module.");
      api = await instantiateCore(await response.arrayBuffer());
      cells = Int32Array.from(data.cells);
      count = data.count || 65536;
      width = data.width || 256;
      reset();
      clearInterval(timer);
      timer = setInterval(step, 50);
      postMessage({ kind: "ready" });
    } else if (data.kind === "cells") {
      cells = Int32Array.from(data.cells);
      reset();
    } else if (data.kind === "run") running = data.value;
    else if (data.kind === "step") {
      const was = running;
      running = true;
      step();
      running = was;
    } else if (data.kind === "configure") {
      count = Math.max(16, Math.min(100000, data.count));
      width = data.width || width;
      reset();
    } else if (data.kind === "reset") reset();
  } catch (e) {
    running = false;
    postMessage({ kind: "error", message: e.message });
  }
};
