import { $, status, json } from "/assets/ui.mjs";
import { kernel, size } from "/bench/kernel.mjs";
import { Swarm, toHex, fromHex, magnet } from "./peer.mjs";

const fresh = await kernel("/bench/torrent.wasm", "fern:showcase/torrent").catch(
  (error) => {
    status("connection", error.message, false, true);
    throw error;
  },
);
const scheme = location.protocol === "https:" ? "wss" : "ws";
const tracker = `${scheme}://${location.host}/torrent/announce`;
const MAX_BYTES = 64 * 1024 * 1024;
let swarm = null;

const bytes = async (name, ...args) => {
  const k = await fresh();
  return k.list(k.call(name, ...args));
};
const pieceLength = (n) => (n <= 1 << 20 ? 16384 : n <= 16 << 20 ? 65536 : 262144);

function grid() {
  const cells = $("pieces");
  if (!swarm?.have) return;
  if (cells.childElementCount !== swarm.have.length) {
    cells.replaceChildren(...Array.from(swarm.have, () => document.createElement("i")));
  }
  swarm.have.forEach((h, i) => cells.children[i].classList.toggle("have", !!h));
}

function render() {
  if (!swarm) return;
  const meta = swarm.meta;
  const done = meta ? swarm.have.reduce((n, h) => n + h, 0) : 0;
  $("stat-peers").textContent = String(swarm.wires.size);
  $("stat-down").textContent = size(swarm.downloaded);
  $("stat-up").textContent = size(swarm.uploaded);
  $("stat-pieces").textContent = meta ? `${done} / ${meta.pieces}` : "waiting for metadata";
  grid();
  if (meta) {
    $("file-name").textContent = meta.name;
    $("file-size").textContent = size(meta.length);
  }
}

function share(infoHash, name, info) {
  const link = magnet(infoHash, name, tracker);
  $("hash").textContent = toHex(infoHash);
  $("magnet").value = link;
  $("share-link").href = `${location.origin}/torrent/#${toHex(infoHash)}`;
  $("share-link").textContent = `${location.host}/torrent/#${toHex(infoHash).slice(0, 12)}…`;
  if (info)
    bytes("torrent-file", info, tracker).then((file) => {
      $("torrent-file").href = URL.createObjectURL(new Blob([file], { type: "application/x-bittorrent" }));
      $("torrent-file").download = `${name}.torrent`;
      $("torrent-file").hidden = false;
    });
  $("swarm").hidden = false;
}

function watch(s) {
  swarm = s;
  for (const event of ["progress", "info"]) s.addEventListener(event, render);
  s.addEventListener("info", () => {
    if ($("torrent-file").hidden) share(s.infoHash, s.meta.name, s.info);
  });
  s.addEventListener("failure", (e) => status("connection", `Tracker: ${e.detail}`, false, true));
  s.addEventListener("tracker-closed", () => status("connection", "The tracker connection closed", false, true));
  s.addEventListener("done", () => {
    const url = URL.createObjectURL(new Blob([s.data]));
    $("save").href = url;
    $("save").download = s.meta.name;
    $("save").hidden = false;
    $("swarm").dataset.state = "complete";
    status("connection", "Downloaded and verified · now seeding", true);
  });
  s.start();
  render();
}

$("source").onchange = () => {
  $("file-field").hidden = $("source").value !== "file";
};

$("seed-form").onsubmit = async (event) => {
  event.preventDefault();
  $("seed-run").disabled = true;
  try {
    let data, name;
    if ($("source").value === "file") {
      const file = $("file").files[0];
      if (!file) throw Error("Choose a file first.");
      if (file.size > MAX_BYTES) throw Error("Choose a file of 64 MiB or less.");
      data = new Uint8Array(await file.arrayBuffer());
      name = file.name;
    } else {
      const length = Number($("source").value);
      data = await bytes("sample", Number($("seed").value) || 0, length);
      name = `fern-sample-${$("seed").value || 0}.txt`;
    }
    status("connection", "Hash the pieces in Fern Wasm");
    const started = performance.now();
    const info = await bytes("info", name, data, pieceLength(data.length));
    const infoHash = await bytes("infohash", info);
    $("hash-ms").textContent = `${(performance.now() - started).toFixed(0)} ms`;
    await json("/torrent/register", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ info_hash: toHex(infoHash), name, length: data.length }),
    });
    swarm?.stop();
    const s = new Swarm({ kernel: fresh, infoHash, info, data, tracker });
    await s.setInfo(info, data);
    share(infoHash, name, info);
    watch(s);
    $("swarm").dataset.state = "seeding";
    status("connection", "Seeding · keep this page open", true);
  } catch (error) {
    status("connection", error.message, false, true);
  } finally {
    $("seed-run").disabled = false;
  }
};

$("copy").onclick = () => navigator.clipboard?.writeText($("magnet").value);

const wanted = location.hash.slice(1).toLowerCase();
if (/^[0-9a-f]{40}$/.test(wanted)) {
  const infoHash = fromHex(wanted);
  share(infoHash, "download", null);
  $("swarm").dataset.state = "downloading";
  $("seed-form").hidden = true;
  $("hashed").hidden = true;
  watch(new Swarm({ kernel: fresh, infoHash, tracker }));
  status("connection", "Looking for peers", true);
} else {
  status("connection", "Ready · Fern Wasm loaded", true);
}
