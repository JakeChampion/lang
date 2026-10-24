import test from "node:test";
import assert from "node:assert/strict";
import {
  decode,
  destination,
  move,
  reconcile,
} from "../web/gather/protocol.mjs";
test("destinations have exact little-endian coordinates and bounds", () => {
  assert.deepEqual(
    [...new Uint8Array(destination(0x12345678, 512, 320))],
    [0x78, 0x56, 0x34, 0x12, 0, 32, 0, 20],
  );
  assert.deepEqual(
    [...new Uint8Array(destination(1, 9000, -5))],
    [1, 0, 0, 0, 0, 64, 0, 0],
  );
});
test("snapshot contract and malformed lengths", () => {
  const bytes = new ArrayBuffer(32 + 12 + 320),
    v = new DataView(bytes);
  v.setUint8(0, 70);
  v.setUint8(1, 67);
  v.setUint8(2, 1);
  v.setUint32(4, 42, true);
  v.setUint16(8, 3, true);
  v.setUint16(10, 1, true);
  v.setUint32(12, 120000, true);
  v.setUint32(16, 12, true);
  v.setUint32(20, 2048, true);
  v.setUint16(24, 64, true);
  v.setUint16(26, 1024, true);
  v.setUint16(28, 640, true);
  v.setUint16(32, 9, true);
  v.setUint16(34, 8192, true);
  v.setUint16(36, 5120, true);
  v.setUint16(38, 7, true);
  v.setUint32(40, 1234, true);
  const frame = decode(bytes);
  assert.equal(frame.tick, 42);
  assert.equal(frame.round, 3);
  assert.equal(frame.score, 12);
  assert.deepEqual(frame.players, [
    { id: 9, x: 512, y: 320, score: 7, seq: 1234 },
  ]);
  assert.equal(frame.seeds.length, 64);
  assert.throws(() => decode(bytes.slice(0, -1)), /length/);
  assert.throws(() => decode(new ArrayBuffer(2)));
  v.setUint8(2, 99);
  assert.throws(() => decode(bytes));
});
test("prediction bounds elapsed time and replays only unacknowledged input", () => {
  assert.deepEqual(move({ x: 0, y: 0 }, { x: 100, y: 0 }, 9), { x: 18, y: 0 });
  assert.deepEqual(
    reconcile({ x: 0, y: 0, seq: 2 }, [
      { seq: 1, x: 100, y: 0, dt: 0.05 },
      { seq: 3, x: 100, y: 0, dt: 0.05 },
    ]),
    { x: 9, y: 0 },
  );
});
