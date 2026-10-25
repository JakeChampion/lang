import assert from "node:assert/strict";
export async function until(predicate, message, timeout = 10000) {
  const end = performance.now() + timeout;
  while (performance.now() < end) {
    if (predicate()) return;
    await new Promise((r) => setTimeout(r, 10));
  }
  throw Error(message);
}
export function factoryPacket(buffer, previous) {
  const bytes = Buffer.from(buffer),
    v = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  assert.equal(bytes.toString("ascii", 0, 2), "FF");
  assert.equal(v.getUint8(2), 2);
  const count = v.getUint32(76, true),
    items = v.getUint32(84, true),
    delta = !!v.getUint8(3),
    sequence = Number(v.getBigUint64(56, true)),
    base = Number(v.getBigUint64(64, true));
  assert.equal(count, 65536);
  assert.equal(bytes.length, 96 + items * (delta ? 5 : 3));
  if (delta) assert.equal(previous?.sequence, base, "a delta skipped its base");
  const cargo = delta ? previous.cargo : new Uint16Array(count),
    power = delta ? previous.power : new Uint8Array(count);
  for (let i = 0; i < items; i++) {
    let offset = 96 + i * (delta ? 5 : 3),
      index = delta ? v.getUint16(offset, true) : i;
    if (delta) offset += 2;
    assert.ok(index < count);
    cargo[index] = v.getUint16(offset, true);
    power[index] = v.getUint8(offset + 2);
    assert.ok((cargo[index] & 15) + ((cargo[index] >>> 4) & 15) <= 15);
    assert.ok(power[index] === 0 || power[index] === 1);
  }
  const state = {
    cargo,
    power,
    sequence,
    tick: Number(v.getBigUint64(8, true)),
    produced: Number(v.getBigUint64(16, true)),
    consumed: Number(v.getBigUint64(24, true)),
    removed: Number(v.getBigUint64(32, true)),
    refined: Number(v.getBigUint64(40, true)),
    running: !!v.getUint32(4, true),
    bytes: bytes.length,
    delta,
  };
  let inventory = 0;
  for (let i = 0; i < count; i++)
    inventory += (cargo[i] & 15) + 2 * ((cargo[i] >>> 4) & 15);
  assert.equal(
    state.produced,
    state.consumed + state.removed + inventory,
    "factory lost or duplicated cargo",
  );
  return state;
}
export async function join(base, app, room) {
  const socket = new WebSocket(
      base.replace(/^http/, "ws") + `/${app}/ws?room=${room}`,
    ),
    peer = {
      socket,
      seq: 0,
      messages: [],
      snapshot: null,
      frame: null,
      error: null,
      sent: new Map(),
      latencies: [],
      frames: 0,
      bytes: 0,
    };
  socket.binaryType = "arraybuffer";
  socket.onmessage = ({ data }) => {
    try {
      if (typeof data === "string") {
        const message = JSON.parse(data);
        peer.messages.push(message);
        if (peer.messages.length > 512) peer.messages.shift();
        if (message.kind === "snapshot") peer.snapshot = message;
        if (message.kind === "change")
          for (let i = 0; i < message.points.length; i++)
            peer.snapshot.cells[message.points[i]] = message.values[i];
        if (message.kind === "program")
          for (let i = 0; i < message.points.length; i++)
            peer.snapshot.program[message.points[i]] = message.values[i];
        if (message.kind === "ack") {
          const at = peer.sent.get(message.seq);
          if (at !== undefined) {
            peer.latencies.push(performance.now() - at);
            peer.sent.delete(message.seq);
          }
        }
      } else {
        peer.frame = factoryPacket(data, peer.frame);
        peer.frames++;
        peer.bytes += data.byteLength;
      }
    } catch (error) {
      peer.error = error;
    }
  };
  socket.onerror = (e) => {
    peer.error = Error(e.message || "WebSocket error");
  };
  peer.command = (kind, points = [], values = []) => {
    const seq = ++peer.seq;
    peer.sent.set(seq, performance.now());
    socket.send(JSON.stringify({ kind, seq, points, values }));
    return seq;
  };
  await until(() => peer.snapshot || peer.error, "no project snapshot");
  if (peer.error) throw peer.error;
  return peer;
}
