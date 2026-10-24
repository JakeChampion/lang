import { $, status, toast, copy } from "/assets/ui.mjs";

const canvas = $("court"),
  context = canvas.getContext("2d");
const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)").matches;
let socket,
  frame,
  previous,
  receivedAt = 0,
  sentAt = 0,
  target = 0.5,
  inFlight = false,
  pending = "tick",
  mode = "solo",
  inviteAfterJoin = false;
const keys = new Set(),
  trail = [];
function resize() {
  const rect = canvas.getBoundingClientRect(),
    ratio = Math.min(devicePixelRatio || 1, 2);
  canvas.width = Math.round(rect.width * ratio);
  canvas.height = Math.round(((rect.width * 9) / 16) * ratio);
}
resize();
addEventListener("resize", resize);
function connect(room = "", newMode = "solo") {
  if (socket) {
    socket.onclose = socket.onmessage = socket.onopen = null;
    socket.close();
  }
  frame = null;
  previous = null;
  inFlight = false;
  pending = "tick";
  target = 0.5;
  mode = newMode;
  status("connection", "Connecting to the court");
  const url = new URL("/rally/ws", location.origin);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const current = new WebSocket(url);
  socket = current;
  current.onopen = () => {
    if (socket !== current) return;
    sentAt = performance.now();
    inFlight = true;
    current.send(JSON.stringify({ kind: "join", room, mode, y: target }));
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
    previous = frame?.game || value.game;
    frame = value;
    receivedAt = performance.now();
    $("latency").textContent = (receivedAt - sentAt).toFixed(1);
    inFlight = false;
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
      ? "You are playing Fern. The server moves the other paddle and checks every collision."
      : "A shared court. Send the invite link to your opponent. Each player controls one paddle.";
    $("court-message").hidden = value.game.running;
    if (!value.game.running) {
      document.querySelector("#court-message strong").textContent =
        !value.game.solo && value.players < 2
          ? "A court for two."
          : "Ready for a rally?";
      document.querySelector("#court-message span").textContent =
        !value.game.solo && value.players < 2
          ? "Share your invite link. Start when your friend arrives."
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
      inFlight = false;
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
  pending = kind;
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
  target = Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height));
}
canvas.onpointerdown = (event) => {
  canvas.setPointerCapture(event.pointerId);
  point(event);
};
canvas.onpointermove = (event) => {
  if (event.buttons || event.pointerType === "mouse") point(event);
};
const timer = setInterval(() => {
  if (document.hidden || !frame || socket?.readyState !== WebSocket.OPEN)
    return;
  if (keys.has("ArrowUp") || keys.has("KeyW"))
    target = Math.max(0, target - 0.04);
  if (keys.has("ArrowDown") || keys.has("KeyS"))
    target = Math.min(1, target + 0.04);
  if (inFlight) {
    if (performance.now() - sentAt > 5000) socket.close();
    return;
  }
  inFlight = true;
  sentAt = performance.now();
  socket.send(
    JSON.stringify({ kind: pending, room: frame.room, mode, y: target }),
  );
  pending = "tick";
}, 33);
function draw(now) {
  context.setTransform(canvas.width / 960, 0, 0, canvas.height / 540, 0, 0);
  context.clearRect(0, 0, 960, 540);
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
  const alpha = reducedMotion ? 1 : Math.min(1, (now - receivedAt) / 33);
  const lerp = (key) => {
    const old = previous?.[key] ?? game[key];
    return Math.abs(game[key] - old) > 0.3
      ? game[key]
      : old + (game[key] - old) * alpha;
  };
  const x = lerp("x") * 960,
    y = lerp("y") * 540;
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
  context.roundRect(51, lerp("left") * 540 - 56, 10, 112, 5);
  context.roundRect(899, lerp("right") * 540 - 56, 10, 112, 5);
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
