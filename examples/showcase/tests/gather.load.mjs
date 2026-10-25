import assert from "node:assert/strict";
import { destination, decode } from "../web/gather/protocol.mjs";
const base = process.argv[2] || "http://localhost:8080",
  total = Number(process.argv[3] || 512),
  seconds = Number(process.argv[4] || 10);
assert.ok(total >= 1 && total <= 512);
assert.ok(seconds >= 2 && seconds <= 60);
const peers = [],
  ids = new Set(),
  latencies = [];
let bytes = 0,
  failure,
  started;
const initial = await (await fetch(base + "/gather/status")).json();
assert.equal(initial.players, 0, "use a fresh server for the concurrency test");
const before = await (await fetch(base + "/pulse/snapshot")).json();
function connect(index) {
  return new Promise((resolve, reject) => {
    const socket = new WebSocket(base.replace(/^http/, "ws") + "/gather/ws");
    socket.binaryType = "arraybuffer";
    const peer = {
      socket,
      id: 0,
      seq: 0,
      snapshots: 0,
      count: 0,
      latest: null,
      pending: new Map(),
    };
    peers.push(peer);
    const timeout = setTimeout(
      () => reject(Error("participant handshake timed out")),
      15000,
    );
    socket.onmessage = ({ data }) => {
      try {
        if (typeof data === "string") {
          const hello = JSON.parse(data);
          assert.equal(hello.capacity, 512);
          assert.ok(!ids.has(hello.id));
          ids.add(hello.id);
          peer.id = hello.id;
          clearTimeout(timeout);
          resolve(peer);
          return;
        }
        bytes += data.byteLength;
        const message = decode(data);
        if (message.kind === "ack") {
          if (peer.pending.has(message.seq)) {
            latencies.push(performance.now() - peer.pending.get(message.seq));
            peer.pending.delete(message.seq);
          }
          return;
        }
        peer.snapshots++;
        peer.count = message.players.length;
        peer.latest = message;
        assert.ok(
          message.players.every(
            (p) => p.x >= 0 && p.x <= 1024 && p.y >= 0 && p.y <= 640,
          ),
        );
      } catch (error) {
        failure = error;
        reject(error);
      }
    };
    socket.onerror = () => {
      clearTimeout(timeout);
      reject(Error("participant connection failed"));
    };
  });
}
let timer;
try {
  for (let at = 0; at < total; at += 32)
    await Promise.all(
      Array.from({ length: Math.min(32, total - at) }, (_, i) =>
        connect(at + i),
      ),
    );
  started = performance.now();
  timer = setInterval(() => {
    const elapsed = (performance.now() - started) / 1000;
    for (const peer of peers)
      if (
        peer.socket.readyState === WebSocket.OPEN &&
        peer.socket.bufferedAmount < 8192
      ) {
        const seq = ++peer.seq,
          phase = peer.id * 2.399963 + elapsed * 0.7;
        peer.pending.set(seq, performance.now());
        if (peer.pending.size > 128)
          peer.pending.delete(peer.pending.keys().next().value);
        peer.socket.send(
          destination(
            seq,
            512 + Math.cos(phase) * 420,
            320 + Math.sin(phase * 1.3) * 250,
          ),
        );
      }
  }, 50);
  await new Promise((resolve) => setTimeout(resolve, seconds * 1000));
  if (failure) throw failure;
  const live = await (await fetch(base + "/gather/status")).json(),
    metrics = await (await fetch(base + "/pulse/snapshot")).json();
  assert.equal(live.players, total);
  assert.equal(ids.size, total);
  assert.ok(
    peers.every(
      (p) =>
        p.socket.readyState === WebSocket.OPEN &&
        p.count === total &&
        p.snapshots >= seconds * 5,
    ),
    "every participant must receive the shared world at least five times per second",
  );
  assert.ok(
    latencies.length >= total * seconds * 5,
    "input acknowledgements must continue under fanout load",
  );
  assert.ok(
    peers[0].latest.score > 0,
    "server-side collection must work under load",
  );
  if (total === 512) {
    await new Promise((resolve, reject) => {
      const extra = new WebSocket(base.replace(/^http/, "ws") + "/gather/ws");
      extra.onopen = () => {
        extra.close();
        reject(Error("capacity limit accepted a 513th participant"));
      };
      extra.onerror = resolve;
    });
  }
  latencies.sort((a, b) => a - b);
  const p50 = latencies[Math.floor(latencies.length * 0.5)],
    p95 = latencies[Math.floor(latencies.length * 0.95)];
  assert.ok(
    p95 < 1000,
    "input acknowledgements stalled for a second under load",
  );
  console.log(
    JSON.stringify({
      participants: total,
      seconds,
      acknowledgements: latencies.length,
      ack_median_ms: +p50.toFixed(2),
      ack_p95_ms: +p95.toFixed(2),
      snapshots_per_second: +(
        peers.reduce((sum, p) => sum + p.snapshots, 0) /
        total /
        seconds
      ).toFixed(2),
      received_mib: +(bytes / 1048576).toFixed(2),
      server_rss_mib: +(metrics.memory.rss / 1048576).toFixed(2),
      rss_increase_mib: +(
        (metrics.memory.rss - before.memory.rss) /
        1048576
      ).toFixed(2),
      team_score: peers[0].latest.score,
    }),
  );
} finally {
  clearInterval(timer);
  await Promise.all(
    peers.map(
      (peer) =>
        new Promise((resolve) => {
          if (peer.socket.readyState === WebSocket.CLOSED) {
            resolve();
            return;
          }
          peer.socket.addEventListener("close", resolve, { once: true });
          peer.socket.close();
        }),
    ),
  );
}
const until = performance.now() + 5000;
while (
  (await (await fetch(base + "/gather/status")).json()).players &&
  performance.now() < until
)
  await new Promise((resolve) => setTimeout(resolve, 50));
assert.equal(
  (await (await fetch(base + "/gather/status")).json()).players,
  0,
  "all seats must release after disconnect",
);
console.log(
  "capacity, live state, movement, collection, fanout, acknowledgements, and seat cleanup OK",
);
