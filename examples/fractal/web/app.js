// Fern Fractal Explorer: a Leaflet map whose tiles a Fern server renders.
//
// Tile (z, x, y) covers part of the square from -2.5 - 2i to 1.5 + 2i, as
// in fractal.fern. In Leaflet's simple CRS that square is lng 0..256 and
// lat -256..0 at zoom 0, so a point c maps to
// lng = (re + 2.5) * 64 and lat = (im - 2) * 64.
import { TileDelivery, TileCache, percentile } from "./delivery.mjs";

const MAX_ZOOM = 42;
const BASE_PATH = "/fractal";
let delivery = null;

const places = {
  home: { re: -0.6, im: 0, z: 2 },
  seahorse: { re: -0.743643887037151, im: 0.13182590420533, z: 14 },
  elephant: { re: 0.2756, im: 0.0074, z: 11 },
  spiral: { re: -0.761574, im: -0.0847596, z: 15 },
  minibrot: { re: -1.7548776662466927, im: 0, z: 9 },
};
const DIVE = { re: -0.7436438870371587, im: 0.13182590420531198, z: 38 };

const toLatLng = (re, im) => L.latLng((im - 2) * 64, (re + 2.5) * 64);
const toComplex = (ll) => ({ re: ll.lng / 64 - 2.5, im: ll.lat / 64 + 2 });

const $ = (id) => document.getElementById(id);
const state = {
  transport: "ws",
  tiles: 0,
  localHits: 0,
  cacheHits: 0,
  httpTiles: 0,
  recent: [],
};

// ---- the map -------------------------------------------------------------

const map = L.map("map", {
  crs: L.CRS.Simple,
  minZoom: 1,
  maxZoom: MAX_ZOOM,
  zoomSnap: 1,
  wheelPxPerZoomLevel: 90,
  maxBounds: [[-320, -64], [64, 320]],
  maxBoundsViscosity: 0.6,
  zoomControl: true,
});

const Fractal = L.GridLayer.extend({
  createTile(coords, done) {
    const wrap = L.DomUtil.create("div", "tile-wrap");
    const n = 2 ** coords.z;
    if (coords.x < 0 || coords.y < 0 || coords.x >= n || coords.y >= n) {
      setTimeout(() => done(null, wrap), 0);
      return wrap;
    }
    const job = { z: coords.z, x: coords.x, y: coords.y, wrap, done, cancelled: false };
    wrap._job = job;
    fetchTile(job);
    return wrap;
  },
});

const layer = new Fractal({ tileSize: 256, maxZoom: MAX_ZOOM, maxNativeZoom: MAX_ZOOM, keepBuffer: 1, updateWhenZooming: false });
layer.on("tileunload", (e) => {
  const job = e.tile._job;
  if (job) {
    job.cancel?.();
    if (job.url) URL.revokeObjectURL(job.url);
  }
});
layer.addTo(map);

function showTile(job, blob, info) {
  if (job.cancelled) return;
  job.url = URL.createObjectURL(blob);
  const img = new Image(256, 256);
  img.onload = () => {
    if (job.cancelled) return;
    info.elapsedMs = performance.now() - job.requestedAt;
    record(job, info);
    job.done(null, job.wrap);
  };
  img.onerror = () => job.done(new Error("bad tile"), job.wrap);
  img.src = job.url;
  job.wrap.appendChild(img);
  const frame = L.DomUtil.create("div", "tile-frame", job.wrap);
  const label = L.DomUtil.create("div", "tile-label", job.wrap);
  label.textContent = info.cached ? ((info.cached === "local" || info.cached === "browser") ? "Local cache" : "CDN cache") : fmtMs(info.ms);
  label.style.color = info.cached ? "#7fd8ff" : heat(info.ms);
  frame.title = `${info.iterations.toLocaleString()} executed work units`;
}

// Requests from both transports use one bounded scheduler and cache.
function fetchTile(job) {
  job.cancel = delivery.request(job);
}

// ---- stats, feed and sparkline -------------------------------------------

