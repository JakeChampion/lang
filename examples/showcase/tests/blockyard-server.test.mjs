import assert from "node:assert/strict";
import { controls, edit, decode } from "../web/blockyard/protocol.mjs";
const base = process.argv[2] || "http://localhost:8080",
  peers = [];
async function until(check, message) {
  const end = performance.now() + 6000;
  while (performance.now() < end) {
    if (check()) return;
    await new Promise((r) => setTimeout(r, 20));
  }
  throw Error(message);
}
async function join(world = "commons") {
  const socket = new WebSocket(
      base.replace(/^http/, "ws") + `/blockyard/ws?world=${world}`,
    ),
    peer = { socket, messages: [], frame: null, seq: 0 };
  peers.push(peer);
  socket.binaryType = "arraybuffer";
  socket.onmessage = ({ data }) => {
    if (typeof data === "string") {
      const value = JSON.parse(data);
      peer.messages.push(value);
      if (value.kind === "welcome") peer.welcome = value;
    } else {
      const value = decode(data);
      if (value.kind === "snapshot") peer.frame = value;
    }
  };
  await until(() => peer.welcome, "no welcome");
  return peer;
}
try {
  const a = await join(),
    b = await join(),
    other = await join("meadow");
  assert.notEqual(a.welcome.id, b.welcome.id);
  assert.equal(a.welcome.persistent, true);
  await until(
    () =>
      a.frame?.total === 2 && b.frame?.total === 2 && other.frame?.total === 1,
    "shared or isolated counts differ",
  );
  await until(
    () => a.frame.players.find((p) => p.id === a.welcome.id)?.grounded,
    "player did not reach the ground",
  );
  const own = a.frame.players.find((p) => p.id === a.welcome.id),
    target = { x: Math.floor(own.x), y: 13, z: Math.floor(own.z - 0.08) };
  a.socket.send(
    controls(++a.seq, {
      mx: 0,
      mz: 0,
      up: 0,
      fly: false,
      jump: false,
      yaw: 0,
      pitch: -1.48,
    }),
  );
  a.socket.send(edit(1, 0, 3, target));
  await until(
    () =>
      a.messages.some(
        (m) =>
          m.kind === "edits" &&
          m.blocks.some((c) => c.actor === a.welcome.id && c.nonce === 1),
      ),
    "authoritative edit was not confirmed",
  );
  await until(
    () =>
      b.messages.some(
        (m) =>
          m.kind === "edits" &&
          m.blocks.some((c) => c.actor === a.welcome.id && c.nonce === 1),
      ),
    "second participant did not receive the edit",
  );
  assert.equal(
    other.messages.some((m) => m.kind === "edits"),
    false,
    "edits crossed worlds",
  );
  const path = `/blockyard/chunk?x=${target.x >> 4}&z=${target.z >> 4}`,
    response = await fetch(base + path),
    chunk = await response.json(),
    at = (target.y * 16 + (target.z & 15)) * 16 + (target.x & 15);
  assert.ok(chunk.blocks.some((b) => b.at === at && b.material === 0));
  assert.equal(
    (
      await fetch(base + path, {
        headers: { "If-None-Match": response.headers.get("etag") },
      })
    ).status,
    304,
  );
  a.socket.send(edit(2, 0, 3, { x: 1, y: 1, z: 1 }));
  await until(
    () => a.messages.some((m) => m.kind === "reject" && m.nonce === 2),
    "distant edit was not rejected",
  );
  assert.equal(
    (await fetch(base + "/blockyard/status?world=unknown")).status,
    404,
  );
  assert.equal((await fetch(base + "/blockyard/chunk?x=32")).status, 400);
  assert.equal((await fetch(base + "/blockyard/chunk?x=no")).status, 400);
  console.log(
    JSON.stringify({ path, at, material: 0, revision: chunk.revision }),
  );
} finally {
  for (const p of peers) p.socket.close();
}
