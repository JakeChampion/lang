import { $, status, json } from "/assets/ui.mjs";
import { kernel, hex, size, pause } from "/bench/kernel.mjs";

const fresh = await kernel("/bench/imaging.wasm", "fern:showcase/image").catch(
  (error) => {
    status("connection", error.message, false, true);
    throw error;
  },
);
status("connection", "Ready · Fern Wasm loaded", true);

const MAX_PIXELS = 12 * 1024 * 1024;
const ms = (value) => (value == null ? "—" : `${value.toFixed(1)} ms`);
const digest = async (bytes) =>
  hex(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes)));

// One stage in a fresh instance, so copies of earlier inputs never accumulate.
async function stage(name, ...args) {
  const instance = await fresh();
  const started = performance.now();
  const bytes = instance.list(instance.call(name, ...args));
  return { bytes, ms: performance.now() - started };
}

async function decode(file) {
  const started = performance.now();
  const bitmap = await createImageBitmap(file);
  if (bitmap.width * bitmap.height > MAX_PIXELS)
    throw Error("Choose an image of 12 megapixels or less.");
  const canvas = new OffscreenCanvas(bitmap.width, bitmap.height);
  const context = canvas.getContext("2d");
  context.drawImage(bitmap, 0, 0);
  const pixels = context.getImageData(0, 0, bitmap.width, bitmap.height).data;
  return {
    rgba: new Uint8Array(pixels.buffer),
    width: bitmap.width,
    height: bitmap.height,
    ms: performance.now() - started,
  };
}

// The browser's own resize, grayscale filter, and PNG encoder on the same pixels.
async function canvasPipeline(rgba, width, height, target, outHeight, filter, tone) {
  const source = await createImageBitmap(
    new ImageData(new Uint8ClampedArray(rgba.buffer, rgba.byteOffset, rgba.length), width, height),
  );
  const canvas = new OffscreenCanvas(target, outHeight);
  const context = canvas.getContext("2d");
  context.imageSmoothingEnabled = filter === 0;
  let started = performance.now();
  context.drawImage(source, 0, 0, target, outHeight);
  context.getImageData(0, 0, 1, 1);
  const resize = performance.now() - started;
  let toneMs = null;
  if (tone === 1) {
    const toned = new OffscreenCanvas(target, outHeight);
    const tc = toned.getContext("2d");
    started = performance.now();
    tc.filter = "grayscale(1)";
    tc.drawImage(canvas, 0, 0);
    tc.getImageData(0, 0, 1, 1);
    toneMs = performance.now() - started;
    context.drawImage(toned, 0, 0);
  }
  started = performance.now();
  const blob = await canvas.convertToBlob({ type: "image/png" });
  return { resize, tone: toneMs, encode: performance.now() - started, bytes: blob.size };
}

function preview(rgba, width, height) {
  const canvas = $("input");
  canvas.width = width;
  canvas.height = height;
  canvas
    .getContext("2d")
    .putImageData(new ImageData(new Uint8ClampedArray(rgba.buffer, rgba.byteOffset, rgba.length), width, height), 0, 0);
  $("input-caption").textContent = `Input · ${width} × ${height}`;
}

$("source").onchange = () => {
  const own = $("source").value === "file";
  $("file-field").hidden = !own;
  $("seed-field").hidden = own;
};

