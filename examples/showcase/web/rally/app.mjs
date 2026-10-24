import { mountFullscreen } from "/assets/ui.mjs";
import { $, status, toast, copy } from "/assets/ui.mjs";
import { paddle, reconcile, ball } from "./prediction.mjs";

const canvas = $("court"),
  context = canvas.getContext("2d");
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;
let socket,
  frame,
  receivedAt = 0,
  target = 0.5,
  localPaddle = 0.5,
  sequence = 0,
  rtt = 0,
  lastSent = 0,
  lastDraw = 0,
  mode = "solo",
  inviteAfterJoin = false;
const sent = new Map(),
  inputHistory = [];
function transmit(kind, room = frame?.room || "") {
  if (socket?.readyState !== WebSocket.OPEN || socket.bufferedAmount > 16384)
    return;
  const now = performance.now(),
    seq = ++sequence;
  sent.set(seq, now);
  inputHistory.push({
    seq,
    y: target,
    dt: Math.min(0.05, Math.max(0, (now - lastSent) / 1000)),
  });
  while (sent.size > 128) sent.delete(sent.keys().next().value);
  if (inputHistory.length > 128) inputHistory.shift();
  lastSent = now;
  socket.send(JSON.stringify({ kind, room, mode, y: target, seq }));
}
const keys = new Set(),
  trail = [];
