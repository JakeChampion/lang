import { mountFullscreen } from "/assets/ui.mjs";
import { $, bytes, json, status, toast, fitCanvas } from "/assets/ui.mjs";

let source,
  pollTimer,
  watchdog,
  manualPause = false,
  polling = false,
  fetching = false,
  failures = 0;
const samples = [];
const compact = (value) =>
  value >= 10000
    ? new Intl.NumberFormat(undefined, {
        notation: "compact",
        maximumFractionDigits: 1,
      }).format(value)
    : number(value);
const number = (value) =>
  Number.isFinite(value) ? value.toLocaleString() : "—";
function percentile(values, fraction) {
  if (!values.length) return null;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.ceil(sorted.length * fraction) - 1];
}
function uptime(ms) {
  const seconds = Math.floor(ms / 1000);
  return seconds < 60
    ? `${seconds}s`
    : seconds < 3600
      ? `${Math.floor(seconds / 60)}m ${seconds % 60}s`
      : `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
function chart(id, key, format, fixedMax) {
  const canvas = $(id),
    context = fitCanvas(canvas),
    width = canvas.width,
    height = canvas.height;
  const values = samples.map((s) => s[key]).filter(Number.isFinite);
  context.clearRect(0, 0, width, height);
  context.strokeStyle = "#68865a30";
  context.lineWidth = 1;
  for (let row = 0; row <= 3; row++) {
    const y = (row * (height - 22)) / 3 + 8;
    context.beginPath();
    context.moveTo(0, y);
    context.lineTo(width, y);
    context.stroke();
  }
  context.font = `${Math.max(9, (width / canvas.getBoundingClientRect().width) * 9)}px monospace`;
  context.fillStyle = "#93a38d";
  if (!values.length) {
    context.fillText("No measurement available on this host.", 12, height / 2);
    return;
  }
  const maximum = Math.max(fixedMax || 1, ...values) * (fixedMax ? 1 : 1.15);
  context.fillText(format(maximum), 4, 12);
  context.strokeStyle = "#c7df94";
  context.lineWidth = 2;
  context.beginPath();
  let started = false,
    lastTime = 0;
  const end = samples.at(-1).at,
    start = Math.min(end - 59000, samples[0].at);
  for (const sample of samples) {
    if (!Number.isFinite(sample[key])) {
      started = false;
      continue;
    }
    const x = Math.max(
        0,
        ((sample.at - start) / Math.max(1, end - start)) * width,
      ),
      y = height - 10 - (sample[key] / maximum) * (height - 34);
    if (!started || sample.at - lastTime > 3000) context.moveTo(x, y);
    else context.lineTo(x, y);
    started = true;
    lastTime = sample.at;
  }
  context.stroke();
}
function charts() {
  chart("rss-chart", "rss", bytes);
  chart("cpu-chart", "cpu", (n) => `${n.toFixed(0)}%`, 100);
}
function receive(value) {
  clearTimeout(watchdog);
  failures = 0;
  const previous = samples.at(-1),
    at = value.at_ms;
  if (!previous || at > previous.at) {
    samples.push({
      at,
      rss: value.memory.rss,
      cpu: value.memory.cpu_percent,
      requests: value.requests,
    });
    if (samples.length > 60) samples.shift();
  }
  status(
    "connection",
    `${polling ? "HTTP polling" : "SSE"} · live from ${value.instance}`,
    true,
  );
  $("sample-time").textContent = new Date(at).toLocaleTimeString();
  $("rss").textContent = bytes(value.memory.rss);
  $("peak").textContent = bytes(value.memory.peak_rss);
  $("cpu").textContent = Number.isFinite(value.memory.cpu_percent)
    ? `${value.memory.cpu_percent.toFixed(1)}%`
    : "—";
  $("requests").textContent = compact(value.requests);
  $("requests").title = number(value.requests);
  $("allocations").textContent = compact(value.allocations_total);
  $("allocations").title = number(value.allocations_total);
  $("arena").textContent = bytes(value.arena_bytes);
  $("peers").textContent = number(value.websocket_connections);
  if (previous && at > previous.at)
    $("request-rate").textContent =
      `${(((value.requests - previous.requests) * 1000) / (at - previous.at)).toFixed(1)} requests / second`;
  const p95 = percentile(
    value.recent.map((request) => request.elapsed_us / 1000),
    0.95,
  );
  $("latency").textContent = p95 === null ? "—" : `${p95.toFixed(2)} ms`;
  for (const [id, field] of [
    ["anonymous", "anonymous"],
    ["file-backed", "file_backed"],
    ["virtual", "virtual_bytes"],
    ["swap", "swap"],
    ["container", "container_bytes"],
    ["container-limit", "container_limit"],
  ])
    $(id).textContent = bytes(value.memory[field]);
  $("threads").textContent = number(value.memory.threads);
  $("uptime").textContent = uptime(value.uptime_ms);
  for (const [id, field] of [
    ["tiles", "rendered_tiles"],
    ["renders", "active_renders"],
    ["games", "game_rooms"],
    ["arena-players", "arena_players"],
    ["voxel-players", "voxel_players"],
    ["voxel-blocks", "voxel_blocks"],
    ["canvases", "canvas_rooms"],
    ["strokes", "strokes"],
    ["messages", "websocket_messages"],
  ])
    $(id).textContent = number(value[field]);
  $("errors").textContent = `${value.client_errors} / ${value.server_errors}`;
  $("instance").textContent = value.instance;
  $("rss-range").textContent = `${bytes(value.memory.rss)} NOW`;
  const fragment = document.createDocumentFragment();
  for (const request of [...value.recent].reverse().slice(0, 9)) {
    const row = document.createElement("tr");
    for (const text of [
      request.method,
      request.path,
      request.status,
      (request.elapsed_us / 1000).toFixed(2),
    ]) {
      const cell = document.createElement("td");
      cell.textContent = text;
      row.append(cell);
    }
    row.children[1].title = request.path;
    row.children[2].className = `status-code ${request.status < 400 ? "good" : "bad"}`;
    fragment.append(row);
  }
  $("recent").replaceChildren(fragment);
  charts();
}
async function poll() {
  if (fetching || manualPause || document.hidden) return;
  fetching = true;
  try {
    receive(await json("/pulse/snapshot", { cache: "no-store" }));
  } catch (error) {
    status("connection", error.message, false, true);
  } finally {
    fetching = false;
  }
}
function fallback() {
  if (manualPause || document.hidden) return;
  source?.close();
  clearTimeout(watchdog);
  polling = true;
  status("connection", "Switching to HTTP snapshots");
  clearInterval(pollTimer);
  poll();
  pollTimer = setInterval(poll, 1500);
}
function stop() {
  source?.close();
  clearInterval(pollTimer);
  clearTimeout(watchdog);
}
function start() {
  stop();
  polling = false;
  failures = 0;
  if (manualPause || document.hidden) return;
  source = new EventSource("/pulse/events");
  watchdog = setTimeout(fallback, 8000);
  source.addEventListener("metrics", (event) => {
    try {
      receive(JSON.parse(event.data));
      watchdog = setTimeout(fallback, 8000);
    } catch {
      fallback();
    }
  });
  source.onerror = () => {
    status("connection", "Reconnecting to the event stream");
    if (++failures >= 3) fallback();
  };
}
$("pause").onclick = () => {
  manualPause = !manualPause;
  $("pause").textContent = manualPause ? "Resume updates" : "Pause updates";
  if (manualPause) {
    stop();
    status("connection", "Paused · showing the last sample");
  } else start();
};
$("traffic").onclick = async () => {
  $("traffic").disabled = true;
  try {
    let next = 0;
    await Promise.all(
      [0, 1].map(async () => {
        while (next < 20) {
          const id = next++;
          const response = await fetch(`/healthz?pulse=${id}`, {
            cache: "no-store",
          });
          await response.text();
        }
      }),
    );
    toast("20 real requests sent. Watch the counters.");
  } catch {
    toast("A request failed. Check the connection.");
  } finally {
    $("traffic").disabled = false;
  }
};
addEventListener("resize", charts);
document.addEventListener("visibilitychange", () => {
  if (document.hidden) stop();
  else if (!manualPause) start();
});
addEventListener("pagehide", stop);
start();

mountFullscreen(document.querySelector("main"));