$("controls").onsubmit = async (event) => {
  event.preventDefault();
  $("run").disabled = true;
  try {
    const own = $("source").value === "file";
    const seed = Number($("seed").value) || 0;
    const filter = Number($("filter").value);
    const tone = Number($("tone").value);
    let rgba, width, height, sourceRow;
    if (own) {
      const file = $("file").files[0];
      if (!file) throw Error("Choose an image first.");
      status("connection", "Decode your image in this browser");
      const decoded = await decode(file);
      ({ rgba, width, height } = decoded);
      sourceRow = ["Decode", null, null, decoded.ms];
    } else {
      [width, height] = $("source").value.split("x").map(Number);
      status("connection", "Draw the test image in Fern Wasm");
      const drawn = await stage("sample", seed, width, height);
      rgba = drawn.bytes;
      sourceRow = ["Draw test image", drawn.ms, null, null];
    }
    const target = Math.min(Number($("target").value), width);
    const query = `seed=${seed}&width=${width}&height=${height}&target=${target}&filter=${filter}&tone=${tone}`;
    const server = own ? null : json(`/image/bench?${query}`).catch((e) => e);
    preview(rgba, width, height);
    const outHeight = (await fresh()).call("fit", width, height, target);
    if (outHeight <= 0) throw Error("This image cannot be resized to that width.");

    status("connection", "Resize in Fern Wasm");
    await pause();
    const resized = await stage("resize", rgba, width, height, target, filter);
    if (!resized.bytes.length) throw Error("Fern rejected the image.");
    status("connection", "Set the tone in Fern Wasm");
    await pause();
    const toned = await stage("tone", resized.bytes, target, outHeight, tone);
    status("connection", "Encode the PNG in Fern Wasm");
    await pause();
    const png = await stage("encode", toned.bytes, target, outHeight);
    if (!png.bytes.length) throw Error("The PNG encoder rejected the image.");

    const url = URL.createObjectURL(new Blob([png.bytes], { type: "image/png" }));
    $("output").src = url;
    $("output").hidden = false;
    $("download").href = url;
    $("download").hidden = false;
    $("output-caption").textContent = `Fern output · ${target} × ${outHeight} · ${size(png.bytes.length)}`;

    status("connection", "Run the same steps with the browser's canvas");
    await pause();
    const browser = await canvasPipeline(rgba, width, height, target, outHeight, filter, tone);

    let native = null;
    let verdict = "Your image stayed in this browser. The server received nothing.";
    let match = "local";
    if (server) {
      status("connection", "Wait for the native server");
      native = await server;
      if (native instanceof Error) {
        verdict = `The server did not run the pipeline: ${native.message}`;
        match = "unknown";
        native = null;
      } else {
        const ours = await digest(png.bytes);
        match = String(ours === native.sha256);
        verdict =
          ours === native.sha256
            ? `✓ Identical PNG files. SHA-256 ${ours.slice(0, 16)}… on both sides.`
            : `✗ The PNG files differ: ${ours.slice(0, 16)}… here, ${native.sha256.slice(0, 16)}… on the server.`;
      }
    }
    const nms = (ns) => (native ? ns / 1e6 : null);
    sourceRow[2] = own ? null : nms(native?.sample_ns);
    const rows = [
      sourceRow,
      ["Resize", resized.ms, nms(native?.resize_ns), browser.resize],
      ["Tone", tone ? toned.ms : null, tone ? nms(native?.tone_ns) : null, browser.tone],
      ["PNG encode", png.ms, nms(native?.encode_ns), browser.encode],
    ];
    $("results").innerHTML =
      rows
        .map(
          ([name, wasm, server, canvas]) =>
            `<tr><td class="engine"><strong>${name}</strong></td><td class="number">${ms(wasm)}</td><td class="number">${ms(server)}</td><td class="number">${ms(canvas)}</td></tr>`,
        )
        .join("") +
      `<tr><td class="engine"><strong>PNG size</strong></td><td class="number">${size(png.bytes.length)}</td><td class="number">${native ? size(native.png_bytes) : "—"}</td><td class="number">${size(browser.bytes)}</td></tr>`;
    $("verdict").textContent = verdict;
    $("verdict").dataset.match = match;
    $("summary").textContent = `${width} × ${height} to ${target} × ${outHeight}, ${filter ? "nearest neighbor" : "bilinear"}, ${tone ? "grayscale" : "color"}.`;
    status("connection", "Done", true);
  } catch (error) {
    status("connection", error.message, false, true);
  } finally {
    $("run").disabled = false;
  }
};
