import { instantiateCore } from "../shared/wasm.mjs";

const $ = (id) => document.getElementById(id);
const canvas = $("terrain"),
  context = canvas.getContext("2d");
const motion = matchMedia("(prefers-reduced-motion: reduce)");
let height,
  frame = 0,
  previous = 0,
  paused = motion.matches;
let x = 941,
  z = 136,
  heading = 0,
  dragging = null,
  pixels,
  skyline;
const keys = new Set();

function resize() {
  canvas.width = Number($("detail").value);
  canvas.height = canvas.width * 0.75;
  pixels = context.createImageData(canvas.width, canvas.height);
  skyline = new Int32Array(canvas.width);
  draw();
}
function draw() {
  if (!height) return;
  const start = performance.now(),
    w = canvas.width,
    h = canvas.height;
  const data = pixels.data;
  for (let i = 0; i < data.length; i += 4) {
    data[i] = 16;
    data[i + 1] = 6;
    data[i + 2] = 184;
    data[i + 3] = 255;
  }
  skyline.fill(h);
  const sin = Math.sin(heading),
    cos = Math.cos(heading);
  const altitude = Number($("altitude").value);
  let count = 0;
  for (let distance = 10; distance < 700; distance += 3 + distance * 0.022) {
    for (let col = 0; col < w; col++) {
      const side = (col / w - 0.5) * distance * 1.4;
      const elevation = height(
        x + side * cos + distance * sin,
        z + distance * cos - side * sin,
      );
      const row = Math.floor(
        h * 0.38 + ((altitude - elevation) * h * 0.9) / distance,
      );
      if (row >= 0 && row < skyline[col]) {
        skyline[col] = row;
        const at = (row * w + col) * 4;
        data[at] = 243;
        data[at + 1] = 245;
        data[at + 2] = 43;
        count++;
      }
    }
  }
  context.putImageData(pixels, 0, 0);
  $("readout").textContent =
    `X ${Math.round(x)} · Z ${Math.round(z)} · ${count} PIXELS · ${(performance.now() - start).toFixed(1)} MS`;
}
function tick(now) {
  frame = 0;
  if (paused || document.hidden || !height) return;
  const dt = previous ? Math.min((now - previous) / 1000, 0.05) : 0;
  previous = now;
  heading +=
    ((keys.has("ArrowRight") ? 1 : 0) - (keys.has("ArrowLeft") ? 1 : 0)) * dt;
  if (keys.has("ArrowUp") || keys.has("ArrowDown")) {
    const input = $("altitude");
    input.value =
      Number(input.value) + (keys.has("ArrowUp") ? 1 : -1) * dt * 40;
    $("altitude-value").value = input.value;
  }
  const travel = Number($("speed").value) * dt;
  x += Math.sin(heading) * travel;
  z += Math.cos(heading) * travel;
  draw();
  frame = requestAnimationFrame(tick);
}
function schedule() {
  cancelAnimationFrame(frame);
  frame = 0;
  previous = 0;
  $("pause").textContent = paused ? "Resume flight" : "Pause flight";
  if (!paused && !document.hidden && height)
    frame = requestAnimationFrame(tick);
}
$("pause").onclick = () => {
  paused = !paused;
  schedule();
};
$("reset").onclick = () => {
  x = 941;
  z = 136;
  heading = 0;
  $("altitude").value = 130;
  $("speed").value = 35;
  for (const id of ["altitude", "speed"]) $(id + "-value").value = $(id).value;
  draw();
};
for (const id of ["speed", "altitude"])
  $(id).oninput = () => {
    $(id + "-value").value = $(id).value;
    if (paused) draw();
  };
$("detail").onchange = resize;
$("fullscreen").hidden = !document.fullscreenEnabled;
$("fullscreen").onclick = async () => {
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await $("stage").requestFullscreen();
  } catch (error) {
    $("status").textContent = `Fullscreen unavailable: ${error.message}`;
  }
};
canvas.onkeydown = (event) => {
  if (event.code === "Space") {
    event.preventDefault();
    if (!event.repeat && height) {
      paused = !paused;
      schedule();
    }
  }
  if (event.key.startsWith("Arrow")) {
    event.preventDefault();
    keys.add(event.key);
    if (paused) {
      if (event.key === "ArrowLeft") heading -= 0.08;
      if (event.key === "ArrowRight") heading += 0.08;
      if (event.key === "ArrowUp")
        $("altitude").value = Number($("altitude").value) + 2;
      if (event.key === "ArrowDown")
        $("altitude").value = Number($("altitude").value) - 2;
      $("altitude-value").value = $("altitude").value;
      draw();
    }
  }
};
canvas.onkeyup = (event) => keys.delete(event.key);
canvas.onblur = () => keys.clear();
canvas.onpointerdown = (event) => {
  dragging = event.clientX;
  canvas.setPointerCapture(event.pointerId);
  canvas.focus();
};
canvas.onpointermove = (event) => {
  if (dragging === null) return;
  heading +=
    ((event.clientX - dragging) / canvas.getBoundingClientRect().width) * 2;
  dragging = event.clientX;
  if (paused) draw();
};
canvas.onpointerup =
  canvas.onpointercancel =
  canvas.onlostpointercapture =
    () => {
      dragging = null;
    };
document.addEventListener("visibilitychange", () => {
  keys.clear();
  schedule();
});
motion.addEventListener("change", (event) => {
  if (event.matches) {
    paused = true;
    schedule();
  }
});

try {
  if (!context) throw new Error("Canvas 2D is unavailable");
  const response = await fetch(new URL("./kernel.wasm", import.meta.url));
  if (!response.ok)
    throw new Error(`Kernel download failed (${response.status})`);
  const exports = await instantiateCore(await response.arrayBuffer());
  height = exports["fern:showcase/contour#height"];
  if (typeof height !== "function")
    throw new Error("Terrain export is missing");
  resize();
  $("pause").disabled = $("reset").disabled = false;
  $("status").textContent = "Fern Wasm · ready to fly";
  schedule();
} catch (error) {
  $("status").textContent = `Unable to start terrain: ${error.message}`;
}
