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
let powered,
  refined = 0,
  kernelLoading;
let program = Uint8Array.from([
    0x11,
    0x22,
    0x23,
    0x70,
    0x80,
    ...Array(11).fill(0x80),
  ]),
  cpuState = 0,
  cpuRunning = false,
  cpuOutput = 0,
  cpuSteps = 0;
const fn = (name) => api[`fern:showcase/${app}#${name}`];
function reset() {
  values = new Int32Array(count);
  next = new Int32Array(count);
  incoming = app === "foundry" ? new Int32Array(count) : null;
  outgoing = app === "foundry" ? new Int32Array(count) : null;
  capacity = app === "foundry" ? new Int32Array(count) : null;
  tick = 0;
  steps = 0;
  consumed = 0;
  produced = 0;
  measured = [];
  generation++;
  refined = 0;
  powered = app === "foundry" ? new Uint8Array(count) : null;
  if (app === "foundry" && api && cells) {
    const queue = new Uint32Array(count);
    let head = 0,
      tail = 0;
    for (let i = 0; i < count; i++)
      if (cells[i] === 8) {
        powered[i] = 1;
        queue[tail++] = i;
      }
    while (head < tail) {
      const i = queue[head++];
      for (const j of [i - 1, i + 1, i - width, i + width])
        if (
          j >= 0 &&
          j < count &&
          ((j !== i - 1 && j !== i + 1) ||
            Math.floor(i / width) === Math.floor(j / width)) &&
          !powered[j] &&
          fn("conducts")(cells[j])
        ) {
          powered[j] = 1;
          queue[tail++] = j;
        }
    }
  }
}
function cpuStep() {
  const pc = (cpuState >>> 4) & 15;
  if (cpuState & 256) return;
  const instruction = program[pc];
  cpuState = fn("cpu")(instruction, cpuState & 15, pc);
  cpuSteps++;
  if (cpuState & 512) cpuOutput = cpuState & 15;
  postMessage({
    kind: "cpu",
    pc: (cpuState >>> 4) & 15,
    accumulator: cpuState & 15,
    halted: !!(cpuState & 256),
    carry: !!(cpuState & 1024),
    output: cpuOutput,
    steps: cpuSteps,
    instruction,
  });
  if (cpuState & 256) cpuRunning = false;
}
function step() {
  if (api && app === "circuit" && cpuRunning) cpuStep();
  if (!running || !cells) return;
  const at = performance.now();
  if (app === "foundry") {
    incoming.fill(0);
    outgoing.fill(0);
    for (let i = 0; i < count; i++)
      capacity[i] = 15 - (values[i] & 15) - ((values[i] >>> 4) & 15);
    for (let i = 0; i < count; i++) {
      const kind = cells[i] || 0,
        item = fn("material")(kind, values[i]);
      if (!item || kind === 6 || !kind || (kind === 7 && !powered[i])) continue;
      const target = fn("output")(kind, i, width, count);
      let connected = true;
      if (kind === 9)
        connected =
          tick % 4 === 0 &&
          (i % width) + 4 < width &&
          i + 4 < count &&
          fn("rail-path")(
            cells[i + 1],
            cells[i + 2],
            cells[i + 3],
            cells[i + 4],
          );
      if (
        target !== i &&
        connected &&
        fn("accepts")(cells[target] || 0, item) &&
        capacity[target] > 0
      ) {
        outgoing[i] = item;
        incoming[target] += item;
        capacity[target]--;
      }
    }
    for (let i = 0; i < count; i++) {
      const kind = cells[i] || 0;
      if (kind === 6) consumed += fn("units")(incoming[i]);
      if (
        kind === 1 &&
        tick % 8 === 0 &&
        (values[i] & 15) - (outgoing[i] & 15) < 15
      )
        produced++;
      const value = fn("state")(
        kind,
        values[i],
        incoming[i],
        outgoing[i],
        tick,
        powered[i],
      );
      if (
        kind === 7 &&
        ((value >>> 4) & 15) >
          ((values[i] >>> 4) & 15) +
            ((incoming[i] >>> 4) & 15) -
            ((outgoing[i] >>> 4) & 15)
      )
        refined++;
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
        refined,
        powered: powered?.slice(0, Math.min(count, 65536)),
        consumed,
        steps,
        p50: percent(0.5),
        p95: percent(0.95),
        p99: percent(0.99),
        memory: api.memory.buffer.byteLength,
        hostBytes:
          values.byteLength +
          next.byteLength +
          cells.byteLength +
          (incoming?.byteLength || 0) +
          (outgoing?.byteLength || 0) +
          (capacity?.byteLength || 0) +
          (powered?.byteLength || 0),
      },
      [display.buffer],
    );
    last = now;
  }
}
onmessage = async ({ data }) => {
  try {
    if (data.kind !== "init" && kernelLoading) await kernelLoading;
    if (data.kind === "init") {
      app = data.app;
      kernelLoading ||= (async () => {
        const response = await fetch(`/labs/${app}.wasm`);
        if (!response.ok) throw Error("Cannot load Fern simulation module.");
        api = await instantiateCore(await response.arrayBuffer());
      })();
      await kernelLoading;
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
    } else if (data.kind === "program") {
      program = Uint8Array.from(data.program.slice(0, 16));
      cpuState = 0;
      cpuOutput = 0;
      cpuSteps = 0;
      cpuRunning = false;
      postMessage({
        kind: "cpu",
        pc: 0,
        accumulator: 0,
        halted: false,
        carry: false,
        output: 0,
        steps: 0,
        instruction: 0,
      });
    } else if (data.kind === "cpu-step") cpuStep();
    else if (data.kind === "cpu-run") cpuRunning = data.value;
    else if (data.kind === "reset") reset();
  } catch (e) {
    running = false;
    postMessage({ kind: "error", message: e.message });
  }
};
