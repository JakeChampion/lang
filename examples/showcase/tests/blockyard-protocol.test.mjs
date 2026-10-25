import test from "node:test";
import assert from "node:assert/strict";
import { controls, edit, decode, ray } from "../web/blockyard/protocol.mjs";
import { meshChunk } from "../web/blockyard/mesh.mjs";

test("controls and edits have exact little-endian layouts", () => {
  const bytes = new DataView(
    controls(0x12345678, {
      mx: 1,
      mz: -1,
      up: 0,
      jump: true,
      fly: true,
      yaw: Math.PI,
      pitch: 0,
    }),
  );
  assert.equal(bytes.byteLength, 16);
  assert.equal(bytes.getUint8(1), 3);
  assert.equal(bytes.getInt8(2), 127);
  assert.equal(bytes.getInt8(3), -127);
  assert.equal(bytes.getUint16(6, true), 32768);
  assert.equal(bytes.getUint32(10, true), 0x12345678);
  const block = new DataView(edit(9, 1, 7, { x: 256, y: 14, z: 257 }));
  assert.equal(block.getUint8(0), 2);
  assert.equal(block.getUint8(1), 1);
  assert.equal(block.getUint16(4, true), 256);
  assert.equal(block.getUint16(8, true), 257);
  assert.equal(block.getUint32(10, true), 9);
});
test("snapshots validate counts, coordinates, and velocity flags", () => {
  const bytes = new ArrayBuffer(32),
    v = new DataView(bytes);
  v.setUint8(0, 66);
  v.setUint8(1, 80);
  v.setUint8(2, 1);
  v.setUint32(4, 8, true);
  v.setUint16(8, 1, true);
  v.setUint16(10, 512, true);
  v.setUint16(16, 1, true);
  v.setUint16(18, 256 * 64, true);
  v.setUint16(20, 14 * 64, true);
  v.setUint16(22, 256 * 64, true);
  v.setInt16(26, -512 * 4 + 1, true);
  v.setUint32(28, 42, true);
  const frame = decode(bytes);
  assert.equal(frame.players[0].vy, -2);
  assert.equal(frame.players[0].fly, true);
  assert.equal(frame.players[0].seq, 42);
  assert.equal(frame.total, 512);
  assert.throws(() => decode(bytes.slice(0, 31)), /count/);
  v.setUint16(8, 513, true);
  assert.throws(() => decode(bytes), /count/);
});
test("greedy meshes remove internal faces and combine adjacent faces", () => {
  const data = new Uint8Array(16 * 48 * 16);
  data[(4 * 16 + 5) * 16 + 3] = 3;
  const one = meshChunk(data, 0, 0);
  assert.equal(one.opaque.length / 8 / 3, 12);
  assert.equal(one.alpha.length, 0);
  data[(4 * 16 + 5) * 16 + 4] = 3;
  const two = meshChunk(data, 0, 0);
  assert.equal(two.opaque.length / 8 / 3, 12);
  data.fill(3);
  const filled = meshChunk(data, 0, 0);
  assert.equal(filled.opaque.length / 8 / 3, 12);
  const xs = [],
    ys = [],
    zs = [];
  for (let i = 0; i < filled.opaque.length; i += 8) {
    xs.push(filled.opaque[i]);
    ys.push(filled.opaque[i + 1]);
    zs.push(filled.opaque[i + 2]);
  }
  assert.deepEqual(
    [
      Math.min(...xs),
      Math.max(...xs),
      Math.min(...ys),
      Math.max(...ys),
      Math.min(...zs),
      Math.max(...zs),
    ],
    [0, 16, 0, 48, 0, 16],
  );
});
test("neighbor chunks suppress shared faces and glass uses a separate pass", () => {
  const data = new Uint8Array(16 * 48 * 16);
  data[(4 * 16 + 4) * 16] = 3;
  const adjacent = meshChunk(data, 0, 0, (x, y, z) =>
    x === -1 && y === 4 && z === 4 ? 3 : 0,
  );
  assert.equal(adjacent.opaque.length / 8 / 3, 10);
  data.fill(0);
  data[(4 * 16 + 4) * 16 + 4] = 8;
  const glass = meshChunk(data, 0, 0);
  assert.equal(glass.opaque.length, 0);
  assert.equal(glass.alpha.length / 8 / 3, 12);
});
test("grid rays stop at the first solid cell and preserve the adjacent face", () => {
  const hit = ray(
    (x, y, z) => (z === 3 ? 3 : 0),
    { x: 5.5, y: 2, z: 5.5 },
    0,
    0,
  );
  assert.deepEqual([hit.x, hit.y, hit.z], [5, 3, 3]);
  assert.deepEqual(hit.adjacent, { x: 5, y: 3, z: 4 });
  assert.equal(
    ray(() => 0, { x: 5, y: 2, z: 5 }, 0, 0),
    null,
  );
});
