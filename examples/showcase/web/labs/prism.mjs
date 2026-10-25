import {
  shell,
  $,
  action,
  error,
  notice,
  measures,
  download,
} from "./shell.mjs";
import { instantiateCore } from "/assets/wasm.mjs";
shell(
  "Prism",
  "Move a sphere, turn the camera, and watch light converge. Every pixel sample comes from Fern.",
  `<label>Image width<select id="width"><option>160</option><option selected>320</option><option>640</option></select></label><label>Render workers<select id="workers"><option>1</option><option>2</option><option>4</option></select></label><label>Samples per pixel<select id="samples"><option>1</option><option>8</option><option selected>32</option><option>128</option></select></label><label>Camera angle<input id="angle" type="range" min="-150" max="150" value="0"></label><h2>Move the green sphere</h2><label>X<input id="sphere-x" type="range" min="64" max="192" value="128"></label><label>Y<input id="sphere-y" type="range" min="128" max="176" value="128"></label><label>Z<input id="sphere-z" type="range" min="64" max="176" value="96"></label><label>Exposure<input id="exposure" type="range" min="25" max="300" value="150"></label><button id="reference">Compare native and Wasm</button><output id="reference-result" class="hint"></output><p class="hint">Drag the image to orbit. The worker cancels old renders as soon as a scene changes. More samples reduce noise and increase work.</p>`,
);
$("lesson").textContent =
  "Fern traces diffuse and mirror bounces through four spheres, including the floor. Samples contain linear light in three 10-bit channels. The worker accumulates samples, then applies exposure and tone mapping for the display.";
const canvas = $("view"),
  context = canvas.getContext("2d");
canvas.style.height = "auto";
canvas.style.minHeight = "0";
canvas.style.aspectRatio = "8 / 5";
$("stage").style.minHeight = "0";
const workers = [];
let laneFrames = new Map(),
  renderStarted = 0;
function postAll(data) {
  for (const worker of workers) worker.postMessage(data);
}
function pool(count) {
  while (workers.length > count) workers.pop().terminate();
  while (workers.length < count) {
    const worker = new Worker("/labs/prism-worker.mjs", { type: "module" });
    worker.onmessage = receive;
    workers.push(worker);
  }
}
let requestID = 0;
let renderTimer,
  lastImage,
  completed = 0,
  dragX;
const scene = () =>
  Number($("sphere-x").value) |
  (Number($("sphere-y").value) << 8) |
  (Number($("sphere-z").value) << 16);
function render() {
  notice("Render in progress.");
  canvas.dataset.progress = "0";
  canvas.dataset.samples = "0";
  const count = Number($("workers").value);
  pool(count);
  laneFrames = new Map();
  renderStarted = performance.now();
  lastImage = null;
  const id = ++requestID;
  const width = Number($("width").value);
  canvas.width = width;
  canvas.height = Math.round(width * 0.625);
  for (let lane = 0; lane < count; lane++)
    workers[lane].postMessage({
      kind: "render",
      requestID: id,
      lane,
      lanes: count,
      width,
      samples: Number($("samples").value),
      scene: scene(),
      angle: (Number($("angle").value) * Math.PI) / 180,
      exposure: Number($("exposure").value) / 100,
    });
}
function schedule() {
  canvas.dataset.progress = "0";
  requestID++;
  postAll({ kind: "cancel" });
  clearTimeout(renderTimer);
  renderTimer = setTimeout(render, 80);
}
for (const id of [
  "width",
  "workers",
  "samples",
  "angle",
  "sphere-x",
  "sphere-y",
  "sphere-z",
  "exposure",
])
  $(id).oninput = schedule;
