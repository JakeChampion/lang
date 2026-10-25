import assert from "node:assert/strict";
import { controls, decode } from "../web/blockyard/protocol.mjs";
const base = process.argv[2] || "http://localhost:8080",
  total = Number(process.argv[3] || 512),
  seconds = Number(process.argv[4] || 5),
  mode = process.argv[5] || "both";
assert.ok(total > 0 && total <= 512);
assert.ok(seconds >= 2 && seconds <= 60);
const peers = [],
  ids = new Set(),
  phases = [];
let failure,
  bytes = 0,
  latencies = [],
  timer,
  started;
const initial = await (await fetch(base + "/blockyard/status")).json();
assert.equal(initial.players, 0, "use an empty world for this load test");
const before = await (await fetch(base + "/pulse/snapshot")).json();
function connect() {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(base.replace(/^http/, "ws") + "/blockyard/ws"),
      peer = {
        socket,
        id: 0,
        seq: 0,
        snapshots: 0,
        latest: null,
        pending: new Map(),
        travelled: false,
      };
    peers.push(peer);
    socket.binaryType = "arraybuffer";
    const timeout = setTimeout(
      () => reject(Error("participant handshake timed out")),
      15000,
    );
    socket.onerror = () => {
      clearTimeout(timeout);
      reject(Error("participant connection failed"));
    };
    socket.onmessage = ({ data }) => {
      try {
        if (typeof data === "string") {
          const message = JSON.parse(data);
          if (message.kind === "welcome") {
            assert.equal(message.capacity, 512);
            assert.ok(!ids.has(message.id));
            ids.add(message.id);
            peer.id = message.id;
            clearTimeout(timeout);
            resolve(peer);
          }
          if (message.kind === "travel") peer.travelled = true;
          return;
        }
        bytes += data.byteLength;
        const view = new DataView(data);
        if (view.getUint8(1) === 65) {
          const message = decode(data);
          if (peer.pending.has(message.seq)) {
            latencies.push(performance.now() - peer.pending.get(message.seq));
            peer.pending.delete(message.seq);
          }
          return;
        }
        assert.equal(view.getUint8(0), 66);
        assert.equal(view.getUint8(1), 80);
        assert.equal(view.getUint8(2), 1);
        const count = view.getUint16(8, true);
        assert.ok(count <= 512);
        assert.equal(data.byteLength, 16 + count * 16);
        peer.snapshots++;
        peer.latest = data;
      } catch (error) {
        failure = error;
        reject(error);
      }
    };
  });
}
async function phase(name, expected) {
  latencies = [];
  bytes = 0;
  for (const p of peers) p.snapshots = 0;
  const start = performance.now();
  await new Promise((r) => setTimeout(r, seconds * 1000));
  if (failure) throw failure;
  const elapsed = (performance.now() - start) / 1000;
  for (const peer of peers) {
    assert.equal(
      peer.socket.readyState,
      WebSocket.OPEN,
      "participant disconnected under load",
    );
    assert.ok(
      peer.snapshots >= seconds * 5,
      "participant received fewer than five snapshots per second",
    );
    const frame = decode(peer.latest);
    assert.equal(frame.total, total);
    assert.equal(
      frame.players.length,
      expected,
      "interest filtering included distant participants",
    );
    assert.ok(
      frame.players.some((p) => p.id === peer.id),
      "snapshot omitted its own participant",
    );
  }
  assert.ok(
    latencies.length >= total * seconds * 10,
    "input acknowledgements stalled",
  );
  latencies.sort((a, b) => a - b);
  const p50 = latencies[Math.floor(latencies.length * 0.5)],
    p95 = latencies[Math.floor(latencies.length * 0.95)];
  assert.ok(p95 < 1000, "input acknowledgements stalled for a second");
  const metrics = await (await fetch(base + "/pulse/snapshot")).json();
  const result = {
    phase: name,
    participants: total,
    nearby_per_participant: expected,
    seconds: +elapsed.toFixed(2),
    acknowledgements: latencies.length,
    ack_median_ms: +p50.toFixed(2),
    ack_p95_ms: +p95.toFixed(2),
    snapshots_per_second: +(
      peers.reduce((s, p) => s + p.snapshots, 0) /
      total /
      elapsed
    ).toFixed(2),
    received_mib: +(bytes / 1048576).toFixed(2),
    server_rss_mib: +(metrics.memory.rss / 1048576).toFixed(2),
    rss_increase_mib: +(
      (metrics.memory.rss - before.memory.rss) /
      1048576
    ).toFixed(2),
  };
  phases.push(result);
  console.log(JSON.stringify(result));
}
try {
  for (let at = 0; at < total; at += 32)
    await Promise.all(
      Array.from({ length: Math.min(32, total - at) }, connect),
    );
  started = performance.now();
  timer = setInterval(() => {
    const t = (performance.now() - started) / 1000;
    for (const p of peers)
      if (
        p.socket.readyState === WebSocket.OPEN &&
        p.socket.bufferedAmount < 8192
      ) {
        const seq = ++p.seq;
        p.pending.set(seq, performance.now());
        if (p.pending.size > 128)
          p.pending.delete(p.pending.keys().next().value);
        p.socket.send(
          controls(seq, {
            mx: Math.sin(p.id * 2.4 + t) * 0.2,
            mz: Math.cos(p.id * 2.4 + t) * 0.2,
            up: 0,
            fly: true,
            jump: false,
            yaw: 0,
            pitch: 0,
          }),
        );
      }
  }, 50);
  await phase("crowded", total);
  if (total === 512)
    await new Promise((resolve, reject) => {
      const extra = new WebSocket(
        base.replace(/^http/, "ws") + "/blockyard/ws",
      );
      const timeout = setTimeout(
        () => reject(Error("capacity check timed out")),
        5000,
      );
      extra.onopen = () => {
        clearTimeout(timeout);
        extra.close();
        reject(Error("accepted a 513th participant"));
      };
      extra.onerror = () => {
        clearTimeout(timeout);
        resolve();
      };
    });
  if (mode !== "crowded") {
    assert.equal(
      total % 4,
      0,
      "the spread test needs a multiple of four participants",
    );
    for (const p of peers) {
      const data = new ArrayBuffer(16),
        v = new DataView(data);
      v.setUint8(0, 3);
      v.setUint8(1, (p.id - 1) % 4);
      v.setUint32(10, ++p.seq, true);
      p.socket.send(data);
    }
    await new Promise((r) => setTimeout(r, 1000));
    assert.ok(
      peers.every((p) => p.travelled),
      "server did not confirm world travel",
    );
    await phase("four distant areas", total / 4);
  }
} finally {
  clearInterval(timer);
  for (const p of peers) p.socket.close();
  const until = performance.now() + 5000;
  let count = total;
  while (count && performance.now() < until) {
    await new Promise((r) => setTimeout(r, 50));
    try {
      count = (await (await fetch(base + "/blockyard/status")).json()).players;
    } catch {}
  }
  assert.equal(count, 0, "seats did not return to zero");
}
console.log(
  "real connections, capacity, shared simulation, spatial fanout, input acknowledgements, and cleanup OK",
);
