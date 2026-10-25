import {
  shell,
  $,
  action,
  error,
  roomControls,
  bindRoom,
  download,
  fileInput,
  measures,
  percentiles,
  notice,
} from "./shell.mjs";
import { Session } from "./session.mjs";
shell(
  "Circuit",
  "Wire a logic circuit. Watch signals propagate one generation at a time, or run a hundred thousand gates.",
  `${roomControls()}<h2>Selected gate <span id="selected">0</span></h2><label>Gate type<select id="kind"><option value="8">Input OFF</option><option value="9">Input ON</option><option value="1">Wire</option><option value="2">NOT</option><option value="3">AND</option><option value="4">OR</option><option value="5">XOR</option><option value="6">NAND</option><option value="7">NOR</option><option value="0">Latch</option></select></label><div class="buttons"><button id="source-a">Connect A</button><button id="source-b">Connect B</button><button id="toggle">Toggle input</button></div><p id="wires" class="hint"></p><label>Visible page<input id="page" type="range" min="0" max="340" value="0"></label><h2>Push it</h2><label>Gate count<select id="count"><option value="64">64</option><option value="1024">1024</option><option value="10000">10000</option><option value="100000">100000</option></select></label><button id="adder">Load a full adder</button><button id="oscillator">Load a ring oscillator</button><p class="hint">Tap a gate to select it. Choose Connect A or B, then tap its input gate. An input button needs no keyboard. Large workloads extend the editable circuit with a generated gate chain.</p>`,
);
$("lesson").textContent =
  "Fern evaluates every gate from the previous generation. The worker swaps input and output buffers after a complete pass. This gives feedback loops a defined propagation delay.";
bindRoom();
let selected = 0,
  connect = null,
  frame,
  paused = false,
  initialized = false;
const labels = [
  "LATCH",
  "WIRE",
  "NOT",
  "AND",
  "OR",
  "XOR",
  "NAND",
  "NOR",
  "OFF",
  "ON",
];
const worker = new Worker("/labs/simulation-worker.mjs", { type: "module" });
const session = new Session("circuit", (cells) => {
  worker.postMessage({
    kind: initialized ? "cells" : "init",
    app: "circuit",
    cells,
    count: Number($("count").value),
  });
  initialized = true;
  refresh();
  draw();
});
worker.onmessage = ({ data }) => {
  if (data.kind === "error") {
    error(Error(data.message));
    return;
  }
  if (data.kind !== "frame") return;
  frame = data;
  draw();
  measures({
    Gates: data.count,
    Generation: data.tick,
    "Step p50": data.p50.toFixed(2) + " ms",
    p95: data.p95.toFixed(2) + " ms",
    p99: data.p99.toFixed(2) + " ms",
    "Wasm memory": (data.memory / 1048576).toFixed(2) + " MiB",
    "Host arrays": (data.hostBytes / 1048576).toFixed(2) + " MiB",
    "Network p95": percentiles(session.latencies).p95.toFixed(1) + " ms",
  });
};
const canvas = $("view"),
  ctx = canvas.getContext("2d");
