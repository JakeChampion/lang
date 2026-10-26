// Run against a dedicated local server. These clients use real connections.
import assert from "node:assert/strict";
import { fork } from "node:child_process";
import { fileURLToPath } from "node:url";
import { readdir } from "node:fs/promises";
import { factoryPacket } from "./labs-test-client.mjs";
import { destination, decode as arenaPacket } from "../web/gather/protocol.mjs";
import { controls, decode as worldPacket } from "../web/blockyard/protocol.mjs";
const childMode = process.argv[2] === "--worker";
const args = process.argv.slice(childMode ? 3 : 2);
const [
  base = "http://localhost:8080",
  app = "foundry",
  duration = "180",
  capacity,
] = args;
assert.ok(
  ["foundry", "gather", "blockyard"].includes(app),
  "Choose foundry, gather, or blockyard",
);
const seconds = Number(duration),
  count = Number(capacity || (app === "foundry" ? 256 : 512));
assert.ok(
  Number.isInteger(count) &&
    count >= (childMode ? 8 : 16) &&
    count <= (app === "foundry" ? 256 : 512),
);
assert.ok(Number.isInteger(seconds) && seconds >= 10 && seconds <= 3600);
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const read = async (path) => {
  const response = await fetch(base + path, {
    signal: AbortSignal.timeout(10000),
  });
  assert.equal(response.status, 200, path);
  return response.json();
};
const room = "soak";
let failed,
  frames = 0,
  acknowledgements = 0,
  ringAt = 0;
const latencies = [],
  peers = [];
