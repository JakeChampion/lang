import { mountFullscreen } from "/assets/ui.mjs";
import { $, status, toast } from "/assets/ui.mjs";
import { load } from "./kernel.mjs";
const canvas = $("field"),
  max = 16384,
  points = new Float32Array(max * 3);
let kernel,
  renderer,
  frame = 0,
  previous = 0,
  elapsed = 0,
  frames = 0,
  measuredAt = 0,
  paused = matchMedia("(prefers-reduced-motion: reduce)").matches,
  px = 0.5,
  py = 0.5,
  force = 0,
  energy = 0,
  count = 4096,
  audio,
  source,
  analyser,
  worker,
  audioID = 0,
  bins;
function shader(gl, type, text) {
  const s = gl.createShader(type);
  gl.shaderSource(s, text);
  gl.compileShader(s);
  if (!gl.getShaderParameter(s, gl.COMPILE_STATUS))
    throw Error(gl.getShaderInfoLog(s));
  return s;
}
function graphics() {
  const gl = canvas.getContext("webgl", {
    alpha: false,
    antialias: false,
    powerPreference: "low-power",
  });
  if (!gl) {
    const context = canvas.getContext("2d");
    if (!context) throw Error("Your browser cannot create a canvas renderer.");
    return {
      name: "Canvas 2D",
      draw() {
        context.fillStyle = "#090e22";
        context.fillRect(0, 0, canvas.width, canvas.height);
        context.globalCompositeOperation = "lighter";
        for (let i = 0; i < count; i++) {
          context.fillStyle = `hsla(${210 + points[i * 3 + 2] * 100},75%,65%,.55)`;
          context.fillRect(
            ((points[i * 3] + 1) * canvas.width) / 2,
            ((1 - points[i * 3 + 1]) * canvas.height) / 2,
            2,
            2,
          );
        }
        context.globalCompositeOperation = "source-over";
      },
    };
  }
  const program = gl.createProgram();
  gl.attachShader(
    program,
    shader(
      gl,
      gl.VERTEX_SHADER,
      `attribute vec3 point; varying float hue; uniform float size; void main(){gl_Position=vec4(point.xy,0.,1.);gl_PointSize=size;hue=point.z;}`,
    ),
  );
  gl.attachShader(
    program,
    shader(
      gl,
      gl.FRAGMENT_SHADER,
      `precision mediump float;varying float hue;void main(){float r=length(gl_PointCoord-.5)*2.;if(r>1.)discard;vec3 color=mix(vec3(.22,.72,.88),vec3(.87,.39,.92),hue);gl_FragColor=vec4(color,(1.-r)*(1.-r)*.85);}`,
    ),
  );
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS))
    throw Error(gl.getProgramInfoLog(program));
  gl.useProgram(program);
  gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer());
  gl.bufferData(gl.ARRAY_BUFFER, points.byteLength, gl.DYNAMIC_DRAW);
  const location = gl.getAttribLocation(program, "point");
  gl.enableVertexAttribArray(location);
  gl.vertexAttribPointer(location, 3, gl.FLOAT, false, 12, 0);
  const size = gl.getUniformLocation(program, "size");
  gl.enable(gl.BLEND);
  gl.blendFunc(gl.SRC_ALPHA, gl.ONE);
  gl.clearColor(0.035, 0.055, 0.13, 1);
  return {
    name: "WebGL",
    draw() {
      gl.viewport(0, 0, canvas.width, canvas.height);
      gl.clear(gl.COLOR_BUFFER_BIT);
      gl.uniform1f(size, Math.min(devicePixelRatio || 1, 2) * 5);
      gl.bufferSubData(gl.ARRAY_BUFFER, 0, points.subarray(0, count * 3));
      gl.drawArrays(gl.POINTS, 0, count);
    },
  };
}
function resize() {
  const rect = canvas.getBoundingClientRect(),
    ratio = Math.min(devicePixelRatio || 1, 2);
  canvas.width = Math.round(rect.width * ratio);
  canvas.height = Math.round(rect.height * ratio);
  if (paused && kernel) draw(performance.now());
}
function draw(now) {
  frame = 0;
  if (!kernel || document.hidden) return;
  const dt = previous ? Math.min(0.05, (now - previous) / 1000) : 0;
  previous = now;
  if (!paused) elapsed += dt;
  if (analyser) {
    analyser.getByteFrequencyData(bins);
    energy = bins.reduce((a, b) => a + b, 0) / (bins.length * 255);
  }
  const at = performance.now();
  for (let i = 0; i < count; i++) {
    const packed = kernel.particle(i, elapsed, px, py, force, energy) >>> 0;
    points[i * 3] = ((packed >>> 16) / 65535) * 2 - 1;
    points[i * 3 + 1] = 1 - ((packed & 65535) / 65535) * 2;
    points[i * 3 + 2] = (i % 97) / 96;
  }
  const spent = performance.now() - at;
  renderer.draw();
  frames++;
  if (now - measuredAt > 500 || paused) {
    $("kernel-time").textContent = spent.toFixed(2);
    $("memory").textContent = (kernel.memory.buffer.byteLength / 1024).toFixed(
      0,
    );
    $("fps").textContent = paused
      ? "PAUSED"
      : Math.min(
          240,
          Math.round((frames * 1000) / (now - measuredAt)),
        ).toString();
    frames = 0;
    measuredAt = now;
  }
  canvas.dataset.frames = String(Number(canvas.dataset.frames || 0) + 1);
  if (!paused) frame = requestAnimationFrame(draw);
}
function refresh() {
  if (!frame && kernel) frame = requestAnimationFrame(draw);
}
function setPaused(value) {
  paused = value;
  previous = 0;
  cancelAnimationFrame(frame);
  frame = 0;
  $("pause").textContent = paused ? "Resume field" : "Pause field";
  if (audio) {
    if (paused) audio.suspend();
    else audio.resume();
  }
  refresh();
}
$("pause").onclick = () => setPaused(!paused);
$("reset").onclick = () => {
  elapsed = 0;
  force = energy = 0;
  refresh();
};
$("density").oninput = () => {
  count = Number($("density").value);
  $("count").value = count.toLocaleString();
  refresh();
};
function pointer(e) {
  const r = canvas.getBoundingClientRect();
  px = (e.clientX - r.left) / r.width;
  py = (e.clientY - r.top) / r.height;
  force = e.shiftKey ? -1 : Number($("interaction").value);
  refresh();
}
canvas.onpointerdown = (e) => {
  canvas.setPointerCapture(e.pointerId);
  pointer(e);
};
canvas.onpointermove = pointer;
canvas.onpointerleave = (event) => {
  if (event.pointerType === "mouse") {
    force = 0;
    refresh();
  }
};
canvas.onkeydown = (e) => {
  if (
    ![
      "ArrowUp",
      "ArrowDown",
      "ArrowLeft",
      "ArrowRight",
      " ",
      "Escape",
    ].includes(e.key)
  )
    return;
  e.preventDefault();
  if (e.key === " ") {
    setPaused(!paused);
    return;
  }
  px = Math.max(
    0,
    Math.min(
      1,
      px + (e.key === "ArrowRight" ? 0.04 : e.key === "ArrowLeft" ? -0.04 : 0),
    ),
  );
  py = Math.max(
    0,
    Math.min(
      1,
      py + (e.key === "ArrowDown" ? 0.04 : e.key === "ArrowUp" ? -0.04 : 0),
    ),
  );
  force =
    e.key === "Escape" ? 0 : e.shiftKey ? -1 : Number($("interaction").value);
  if (e.key === "Escape") $("interaction").value = "0";
  refresh();
};
canvas.addEventListener("webglcontextlost", (e) => {
  e.preventDefault();
  setPaused(true);
  status("connection", "Graphics context lost. Reload to resume.", false, true);
});
function synthesize() {
  if (worker)
    worker.postMessage({ id: ++audioID, tone: Number($("tone").value) });
}
$("audio").onclick = async () => {
  if (audio) {
    await audio.close();
    audio = analyser = null;
    worker.terminate();
    worker = null;
    source = null;
    audioID++;
    $("audio").textContent = "Enable audio ♫";
    energy = 0;
    refresh();
    return;
  }
  try {
    audio = new AudioContext();
    await audio.resume();
    analyser = audio.createAnalyser();
    analyser.fftSize = 256;
    bins = new Uint8Array(analyser.frequencyBinCount);
    analyser.connect(audio.destination);
    worker = new Worker(new URL("./audio-worker.mjs", import.meta.url), {
      type: "module",
    });
    $("audio").textContent = "Prepare Fern audio…";
    worker.onmessage = ({ data }) => {
      if (!audio || data.id !== audioID) return;
      if (data.error) {
        toast(data.error);
        return;
      }
      const buffer = audio.createBuffer(1, data.pcm.length, data.rate);
      buffer.copyToChannel(data.pcm, 0);
      source?.stop();
      source = audio.createBufferSource();
      source.buffer = buffer;
      source.loop = true;
      source.connect(analyser);
      source.start();
      $("audio").textContent = "Mute audio ♫";
      canvas.dataset.audio = "playing";
      if (paused) audio.suspend();
    };
    worker.onerror = (e) => toast(`Audio worker: ${e.message}`);
    synthesize();
  } catch (error) {
    toast(error.message);
    if (audio) await audio.close();
    audio = null;
    worker?.terminate();
    worker = null;
  }
};
$("tone").onchange = synthesize;
$("interaction").onchange = () => {
  force = Number($("interaction").value);
  refresh();
};
addEventListener("resize", resize);
addEventListener("visibilitychange", () => {
  previous = 0;
  if (document.hidden) {
    cancelAnimationFrame(frame);
    frame = 0;
    audio?.suspend();
  } else {
    refresh();
    if (!paused) audio?.resume();
  }
});
addEventListener("pagehide", () => {
  cancelAnimationFrame(frame);
  worker?.terminate();
  audio?.close();
});
try {
  kernel = await load();
  renderer = graphics();
  $("renderer").textContent = renderer.name;
  $("wasm-size").textContent = (kernel.bytes / 1024).toFixed(1);
  resize();
  setPaused(paused);
  status(
    "connection",
    "Live · Fern Wasm · all computation on this device",
    true,
  );
} catch (error) {
  status("connection", error.message, false, true);
  $("pause").disabled = $("audio").disabled = true;
}

mountFullscreen(document.querySelector(".swarm-player"));

new ResizeObserver(resize).observe(canvas);
