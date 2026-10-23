// Fern Fractal Explorer: a Leaflet map whose tiles a Fern server renders.
//
// Tile (z, x, y) covers part of the square from -2.5 - 2i to 1.5 + 2i, as
// in fractal.fern. In Leaflet's simple CRS that square is lng 0..256 and
// lat -256..0 at zoom 0, so a point c maps to
// lng = (re + 2.5) * 64 and lat = (im - 2) * 64.
"use strict";

const MAX_ZOOM = 42;
const IN_FLIGHT = 4;

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
  renderMs: 0,
  iterations: 0,
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
    job.cancelled = true;
    if (job.url) URL.revokeObjectURL(job.url);
  }
});
layer.addTo(map);

function showTile(job, blob, info) {
  if (job.cancelled) return;
  job.url = URL.createObjectURL(blob);
  const img = new Image(256, 256);
  img.onload = () => job.done(null, job.wrap);
  img.onerror = () => job.done(new Error("bad tile"), job.wrap);
  img.src = job.url;
  job.wrap.appendChild(img);
  const frame = L.DomUtil.create("div", "tile-frame", job.wrap);
  const label = L.DomUtil.create("div", "tile-label", job.wrap);
  label.textContent = info.cached ? `CDN hit · ${fmtMs(info.ms)}` : fmtMs(info.ms);
  label.style.color = info.cached ? "#7fd8ff" : heat(info.ms);
  frame.title = `${info.iterations.toLocaleString()} iterations`;
  record(job, info);
}

// ---- transport: WebSocket ------------------------------------------------

let socket = null;
let socketReady = false;
let nextId = 1;
const queue = [];
const pending = new Map();

function connect() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  socket = new WebSocket(`${proto}//${location.host}/ws`);
  socket.binaryType = "arraybuffer";
  const fallback = setTimeout(() => {
    if (!socketReady && state.transport === "ws") {
      setServer("WebSocket blocked, using HTTP", false);
      document.querySelector('[data-transport="http"]').click();
    }
  }, 5000);
  socket.onopen = () => {
    clearTimeout(fallback);
    socketReady = true;
    pump();
  };
  socket.onmessage = (ev) => {
    if (typeof ev.data === "string") {
      const hello = JSON.parse(ev.data);
      setServer(`socket → container ${hello.instance}, up ${fmtUptime(hello.uptime_ms)}`, true);
      setBinarySize(hello.binary_bytes);
      return;
    }
    if (ev.data.byteLength < 4) return;
    const view = new DataView(ev.data);
    const id = view.getUint32(0);
    const job = pending.get(id);
    pending.delete(id);
    if (job && ev.data.byteLength > 20) {
      const micros = view.getUint32(4);
      const iterations = view.getUint32(8) * 4294967296 + view.getUint32(12);
      const containerTiles = view.getUint32(16);
      showTile(job, new Blob([ev.data.slice(20)], { type: "image/png" }), { ms: micros / 1000, iterations, cached: false, containerTiles });
    } else if (job) {
      job.done(new Error("refused"), job.wrap);
    }
    pump();
  };
  socket.onclose = () => {
    socketReady = false;
    setServer("socket closed, reconnecting...", false);
    for (const job of pending.values()) queue.unshift(job);
    pending.clear();
    if (state.transport === "ws") setTimeout(connect, 1000);
  };
}

function pump() {
  while (socketReady && pending.size < IN_FLIGHT && queue.length > 0) {
    const job = queue.shift();
    if (job.cancelled) continue;
    const id = nextId++;
    pending.set(id, job);
    socket.send(`${id} ${job.z} ${job.x} ${job.y}`);
  }
}

// ---- transport: HTTP -----------------------------------------------------

async function fetchHttp(job) {
  try {
    const res = await fetch(`/tile/${job.z}/${job.x}/${job.y}`);
    if (!res.ok) throw new Error(res.statusText);
    const timing = res.headers.get("server-timing") || "";
    const ms = parseFloat((timing.match(/dur=([\d.]+)/) || [])[1] || "0");
    const iterations = parseInt(res.headers.get("x-fern-iterations") || "0", 10);
    const cacheStatus = (res.headers.get("cache-status") || "").toLowerCase();
    const cached = /edge"?;\s*hit/.test(cacheStatus) || /;\s*hit/.test(cacheStatus);
    showTile(job, await res.blob(), { ms, iterations, cached });
  } catch (err) {
    job.done(err, job.wrap);
  }
}

function fetchTile(job) {
  if (state.transport === "ws") {
    queue.push(job);
    pump();
  } else {
    fetchHttp(job);
  }
}

// ---- stats, feed and sparkline -------------------------------------------

function record(job, info) {
  state.tiles++;
  if (info.cached) {
    state.cacheHits++;
  } else {
    state.renderMs += info.ms;
    state.iterations += info.iterations;
  }
  if (state.transport === "http") state.httpTiles++;
  state.recent.push(info.cached ? 0 : info.ms);
  if (state.recent.length > 60) state.recent.shift();

  $("s-tiles").textContent = state.tiles.toLocaleString();
  const rendered = state.tiles - state.cacheHits;
  $("s-avg").textContent = rendered ? (state.renderMs / rendered).toFixed(1) : "-";
  $("s-rate").textContent = state.renderMs ? (state.iterations / state.renderMs / 1000).toFixed(0) : "-";
  $("s-cache").textContent = state.httpTiles ? `${state.cacheHits}/${state.httpTiles}` : "-";
  drawSpark();

  const li = document.createElement("li");
  const where = `z${job.z} (${job.x}, ${job.y})`;
  li.innerHTML = `<span></span><span class="ms"></span><span class="sub"></span>`;
  li.children[0].textContent = where;
  li.children[1].textContent = info.cached ? "CDN" : fmtMs(info.ms);
  li.children[1].style.color = info.cached ? "#7fd8ff" : heat(info.ms);
  li.children[2].textContent = info.cached
    ? "served from the Netlify edge cache"
    : `${fmtCount(info.iterations)} iterations${info.containerTiles ? ` \u00b7 #${info.containerTiles.toLocaleString()} here` : ""}`;
  const list = $("feed-list");
  list.prepend(li);
  while (list.children.length > 9) list.lastChild.remove();
}

function drawSpark() {
  const c = $("spark");
  const g = c.getContext("2d");
  g.clearRect(0, 0, c.width, c.height);
  const max = Math.max(10, ...state.recent);
  const w = c.width / 60;
  state.recent.forEach((ms, i) => {
    const h = ms ? Math.max(3, (ms / max) * (c.height - 6)) : 3;
    g.fillStyle = ms ? heat(ms) : "#7fd8ff";
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
    const s = await (await fetch("/api/stats", { cache: "no-store" })).json();
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
map.on("moveend zoomend", updateWhere);

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
    if (state.transport === b.dataset.transport) return;
    state.transport = b.dataset.transport;
    document.querySelectorAll("[data-transport]").forEach((o) => o.classList.toggle("on", o === b));
    if (state.transport === "ws") {
      connect();
    } else if (socket) {
      socket.onclose = null;
      socket.close();
      socket = null;
      socketReady = false;
      queue.length = 0;
      pending.clear();
      pollStats();
    }
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

const hash = location.hash.slice(1).split("/").map(Number);
if (hash.length === 3 && hash.every(Number.isFinite)) {
  go({ z: Math.min(MAX_ZOOM, Math.max(1, hash[0])), re: hash[1], im: hash[2] }, false);
} else {
  go(places.home, false);
}
updateWhere();
connect();