const checked = () => {
  if (failed) throw failed;
};
async function until(check, message, timeout = 15000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) {
    checked();
    if (await check()) return;
    await pause(50);
  }
  throw Error(message);
}
function latency(value) {
  acknowledgements++;
  latencies[ringAt++ % 8192] = value;
}
async function connect(index) {
  const path = app === "foundry" ? `/foundry/ws?room=${room}` : `/${app}/ws`;
  const socket = new WebSocket(base.replace(/^http/, "ws") + path);
  const peer = {
    socket,
    index,
    id: 0,
    seq: 0,
    pending: new Map(),
    lastFrame: performance.now(),
    ready: false,
    hasSelf: false,
    expectedClose: false,
    sequence: null,
    cargo: null,
  };
  socket.binaryType = "arraybuffer";
  socket.onclose = (event) => {
    if (!peer.expectedClose)
      failed ||= Error(
        `Unexpected ${app} close: ${event.code} ${event.reason}`,
      );
  };
  socket.onerror = () => {
    if (!peer.expectedClose) failed ||= Error(`${app} transport error`);
  };
  socket.onmessage = ({ data }) => {
    try {
      if (typeof data === "string") {
        const message = JSON.parse(data);
        if (message.kind === "ack") {
          const sent = peer.pending.get(message.seq);
          if (sent !== undefined) {
            latency(performance.now() - sent);
            peer.pending.delete(message.seq);
          }
        } else if (message.kind === "snapshot") {
          assert.equal(message.cells.length, 65536);
          peer.ready = true;
        } else if (message.id) {
          peer.id = message.id;
          peer.ready = true;
        }
        return;
      }
      if (app === "foundry") {
        const v = new DataView(data);
        assert.ok(data.byteLength >= 96);
        assert.equal(v.getUint16(0, false), 0x4646);
        assert.equal(v.getUint8(2), 2);
        const delta = v.getUint8(3) === 1,
          items = v.getUint32(84, true);
        assert.equal(v.getUint32(76, true), 65536);
        assert.ok(items <= 65536);
        assert.equal(data.byteLength, 96 + items * (delta ? 5 : 3));
        if (delta)
          assert.equal(
            Number(v.getBigUint64(64, true)),
            peer.sequence,
            "Factory delta gap",
          );
        peer.sequence = Number(v.getBigUint64(56, true));
        if (index === 0) peer.cargo = factoryPacket(data, peer.cargo);
      } else {
        const frame = app === "gather" ? arenaPacket(data) : worldPacket(data);
        if (frame.kind === "ack") {
          const sent = peer.pending.get(frame.seq);
          if (sent !== undefined) {
            latency(performance.now() - sent);
            peer.pending.delete(frame.seq);
          }
          return;
        }
        const self = frame.players.some((player) => player.id === peer.id);
        if (peer.hasSelf) assert.ok(self, "Snapshot omitted its participant");
        peer.hasSelf ||= self;
      }
      frames++;
      peer.lastFrame = performance.now();
    } catch (error) {
      failed ||= error;
    }
  };
  try {
    await until(
      () => peer.ready && (app === "foundry" || peer.hasSelf),
      "Connection did not receive its initial state",
    );
  } catch (error) {
    peer.expectedClose = true;
    socket.close();
    throw error;
  }
  return peer;
}
function sendInputs() {
  for (const p of peers) {
    if (
      p.socket.readyState !== WebSocket.OPEN ||
      p.socket.bufferedAmount > 32768
    )
      continue;
    const seq = ++p.seq,
      now = performance.now();
    p.pending.set(seq, now);
    if (p.pending.size > 64) {
      failed ||= Error("Input acknowledgements stalled");
      return;
    }
    if (app === "foundry")
      p.socket.send(
        JSON.stringify({ kind: "sync", seq, points: [], values: [] }),
      );
    else if (app === "gather")
      p.socket.send(
        destination(
          seq,
          512 + Math.cos(now / 1000 + p.id) * 420,
          320 + Math.sin(now / 1300 + p.id) * 250,
        ),
      );
    else
      p.socket.send(
        controls(seq, {
          mx: Math.sin(now / 1000 + p.id) * 0.2,
          mz: Math.cos(now / 1000 + p.id) * 0.2,
          up: 0,
          fly: true,
          jump: false,
          yaw: 0,
          pitch: 0,
        }),
      );
  }
}
async function close(peer) {
  peer.expectedClose = true;
  if (peer.socket.readyState === WebSocket.CLOSED) return;
  const done = new Promise((resolve) =>
    peer.socket.addEventListener("close", resolve, { once: true }),
  );
  peer.socket.close(1000, "Soak reconnect");
  await Promise.race([
    done,
    pause(5000).then(() => {
      throw Error("Close handshake stalled");
    }),
  ]);
}
let inputTimer;
if (childMode) {
  try {
    for (let at = 0; at < count; at += 16)
      peers.push(
        ...(await Promise.all(
          Array.from({ length: Math.min(16, count - at) }, (_, i) =>
            connect(at + i),
          ),
        )),
      );
    inputTimer = setInterval(sendInputs, app === "foundry" ? 100 : 50);
    process.send({ kind: "ready" });
    setInterval(() => {
      if (failed) {
        process.send({ kind: "error", message: failed.message });
        process.exit(1);
      }
      process.send({ kind: "status", frames, acknowledgements });
    }, 1000);
  } catch (error) {
    process.send({ kind: "error", message: error.message });
    process.exit(1);
  }
} else {
  let worker,
    workerFrames = 0,
    reconnects = 0,
    crashes = 0;
  const childCount = Math.max(8, Math.floor(count / 8)),
    mainCount = count - childCount;
  const samples = [],
    pid = process.env.FERN_SOAK_PID;
  const descriptors = async () =>
    pid ? (await readdir(`/proc/${pid}/fd`)).length : null;
  const metrics = () => read("/pulse/snapshot");
  const active = async () => (await metrics()).websocket_connections;
  const percentile = (q) => {
    const sorted = [...latencies].sort((a, b) => a - b);
    return (
      sorted[Math.min(sorted.length - 1, Math.floor(sorted.length * q))] || 0
    );
  };
  async function startWorker() {
    const child = fork(
      fileURLToPath(import.meta.url),
      ["--worker", base, app, String(seconds), String(childCount)],
      { execArgv: [], stdio: ["ignore", "ignore", "inherit", "ipc"] },
    );
    const value = {
      child,
      expected: false,
      ready: false,
      hasSelf: false,
      frames: 0,
      last: performance.now(),
    };
    child.on("message", (message) => {
      value.last = performance.now();
      if (message.kind === "error") failed ||= Error(message.message);
      if (message.kind === "ready") value.ready = true;
      if (message.kind === "status") value.frames = message.frames;
    });
    child.on("error", (error) => {
      failed ||= error;
    });
    child.on("exit", (code, signal) => {
      if (!value.expected) failed ||= Error(`Worker exited: ${code} ${signal}`);
    });
    worker = value;
    await until(() => value.ready, "Client worker did not start", 30000);
  }
  async function killWorker() {
    worker.expected = true;
    workerFrames += worker.frames;
    const exited = new Promise((resolve) => worker.child.once("exit", resolve));
    worker.child.kill("SIGKILL");
    await exited;
    crashes++;
  }
  async function churn() {
    const batch = Math.max(1, Math.floor(mainCount / 16));
    const old = peers.splice(0, batch);
    await Promise.all(old.map(close));
    await until(
      async () => (await active()) === count - batch,
      "Graceful disconnect leaked seats",
    );
    peers.push(...(await Promise.all(old.map((p) => connect(p.index)))));
    reconnects += batch;
    await killWorker();
    await until(
      async () => (await active()) === mainCount,
      "Abrupt client death leaked seats",
    );
    await startWorker();
    await until(
      async () => (await active()) === count,
      "Reconnect did not restore all seats",
    );
  }
  try {
    assert.equal(await active(), 0, "Use a dedicated empty server");
    const idleFd = await descriptors();
    for (let at = 0; at < mainCount; at += 16)
      peers.push(
        ...(await Promise.all(
          Array.from({ length: Math.min(16, mainCount - at) }, (_, i) =>
            connect(at + i),
          ),
        )),
      );
    inputTimer = setInterval(sendInputs, app === "foundry" ? 100 : 50);
    await startWorker();
    if (app === "foundry") {
      const first = peers[0];
      first.socket.send(
        JSON.stringify({
          kind: "resume",
          seq: ++first.seq,
          points: [],
          values: [],
        }),
      );
    }
    await until(
      async () => (await active()) === count,
      "Some participants did not connect",
    );
    const start = performance.now(),
      instance = (await metrics()).instance;
    const warmup = Math.min(15000, (seconds * 1000) / 3),
      cycle = Math.min(30000, (seconds * 1000) / 3);
    let nextChurn = cycle;
    while (performance.now() - start < seconds * 1000) {
      await pause(Math.min(5000, seconds * 1000 - (performance.now() - start)));
      checked();
      if (
        performance.now() - start >= nextChurn &&
        performance.now() - start < seconds * 1000 - 2000
      ) {
        await churn();
        nextChurn += cycle;
      }
      const now = performance.now();
      assert.ok(
        peers.every(
          (p) =>
            p.socket.readyState === WebSocket.OPEN && now - p.lastFrame < 5000,
        ),
        "Participant snapshots stalled",
      );
      assert.ok(now - worker.last < 10000, "Client worker stalled");
      const snapshot = await metrics();
      assert.equal(
        snapshot.instance,
        instance,
        "Server restarted during the test",
      );
      assert.equal(snapshot.websocket_connections, count);
      const sample = {
        seconds: +((now - start) / 1000).toFixed(2),
        rss: snapshot.memory.rss,
        cpu_percent: snapshot.memory.cpu_percent,
        connections: snapshot.websocket_connections,
        descriptors: await descriptors(),
        recent_ack_p95_ms: +percentile(0.95).toFixed(2),
        frames: frames + workerFrames + worker.frames,
      };
      assert.ok(
        sample.recent_ack_p95_ms < 1000,
        "Acknowledgements stalled during the sample window",
      );
      samples.push(sample);
      console.log(JSON.stringify({ app, sample }));
    }
    const steady = samples.filter((s) => s.seconds * 1000 >= warmup),
      first = steady[0],
      last = steady.at(-1);
    assert.ok(Number.isFinite(first.rss), "RSS measurements are required");
    const meanT = steady.reduce((s, p) => s + p.seconds, 0) / steady.length,
      meanR = steady.reduce((s, p) => s + p.rss, 0) / steady.length;
    const denominator = steady.reduce(
      (s, p) => s + (p.seconds - meanT) ** 2,
      0,
    );
    const slope = denominator
      ? ((steady.reduce(
          (s, p) => s + (p.seconds - meanT) * (p.rss - meanR),
          0,
        ) /
          denominator) *
          60) /
        1048576
      : 0;
    const growth = (last.rss - first.rss) / 1048576;
    assert.ok(
      growth <= 32 && !(growth > 8 && slope > 2),
      "Server RSS keeps growing after warmup",
    );
    if (pid)
      assert.ok(
        last.descriptors <= first.descriptors + 8,
        "Descriptors grew under reconnect load",
      );
    assert.ok(
      percentile(0.95) < 1000,
      "Acknowledgements exceeded the one-second stall guard",
    );
    assert.ok(
      acknowledgements >= mainCount * seconds * 5,
      "Too few input acknowledgements",
    );
    console.log(
      JSON.stringify({
        app,
        participants: count,
        seconds,
        graceful_reconnects: reconnects,
        killed_client_processes: crashes,
        acknowledgements,
        recent_ack_p95_ms: +percentile(0.95).toFixed(2),
        server_rss_growth_mib: +growth.toFixed(2),
        rss_slope_mib_per_min: +slope.toFixed(3),
        client_sample_capacity: 8192,
      }),
    );
    clearInterval(inputTimer);
    await Promise.all(peers.map(close));
    await killWorker();
    await until(
      async () => (await active()) === 0,
      "Client cleanup leaked seats",
    );
    if (pid)
      await until(
        async () => (await descriptors()) <= idleFd + 8,
        "Client cleanup leaked descriptors",
      );
    console.log(`${app}: seat cleanup and bounded telemetry OK`);
  } finally {
    clearInterval(inputTimer);
    for (const p of peers) {
      p.expectedClose = true;
      p.socket.close();
    }
    if (worker && !worker.child.killed) {
      worker.expected = true;
      worker.child.kill("SIGKILL");
    }
  }
}
