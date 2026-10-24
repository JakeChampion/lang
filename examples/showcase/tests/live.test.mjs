import assert from "node:assert/strict";
import { paddle, reconcile, ball } from "../web/rally/prediction.mjs";

assert.equal(paddle(0.5, 1, 1 / 60), 0.5 + 1.6 / 60);
assert.equal(paddle(0.89, 5, 0.05), 0.895);
assert.equal(
  reconcile(
    0.4,
    [
      { seq: 1, y: 1, dt: 0.02 },
      { seq: 2, y: 0, dt: 0.02 },
    ],
    1,
  ),
  0.368,
);
const reflected = ball(
  { x: 0.5, y: 0.98, vx: 0.4, vy: 0.5, running: true },
  0.1,
);
assert.ok(reflected.y < 0.986 && reflected.x > 0.5);
console.log("prediction, reconciliation, and wall reflection OK");

if (process.argv[2]) {
  const base = process.argv[2].replace(/^http/, "ws");
  const peers = [];
  async function connect(path) {
    const socket = new WebSocket(base + path),
      messages = [],
      waiters = [];
    peers.push(socket);
    socket.onmessage = ({ data }) => {
      const value = JSON.parse(data);
      const index = waiters.findIndex((w) => w.accept(value));
      if (index >= 0) {
        const [w] = waiters.splice(index, 1);
        clearTimeout(w.timer);
        w.resolve(value);
      } else messages.push(value);
    };
    await new Promise((resolve, reject) => {
      socket.onopen = resolve;
      socket.onerror = reject;
    });
    return {
      socket,
      send: (value) => socket.send(JSON.stringify(value)),
      next: (accept = () => true) => {
        const index = messages.findIndex(accept);
        if (index >= 0) return Promise.resolve(messages.splice(index, 1)[0]);
        return new Promise((resolve, reject) => {
          const waiter = {
            accept,
            resolve,
            timer: setTimeout(
              () => reject(Error("No matching push: " + path)),
              3000,
            ),
          };
          waiters.push(waiter);
        });
      },
    };
  }
  try {
    const room = "ws-" + Date.now();
    const a = await connect(`/mosaic/ws?room=${room}`),
      b = await connect(`/mosaic/ws?room=${room}`);
    assert.equal((await a.next()).kind, "snapshot");
    assert.equal((await b.next()).kind, "snapshot");
    const at = performance.now();
    a.send({ kind: "paint", seq: 1, color: 7, points: [0, 1] });
    assert.equal((await a.next((v) => v.kind === "ack")).seq, 1);
    const change = await b.next((v) => v.kind === "paint");
    assert.deepEqual(change.data.points, [0, 1]);
    assert.equal(change.data.color, 7);
    console.log(`peer paint push: ${(performance.now() - at).toFixed(2)} ms`);
    for (let seq = 2; seq <= 65; seq++)
      a.send({ kind: "paint", seq, color: seq % 12, points: [seq] });
    for (let seq = 2; seq <= 65; seq++)
      assert.equal((await a.next((v) => v.kind === "ack")).seq, seq);
    const last = await b.next(
      (v) => v.kind === "paint" && v.data.revision === 65,
    );
    assert.deepEqual(last.data.points, [65]);
    a.send({ kind: "paint", seq: 66, color: 1, points: [2560] });
    assert.equal((await a.next((v) => v.kind === "error")).seq, 66);
    a.send({ kind: "ping", seq: 67, color: 0, points: [] });
    assert.equal((await a.next((v) => v.kind === "pong")).revision, 65);
    const left = await connect("/rally/ws");
    left.send({ kind: "join", room: "", mode: "duo", y: 0.5, seq: 1 });
    const joined = await left.next();
    assert.equal(joined.seat, 0);
    const right = await connect("/rally/ws");
    right.send({
      kind: "join",
      room: joined.room,
      mode: "duo",
      y: 0.5,
      seq: 1,
    });
    assert.equal((await right.next()).seat, 1);
    assert.equal((await left.next((v) => v.players === 2)).players, 2);
    left.send({
      kind: "start",
      room: joined.room,
      mode: "duo",
      y: 0.2,
      seq: 2,
    });
    assert.equal((await right.next((v) => v.game.running)).game.running, true);
    left.send({
      kind: "tick",
      room: joined.room,
      mode: "duo",
      y: 0.895,
      seq: 3,
    });
    const moving = await left.next((v) => v.seq === 3);
    await new Promise((resolve) => setTimeout(resolve, 35));
    right.send({
      kind: "tick",
      room: joined.room,
      mode: "duo",
      y: 0.5,
      seq: 2,
    });
    const continued = await right.next((v) => v.seq === 2);
    assert.ok(
      continued.game.left > moving.game.left + 0.03,
      "both paddles must advance on either player's input",
    );
    right.socket.close();
    assert.equal((await left.next((v) => v.players === 1)).game.running, false);
    console.log(
      "WebSocket snapshot, burst, atomic validation, peer push, and seat release OK",
    );
  } finally {
    for (const p of peers) p.close();
  }
}
