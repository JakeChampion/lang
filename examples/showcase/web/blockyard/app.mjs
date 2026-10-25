import { $, toast, copy, mountFullscreen, json } from "/assets/ui.mjs";
import {
  controls,
  edit,
  travel,
  decode,
  predict,
  ray,
  instantiate,
  chunkKey,
  voxelKey,
  localKey,
} from "./protocol.mjs";
import { palette as colors } from "./mesh.mjs";
import { Renderer } from "./renderer.mjs";

const canvas = $("view"),
  world = new URLSearchParams(location.search).get("world") || "commons";
const chunks = new Map(),
  wanted = new Set(),
  loading = new Set(),
  updates = new Map(),
  pending = new Map(),
  overlay = new Map(),
  sent = new Map(),
  history = [],
  keys = new Set();
let renderer,
  kernel,
  worker,
  workerReady,
  epoch = 1,
  id = 0,
  socket,
  local = { x: 256.5, y: 16, z: 265, vy: 0, grounded: false },
  yaw = 0,
  pitch = -0.18,
  fly = false,
  sequence = 0,
  nonce = 0,
  selected = 3,
  frame,
  previous = new Map(),
  tickSpan = 1,
  received = 0,
  rtt = 0;
let animation = 0,
  lastDraw = 0,
  lastSend = 0,
  lastChunks = "",
  lastHud = 0,
  frames = 0,
  lastMeasure = performance.now(),
  radius = 3,
  activeFetches = 0,
  ready = false,
  pointerLook,
  joystick = { x: 0, y: 0 },
  held = { jump: false, up: false, down: false },
  cameraError = { x: 0, y: 0, z: 0 },
  firstSnapshot = true;
const materials = [
  [1, "Grass"],
  [2, "Earth"],
  [3, "Stone"],
  [4, "Sand"],
  [5, "Wood"],
  [6, "Leaves"],
  [7, "Brick"],
  [8, "Glass"],
  [9, "Slate"],
  [10, "Light"],
];
$("world").value = world;
$("world-name").textContent = world.toUpperCase();
$("play").disabled = true;
const fullscreen = mountFullscreen($("game"), {
  container: document.querySelector(".toolbar-buttons"),
});
$("world").onchange = () => {
  location.href = `/blockyard/?world=${encodeURIComponent($("world").value)}`;
};
$("share").onclick = () =>
  copy(`${location.origin}/blockyard/?world=${encodeURIComponent(world)}`);
