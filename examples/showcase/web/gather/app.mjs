import { $, status, toast, copy, mountFullscreen, json } from "/assets/ui.mjs";
import { destination, decode, move, reconcile } from "./protocol.mjs";
const canvas = $("arena"),
  context = canvas.getContext("2d"),
  keys = new Set(),
  sent = new Map(),
  history = [];
let animation = 0;
let socket,
  id = 0,
  frame,
  previous = new Map(),
  received = 0,
  lastDraw = 0,
  lastSent = 0,
  lastHud = 0,
  seq = 0,
  rtt = 0,
  local,
  target = { x: 512, y: 320 },
  snapshots = 0,
  measured = performance.now();
function resize() {
  const rect = canvas.getBoundingClientRect(),
    ratio = Math.min(devicePixelRatio || 1, 2);
  canvas.width = Math.round(rect.width * ratio);
  canvas.height = Math.round(rect.height * ratio);
  requestDraw();
}
new ResizeObserver(resize).observe(canvas);
addEventListener("resize", resize);
resize();
function point(event, snap = false) {
  const r = canvas.getBoundingClientRect(),
    scale = Math.min(r.width / 1024, r.height / 640);
  target = {
    x: Math.max(
      0,
      Math.min(
        1024,
        (event.clientX - r.left - (r.width - 1024 * scale) / 2) / scale,
      ),
    ),
    y: Math.max(
      0,
      Math.min(
        640,
        (event.clientY - r.top - (r.height - 640 * scale) / 2) / scale,
      ),
    ),
  };
  if (snap && event.pointerType === "touch" && frame) {
    let nearest,
      distance = (24 / scale) ** 2;
    for (const seed of frame.seeds)
      if (seed.active) {
        const squared = (target.x - seed.x) ** 2 + (target.y - seed.y) ** 2;
        if (squared < distance) {
          distance = squared;
          nearest = seed;
        }
      }
    if (nearest) target = { x: nearest.x, y: nearest.y };
  }
}
canvas.onpointerdown = (event) => {
  canvas.setPointerCapture(event.pointerId);
  point(event, true);
};
canvas.onpointermove = (event) => {
  if (event.buttons) point(event);
};
addEventListener("keydown", (event) => {
  if (!id || ["INPUT", "SELECT", "TEXTAREA"].includes(event.target.tagName))
    return;
  if (
    [
      "ArrowUp",
      "ArrowDown",
      "ArrowLeft",
      "ArrowRight",
      "KeyW",
      "KeyA",
      "KeyS",
      "KeyD",
    ].includes(event.code)
  ) {
    keys.add(event.code);
    event.preventDefault();
  }
});
addEventListener("keyup", (event) => {
  keys.delete(event.code);
  if (!keys.size && local) target = { ...local };
});
addEventListener("blur", () => {
  keys.clear();
  if (local) target = { ...local };
});
function connect() {
  if (socket?.readyState === WebSocket.OPEN) {
    socket.close();
    return;
  }
  $("join").disabled = true;
  status("connection", "Connect to the arena");
  const url = new URL("/gather/ws", location.origin);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const current = new WebSocket(url);
  socket = current;
  current.binaryType = "arraybuffer";
  current.onmessage = ({ data }) => {
    if (socket !== current) return;
    try {
      if (typeof data === "string") {
        const welcome = JSON.parse(data);
        id = welcome.id;
        seq = 0;
        sent.clear();
        history.length = 0;
        local = null;
        $("join").disabled = false;
        $("join").textContent = "Leave arena";
        lastSent = performance.now();
        requestDraw();
        status("connection", `Connected · server ${welcome.instance}`, true);
        return;
      }
      const value = decode(data),
        now = performance.now();
      if (value.kind === "ack") {
        if (sent.has(value.seq)) {
          const sample = now - sent.get(value.seq);
          rtt = rtt ? rtt * 0.8 + sample * 0.2 : sample;
          $("latency").textContent = rtt.toFixed(1);
          $("server-time").textContent = (value.serverUs / 1000).toFixed(3);
          sent.delete(value.seq);
        }
        return;
      }
      if (frame && value.tick <= frame.tick) return;
      previous = new Map(
        (frame?.players || value.players).map((p) => [p.id, p]),
      );
      frame = value;
      received = now;
      snapshots++;
      const own = frame.players.find((p) => p.id === id);
      if (own) {
        if (!local) {
          local = { x: own.x, y: own.y };
          target = { ...local };
        } else
          local = frame.paused
            ? { x: own.x, y: own.y }
            : reconcile(own, history);
        if (frame.paused) history.length = 0;
        while (history.length && history[0].seq <= own.seq) history.shift();
        $("personal").textContent = own.score;
      }
      $("players").textContent = `${frame.players.length} / 512`;
      $("score").textContent = `${frame.score} / ${frame.goal}`;
      $("round").textContent = frame.paused
        ? "NEXT ROUND"
        : `ROUND ${frame.round}`;
      $("arena-message").hidden = !frame.paused;
      if (frame.paused) {
        $("arena-message").querySelector("strong").textContent =
          frame.score >= frame.goal ? "Team goal reached." : "Round complete.";
        $("arena-message").querySelector("span").textContent =
          `The team collected ${frame.score} seeds. The next round starts in ten seconds.`;
      }
      const leaders = [...frame.players]
        .sort((a, b) => b.score - a.score || a.id - b.id)
        .slice(0, 5);
      $("leaders").replaceChildren(
        ...leaders.map((player) => {
          const li = document.createElement("li"),
            name = document.createElement("span"),
            score = document.createElement("b");
          name.textContent =
            player.id === id ? `You · player ${id}` : `Player ${player.id}`;
          score.textContent = player.score;
          li.append(name, score);
          return li;
        }),
      );
      if (now - measured >= 1000) {
        $("rate").textContent = ((snapshots * 1000) / (now - measured)).toFixed(
          1,
        );
        snapshots = 0;
        measured = now;
      }
    } catch (error) {
      toast(error.message);
      current.close(1002, "invalid arena response");
    }
  };
  current.onclose = () => {
    if (socket !== current) return;
    id = 0;
    frame = null;
    local = null;
    sent.clear();
    history.length = 0;
    $("join").disabled = false;
    $("join").textContent = "Join arena →";
    $("arena-message").hidden = false;
    $("arena-message").querySelector("strong").textContent =
      "Join the arena to play.";
    $("arena-message").querySelector("span").textContent =
      "Touch a destination or drag to steer. Collect the gold seeds.";
    status("connection", "Disconnected · select Join to return");
    refreshCount();
  };
  current.onerror = () =>
    status(
      "connection",
      "Could not join. Try again when a seat is available.",
      false,
      true,
    );
}
async function refreshCount() {
  try {
    const value = await json("/gather/status");
    if (!id) $("players").textContent = `${value.players} / ${value.capacity}`;
  } catch {}
}
$("join").onclick = connect;
$("share").onclick = () => copy(`${location.origin}/gather/`);
const timer = setInterval(() => {
  if (
    !id ||
    !local ||
    frame?.paused ||
    document.hidden ||
    socket?.readyState !== WebSocket.OPEN ||
    socket.bufferedAmount > 16384
  )
    return;
  const now = performance.now(),
    number = ++seq;
  sent.set(number, now);
  if (sent.size > 128) sent.delete(sent.keys().next().value);
  history.push({
    seq: number,
    ...target,
    dt: Math.min(0.1, (now - lastSent) / 1000),
  });
  if (history.length > 128) history.shift();
  lastSent = now;
  socket.send(destination(number, target.x, target.y));
}, 50);
function requestDraw() {
  if (!animation && !document.hidden) animation = requestAnimationFrame(draw);
}
function draw(now) {
  animation = 0;
  const dt = lastDraw ? Math.min(0.05, (now - lastDraw) / 1000) : 0;
  lastDraw = now;
  context.setTransform(1, 0, 0, 1, 0, 0);
  context.clearRect(0, 0, canvas.width, canvas.height);
  const scale = Math.min(canvas.width / 1024, canvas.height / 640);
  context.setTransform(
    scale,
    0,
    0,
    scale,
    (canvas.width - 1024 * scale) / 2,
    (canvas.height - 640 * scale) / 2,
  );
  const cssScale = scale / Math.min(devicePixelRatio || 1, 2);
  context.strokeStyle = "#285145";
  context.lineWidth = 0.7;
  context.beginPath();
  for (let x = 0; x <= 1024; x += 128) {
    context.moveTo(x, 0);
    context.lineTo(x, 640);
  }
  for (let y = 0; y <= 640; y += 80) {
    context.moveTo(0, y);
    context.lineTo(1024, y);
  }
  context.stroke();
  if (local && !frame?.paused) {
    let dx =
      (keys.has("ArrowRight") || keys.has("KeyD") ? 1 : 0) -
      (keys.has("ArrowLeft") || keys.has("KeyA") ? 1 : 0);
    let dy =
      (keys.has("ArrowDown") || keys.has("KeyS") ? 1 : 0) -
      (keys.has("ArrowUp") || keys.has("KeyW") ? 1 : 0);
    if (dx || dy)
      target = {
        x: Math.max(0, Math.min(1024, local.x + dx * 100)),
        y: Math.max(0, Math.min(640, local.y + dy * 100)),
      };
    local = move(local, target, dt);
    canvas.dataset.x = local.x.toFixed(2);
    canvas.dataset.y = local.y.toFixed(2);
  }
  if (frame) {
    context.fillStyle = "#e7c76c";
    for (const seed of frame.seeds)
      if (seed.active) {
        context.beginPath();
        context.arc(seed.x, seed.y, Math.max(4, 2 / cssScale), 0, Math.PI * 2);
        context.fill();
      }
    const age = Math.min(0.15, Math.max(0, (now - received + rtt / 2) / 1000));
    for (const player of frame.players) {
      const old = previous.get(player.id) || player;
      const x =
        player.id === id && local
          ? local.x
          : Math.max(
              0,
              Math.min(1024, player.x + ((player.x - old.x) * age) / 0.1),
            );
      const y =
        player.id === id && local
          ? local.y
          : Math.max(
              0,
              Math.min(640, player.y + ((player.y - old.y) * age) / 0.1),
            );
      context.fillStyle = `hsl(${(player.id * 137.508) % 360} 55% 65%)`;
      context.beginPath();
      context.arc(
        x,
        y,
        player.id === id
          ? Math.max(7, 7 / cssScale)
          : Math.max(5, 3 / cssScale),
        0,
        Math.PI * 2,
      );
      context.fill();
      if (player.id === id) {
        context.strokeStyle = "#fff";
        context.lineWidth = 2;
        context.stroke();
        context.font = `${Math.max(11, 11 / cssScale)}px monospace`;
        context.textAlign = "center";
        context.fillStyle = "#fff";
        context.fillText("YOU", x, y - Math.max(14, 14 / cssScale));
      }
    }
    if (local) {
      context.strokeStyle = "#ffffff65";
      context.beginPath();
      context.arc(
        target.x,
        target.y,
        Math.max(8, 5 / cssScale),
        0,
        Math.PI * 2,
      );
      context.stroke();
    }
    if (now - lastHud > 200) {
      const seconds = Math.ceil(
        Math.max(0, frame.remainingMs - now + received) / 1000,
      );
      $("time").textContent =
        `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
      lastHud = now;
    }
  }
  if (id && !document.hidden) animation = requestAnimationFrame(draw);
}
addEventListener("visibilitychange", () => {
  cancelAnimationFrame(animation);
  animation = 0;
  lastDraw = 0;
  if (!document.hidden) requestDraw();
});
addEventListener("pagehide", () => {
  cancelAnimationFrame(animation);
  clearInterval(timer);
  socket?.close();
});
mountFullscreen(document.querySelector(".gather-player"));
refreshCount();
requestDraw();
