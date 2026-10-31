import { demos } from "./demos.mjs";
import { layout as L, word, putword } from "./kernel.mjs";

const $ = (id) => document.getElementById(id);
const canvas = $("screen"),
  ctx = canvas.getContext("2d");
const motion = matchMedia("(prefers-reduced-motion: reduce)");
const encoder = new TextEncoder(),
  decoder = new TextDecoder();
const names =
  "LIT INC POP NIP SWP ROT DUP OVR EQU NEQ GTH LTH JMP JCN JSR STH LDZ STZ LDR STR LDA STA DEI DEO ADD SUB MUL DIV AND ORA EOR SFT".split(
    " ",
  );
const hex = (n, size = 2) => n.toString(16).padStart(size, "0");
let state,
  rom,
  worker,
  counter = 0,
  generation = 0,
  frame = 0,
  busy = false,
  running = false,
  loading = true;
let lastScreen = 0;
let queued = [],
  currentEvent = null,
  buttons = 0;
const requests = new Map();

function makeWorker() {
  worker = new Worker(new URL("./worker.mjs", import.meta.url), {
    type: "module",
  });
  worker.onmessage = ({ data }) => {
    const request = requests.get(data.id);
    if (!request) return;
    requests.delete(data.id);
    clearTimeout(request.timer);
    if (data.error) request.reject(new Error(data.error));
    else request.resolve(data.result);
  };
  worker.onerror = (event) =>
    failWorker(new Error(event.message || "Machine worker failed"));
}
function failWorker(error) {
  worker?.terminate();
  worker = null;
  for (const request of requests.values()) {
    clearTimeout(request.timer);
    request.reject(error);
  }
  requests.clear();
}
function request(action, data = {}) {
  if (!worker) makeWorker();
  const id = ++counter;
  return new Promise((resolve, reject) => {
    const timer = setTimeout(
      () =>
        failWorker(
          new Error(
            "This operation exceeded the 5-second work limit. Reset or try a smaller program.",
          ),
        ),
      5000,
    );
    requests.set(id, { resolve, reject, timer });
    worker.postMessage({ id, action, ...data });
  });
}
function halt() {
  running = false;
  cancelAnimationFrame(frame);
  frame = 0;
  generation++;
  controls();
}
function exited() {
  return state && !state[L.meta + 4] && state[L.dev + 15] !== 0;
}
function controls() {
  const ready = !!state && !loading;
  for (const id of ["run", "step", "reset", "download", "send"])
    $(id).disabled =
      !ready ||
      (id === "step" && busy) ||
      (exited() && ["run", "step", "send"].includes(id));
  $("pause").disabled = !ready || !running;
  for (const id of ["assemble", "assemble-step", "demo", "upload"])
    $(id).disabled = loading;
  $("mode").textContent = !state
    ? "NO ROM"
    : exited()
      ? "EXITED"
      : running
        ? state[L.meta + 4]
          ? "RUNNING"
          : "EVENTS"
        : "PAUSED";
}
function problem(error) {
  $("diagnostic").textContent = error.message;
  $("diagnostic").hidden = false;
  $("status").textContent = "Machine paused · see diagnostic";
  halt();
}
function opcode(byte) {
  if (byte === 0) return "BRK";
  if (byte === 32) return "JCI";
  if (byte === 64) return "JMI";
  if (byte === 96) return "JSI";
  return (
    names[byte & 31] +
    (byte & 32 ? "2" : "") +
    (byte & 64 ? "r" : "") +
    (byte & 128 && byte & 31 ? "k" : "")
  );
}
function dump(bytes, start, mark = -1) {
  const fragment = document.createDocumentFragment();
  for (let row = 0; row < bytes.length; row += 16) {
    fragment.append(`${hex(start + row, 4)}  `);
    for (let col = 0; col < 16 && row + col < bytes.length; col++) {
      const text = hex(bytes[row + col]);
      if (start + row + col === mark) {
        const node = document.createElement("mark");
        node.textContent = text;
        fragment.append(node);
      } else fragment.append(text);
      fragment.append(" ");
    }
    fragment.append("\n");
  }
  return fragment;
}
function render() {
  if (!state) return;
  const pc = word(state, L.meta),
    width = word(state, L.meta + 16),
    height = word(state, L.meta + 18);
  if (canvas.width !== width || canvas.height !== height) {
    canvas.width = width;
    canvas.height = height;
  }
  const image = ctx.createImageData(width, height),
    palette = [];
  for (let i = 0; i < 4; i++) {
    const shift = i % 2 ? 0 : 4;
    palette.push(
      [8, 10, 12].map(
        (port) =>
          ((state[L.dev + port + Math.floor(i / 2)] >> shift) & 15) * 17,
      ),
    );
  }
  for (let y = 0; y < height; y++)
    for (let x = 0; x < width; x++) {
      const pixel = state[L.screen + y * 512 + x];
      const color = palette[pixel >> 2 || pixel & 3],
        at = (y * width + x) * 4;
      image.data[at] = color[0];
      image.data[at + 1] = color[1];
      image.data[at + 2] = color[2];
      image.data[at + 3] = 255;
    }
  ctx.putImageData(image, 0, 0);
  $("screen-size").textContent = `VARVARA SCREEN / ${width} × ${height}`;
  $("pc").textContent = hex(pc, 4);
  $("next-op").textContent = state[L.meta + 4]
    ? opcode(state[pc])
    : "BRK / YIELDED";
  $("steps").textContent = (
    (word(state, L.meta + 8) * 65536 + word(state, L.meta + 10)) >>>
    0
  ).toLocaleString();
  for (const [id, base, count] of [
    ["wst", L.wst, state[L.meta + 2]],
    ["rst", L.rst, state[L.meta + 3]],
  ]) {
    $(id).textContent = count
      ? [...state.slice(base, base + count)].map((b) => hex(b)).join(" ") +
        " ← TOP"
      : "(empty)";
    $(id + "-count").textContent = `${count} bytes`;
  }
  const page = parseInt($("memory-page").value, 16);
  if (Number.isFinite(page) && /^[\da-f]{1,4}$/i.test($("memory-page").value)) {
    const start = page & 0xff00;
    $("memory").replaceChildren(
      dump(state.slice(start, start + 256), start, state[L.meta + 4] ? pc : -1),
    );
  }
  $("devices").replaceChildren(dump(state.slice(L.dev, L.dev + 256), 0));
  $("console").textContent = decoder.decode(
    state.slice(L.console, L.console + word(state, L.meta + 12)),
  );
  const unsupported = state[L.meta + 14],
    missing = [];
  if (unsupported & 1) missing.push("Audio");
  if (unsupported & 2) missing.push("File");
  if (unsupported & 4) missing.push("Expansion banks");
  if (unsupported & 8) missing.push("Subprocess");
  $("device-warning").hidden = missing.length === 0;
  $("device-warning").textContent =
    `This ROM wrote to unavailable devices: ${missing.join(", ")}. It may require a full Varvara emulator.`;
  controls();
}
function datePorts() {
  const date = new Date(),
    start = new Date(date.getFullYear(), 0, 1);
  putword(state, L.dev + 0xc0, date.getFullYear());
  [
    date.getMonth(),
    date.getDate(),
    date.getHours(),
    date.getMinutes(),
    date.getSeconds(),
    date.getDay(),
  ].forEach((n, i) => {
    state[L.dev + 0xc2 + i] = n;
  });
  const days = Math.floor(
    (Date.UTC(date.getFullYear(), date.getMonth(), date.getDate()) -
      Date.UTC(start.getFullYear(), 0, 1)) /
      86400000,
  );
  putword(state, L.dev + 0xc8, days);
  const winter = new Date(date.getFullYear(), 0, 1).getTimezoneOffset(),
    summer = new Date(date.getFullYear(), 6, 1).getTimezoneOffset();
  state[L.dev + 0xca] =
    date.getTimezoneOffset() < Math.max(winter, summer) ? 1 : 0;
}
function schedule() {
  if (!frame && !busy && running && !document.hidden && state)
    frame = requestAnimationFrame(() => {
      frame = 0;
      advance(false);
    });
}
async function advance(single) {
  if (busy || !state || exited() || document.hidden) return;
  const version = generation;
  busy = true;
  controls();
  try {
    let entry = -1;
    if (!state[L.meta + 4]) {
      if (currentEvent) {
        for (const port of currentEvent.clear || []) state[L.dev + port] = 0;
        currentEvent = null;
      }
      if (queued.length) {
        currentEvent = queued.shift();
        for (const [port, value] of currentEvent.bytes)
          state[L.dev + port] = value;
        entry = word(state, L.dev + currentEvent.vector);
      } else {
        entry = word(state, L.dev + 0x20);
        if (!single && entry && performance.now() - lastScreen < 1000 / 60)
          return;
        lastScreen = performance.now();
      }
      if (entry === 0) {
        if (queued.length) schedule();
        return;
      }
    }
    datePorts();
    const next = await request("run", {
      state,
      budget: single ? 1 : 20000,
      entry,
    });
    if (generation !== version) return;
    if (next.length !== L.size)
      throw new Error("The machine returned an invalid state");
    state = next;
    if (exited()) running = false;
    render();
    $("status").textContent = exited()
      ? `ROM exited · code ${state[L.dev + 15] & 127}`
      : single
        ? "Single instruction · Fern CPU"
        : "Fern CPU · browser devices";
  } catch (error) {
    if (generation === version) problem(error);
  } finally {
    busy = false;
    controls();
    if (
      running &&
      state &&
      !exited() &&
      (state[L.meta + 4] || queued.length || word(state, L.dev + 0x20))
    )
      schedule();
  }
}
async function install(bytes, autorun) {
  halt();
  failWorker(new Error("Machine reset"));
  const version = generation;
  loading = true;
  controls();
  try {
    const fresh = await request("boot", { rom: bytes });
    if (version !== generation) return;
    if (fresh.length !== L.size)
      throw new Error("Load a ROM between 1 and 65,280 bytes");
    state = fresh;
    rom = bytes;
    queued = [];
    currentEvent = null;
    buttons = 0;
    $("rom-size").textContent = `${bytes.length.toLocaleString()} BYTE ROM`;
    $("diagnostic").hidden = true;
    $("status").textContent = "Fern Uxn · ready";
    running = autorun;
    render();
  } catch (error) {
    if (version === generation) problem(error);
  } finally {
    loading = false;
    controls();
    schedule();
  }
}
async function assemble(autorun) {
  halt();
  failWorker(new Error("Assembly replaced previous work"));
  const version = generation;
  loading = true;
  controls();
  try {
    const bytes = await request("assemble", { source: $("source").value });
    if (version === generation) await install(bytes, autorun);
  } catch (error) {
    if (version === generation) problem(error);
  } finally {
    loading = false;
    controls();
  }
}
function choose() {
  const demo = demos[$("demo").value];
  $("source").value = demo.source;
  $("demo-description").textContent = demo.description;
  assemble(!motion.matches);
}
$("assemble").onclick = () => assemble(true);
$("assemble-step").onclick = () => assemble(false);
$("demo").onchange = choose;
$("source").onkeydown = (event) => {
  if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
    event.preventDefault();
    assemble(true);
  }
};
$("run").onclick = () => {
  running = true;
  controls();
  schedule();
};
$("pause").onclick = halt;
$("step").onclick = () => {
  halt();
  advance(true);
};
$("reset").onclick = () => install(rom, false);
$("memory-page").oninput = render;
$("download").onclick = () => {
  const url = URL.createObjectURL(
    new Blob([rom], { type: "application/octet-stream" }),
  );
  const link = document.createElement("a");
  link.href = url;
  link.download = "fern-uxn.rom";
  link.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
};
$("upload").onchange = async () => {
  const file = $("upload").files[0];
  if (!file) return;
  if (file.size < 1 || file.size > 65280)
    return problem(new Error("Load a ROM between 1 and 65,280 bytes"));
  await install(new Uint8Array(await file.arrayBuffer()), false);
  $("status").textContent = `Loaded ${file.name} · paused`;
  $("upload").value = "";
};
function enqueue(event, coalesce = false) {
  if (!state || exited()) return;
  if (coalesce && queued.at(-1)?.vector === event.vector)
    queued[queued.length - 1] = event;
  else if (queued.length < 4096) queued.push(event);
  schedule();
}
$("console-form").onsubmit = (event) => {
  event.preventDefault();
  if (!state) return;
  for (const byte of encoder.encode(
    $("console-input").value.slice(0, 1024) + "\n",
  ))
    enqueue({
      vector: 0x10,
      bytes: [
        [0x12, byte],
        [0x17, 1],
      ],
    });
  $("console-input").value = "";
  running = true;
  controls();
  schedule();
};
const keyBits = {
  Control: 1,
  Alt: 2,
  Shift: 4,
  Home: 8,
  ArrowUp: 16,
  ArrowDown: 32,
  ArrowLeft: 64,
  ArrowRight: 128,
};
canvas.onkeydown = (event) => {
  if (event.key in keyBits) {
    event.preventDefault();
    buttons |= keyBits[event.key];
  }
  const byte =
    event.key.length === 1
      ? event.key.charCodeAt(0) & 255
      : event.key === "Enter"
        ? 13
        : event.key === "Backspace"
          ? 8
          : 0;
  enqueue({
    vector: 0x80,
    bytes: [
      [0x82, buttons],
      [0x83, byte],
    ],
    clear: [0x83],
  });
};
canvas.onkeyup = (event) => {
  if (!(event.key in keyBits)) return;
  event.preventDefault();
  buttons &= ~keyBits[event.key];
  enqueue({
    vector: 0x80,
    bytes: [
      [0x82, buttons],
      [0x83, 0],
    ],
  });
};
canvas.onblur = () => {
  buttons = 0;
  enqueue({
    vector: 0x80,
    bytes: [
      [0x82, 0],
      [0x83, 0],
    ],
  });
};
function pointer(event) {
  const box = canvas.getBoundingClientRect();
  const x = Math.max(
    0,
    Math.min(
      canvas.width - 1,
      Math.floor(((event.clientX - box.x) / box.width) * canvas.width),
    ),
  );
  const y = Math.max(
    0,
    Math.min(
      canvas.height - 1,
      Math.floor(((event.clientY - box.y) / box.height) * canvas.height),
    ),
  );
  enqueue(
    {
      vector: 0x90,
      bytes: [
        [0x92, x >> 8],
        [0x93, x & 255],
        [0x94, y >> 8],
        [0x95, y & 255],
        [
          0x96,
          (event.buttons & 1) |
            ((event.buttons & 2) << 1) |
            ((event.buttons & 4) >> 1),
        ],
      ],
    },
    event.type === "pointermove",
  );
}
canvas.onpointerdown = (event) => {
  canvas.focus();
  canvas.setPointerCapture(event.pointerId);
  pointer(event);
};
canvas.onpointermove = canvas.onpointerup = pointer;
canvas.onpointercancel = () => enqueue({ vector: 0x90, bytes: [[0x96, 0]] });
canvas.oncontextmenu = (event) => event.preventDefault();
canvas.addEventListener(
  "wheel",
  (event) => {
    event.preventDefault();
    const x = Math.sign(event.deltaX),
      y = Math.sign(event.deltaY);
    enqueue({
      vector: 0x90,
      bytes: [
        [0x9a, (x >> 8) & 255],
        [0x9b, x & 255],
        [0x9c, (y >> 8) & 255],
        [0x9d, y & 255],
      ],
      clear: [0x9a, 0x9b, 0x9c, 0x9d],
    });
  },
  { passive: false },
);
document.addEventListener("visibilitychange", () => {
  if (document.hidden) {
    cancelAnimationFrame(frame);
    frame = 0;
    buttons = 0;
    enqueue({
      vector: 0x80,
      bytes: [
        [0x82, 0],
        [0x83, 0],
      ],
    });
  } else schedule();
});
motion.addEventListener("change", (event) => {
  if (event.matches) halt();
});
window.addEventListener("pagehide", () => {
  halt();
  failWorker(new Error("Page closed"));
});
if (!ctx) problem(new Error("Canvas 2D is required for this showcase"));
else {
  loading = false;
  choose();
}