function receive({ data }) {
  if (data.kind === "error") {
    error(Error(data.message));
    return;
  }
  if (data.kind !== "image" || data.requestID !== requestID) return;
  if (canvas.width !== data.width || canvas.height !== data.imageHeight) {
    canvas.width = data.width;
    canvas.height = data.imageHeight;
  }
  lastImage = new ImageData(data.bytes, data.width, data.height);
  context.putImageData(lastImage, 0, data.row);
  laneFrames.set(data.lane, data);
  const all = [...laneFrames.values()],
    count = Number($("workers").value),
    totalPixels = data.width * data.imageHeight;
  const progress = all.reduce(
      (sum, part) =>
        sum + (part.progress * part.width * part.height) / totalPixels,
      0,
    ),
    done = all.length === count && all.every((part) => part.done);
  canvas.dataset.samples =
    all.length === count ? Math.min(...all.map((part) => part.samples)) : 0;
  canvas.dataset.progress = progress;
  measures({
    Workers: count,
    "Samples / pixel": canvas.dataset.samples,
    Progress: (progress * 100).toFixed(1) + "%",
    "Camera rays": all
      .reduce((sum, part) => sum + part.rays, 0)
      .toLocaleString(),
    "Compute time (sum)":
      all.reduce((sum, part) => sum + part.cpu, 0).toFixed(1) + " ms",
    "Wall time": ((performance.now() - renderStarted) / 1000).toFixed(2) + " s",
    "Slowest slice p95":
      Math.max(...all.map((part) => part.p95)).toFixed(2) + " ms",
    "Wasm memory":
      (all.reduce((sum, part) => sum + part.memory, 0) / 1048576).toFixed(2) +
      " MiB",
    "Render buffers":
      (all.reduce((sum, part) => sum + part.hostBytes, 0) / 1048576).toFixed(
        2,
      ) + " MiB",
  });
  if (done) {
    completed++;
    notice("Render complete.");
  }
}

canvas.onpointerdown = (e) => {
  dragX = e.clientX;
  canvas.setPointerCapture(e.pointerId);
};
canvas.onpointermove = (e) => {
  if (dragX === undefined) return;
  const next = Math.max(
    -150,
    Math.min(150, Number($("angle").value) + (e.clientX - dragX) * 0.4),
  );
  $("angle").value = String(Math.round(next));
  dragX = e.clientX;
  schedule();
};
canvas.onpointerup = canvas.onpointercancel = () => (dragX = undefined);
action("Render", render);
action("Cancel", () => {
  requestID++;
  postAll({ kind: "cancel" });
  notice("Render cancelled.");
});
action("Save PNG", () => {
  if (!lastImage) throw Error("Render an image first.");
  canvas.toBlob((blob) => {
    if (blob) download("prism.png", blob, "image/png");
  });
});
$("reference").onclick = async () => {
  try {
    const query = new URLSearchParams({
      w: "32",
      h: "20",
      sample: "0",
      scene: String(scene()),
      angle: $("angle").value,
    });
    const response = await fetch("/prism/reference?" + query);
    if (!response.ok) throw Error("Native reference request failed.");
    const native = await response.json(),
      bytes = await (await fetch("/labs/prism.wasm")).arrayBuffer(),
      api = await instantiateCore(bytes),
      pixel = api["fern:showcase/prism#pixel"];
    let max = 0,
      different = 0;
    const at = performance.now();
    for (let y = 0; y < 20; y++)
      for (let x = 0; x < 32; x++) {
        const a =
            pixel(
              x,
              y,
              32,
              20,
              0,
              scene(),
              (Number($("angle").value) * Math.PI) / 180,
            ) >>> 0,
          b = native.pixels[y * 32 + x] >>> 0;
        for (const shift of [0, 10, 20]) {
          const delta = Math.abs(
            ((a >>> shift) & 1023) - ((b >>> shift) & 1023),
          );
          max = Math.max(max, delta);
          if (delta) different++;
        }
      }
    const ms = performance.now() - at;
    $("reference-result").textContent =
      `640 rays compared. Largest channel difference: ${max}/1023. Native ${native.server_us / 1000} ms, browser ${ms.toFixed(2)} ms. ${different} differing channels.`;
    $("reference").dataset.maxDifference = max;
  } catch (e) {
    error(e);
  }
};
addEventListener(
  "pagehide",
  () => {
    for (const worker of workers) worker.terminate();
  },
  { once: true },
);
render();