$("light").onchange = drawSoon;
$("fly").onclick = () => setFly(!fly);
$("leave").onclick = () => socket?.close();
$("explore").onchange = () => {
  if (id && socket?.readyState === WebSocket.OPEN)
    socket.send(travel(++sequence, Number($("explore").value)));
};
function setFly(value) {
  fly = value;
  $("fly").setAttribute("aria-pressed", String(fly));
  $("jump").hidden = fly;
  $("up").hidden = !fly;
  $("down").hidden = !fly;
  held.jump = held.up = held.down = false;
}
function choose(value) {
  selected = value;
  for (const button of $("palette").children)
    button.setAttribute(
      "aria-pressed",
      String(Number(button.dataset.material) === value),
    );
}
for (const [material, name] of materials) {
  const button = document.createElement("button");
  button.className = "material";
  button.dataset.material = material;
  button.title = name;
  button.setAttribute("aria-label", name);
  button.setAttribute("aria-pressed", String(material === selected));
  const swatch = document.createElement("span");
  swatch.className = "swatch";
  swatch.style.setProperty(
    "--block",
    `rgb(${colors[material].map((c) => Math.round(c * 255)).join(" ")})`,
  );
  swatch.setAttribute("aria-hidden", "true");
  const key = document.createElement("span");
  key.className = "key";
  key.textContent = material % 10;
  button.append(swatch, key);
  button.onclick = () => choose(material);
  $("palette").append(button);
}
function input() {
  const forward =
    (keys.has("KeyW") || keys.has("ArrowUp") ? 1 : 0) -
    (keys.has("KeyS") || keys.has("ArrowDown") ? 1 : 0) -
    joystick.y;
  const side =
    (keys.has("KeyD") || keys.has("ArrowRight") ? 1 : 0) -
    (keys.has("KeyA") || keys.has("ArrowLeft") ? 1 : 0) +
    joystick.x;
  let mx = Math.cos(yaw) * side - Math.sin(yaw) * forward,
    mz = -Math.sin(yaw) * side - Math.cos(yaw) * forward;
  const length = Math.max(1, Math.hypot(mx, mz));
  mx /= length;
  mz /= length;
  return {
    mx,
    mz,
    up:
      (held.up || keys.has("Space") ? 1 : 0) -
      (held.down || keys.has("ShiftLeft") || keys.has("ShiftRight") ? 1 : 0),
    jump: held.jump || keys.has("Space"),
    fly,
    yaw,
    pitch,
  };
}
function get(x, y, z) {
  if (x < 0 || z < 0 || x >= 512 || z >= 512 || y < 0 || y >= 48)
    return kernel.terrain(x, y, z);
  const at = voxelKey(x, y, z);
  if (overlay.has(at)) return overlay.get(at).material;
  const chunk = chunks.get(chunkKey(x >> 4, z >> 4));
  return chunk ? chunk.voxels[localKey(x, y, z)] : 3;
}
function resetChunks() {
  epoch++;
  ready = false;
  $("play").disabled = true;
  chunks.clear();
  wanted.clear();
  loading.clear();
  updates.clear();
  lastChunks = "";
  renderer.clear();
  worker.postMessage({ kind: "reset", epoch });
  pending.clear();
  overlay.clear();
  firstSnapshot = true;
  updateChunks(true);
}
function updateChunks(force = false) {
  const cx = Math.floor(local.x / 16),
    cz = Math.floor(local.z / 16),
    stamp = `${cx}/${cz}/${radius}`;
  if (stamp === lastChunks && !force) return;
  lastChunks = stamp;
  wanted.clear();
  for (let z = Math.max(0, cz - radius); z <= Math.min(31, cz + radius); z++)
    for (let x = Math.max(0, cx - radius); x <= Math.min(31, cx + radius); x++)
      wanted.add(chunkKey(x, z));
  for (const key of chunks.keys())
    if (!wanted.has(key)) {
      chunks.delete(key);
      updates.delete(key);
      renderer.remove(key);
      worker.postMessage({ kind: "forget", key, epoch });
    }
  pump();
}
function pump() {
  if (!workerReady) return;
  const choices = [...wanted]
    .filter((key) => !chunks.has(key) && !loading.has(key))
    .sort((a, b) => distance(a) - distance(b));
  while (activeFetches < 4 && choices.length) {
    const key = choices.shift();
    loadChunk(key, epoch);
  }
}
function distance(key) {
  return (
    ((key % 32) * 16 + 8 - local.x) ** 2 +
    (Math.floor(key / 32) * 16 + 8 - local.z) ** 2
  );
}
async function loadChunk(key, version) {
  activeFetches++;
  loading.add(key);
  try {
    const value = await json(
      `/blockyard/chunk?world=${encodeURIComponent(world)}&x=${key % 32}&z=${Math.floor(key / 32)}`,
    );
    if (version !== epoch || !wanted.has(key)) return;
    if (
      value.x !== key % 32 ||
      value.z !== Math.floor(key / 32) ||
      !Array.isArray(value.blocks) ||
      value.blocks.length > 12288
    )
      throw Error("Invalid chunk snapshot");
    const blocks = new Map(
      value.blocks.map((block) => [block.at, block.material]),
    );
    for (const [at, change] of updates.get(key) || [])
      if (change.revision > value.revision) blocks.set(at, change.material);
    worker.postMessage({
      kind: "chunk",
      epoch,
      x: value.x,
      z: value.z,
      revision: value.revision,
      blocks: [...blocks].map(([at, material]) => ({ at, material })),
    });
  } catch (error) {
    if (version === epoch) {
      $("loading").textContent = error.message;
      toast(error.message);
    }
  } finally {
    activeFetches--;
    pump();
  }
}
function onWorker({ data: message }) {
  if (message.kind === "ready") {
    workerReady = true;
    worker.postMessage({ kind: "reset", epoch });
    updateChunks(true);
    return;
  }
  if (message.kind === "error") {
    fatal(message.message);
    return;
  }
  if (message.epoch !== epoch || !wanted.has(message.key)) return;
  loading.delete(message.key);
  const voxels = message.voxels;
  for (const [at, change] of updates.get(message.key) || [])
    if (change.revision > message.revision) voxels[at] = change.material;
  chunks.set(message.key, { x: message.x, z: message.z, voxels });
  renderer.set(
    message.key,
    message.x,
    message.z,
    message.opaque,
    message.alpha,
  );
  const center = chunkKey(Math.floor(local.x / 16), Math.floor(local.z / 16));
  const cx = Math.floor(local.x / 16),
    cz = Math.floor(local.z / 16);
  let neighborhood = true;
  for (let z = Math.max(0, cz - 1); z <= Math.min(31, cz + 1); z++)
    for (let x = Math.max(0, cx - 1); x <= Math.min(31, cx + 1); x++)
      if (!chunks.has(chunkKey(x, z))) neighborhood = false;
  if (neighborhood) {
    ready = true;
    $("play").disabled = false;
    $("loading").textContent = "512 seats. One shared world.";
    if (id) {
      $("welcome").hidden = true;
      $("controls").hidden = false;
    }
  }
  drawSoon();
  pump();
}
function resolve(n) {
  const old = pending.get(n);
  if (!old) return;
  pending.delete(n);
  const at = voxelKey(old.x, old.y, old.z);
  overlay.delete(at);
  for (const [nonce, change] of pending)
    if (voxelKey(change.x, change.y, change.z) === at)
      overlay.set(at, { ...change, nonce });
  worker.postMessage({ kind: "resolve", epoch, nonce: n });
}
function applyEdits(value) {
  if (value.reset) {
    toast(
      "The world changed while this connection was slow. Refresh the nearby chunks.",
    );
    resetChunks();
    return;
  }
  const accepted = [];
  for (const change of value.blocks) {
    if (
      change.x < 0 ||
      change.x >= 512 ||
      change.y < 1 ||
      change.y >= 47 ||
      change.z < 0 ||
      change.z >= 512 ||
      change.material < 0 ||
      change.material > 10
    )
      throw Error("Invalid block update");
    const key = chunkKey(change.x >> 4, change.z >> 4),
      at = localKey(change.x, change.y, change.z);
    if (wanted.has(key)) {
      if (!updates.has(key)) updates.set(key, new Map());
      updates
        .get(key)
        .set(at, { material: change.material, revision: value.revision });
      if (chunks.has(key)) chunks.get(key).voxels[at] = change.material;
      accepted.push(change);
    }
    if (change.actor === id) resolve(change.nonce);
  }
  if (accepted.length)
    worker.postMessage({ kind: "edits", epoch, blocks: accepted });
}
function connect() {
  if (socket?.readyState === WebSocket.OPEN) return;
  $("play").disabled = true;
  $("loading").textContent = "Join the world…";
  const url = new URL(
    `/blockyard/ws?world=${encodeURIComponent(world)}`,
    location.origin,
  );
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const current = new WebSocket(url);
  socket = current;
  current.binaryType = "arraybuffer";
  current.onmessage = ({ data }) => {
    if (socket !== current) return;
    try {
      if (typeof data === "string") {
        const value = JSON.parse(data);
        if (value.kind === "welcome") {
          id = value.id;
          sequence = 0;
          nonce = 0;
          sent.clear();
          history.length = 0;
          pending.clear();
          overlay.clear();
          frame = null;
          previous.clear();
          firstSnapshot = true;
          local = {
            x: value.x,
            y: value.y,
            z: value.z,
            vy: 0,
            grounded: false,
          };
          cameraError = { x: 0, y: 0, z: 0 };
          lastSend = performance.now();
          $("connection").textContent = `Connected · ${value.world}`;
          $("players").textContent = `1 / ${value.capacity}`;
          $("leave").hidden = false;
          $("play-hint").hidden = matchMedia("(pointer:coarse)").matches;
          $("storage").textContent = value.persistent
            ? "Saved world. Edits survive a server process restart."
            : "Temporary world. Edits reset when this server restarts.";
          resetChunks();
          drawSoon();
        } else if (value.kind === "travel") {
          local = {
            x: value.x,
            y: value.y,
            z: value.z,
            vy: 0,
            grounded: false,
          };
          cameraError = { x: 0, y: 0, z: 0 };
          history.length = 0;
          sent.clear();
          frame = null;
          previous.clear();
          lastSend = performance.now();
          resetChunks();
          drawSoon();
        } else if (value.kind === "edits") applyEdits(value);
        else if (value.kind === "reject") {
          resolve(value.nonce);
          toast(value.message);
        }
        return;
      }
      const value = decode(data),
        now = performance.now();
      if (value.kind === "ack") {
        if (sent.has(value.seq)) {
          const sample = now - sent.get(value.seq);
          rtt = rtt ? rtt * 0.8 + sample * 0.2 : sample;
          sent.delete(value.seq);
        }
        return;
      }
      if (frame && value.tick <= frame.tick) return;
      tickSpan = frame ? value.tick - frame.tick : 1;
      previous = new Map(
        (frame?.players || value.players).map((p) => [p.id, p]),
      );
      frame = value;
      received = now;
      $("players").textContent = `${value.total} / 512`;
      $("nearby").textContent = `${value.players.length} nearby`;
      const own = value.players.find((p) => p.id === id);
      if (own && kernel && !ready) {
        local = { ...own };
        drawSoon();
      }
      if (own && kernel && ready) {
        let next = { ...own };
        while (history.length && history[0].seq <= own.seq) history.shift();
        for (const command of history)
          next = predict(kernel, get, next, command.input, command.dt);
        next = predict(
          kernel,
          get,
          next,
          input(),
          Math.min(0.05, (now - lastSend) / 1000),
        );
        const error = {
          x: local.x - next.x,
          y: local.y - next.y,
          z: local.z - next.z,
        };
        if (!firstSnapshot && Math.hypot(error.x, error.y, error.z) < 1.5) {
          cameraError.x += error.x;
          cameraError.y += error.y;
          cameraError.z += error.z;
        } else cameraError = { x: 0, y: 0, z: 0 };
        local = next;
        firstSnapshot = false;
        updateChunks();
      }
      drawSoon();
    } catch (error) {
      toast(error.message);
      current.close(1002, "Invalid world response");
    }
  };
  current.onclose = () => {
    if (socket !== current) return;
    id = 0;
    frame = null;
    keys.clear();
    held.jump = held.up = held.down = false;
    history.length = 0;
    sent.clear();
    for (const n of [...pending.keys()]) resolve(n);
    $("welcome").hidden = false;
    $("controls").hidden = true;
    $("leave").hidden = true;
    $("play").disabled = !ready;
    $("loading").textContent = "Select Join world to return.";
    $("connection").textContent = "Disconnected";
    $("play-hint").hidden = true;
    document.exitPointerLock?.();
    drawSoon();
  };
  current.onerror = () => {
    $("loading").textContent =
      "Could not join. Try again when a seat is available.";
  };
}
$("play").onclick = connect;
function sendControl() {
  if (
    !id ||
    !ready ||
    document.hidden ||
    socket?.readyState !== WebSocket.OPEN ||
    socket.bufferedAmount > 16384
  )
    return;
  const now = performance.now(),
    seq = ++sequence,
    command = input();
  history.push({
    seq,
    input: command,
    dt: Math.min(0.1, (now - lastSend) / 1000),
  });
  if (history.length > 128) history.shift();
  sent.set(seq, now);
  if (sent.size > 128) sent.delete(sent.keys().next().value);
  lastSend = now;
  socket.send(controls(seq, command));
}
setInterval(sendControl, 50);
function changeBlock(action) {
  if (
    !id ||
    !ready ||
    socket?.readyState !== WebSocket.OPEN ||
    pending.size >= 32 ||
    socket.bufferedAmount > 16384
  )
    return;
  const target = ray(get, local, yaw, pitch);
  if (!target) {
    toast("Move closer to a block.");
    return;
  }
  const point = action ? target.adjacent : target;
  if (
    point.y < 1 ||
    point.y >= 47 ||
    point.x < 0 ||
    point.x >= 512 ||
    point.z < 0 ||
    point.z >= 512
  ) {
    toast("That block is outside the build area.");
    return;
  }
  const n = ++nonce,
    change = {
      x: point.x,
      y: point.y,
      z: point.z,
      material: action ? selected : 0,
    };
  pending.set(n, change);
  overlay.set(voxelKey(change.x, change.y, change.z), { ...change, nonce: n });
  worker.postMessage({ kind: "optimistic", epoch, nonce: n, change });
  sendControl();
  socket.send(edit(n, action, selected, point));
  drawSoon();
}
function repeat(button, action) {
  let timer;
  button.onpointerdown = (e) => {
    e.preventDefault();
    button.setPointerCapture(e.pointerId);
    changeBlock(action);
    clearInterval(timer);
    timer = setInterval(() => changeBlock(action), 200);
  };
  const stop = () => {
    clearInterval(timer);
  };
  button.onpointerup = stop;
  button.onpointercancel = stop;
  button.onlostpointercapture = stop;
}
repeat($("break"), 0);
repeat($("place"), 1);
for (const [name, field] of [
  ["jump", "jump"],
  ["up", "up"],
  ["down", "down"],
]) {
  const button = $(name);
  button.onpointerdown = (e) => {
    e.preventDefault();
    button.setPointerCapture(e.pointerId);
    held[field] = true;
  };
  button.onpointerup =
    button.onpointercancel =
    button.onlostpointercapture =
      () => {
        held[field] = false;
      };
}
const pad = $("joystick");
function steer(event) {
  const r = pad.getBoundingClientRect(),
    dx = (event.clientX - r.left - r.width / 2) / 36,
    dy = (event.clientY - r.top - r.height / 2) / 36,
    length = Math.max(1, Math.hypot(dx, dy));
  joystick = { x: dx / length, y: dy / length };
  $("stick").style.transform =
    `translate(${joystick.x * 30}px,${joystick.y * 30}px)`;
}
pad.onpointerdown = (e) => {
  e.preventDefault();
  pad.setPointerCapture(e.pointerId);
  steer(e);
};
pad.onpointermove = (e) => {
  if (pad.hasPointerCapture(e.pointerId)) steer(e);
};
pad.onpointerup =
  pad.onpointercancel =
  pad.onlostpointercapture =
    () => {
      joystick = { x: 0, y: 0 };
      $("stick").style.transform = "";
    };
