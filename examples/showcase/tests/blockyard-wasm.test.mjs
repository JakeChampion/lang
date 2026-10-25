import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { instantiate, predict, ray } from "../web/blockyard/protocol.mjs";
const kernel = await instantiate(await readFile(process.argv[2]));
assert.equal(kernel.height(256, 256), 13);
assert.equal(kernel.terrain(256, 0, 256), 3);
assert.equal(kernel.terrain(256, 30, 256), 0);
assert.equal(kernel.terrain(-1, 14, 256), 3);
assert.equal(kernel.solid(11), 0);
assert.equal(kernel.solid(8), 1);
assert.ok(Math.abs(kernel.clip(2.5, 2, 2.5, 0.4, 0, 1 << 14, 0) - 2.7) < 1e-8);
const get = (x, y, z) => (y < 1 || (x === 3 && y < 6) ? 3 : 0);
const input = { mx: 1, mz: 0, up: 0, jump: false, fly: false };
let player = { x: 2.5, y: 1, z: 2.5, vy: 0, grounded: true };
player = predict(kernel, get, player, input, 0.1);
assert.ok(player.x <= 2.700001, "a one-block wall must stop movement");
assert.ok(Math.abs(player.y - 1) < 1e-8);
player = predict(kernel, get, player, { ...input, mx: 0, jump: true }, 0.025);
assert.ok(player.y > 1, "jump must leave the floor");
player = { x: 5, y: 15, z: 5, vy: -24, grounded: false };
for (let i = 0; i < 100; i++)
  player = predict(kernel, get, player, { ...input, mx: 0 }, 0.1);
assert.ok(
  Math.abs(player.y - 1) < 1e-8,
  "a fast fall must not cross a thin floor",
);
assert.equal(player.grounded, true);
const hit = ray(
  (x, y, z) => kernel.terrain(x, y, z),
  { x: 256.5, y: 14, z: 256.5 },
  0,
  -1.48,
);
assert.deepEqual([hit.x, hit.y, hit.z], [256, 13, 256]);
assert.equal(
  ray(() => 0, { x: 256, y: 14, z: 256 }, 0, 0),
  null,
);
const memory = kernel.memory.buffer.byteLength;
let checksum = 2166136261 >>> 0;
for (let z = 224; z < 288; z++)
  for (let x = 224; x < 288; x++)
    for (let y = 0; y < 48; y++) {
      const material = kernel.terrain(x, y, z);
      assert.ok(material >= 0 && material <= 11);
      checksum = Math.imul(checksum ^ material, 16777619) >>> 0;
    }
assert.equal(
  kernel.memory.buffer.byteLength,
  memory,
  "scalar terrain and collision must retain a fixed Wasm memory size",
);
console.log(
  JSON.stringify({
    terrain_checksum: checksum,
    wasm_memory_bytes: memory,
    collision: "wall, floor, jump, and grid ray OK",
  }),
);
