import {
  shell,
  checkpoint,
  $,
  action,
  notice,
  error,
  roomControls,
  bindRoom,
  download,
  fileInput,
  measures,
  percentiles,
} from "./shell.mjs";
import { Session } from "./session.mjs";
shell(
  "Foundry",
  "Turn ore into a working supply line. Build a factory together, then fill the grid and measure the cost.",
  `${roomControls()}<h2>Build</h2><div class="palette" id="tools"></div><label>View zoom<input id="zoom" type="range" min="6" max="36" value="18"></label><label>View column<input id="pan-x" type="range" min="0" max="230" value="8"></label><label>View row<input id="pan-y" type="range" min="0" max="230" value="8"></label><h2>Push it</h2><label>Execution<select id="execution"><option value="native">Shared native simulation</option><option value="wasm">Local Wasm comparison</option></select></label><label>Active grid<select id="size"><option value="4096">4096 cells</option><option value="16384">16384 cells</option><option value="65536" selected>65536 cells</option></select></label><button id="populate">Build a busy factory</button><p class="hint">Tap or drag to place machines. Erase removes a machine. Smelters need a generator connected by power cables. Two ore units become one ingot after eight powered ticks. Rails move cargo four cells every fourth tick. The native server owns the shared cargo. The local Wasm comparison restarts after a layout revision.</p>`,
);
$("lesson").textContent =
  "The native server reserves destination capacity, advances the shared cargo, and broadcasts compact binary snapshots. The browser can run the same rules in a worker for comparison.";
bindRoom();
const names = [
    "Erase",
    "Ore mine",
    "Belt →",
    "Belt ↓",
    "Belt ←",
    "Belt ↑",
    "Depot",
    "Smelter",
    "Generator",
    "Freight rail",
    "Power cable",
    "Rail station",
  ],
  colors = [
    "#152437",
    "#e6b768",
    "#719fb1",
    "#719fb1",
    "#719fb1",
    "#719fb1",
    "#89d6a6",
    "#e38f72",
    "#79cce6",
    "#b0a8cf",
    "#67d8e6",
    "#d9bc7b",
  ];
let tool = 2,
  frame,
  paused = false,
  drag = false;
for (let i = 0; i < names.length; i++) {
  const b = document.createElement("button");
  b.textContent = names[i];
  b.dataset.tool = i;
  b.setAttribute("aria-pressed", String(i === tool));
  b.onclick = () => {
    tool = i;
    for (const x of $("tools").children)
      x.setAttribute("aria-pressed", String(Number(x.dataset.tool) === i));
  };
  $("tools").append(b);
}
const worker = new Worker("/labs/simulation-worker.mjs", { type: "module" });
let initialized = false;
const session = new Session("foundry", (cells, points, full) => {
  if ($("execution").value === "wasm") {
    worker.postMessage({
      kind: initialized ? "cells" : "init",
      app: "foundry",
      cells,
      count: Number($("size").value),
      width: 256,
    });
    initialized = true;
  }
  draw();
});
const nativeTimes = [];
session.onFactory = (data) => {
  if ($("execution").value !== "native") return;
  frame = data;
  paused = !data.running;
  $("pause").textContent = paused ? "Resume" : "Pause";
  nativeTimes.push(data.stepMs);
  if (nativeTimes.length > 256) nativeTimes.shift();
  draw();
  const p = percentiles(nativeTimes);
  measures({
    Execution: "native server",
    Tick: data.tick,
    "Step p50": p.p50.toFixed(2) + " ms",
    p95: p.p95.toFixed(2) + " ms",
    p99: p.p99.toFixed(2) + " ms",
    "Ore units produced": data.produced,
    "Ore units delivered": data.consumed,
    "Ingots refined": data.refined,
    "Removed by edits": data.removed,
    Snapshot: data.packetBytes + " B",
    "Network p95": session.latencies.length
      ? percentiles(session.latencies).p95.toFixed(1) + " ms"
      : "—",
  });
  canvas.dataset.tick = data.tick;
};
worker.onmessage = ({ data }) => {
  if (data.kind === "error") {
    error(Error(data.message));
    return;
  }
  if (data.kind !== "frame" || $("execution").value !== "wasm") return;
  frame = data;
  draw();
  const rtt = percentiles(session.latencies);
  measures({
    "Step p50": data.p50.toFixed(2) + " ms",
    p95: data.p95.toFixed(2) + " ms",
    p99: data.p99.toFixed(2) + " ms",
    Cells: data.count,
    Tick: data.tick,
    "Ore units produced": data.produced,
    "Ore units delivered": data.consumed,
    "Ingots refined": data.refined,
    "Wasm memory": (data.memory / 1048576).toFixed(2) + " MiB",
    "Host arrays": (data.hostBytes / 1048576).toFixed(2) + " MiB",
    "Network p95": rtt.p95.toFixed(1) + " ms",
    "Server edit": session.serverUs + " µs",
  });
};
const canvas = $("view"),
  ctx = canvas.getContext("2d");
