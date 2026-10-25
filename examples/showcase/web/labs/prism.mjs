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
  `<label>Image width<select id="width"><option>160</option><option selected>320</option><option>640</option></select></label><label>Samples per pixel<select id="samples"><option>1</option><option>8</option><option selected>32</option><option>128</option></select></label><label>Camera angle<input id="angle" type="range" min="-150" max="150" value="0"></label><h2>Move the green sphere</h2><label>X<input id="sphere-x" type="range" min="64" max="192" value="128"></label><label>Y<input id="sphere-y" type="range" min="128" max="176" value="128"></label><label>Z<input id="sphere-z" type="range" min="64" max="176" value="96"></label><label>Exposure<input id="exposure" type="range" min="25" max="300" value="150"></label><button id="reference">Compare native and Wasm</button><p class="hint">Drag the image to orbit. The worker cancels old renders as soon as a scene changes. More samples reduce noise and increase work.</p>`,
);
$("lesson").textContent =
  "Fern traces diffuse and mirror bounces through four spheres, including the floor. Samples contain linear light in three 10-bit channels. The worker accumulates samples, then applies exposure and tone mapping for the display.";
const canvas = $("view"),
  context = canvas.getContext("2d"),
  worker = new Worker("/labs/prism-worker.mjs", { type: "module" });
canvas.style.height = "auto";
canvas.style.minHeight = "0";
canvas.style.aspectRatio = "8 / 5";
$("stage").style.minHeight = "0";
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
  worker.postMessage({
    kind: "render",
    width: Number($("width").value),
    samples: Number($("samples").value),
    scene: scene(),
    angle: (Number($("angle").value) * Math.PI) / 180,
    exposure: Number($("exposure").value) / 100,
  });
}
function schedule() {
  worker.postMessage({ kind: "cancel" });
  clearTimeout(renderTimer);
  renderTimer = setTimeout(render, 80);
}
for (const id of [
  "width",
  "samples",
  "angle",
  "sphere-x",
  "sphere-y",
  "sphere-z",
  "exposure",
])
  $(id).oninput = schedule;
worker.onmessage = ({ data }) => {
  if (data.kind === "error") {
    error(Error(data.message));
    return;
  }
  if (data.kind !== "image") return;
  canvas.width = data.width;
  canvas.height = data.height;
  lastImage = new ImageData(data.bytes, data.width, data.height);
  context.putImageData(lastImage, 0, 0);
  canvas.dataset.samples = data.samples;
  canvas.dataset.progress = data.progress;
  measures({
    "Samples / pixel": data.samples,
    Progress: (data.progress * 100).toFixed(1) + "%",
    "Camera rays": data.rays.toLocaleString(),
    "Worker CPU": data.cpu.toFixed(1) + " ms",
    "Wall time": (data.elapsed / 1000).toFixed(2) + " s",
    "Compute slice p95": data.p95.toFixed(2) + " ms",
    "Wasm memory": (data.memory / 1048576).toFixed(2) + " MiB",
    "Host buffers": (data.hostBytes / 1048576).toFixed(2) + " MiB",
  });
  if (data.done) {
    completed++;
    notice("Render complete.");
  }
};
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
  worker.postMessage({ kind: "cancel" });
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
    notice(
      `640 rays compared. Largest channel difference: ${max}/1023. Native ${native.server_us / 1000} ms, browser ${ms.toFixed(2)} ms. ${different} differing channels.`,
    );
    $("reference").dataset.maxDifference = max;
  } catch (e) {
    error(e);
  }
};
addEventListener("pagehide", () => worker.terminate(), { once: true });
render();
