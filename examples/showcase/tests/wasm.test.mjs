import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { instantiate } from "../web/swarm/kernel.mjs";
const path =
  process.argv[2] || new URL("../web/swarm/kernel.wasm", import.meta.url);
const bytes = await readFile(path),
  { particle, sample, memory } = await instantiate(bytes);
assert.equal(typeof particle, "function");
assert.equal(typeof sample, "function");
const original = particle(42, 1, 0.5, 0.5, 0, 0);
assert.equal(original, particle(42, 1, 0.5, 0.5, 0, 0));
assert.notEqual(original, particle(42, 2, 0.5, 0.5, 0, 0));
assert.notEqual(original, particle(42, 1, 0.2, 0.3, 1, 0));
assert.equal(sample(0, 24000, 220), 0);
assert.equal(sample(1, 0, 220), 0);
let peak = 0,
  rms = 0;
for (let i = 0; i < 24000; i++) {
  const value = sample(i, 24000, 220);
  assert.ok(Number.isFinite(value) && Math.abs(value) <= 1);
  peak = Math.max(peak, Math.abs(value));
  rms += value * value;
}
assert.ok(peak > 0.1 && Math.sqrt(rms / 24000) > 0.02);
assert.notEqual(sample(100, 24000, 165), sample(100, 24000, 330));
const before = memory.buffer.byteLength,
  at = performance.now();
for (let frame = 0; frame < 240; frame++)
  for (let i = 0; i < 4096; i++)
    particle(i, frame / 60, 0.5, 0.5, frame % 2, 0.2);
assert.equal(
  memory.buffer.byteLength,
  before,
  "four seconds of field updates must not grow linear memory",
);
console.log(
  `Fern Wasm: ${bytes.length} bytes, ${((performance.now() - at) / 240).toFixed(3)} ms / 4096 particles, stable ${before} byte memory`,
);
