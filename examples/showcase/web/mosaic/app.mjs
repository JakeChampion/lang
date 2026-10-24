import { mountFullscreen } from "/assets/ui.mjs";
import { $, status, toast, copy } from "/assets/ui.mjs";

const canvas = $("board"),
  context = canvas.getContext("2d"),
  cell = 16;
const requested = new URLSearchParams(location.search).get("room") || "commons";
const room = /^[a-z0-9_-]{1,32}$/.test(requested) ? requested : "commons";
const names = [
  "Paper",
  "Forest",
  "Fern",
  "Meadow",
  "Sage",
  "Sunflower",
  "Apricot",
  "Coral",
  "Heather",
  "Blue",
  "Sky",
  "Ink",
];
let board,
  selected = 1,
  socket,
  reconnect,
  pingTimer,
  drawing = false,
  lastPoint,
  sequence = 0,
  sendQueued = false,
  cursor = { x: 32, y: 20 },
  drawQueued = false;
const outbox = [],
  optimistic = new Map(),
  awaiting = [],
  pending = new Map(),
  pings = new Map();
$("room-label").textContent = `ROOM / ${room.toUpperCase()}`;
$("export").href = `/mosaic/export.png?room=${room}`;
function redraw() {
  if (drawQueued) return;
  drawQueued = true;
  requestAnimationFrame(() => {
    drawQueued = false;
    if (!board) return;
    board.pixels.forEach((color, index) => {
      const chosen = optimistic.get(index)?.color ?? color;
      context.fillStyle = `#${board.palette[chosen].toString(16).padStart(6, "0")}`;
      context.fillRect(
        (index % 64) * cell,
        Math.floor(index / 64) * cell,
        cell,
        cell,
      );
    });
    if ($("grid").checked) {
      context.strokeStyle = "#183b3020";
      context.lineWidth = 1;
      context.beginPath();
      for (let x = 0; x <= 64; x++) {
        context.moveTo(x * cell + 0.5, 0);
        context.lineTo(x * cell + 0.5, 640);
      }
      for (let y = 0; y <= 40; y++) {
        context.moveTo(0, y * cell + 0.5);
        context.lineTo(1024, y * cell + 0.5);
      }
      context.stroke();
    }
    if (document.activeElement === canvas) {
      context.strokeStyle = "#fff";
      context.lineWidth = 3;
      context.strokeRect(
        cursor.x * cell + 2,
        cursor.y * cell + 2,
        cell - 4,
        cell - 4,
      );
      context.strokeStyle = "#183b30";
      context.lineWidth = 1;
      context.strokeRect(
        cursor.x * cell + 2,
        cursor.y * cell + 2,
        cell - 4,
        cell - 4,
      );
    }
    $("revision").textContent = `REVISION / ${board.revision}`;
  });
}
function settle() {
  for (let i = awaiting.length - 1; i >= 0; i--) {
    const item = awaiting[i];
    if (board.revision < item.revision) continue;
    for (const point of item.points)
      if (optimistic.get(point)?.sequence === item.sequence)
        optimistic.delete(point);
    awaiting.splice(i, 1);
  }
  $("sync").textContent =
    outbox.length || pending.size || awaiting.length
      ? "Syncing your pixels…"
      : "All pixels in sync.";
  redraw();
}
function send() {
  if (sendQueued) return;
  sendQueued = true;
  requestAnimationFrame(() => {
    sendQueued = false;
    if (socket?.readyState !== WebSocket.OPEN) return;
    while (
      outbox.length &&
      socket.bufferedAmount < 16384 &&
      pending.size < 128
    ) {
      const item = outbox.shift();
      pending.set(item.sequence, { ...item, sentAt: performance.now() });
      socket.send(
        JSON.stringify({
          kind: "paint",
          seq: item.sequence,
          color: item.color,
          points: item.points,
        }),
      );
    }
    settle();
    if (outbox.length) send();
  });
}
function paint(points) {
  if (!board || socket?.readyState !== WebSocket.OPEN) return;
  if (outbox.length + pending.size > 128) {
    toast("Connection busy. Wait for your strokes to sync.");
    return;
  }
  for (let start = 0; start < points.length; start += 64) {
    const batch = [...new Set(points.slice(start, start + 64))],
      id = ++sequence;
    for (const point of batch)
      optimistic.set(point, { color: selected, sequence: id });
    outbox.push({ color: selected, points: batch, sequence: id });
  }
  redraw();
  send();
}
function segment(a, b) {
  const points = [],
    count = Math.max(Math.abs(b.x - a.x), Math.abs(b.y - a.y));
  for (let i = 0; i <= count; i++) {
    const fraction = count ? i / count : 0;
    points.push(
      Math.round(a.x + (b.x - a.x) * fraction) +
        Math.round(a.y + (b.y - a.y) * fraction) * 64,
    );
  }
  return points;
}
function point(event) {
  const bounds = canvas.getBoundingClientRect(),
    scale = Math.min(bounds.width / 64, bounds.height / 40);
  const rect = {
    left: bounds.left + (bounds.width - 64 * scale) / 2,
    top: bounds.top + (bounds.height - 40 * scale) / 2,
    width: 64 * scale,
    height: 40 * scale,
  };
  return {
    x: Math.max(
      0,
      Math.min(63, Math.floor(((event.clientX - rect.left) / rect.width) * 64)),
    ),
    y: Math.max(
      0,
      Math.min(39, Math.floor(((event.clientY - rect.top) / rect.height) * 40)),
    ),
  };
}
canvas.onpointerdown = (event) => {
  drawing = true;
  canvas.setPointerCapture(event.pointerId);
  lastPoint = point(event);
  cursor = lastPoint;
  paint([cursor.y * 64 + cursor.x]);
};
canvas.onpointermove = (event) => {
  const current = point(event);
  $("coordinate").textContent =
    `${current.x.toString().padStart(2, "0")} / ${current.y.toString().padStart(2, "0")}`;
  if (drawing && (current.x !== lastPoint.x || current.y !== lastPoint.y)) {
    paint(segment(lastPoint, current));
    lastPoint = current;
    cursor = current;
  }
};
canvas.onpointerup = canvas.onpointercancel = () => {
  drawing = false;
};
canvas.onfocus = canvas.onblur = redraw;
canvas.onkeydown = (event) => {
  if (
    !["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", " ", "Enter"].includes(
      event.key,
    )
  )
    return;
  event.preventDefault();
  if (event.key === "ArrowLeft") cursor.x = Math.max(0, cursor.x - 1);
  if (event.key === "ArrowRight") cursor.x = Math.min(63, cursor.x + 1);
  if (event.key === "ArrowUp") cursor.y = Math.max(0, cursor.y - 1);
  if (event.key === "ArrowDown") cursor.y = Math.min(39, cursor.y + 1);
  if (event.key === " " || event.key === "Enter")
    paint([cursor.y * 64 + cursor.x]);
  $("coordinate").textContent = `${cursor.x} / ${cursor.y}`;
  redraw();
};
$("grid").onchange = redraw;
$("share").onclick = () => copy(`${location.origin}/mosaic/?room=${room}`);
$("new-room").onclick = () => {
  location.href = `/mosaic/?room=${crypto.randomUUID().replaceAll("-", "").slice(0, 12)}`;
};
function palette() {
  $("palette").replaceChildren();
  board.palette.forEach((color, i) => {
    const button = document.createElement("button");
    button.className = "swatch";
    button.style.background = `#${color.toString(16).padStart(6, "0")}`;
    button.title = names[i];
    button.setAttribute("aria-label", names[i]);
    button.setAttribute("aria-pressed", String(selected === i));
    button.onclick = () => {
      selected = i;
      for (const [index, swatch] of [...$("palette").children].entries())
        swatch.setAttribute("aria-pressed", String(index === i));
    };
    $("palette").append(button);
  });
  redraw();
}
function connect() {
  const url = new URL(`/mosaic/ws?room=${room}`, location.origin);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  const current = new WebSocket(url);
  socket = current;
  current.onmessage = ({ data }) => {
    if (socket !== current) return;
    let message;
    try {
      message = JSON.parse(data);
    } catch {
      current.close();
      return;
    }
    if (message.kind === "snapshot") {
      board = message.data;
      palette();
      status("connection", "Live · WebSocket · immediate room updates", true);
      settle();
    } else if (message.kind === "paint") {
      const change = message.data;
      if (!board || change.revision <= board.revision) return;
      for (const point of change.points) board.pixels[point] = change.color;
      board.revision = change.revision;
      settle();
    } else if (message.kind === "ack") {
      const item = pending.get(message.seq);
      if (!item) return;
      pending.delete(message.seq);
      awaiting.push({ ...item, revision: message.revision });
      $("latency").textContent = (performance.now() - item.sentAt).toFixed(1);
      $("server-time").textContent = (message.server_us / 1000).toFixed(3);
      settle();
      send();
    } else if (message.kind === "pong") {
      const at = pings.get(message.seq);
      if (at !== undefined)
        $("latency").textContent = (performance.now() - at).toFixed(1);
      pings.delete(message.seq);
    } else if (message.kind === "error") {
      const item = pending.get(message.seq);
      if (item)
        for (const point of item.points)
          if (optimistic.get(point)?.sequence === item.sequence)
            optimistic.delete(point);
      pending.delete(message.seq);
      toast(message.message);
      settle();
      send();
    }
  };
  current.onclose = () => {
    if (socket !== current) return;
    const lost = pending.size + outbox.length;
    outbox.length = awaiting.length = 0;
    pending.clear();
    pings.clear();
    optimistic.clear();
    if (lost)
      toast(
        "Connection lost. Some strokes have no confirmation. The next snapshot shows the saved board.",
      );
    redraw();
    status("connection", "Reconnecting to the shared canvas");
    reconnect = setTimeout(connect, 1000);
  };
  current.onerror = () => current.close();
}
connect();
pingTimer = setInterval(() => {
  if (document.hidden || socket?.readyState !== WebSocket.OPEN || !board)
    return;
  if (pings.size > 2) {
    socket.close();
    return;
  }
  const seq = ++sequence;
  pings.set(seq, performance.now());
  socket.send(JSON.stringify({ kind: "ping", seq, color: 0, points: [] }));
}, 5000);
addEventListener("pagehide", () => {
  clearTimeout(reconnect);
  clearInterval(pingTimer);
  if (socket) {
    socket.onclose = null;
    socket.close();
  }
});

mountFullscreen(document.querySelector(".mosaic-layout"));