function record(job, info) {
  state.tiles++;
  if ((info.cached === "local" || info.cached === "browser")) state.localHits++;
  else if (info.cached) state.cacheHits++;
  if (state.transport === "http" && info.cached !== "local" && info.cached !== "browser") state.httpTiles++;
  state.recent.push({ ...info, z: job.z });
  if (state.recent.length > 120) state.recent.shift();
  updateMetrics();

  const li = document.createElement("li");
  const where = `z${job.z} (${job.x}, ${job.y})`;
  li.innerHTML = `<span></span><span class="ms"></span><span class="sub"></span>`;
  li.children[0].textContent = where;
  li.children[1].textContent = info.cached ? ((info.cached === "local" || info.cached === "browser") ? "LOCAL" : "CDN") : fmtMs(info.ms);
  li.children[1].style.color = info.cached ? "#7fd8ff" : heat(info.ms);
  li.children[2].textContent = info.cached
    ? ((info.cached === "local" || info.cached === "browser") ? "reused from this browser’s tile cache" : "served from the Netlify edge cache")
    : `${fmtCount(info.iterations)} work units${info.containerTiles ? ` \u00b7 #${info.containerTiles.toLocaleString()} here` : ""}`;
  const list = $("feed-list");
  list.prepend(li);
  while (list.children.length > 9) list.lastChild.remove();
}

// Show a recent window at the current zoom, so old views do not distort it.
function updateMetrics() {
  const recent = state.recent.filter((sample) => sample.z === map.getZoom()).slice(-60);
  const origin = recent.filter((sample) => !sample.cached);
  const times = recent.map((sample) => sample.elapsedMs);
  $("s-tiles").textContent = state.tiles.toLocaleString();
  $("s-avg").textContent = times.length ? percentile(times, 0.5).toFixed(1) : "-";
  $("s-rate").textContent = times.length ? percentile(times, 0.95).toFixed(1) : "-";
  $("s-cache").textContent = `${state.localHits} / ${state.cacheHits}`;
  for (const stage of ["render", "compute", "color", "png", "transfer", "queue"]) {
    const values = (stage === "transfer" || stage === "queue" ? recent : origin).map((sample) =>
      stage === "transfer" ? sample.transferMs : stage === "queue" ? sample.queueMs : sample.stages?.[stage]).filter(Number.isFinite);
    $("s-" + stage).textContent = values.length ? percentile(values, 0.5).toFixed(1) : "-";
  }
  const c = $("spark");
  const g = c.getContext("2d");
  g.clearRect(0, 0, c.width, c.height);
  const max = Math.max(10, ...times);
  const w = c.width / 60;
  recent.forEach((sample, i) => {
    const h = Math.max(3, sample.elapsedMs / max * (c.height - 6));
    g.fillStyle = sample.cached ? "#7fd8ff" : heat(sample.elapsedMs);
    g.fillRect(i * w + 1, c.height - h, w - 2, h);
  });
}

function heat(ms) {
  if (ms < 20) return "#3ddc97";
  if (ms < 120) return "#ffb547";
  return "#ff5d73";
}