function resize() {
  const rect = canvas.getBoundingClientRect(),
    ratio = Math.min(devicePixelRatio || 1, 2);
  canvas.width = Math.round(rect.width * ratio);
  canvas.height = Math.round(rect.height * ratio);
}
resize();
addEventListener("resize", resize);
function connect(room = "", newMode = "solo") {
  if (socket) {
    socket.onclose = socket.onmessage = socket.onopen = null;
    socket.close();
  }
  frame = null;
  sent.clear();
  inputHistory.length = 0;
  localPaddle = 0.5;
  rtt = 0;
  sequence = 0;
  lastSent = performance.now();
  target = 0.5;
  mode = newMode;
  status("connection", "Connecting to the court");
  const url = new URL("/rally/ws", location.origin);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const current = new WebSocket(url);
  socket = current;
  current.onopen = () => {
    if (socket !== current) return;
    transmit("join", room);
  };
  current.onmessage = ({ data }) => {
    if (socket !== current) return;
    let value;
    try {
      value = JSON.parse(data);
    } catch {
      status("connection", "Invalid server response", false, true);
      current.close();
      return;
    }
    if (value.error) {
      status("connection", value.error, false, true);
      toast(value.error);
      return;
    }
    const now = performance.now();
    if (sent.has(value.seq)) {
      const sample = now - sent.get(value.seq);
      rtt = rtt ? rtt * 0.8 + sample * 0.2 : sample;
      $("latency").textContent = rtt.toFixed(1);
      $("server-time").textContent = (value.server_us / 1000).toFixed(3);
      for (const seq of sent.keys()) if (seq <= value.seq) sent.delete(seq);
    }
    localPaddle = reconcile(
      value.game[value.seat === 0 ? "left" : "right"],
      inputHistory,
      value.seq,
    );
    while (inputHistory.length && inputHistory[0].seq <= value.seq)
      inputHistory.shift();
    frame = value;
    receivedAt = now;
    $("seat").textContent = value.seat === 0 ? "LEFT" : "RIGHT";
    $("score-left").textContent = value.game.left_score;
    $("score-right").textContent = value.game.right_score;
    status(
      "connection",
      value.game.solo
        ? "Live · you vs Fern"
        : `Live · ${value.players}/2 players`,
      true,
    );
    $("opponent").textContent = value.game.solo
      ? "You control the left paddle. Fern controls the right."
      : "Share the room link with another player. Each player controls one paddle.";
    $("court-message").hidden = value.game.running;
    if (!value.game.running) {
      document.querySelector("#court-message strong").textContent =
        !value.game.solo && value.players < 2
          ? "Waiting for another player."
          : "Ready to play.";
      document.querySelector("#court-message span").textContent =
        !value.game.solo && value.players < 2
          ? "Share the room link, then press Start."
          : "Press Start rally. Move with ↑ ↓, W S, or drag on the court.";
    }
    if (!value.game.solo) {
      history.replaceState(null, "", `#${value.room}`);
      $("invite").hidden = false;
      const link = `${location.origin}/rally/#${value.room}`;
      $("invite-link").href = link;
      $("invite-link").textContent = `Room ${value.room.slice(0, 8)} ↗`;
      if (inviteAfterJoin) {
        inviteAfterJoin = false;
        copy(link);
      }
    } else {
      $("invite").hidden = true;
      history.replaceState(null, "", location.pathname);
    }
  };
  current.onclose = () => {
    if (socket === current) {
      status(
        "connection",
        "Court disconnected · choose a new game",
        false,
        true,
      );
    }
  };
  current.onerror = () =>
    status("connection", "Connection interrupted", false, true);
}
function command(kind) {
  if (!frame || socket?.readyState !== WebSocket.OPEN) {
    toast("Connect to a court first.");
    return;
  }
  transmit(kind);
}
$("start").onclick = () => command("start");
$("pause").onclick = () => command("pause");
$("reset").onclick = () => command("reset");
$("solo").onclick = () => connect("", "solo");
$("duo").onclick = () => {
  inviteAfterJoin = true;
  connect("", "duo");
};
$("copy-room").onclick = () => copy($("invite-link").href);
addEventListener("keydown", (event) => {
  if (["ArrowUp", "ArrowDown", "KeyW", "KeyS"].includes(event.code)) {
    keys.add(event.code);
    event.preventDefault();
  }
});
addEventListener("keyup", (event) => keys.delete(event.code));
addEventListener("blur", () => keys.clear());
function point(event) {
  const rect = canvas.getBoundingClientRect();
  const height = Math.min(rect.height, (rect.width * 9) / 16),
    top = rect.top + (rect.height - height) / 2;
  target = Math.max(0, Math.min(1, (event.clientY - top) / height));
}
canvas.onpointerdown = (event) => {
  canvas.setPointerCapture(event.pointerId);
  point(event);
};
canvas.onpointermove = (event) => {
  if (event.buttons || event.pointerType === "mouse") point(event);
};
// Input is coalesced per frame. A slow reply never blocks the next input.
const timer = setInterval(() => {
  if (document.hidden || !frame || socket?.readyState !== WebSocket.OPEN)
    return;
  if (sent.size && performance.now() - sent.values().next().value > 5000) {
    socket.close();
    return;
  }
  transmit("tick");
}, 16);
function draw(now) {
  const dt = lastDraw ? Math.min(0.05, (now - lastDraw) / 1000) : 0;
  lastDraw = now;
  if (keys.has("ArrowUp") || keys.has("KeyW"))
    target = Math.max(0, target - dt * 1.6);
  if (keys.has("ArrowDown") || keys.has("KeyS"))
    target = Math.min(1, target + dt * 1.6);
  localPaddle = paddle(localPaddle, target, dt);
  canvas.dataset.paddle = localPaddle.toFixed(4);
  context.setTransform(1, 0, 0, 1, 0, 0);
  context.clearRect(0, 0, canvas.width, canvas.height);
  const scale = Math.min(canvas.width / 960, canvas.height / 540);
  context.setTransform(
    scale,
    0,
    0,
    scale,
    (canvas.width - 960 * scale) / 2,
    (canvas.height - 540 * scale) / 2,
  );
  context.strokeStyle = "#abc79230";
  context.lineWidth = 1;
  context.setLineDash([5, 12]);
  context.beginPath();
  context.moveTo(480, 20);
  context.lineTo(480, 520);
  context.stroke();
  context.setLineDash([]);
  context.beginPath();
  context.arc(480, 270, 65, 0, Math.PI * 2);
  context.stroke();
  context.font = "italic 25px Georgia";
  context.fillStyle = "#b4cf981f";
  context.textAlign = "center";
  context.fillText("fern", 480, 278);
  const game = frame?.game || {
    x: 0.5,
    y: 0.5,
    left: 0.5,
    right: 0.5,
    running: false,
  };
  const predicted = ball(game, (now - receivedAt + rtt / 2) / 1000);
  const x = predicted.x * 960,
    y = predicted.y * 540;
  const left = frame?.seat === 0 ? localPaddle : game.left;
  const right = frame?.seat === 1 ? localPaddle : game.right;
  if (!reducedMotion && game.running) {
    trail.push({ x, y });
    if (trail.length > 9) trail.shift();
  } else trail.length = 0;
  trail.forEach((point, i) => {
    context.fillStyle = `rgba(209,232,167,${i / 90})`;
    context.beginPath();
    context.arc(point.x, point.y, 6, 0, Math.PI * 2);
    context.fill();
  });
  context.fillStyle = "#dae8ac";
  context.beginPath();
  context.roundRect(51, left * 540 - 56, 10, 112, 5);
  context.roundRect(899, right * 540 - 56, 10, 112, 5);
  context.fill();
  context.fillStyle = "#f4f3d6";
  context.beginPath();
  context.arc(x, y, 8, 0, Math.PI * 2);
  context.fill();
  requestAnimationFrame(draw);
}
requestAnimationFrame(draw);
const initialRoom = location.hash.slice(1);
connect(
  /^[a-z0-9_-]{1,32}$/.test(initialRoom) ? initialRoom : "",
  initialRoom ? "duo" : "solo",
);
addEventListener("pagehide", () => {
  clearInterval(timer);
  socket?.close();
});

mountFullscreen(document.querySelector(".game-layout"));

new ResizeObserver(resize).observe(canvas);