function refresh() {
  if (!session.cells) return;
  $("selected").textContent = selected;
  $("kind").value = String(session.cells[selected * 3] || 0);
  $("wires").textContent =
    `A ← gate ${session.cells[selected * 3 + 1] || 0} · B ← gate ${session.cells[selected * 3 + 2] || 0}`;
}
function draw() {
  if (!session?.cells) return;
  const r = canvas.getBoundingClientRect(),
    d = Math.min(2, devicePixelRatio || 1);
  canvas.width = Math.round(r.width * d);
  canvas.height = Math.round(r.height * d);
  ctx.scale(d, d);
  ctx.fillStyle = "#0d1728";
  ctx.fillRect(0, 0, r.width, r.height);
  const start = Number($("page").value) * 64,
    w = r.width / 8,
    h = r.height / 8;
  for (let j = 0; j < 64; j++) {
    const i = start + j;
    if (i >= Number($("count").value)) continue;
    const x = ((j % 8) + 0.5) * w,
      y = (Math.floor(j / 8) + 0.5) * h;
    for (let port = 1; port <= 2; port++) {
      const source = session.cells[i * 3 + port] || 0;
      if (source >= start && source < start + 64) {
        const q = source - start;
        ctx.strokeStyle = frame?.values[source] ? "#79e8b0" : "#30465f";
        ctx.beginPath();
        ctx.moveTo(((q % 8) + 0.5) * w, (Math.floor(q / 8) + 0.5) * h);
        ctx.lineTo(x, y);
        ctx.stroke();
      }
    }
    ctx.fillStyle = frame?.values[i] ? "#79e8b0" : "#21344e";
    ctx.strokeStyle = i === selected ? "#ffd077" : "#516783";
    ctx.lineWidth = i === selected ? 3 : 1;
    ctx.beginPath();
    ctx.roundRect(x - w * 0.4, y - h * 0.34, w * 0.8, h * 0.68, 5);
    ctx.fill();
    ctx.stroke();
    ctx.fillStyle = frame?.values[i] ? "#0d2230" : "#c0cedb";
    ctx.textAlign = "center";
    ctx.font = `${Math.max(8, Math.min(12, w / 6))}px ui-monospace,monospace`;
    ctx.fillText(labels[session.cells[i * 3] || 0] || "?", x, y);
    ctx.fillText(String(i), x, y + 13);
  }
}
canvas.onpointerdown = (e) => {
  if (!session.cells) return;
  const r = canvas.getBoundingClientRect(),
    i =
      Number($("page").value) * 64 +
      Math.min(7, Math.floor(((e.clientX - r.left) / r.width) * 8)) +
      Math.min(7, Math.floor(((e.clientY - r.top) / r.height) * 8)) * 8;
  if (i * 3 + 2 >= session.cells.length) return;
  if (connect !== null) {
    session.edit([selected * 3 + connect], [i]);
    connect = null;
    notice("Wire connected.");
  } else selected = i;
  refresh();
  draw();
};
$("kind").onchange = () =>
  session.edit([selected * 3], [Number($("kind").value)]);
$("source-a").onclick = () => {
  connect = 1;
  notice("Tap the gate for input A.");
};
$("source-b").onclick = () => {
  connect = 2;
  notice("Tap the gate for input B.");
};
$("toggle").onclick = () =>
  session.edit([selected * 3], [session.cells[selected * 3] === 9 ? 8 : 9]);
$("page").oninput = draw;
$("count").onchange = () => {
  worker.postMessage({ kind: "configure", count: Number($("count").value) });
  draw();
};
addEventListener("resize", draw);
action(
  "Pause",
  () => {
    paused = !paused;
    worker.postMessage({ kind: "run", value: !paused });
    $("pause").textContent = paused ? "Resume" : "Pause";
  },
  "pause",
);
action("Single generation", () => worker.postMessage({ kind: "step" }));
action("Export circuit", () =>
  download(
    "circuit.json",
    JSON.stringify({ app: "circuit", cells: Array.from(session.cells || []) }),
  ),
);
$("adder").onclick = () => {
  const nodes = [
      [9, 0, 0],
      [8, 0, 0],
      [8, 0, 0],
      [5, 0, 1],
      [5, 3, 2],
      [3, 0, 1],
      [3, 3, 2],
      [4, 5, 6],
    ],
    points = [],
    values = [];
  nodes.flat().forEach((v, i) => {
    points.push(i);
    values.push(v);
  });
  session.edit(points, values);
  selected = 4;
  refresh();
  notice("Full adder: inputs 0, 1, 2. Sum at 4. Carry at 7.");
};
$("oscillator").onclick = () => {
  session.edit([0, 1, 2, 3, 4, 5, 6, 7, 8], [2, 2, 0, 2, 0, 0, 2, 1, 0]);
  notice("Three NOT gates form a feedback loop. Step to follow the signal.");
};
fileInput(".json", async (file) => {
  if (file.size > 1048576) throw Error("Circuit file exceeds 1 MiB.");
  const data = JSON.parse(await file.text());
  if (
    data.app !== "circuit" ||
    !Array.isArray(data.cells) ||
    data.cells.length !== 65536 ||
    data.cells.some(
      (x, i) => !Number.isInteger(x) || x < 0 || x > (i % 3 === 0 ? 9 : 21844),
    )
  )
    throw Error("Invalid circuit.");
  for (let at = 0; at < data.cells.length; at += 128) {
    while (session.pending.size > 512)
      await new Promise((r) => setTimeout(r, 30));
    session.edit(
      data.cells.slice(at, at + 128).map((_, i) => at + i),
      data.cells.slice(at, at + 128),
    );
  }
});
addEventListener("pagehide", () => worker.terminate(), { once: true });
