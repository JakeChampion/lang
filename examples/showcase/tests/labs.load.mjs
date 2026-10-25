import assert from "node:assert/strict";
import { join, until } from "./labs-test-client.mjs";
const base = process.argv[2] || "http://localhost:8080",
  count = Number(process.argv[3] || 128),
  seconds = Number(process.argv[4] || 3),
  peers = [];
assert.ok(Number.isInteger(count) && count >= 2 && count <= 256);
assert.ok(seconds >= 1 && seconds <= 60);
let timer;
try {
  const results = await Promise.allSettled(
    Array.from({ length: count }, () => join(base, "foundry", "crowd")),
  );
  for (const result of results) {
    if (result.status === "rejected") throw result.reason;
    peers.push(result.value);
  }
  await until(
    () => peers.every((p) => p.frame),
    "some clients received no factory state",
    20000,
  );
  for (const p of peers) {
    p.frames = 0;
    p.bytes = 0;
    p.latencies = [];
  }
  const at = performance.now();
  timer = setInterval(() => {
    for (const p of peers)
      if (p.socket.readyState === WebSocket.OPEN) p.command("sync");
  }, 100);
  await new Promise((r) => setTimeout(r, seconds * 1000));
  clearInterval(timer);
  const elapsed = (performance.now() - at) / 1000;
  for (const p of peers) {
    assert.equal(p.error, null);
    assert.ok(
      p.frames / elapsed >= 5,
      `only ${p.frames / elapsed} updates/second`,
    );
    assert.ok(
      p.latencies.length >= seconds * 5,
      "missing input acknowledgements",
    );
  }
  peers[0].command("pause");
  await until(
    () => peers.every((p) => p.frame && !p.frame.running),
    "not every client received the shared pause",
    15000,
  );
  const first = peers[0].frame;
  for (const p of peers) {
    assert.equal(p.frame.tick, first.tick);
    assert.deepEqual(p.frame.cargo, first.cargo);
    assert.deepEqual(p.frame.power, first.power);
  }
  if (count === 256) {
    const extra = new WebSocket(
      base.replace(/^http/, "ws") + "/foundry/ws?room=crowd",
    );
    let failed = false,
      opened = false;
    extra.onerror = () => (failed = true);
    extra.onopen = () => (opened = true);
    await until(() => failed || opened, "capacity handshake did not finish");
    assert.equal(opened, false, "257th live subscription was accepted");
    extra.close();
  }
  const latencies = peers.flatMap((p) => p.latencies).sort((a, b) => a - b),
    percent = (q) =>
      latencies[
        Math.min(latencies.length - 1, Math.floor(latencies.length * q))
      ];
  assert.ok(
    percent(0.95) < 1000,
    "p95 acknowledgement exceeded the stall guard",
  );
  const snapshot = await (await fetch(base + "/pulse/snapshot")).json();
  console.log(
    JSON.stringify({
      clients: count,
      seconds: elapsed,
      updates_per_second:
        peers.reduce((sum, p) => sum + p.frames, 0) / count / elapsed,
      p50_ms: percent(0.5),
      p95_ms: percent(0.95),
      p99_ms: percent(0.99),
      traffic_bytes: peers.reduce((sum, p) => sum + p.bytes, 0),
      rss: snapshot.memory.rss,
      tick: first.tick,
    }),
  );
} finally {
  clearInterval(timer);
  for (const p of peers) p.socket.close();
}