function draw() {
  if (!session?.cells) return;
  const r = canvas.getBoundingClientRect(),
    d = Math.min(devicePixelRatio || 1, 2);
  if (
    canvas.width !== Math.round(r.width * d) ||
    canvas.height !== Math.round(r.height * d)
  ) {
    canvas.width = Math.round(r.width * d);
    canvas.height = Math.round(r.height * d);
  }
  ctx.setTransform(d, 0, 0, d, 0, 0);
  ctx.fillStyle = "#0c1727";
  ctx.fillRect(0, 0, r.width, r.height);
  const scale = Number($("zoom").value),
    ox = Number($("pan-x").value),
    oy = Number($("pan-y").value),
    cols = Math.ceil(r.width / scale),
    rows = Math.ceil(r.height / scale);
  for (let y = 0; y < rows; y++)
    for (let x = 0; x < cols; x++) {
      const index = (y + oy) * 256 + x + ox;
      if (x + ox >= 256 || y + oy >= 256) continue;
      const kind = session.draft.get(index) ?? session.cells[index] ?? 0,
        state = frame?.values[index] || 0,
        cargo = state & 15,
        ingots = (state >>> 4) & 15;
      ctx.strokeStyle = "#203148";
      ctx.strokeRect(x * scale, y * scale, scale, scale);
      if (kind > 0 && kind < colors.length) {
        ctx.fillStyle = colors[kind];
        ctx.fillRect(x * scale + 2, y * scale + 2, scale - 4, scale - 4);
        if (scale >= 14) {
          ctx.fillStyle = "#122034";
          ctx.font = `${Math.min(16, scale - 5)}px system-ui`;
          ctx.textAlign = "center";
          ctx.fillText(
            ["", "M", ">", "v", "<", "^", "D", "S", "G", "=", "+", "R"][kind],
            (x + 0.5) * scale,
            (y + 0.72) * scale,
          );
        }
        if (frame?.powered?.[index]) {
          ctx.strokeStyle = "#62e0ef";
          ctx.lineWidth = 2;
          ctx.strokeRect(x * scale + 2, y * scale + 2, scale - 4, scale - 4);
          ctx.lineWidth = 1;
        }
        if (ingots) {
          ctx.fillStyle = "#78d9e9";
          ctx.fillRect(
            (x + 0.12) * scale,
            (y + 0.12) * scale,
            Math.min(6, scale * 0.3),
            Math.min(6, scale * 0.3),
          );
        }
        if (cargo) {
          ctx.fillStyle = "#f5d68a";
          ctx.beginPath();
          ctx.arc(
            (x + 0.73) * scale,
            (y + 0.3) * scale,
            Math.min(5, 2 + cargo / 5),
            0,
            Math.PI * 2,
          );
          ctx.fill();
        }
      }
    }
}
function paint(e) {
  if (!session.cells) return;
  const r = canvas.getBoundingClientRect(),
    scale = Number($("zoom").value),
    x = Math.floor((e.clientX - r.left) / scale) + Number($("pan-x").value),
    y = Math.floor((e.clientY - r.top) / scale) + Number($("pan-y").value);
  if (x >= 0 && x < 256 && y >= 0 && y < 256) {
    try {
      session.edit([y * 256 + x], [tool]);
    } catch (e) {
      error(e);
    }
  }
}
canvas.onpointerdown = (e) => {
  drag = true;
  canvas.setPointerCapture(e.pointerId);
  paint(e);
};
canvas.onpointermove = (e) => {
  if (drag) paint(e);
};
canvas.onpointerup = canvas.onpointercancel = () => (drag = false);
for (const id of ["zoom", "pan-x", "pan-y"]) $(id).oninput = draw;
addEventListener("resize", draw);
action(
  "Pause",
  () => {
    if ($("execution").value === "native") {
      session.command(paused ? "resume" : "pause");
      return;
    }
    paused = !paused;
    worker.postMessage({ kind: "run", value: !paused });
    $("pause").textContent = paused ? "Resume" : "Pause";
  },
  "pause",
);
action("Single step", () => {
  $("execution").value === "native"
    ? session.command("step")
    : worker.postMessage({ kind: "step" });
});
action("Reset cargo", () => {
  $("execution").value === "native"
    ? session.command("reset")
    : worker.postMessage({ kind: "reset" });
});
action("Export layout", () =>
  download(
    "foundry.json",
    JSON.stringify({
      app: "foundry",
      cells: Array.from(session.currentCells()),
    }),
  ),
);
async function edits(points, values) {
  for (let at = 0; at < points.length; at += 128) {
    while (session.pending.size > 512)
      await new Promise((r) => setTimeout(r, 30));
    session.edit(points.slice(at, at + 128), values.slice(at, at + 128));
  }
  notice("Layout updates queued.");
}
$("populate").onclick = () => {
  const count = Number($("size").value),
    points = [],
    values = [];
  for (let i = 0; i < count; i++) {
    points.push(i);
    values.push(i % 256 === 0 ? 1 : i % 256 === 255 ? 6 : 2);
  }
  edits(points, values).catch(error);
};
$("execution").onchange = () => {
  if ($("execution").value === "wasm") {
    worker.postMessage({
      kind: initialized ? "cells" : "init",
      app: "foundry",
      cells: session.cells,
      count: Number($("size").value),
      width: 256,
    });
    initialized = true;
    worker.postMessage({ kind: "run", value: true });
  } else {
    worker.postMessage({ kind: "run", value: false });
  }
  notice(
    $("execution").value === "native"
      ? "Shared native simulation."
      : "Local Wasm comparison.",
  );
};
$("size").onchange = () =>
  worker.postMessage({
    kind: "configure",
    count: Number($("size").value),
    width: 256,
  });
fileInput(".json", async (file) => {
  if (file.size > 1048576) throw Error("Layout file exceeds 1 MiB.");
  const data = JSON.parse(await file.text());
  if (
    data.app !== "foundry" ||
    !Array.isArray(data.cells) ||
    data.cells.length !== 65536 ||
    data.cells.some((x) => !Number.isInteger(x) || x < 0 || x > 11)
  )
    throw Error("Invalid Foundry layout.");
  await edits(
    data.cells.map((_, i) => i),
    data.cells,
  );
});
addEventListener("pagehide", () => worker.terminate(), { once: true });

checkpoint("foundry");

session.onDraft = draw;
