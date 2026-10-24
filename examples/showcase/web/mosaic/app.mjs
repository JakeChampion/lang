import { $, status, toast, copy, json } from "/assets/ui.mjs";

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
  source,
  drawing = false,
  lastPoint,
  sequence = 0,
  sending = false,
  cursor = { x: 32, y: 20 },
  drawQueued = false;
const outbox = [],
  optimistic = new Map(),
  awaiting = [];
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
    outbox.length || sending || awaiting.length
      ? "Syncing your pixels…"
      : "All pixels in sync.";
  redraw();
}
async function send() {
  if (sending || !outbox.length) return;
  sending = true;
  const item = outbox.shift();
  try {
    const result = await json(`/mosaic/paint?room=${room}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ color: item.color, points: item.points }),
    });
    awaiting.push({ ...item, revision: result.revision });
    settle();
  } catch (error) {
    for (const point of item.points)
      if (optimistic.get(point)?.sequence === item.sequence)
        optimistic.delete(point);
    toast(`Stroke not saved: ${error.message}`);
    redraw();
  } finally {
    sending = false;
    settle();
    send();
  }
}
function paint(points) {
  if (!board) return;
  if (outbox.length > 128) {
    toast("The canvas connection is slow. Let your strokes catch up.");
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
  const rect = canvas.getBoundingClientRect();
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
async function start() {
  board = await json(`/mosaic/state?room=${room}`);
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
  source = new EventSource(`/mosaic/events?room=${room}`);
  source.onopen = () =>
    status("connection", "Live · everyone sees the same canvas", true);
  source.onerror = () =>
    status("connection", "Reconnecting · your canvas stays here");
  source.addEventListener("snapshot", (event) => {
    const value = JSON.parse(event.data);
    if (value.revision < board.revision) {
      outbox.length = awaiting.length = 0;
      optimistic.clear();
      toast("This canvas started a new server session.");
    }
    board = value;
    settle();
  });
  source.addEventListener("paint", (event) => {
    const change = JSON.parse(event.data);
    if (change.revision <= board.revision) return;
    for (const point of change.points) board.pixels[point] = change.color;
    board.revision = change.revision;
    settle();
  });
}
start().catch((error) => {
  status("connection", error.message, false, true);
  toast(error.message);
});
addEventListener("pagehide", () => source?.close());