const fmtMs = (ms) => (ms < 10 ? `${ms.toFixed(2)} ms` : `${ms.toFixed(0)} ms`);
const fmtCount = (n) => (n >= 1e9 ? `${(n / 1e9).toFixed(2)}B` : n >= 1e6 ? `${(n / 1e6).toFixed(1)}M` : n >= 1e3 ? `${(n / 1e3).toFixed(0)}k` : `${n}`);
function fmtUptime(ms) {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

function setServer(text, live) {
  $("s-server").textContent = text;
  $("s-dot").classList.toggle("live", live);
}

function setBinarySize(bytes) {
  if (bytes > 0) $("binary-size").textContent = `${(bytes / 1048576).toFixed(1)} MB Fern`;
}

async function pollStats() {
  if (state.transport !== "http") return;
  try {
    const s = await (await fetch(`${BASE_PATH}/api/stats`, { cache: "no-store" })).json();
    setServer(`HTTP → container ${s.instance}, up ${fmtUptime(s.uptime_ms)}, ${s.tiles.toLocaleString()} tiles since boot`, true);
    setBinarySize(s.binary_bytes);
  } catch {
    setServer("stats unavailable", false);
  }
}
setInterval(pollStats, 3000);

// ---- position, URL and places --------------------------------------------

function updateWhere() {
  const c = toComplex(map.getCenter());
  const z = map.getZoom();
  const digits = Math.min(16, 3 + Math.ceil(z * 0.31));
  $("w-zoom").textContent = `zoom ${z} · ×${fmtCount(2 ** z)}`;
  $("w-coord").textContent = `${c.re.toFixed(digits)} ${c.im < 0 ? "-" : "+"} ${Math.abs(c.im).toFixed(digits)}i`;
  history.replaceState(null, "", `#${z}/${c.re.toFixed(digits)}/${c.im.toFixed(digits)}`);
}
map.on("moveend zoomend", () => { updateWhere(); updateMetrics(); delivery?.kick(); });

function go(p, animate = true) {
  map.flyTo(toLatLng(p.re, p.im), p.z, { animate, duration: 1.6 });
}

let diving = null;
function dive() {
  const button = document.querySelector('[data-place="dive"]');
  if (diving) {
    clearInterval(diving);
    diving = null;
    button.classList.remove("running");
    button.textContent = "Deep dive ↓";
    return;
  }
  button.classList.add("running");
  button.textContent = "Stop dive";
  const target = toLatLng(DIVE.re, DIVE.im);
  map.setView(target, Math.max(3, Math.min(map.getZoom(), 6)));
  diving = setInterval(() => {
    const z = map.getZoom();
    if (z >= DIVE.z) {
      dive();
      return;
    }
    map.setView(target, z + 1, { animate: true });
  }, 1400);
}

document.querySelectorAll("[data-place]").forEach((b) =>
  b.addEventListener("click", () => (b.dataset.place === "dive" ? dive() : go(places[b.dataset.place]))),
);

document.querySelectorAll("[data-transport]").forEach((b) =>
  b.addEventListener("click", () => {
    if (!delivery || state.transport === b.dataset.transport) return;
    delivery.setTransport(b.dataset.transport);
    if (state.transport === "http") pollStats();
    layer.redraw();
  }),
);

document.querySelectorAll("[data-labels]").forEach((b) =>
  b.addEventListener("click", () => {
    document.body.classList.toggle("no-labels", b.dataset.labels === "off");
    document.querySelectorAll("[data-labels]").forEach((o) => o.classList.toggle("on", o === b));
  }),
);

// ---- start ---------------------------------------------------------------

async function start() {
  const response = await fetch(`${BASE_PATH}/api/config`, { cache: "no-store" });
  if (!response.ok) throw new Error("Cannot load renderer configuration");
  const config = await response.json();
  delivery = new TileDelivery({
    version: config.render_version,
    origin: location.origin,
    basePath: BASE_PATH,
    cache: new TileCache(config.binary_bytes > 0 ? 16 * 1024 * 1024 : 0),
    onVersionChange: () => location.reload(),
    priority: (job) => {
      const center = map.project(map.getCenter(), job.z).divideBy(256);
      return Math.abs(job.x + 0.5 - center.x) + Math.abs(job.y + 0.5 - center.y) + Math.abs(job.z - map.getZoom()) * 1000;
    },
    onTile: showTile,
    onError: (job, error) => job.done(error, job.wrap),
    onState: setServer,
    onHello: (hello) => {
      setServer(`socket → container ${hello.instance}, up ${fmtUptime(hello.uptime_ms)}`, true);
      setBinarySize(hello.binary_bytes);
    },
    onTransport: (transport) => {
      state.transport = transport;
      document.querySelectorAll("[data-transport]").forEach((button) => button.classList.toggle("on", button.dataset.transport === transport));
      if (transport === "http") pollStats();
    },
  });
  const hash = location.hash.slice(1).split("/").map(Number);
  if (hash.length === 3 && hash.every(Number.isFinite)) {
    go({ z: Math.min(MAX_ZOOM, Math.max(1, hash[0])), re: hash[1], im: hash[2] }, false);
  } else go(places.home, false);
  updateWhere();
  delivery.setTransport("ws");
}
start().catch((error) => setServer(error.message, false));
window.addEventListener("pagehide", () => delivery?.close());