canvas.oncontextmenu = (e) => e.preventDefault();
canvas.onpointerdown = (e) => {
  if (!id) return;
  if (e.pointerType === "touch" || e.pointerType === "pen") {
    pointerLook = { id: e.pointerId, x: e.clientX, y: e.clientY };
    canvas.setPointerCapture(e.pointerId);
    return;
  }
  if (document.pointerLockElement === canvas)
    changeBlock(e.button === 2 ? 1 : 0);
  else {
    const result = canvas.requestPointerLock?.();
    result?.catch?.(() => toast("Drag the view to look around."));
    pointerLook = { id: e.pointerId, x: e.clientX, y: e.clientY };
    canvas.setPointerCapture(e.pointerId);
  }
};
canvas.onpointermove = (e) => {
  if (!id) return;
  if (document.pointerLockElement === canvas) {
    yaw -= e.movementX * 0.0025;
    pitch = Math.max(-1.48, Math.min(1.48, pitch - e.movementY * 0.0025));
  } else if (pointerLook?.id === e.pointerId) {
    yaw -= (e.clientX - pointerLook.x) * 0.005;
    pitch = Math.max(
      -1.48,
      Math.min(1.48, pitch - (e.clientY - pointerLook.y) * 0.005),
    );
    pointerLook.x = e.clientX;
    pointerLook.y = e.clientY;
  }
};
canvas.onpointerup = canvas.onpointercancel = () => {
  pointerLook = null;
};
document.addEventListener("pointerlockchange", () => {
  $("play-hint").hidden =
    document.pointerLockElement === canvas ||
    !id ||
    matchMedia("(pointer:coarse)").matches;
});
addEventListener("keydown", (e) => {
  if (!id || ["INPUT", "SELECT", "TEXTAREA"].includes(e.target.tagName)) return;
  if (
    [
      "KeyW",
      "KeyA",
      "KeyS",
      "KeyD",
      "ArrowUp",
      "ArrowDown",
      "ArrowLeft",
      "ArrowRight",
      "Space",
      "ShiftLeft",
      "ShiftRight",
    ].includes(e.code)
  ) {
    keys.add(e.code);
    e.preventDefault();
  }
  if (!e.repeat) {
    if (e.code === "KeyF") setFly(!fly);
    if (e.code === "KeyE") changeBlock(1);
    if (e.code === "KeyQ") changeBlock(0);
    if (/^Digit\d$/.test(e.code)) choose(Number(e.code.slice(-1)) || 10);
  }
});
addEventListener("keyup", (e) => keys.delete(e.code));
addEventListener("blur", () => {
  keys.clear();
  held.jump = held.up = held.down = false;
  joystick = { x: 0, y: 0 };
});
function peers(now) {
  if (!frame) return [];
  const age = (now - received) / 1000,
    duration = tickSpan * 0.1,
    blend = Math.min(1, age / 0.1),
    result = [];
  for (const p of frame.players) {
    if (p.id === id) continue;
    const old = previous.get(p.id) || p;
    const extra = Math.max(0, Math.min(0.15, age - 0.1)),
      dx = p.x - old.x,
      dy = p.y - old.y,
      dz = p.z - old.z,
      length = Math.hypot(dx, dz),
      scale = length ? Math.min(extra / duration, (7 * extra) / length) : 0;
    result.push({
      ...p,
      x: old.x + (p.x - old.x) * blend + dx * scale,
      y:
        old.y +
        dy * blend +
        Math.max(-24 * extra, Math.min(8 * extra, (dy * extra) / duration)),
      z: old.z + (p.z - old.z) * blend + dz * scale,
    });
  }
  return result;
}
function drawSoon() {
  if (renderer && !animation && !document.hidden)
    animation = requestAnimationFrame(draw);
}
function draw(now) {
  animation = 0;
  const dt = lastDraw ? Math.min(0.05, (now - lastDraw) / 1000) : 0;
  lastDraw = now;
  if (id && ready) {
    local = predict(kernel, get, local, input(), dt);
    const fade = Math.exp(-dt * 12);
    for (const axis of ["x", "y", "z"]) cameraError[axis] *= fade;
    updateChunks();
  }
  const camera = {
    x: local.x + cameraError.x,
    y: local.y + cameraError.y,
    z: local.z + cameraError.z,
  };
  renderer.draw(
    camera,
    yaw,
    pitch,
    peers(now),
    id && ready ? ray(get, local, yaw, pitch) : null,
    $("light").value,
  );
  canvas.dataset.x = local.x.toFixed(3);
  canvas.dataset.y = local.y.toFixed(3);
  canvas.dataset.z = local.z.toFixed(3);
  canvas.dataset.yaw = yaw.toFixed(4);
  canvas.dataset.pitch = pitch.toFixed(4);
  canvas.dataset.chunks = chunks.size;
  frames++;
  if (now - lastHud > 500) {
    $("latency").textContent = rtt ? rtt.toFixed(0) : "—";
    $("render-stats").textContent =
      `${chunks.size} chunks · ${renderer.triangles.toLocaleString()} terrain triangles · ${pending.size} unconfirmed edits`;
    if (now - lastMeasure >= 1000) {
      const fps = (frames * 1000) / (now - lastMeasure);
      $("fps").textContent = id ? fps.toFixed(0) : "—";
      if (id && fps < 35 && renderer.scale > 0.7) {
        renderer.scale = Math.max(0.7, renderer.scale - 0.1);
        renderer.resize();
      }
      frames = 0;
      lastMeasure = now;
    }
    lastHud = now;
  }
  if (id) drawSoon();
}
document.addEventListener("visibilitychange", () => {
  keys.clear();
  held.jump = held.up = held.down = false;
  if (document.hidden) {
    cancelAnimationFrame(animation);
    animation = 0;
  } else {
    lastDraw = 0;
    lastSend = performance.now();
    drawSoon();
  }
});
addEventListener("resize", () => {
  renderer?.resize();
  drawSoon();
});
canvas.addEventListener("rendererror", (e) => fatal(e.detail));
function fatal(message) {
  $("welcome").hidden = false;
  $("loading").textContent = message;
  $("play").disabled = true;
  toast(message);
}
async function start() {
  try {
    renderer = new Renderer(canvas);
    const response = await fetch("/blockyard/kernel.wasm");
    if (!response.ok) throw Error("The Fern terrain kernel is unavailable.");
    const bytes = await response.arrayBuffer();
    kernel = await instantiate(bytes);
    worker = new Worker(new URL("./mesh-worker.mjs", import.meta.url), {
      type: "module",
    });
    worker.onmessage = onWorker;
    worker.onerror = (e) => fatal(e.message);
    worker.postMessage({ kind: "init", bytes }, [bytes]);
    $("loading").textContent = "Grow the nearby terrain…";
    drawSoon();
    const info = await json("/blockyard/worlds");
    $("world").replaceChildren(
      ...info.worlds.map((entry) => {
        const option = document.createElement("option");
        option.value = entry.name;
        option.textContent = entry.name[0].toUpperCase() + entry.name.slice(1);
        option.selected = entry.name === world;
        return option;
      }),
    );
    $("storage").textContent = info.persistent
      ? "Saved world. Edits survive a server process restart."
      : "Temporary world. Edits reset when this server restarts.";
  } catch (error) {
    fatal(error.message);
  }
}
start();
