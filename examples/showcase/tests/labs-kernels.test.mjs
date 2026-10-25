import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { instantiateCore } from "../web/shared/wasm.mjs";
const directory =
  process.argv[2] || new URL("../web/labs/", import.meta.url).pathname;
const kernels = {};
for (const name of ["foundry", "circuit", "patchbay", "prism"]) {
  const api = await instantiateCore(
    await readFile(join(directory, name + ".wasm")),
  );
  kernels[name] = {
    api,
    fn: (member) => api[`fern:showcase/${name}#${member}`],
  };
}
const gates = kernels.circuit.fn("gate");
for (let kind = 0; kind < 10; kind++)
  for (let a = 0; a < 2; a++)
    for (let b = 0; b < 2; b++)
      for (let previous = 0; previous < 2; previous++) {
        const reference = [
          b ? a : previous,
          a,
          1 - a,
          a & b,
          a | b,
          a ^ b,
          1 - (a & b),
          1 - (a | b),
          0,
          1,
        ][kind];
        assert.equal(gates(kind, a, b, previous), reference, `gate ${kind}`);
      }
const alu = kernels.circuit.fn("alu");
for (let a = 0; a < 16; a++)
  for (let b = 0; b < 16; b++) {
    assert.equal(alu(2, a, b), a + b);
    assert.equal(alu(3, a, b), a ^ b);
    assert.equal(alu(4, a, b), a & b);
  }
const cpu = kernels.circuit.fn("cpu");
let state = 0;
for (const instruction of [0x11, 0x22, 0x23, 0x70])
  state = cpu(instruction, state & 15, (state >>> 4) & 15);
assert.equal(state & 15, 6);
assert.ok(state & 512);
assert.ok(cpu(0x80, 6, 4) & 256);
assert.equal((cpu(0x5a, 0, 0) >>> 4) & 15, 10);
assert.equal((cpu(0x5a, 1, 0) >>> 4) & 15, 1);
const sample = kernels.patchbay.fn("sample"),
  hz = kernels.patchbay.fn("sample-hz"),
  frequency = kernels.patchbay.fn("frequency");
for (let wave = 0; wave < 4; wave++)
  for (let note = 0; note < 128; note++)
    for (const frame of [0, 1, 127, 2048, 47999]) {
      const value = sample(frame, 48000, note, wave, frame / 48000, -1);
      assert.ok(Number.isFinite(value) && Math.abs(value) < 0.3);
      assert.ok(
        Math.abs(
          value -
            hz(frame, 48000, frequency(note, 48000), wave, frame / 48000, -1),
        ) < 1e-12,
      );
    }
let crossings = 0,
  previous = 0;
for (let frame = 0; frame < 48000; frame++) {
  const value = sample(frame, 48000, 69, 0, frame / 48000, -1);
  if (previous <= 0 && value > 0) crossings++;
  previous = value;
}
assert.ok(Math.abs(crossings - 440) <= 1, `A4 frequency ${crossings}`);
for (const rate of [0, 1, 7999, 192001])
  assert.equal(sample(1, rate, 69, 0, 0.01, -1), 0);
const foundry = kernels.foundry.fn;
assert.equal(foundry("output")(9, 2, 8, 8), 6);
assert.equal(foundry("rail-path")(9, 9, 9, 11), 1);
assert.equal(foundry("rail-path")(9, 0, 9, 11), 0);
assert.equal(foundry("accepts")(7, 16), 0);
assert.equal(foundry("accepts")(7, 1), 1);
let smelter = 2;
for (let i = 0; i < 8; i++) smelter = foundry("state")(7, smelter, 0, 0, i, 1);
assert.equal(smelter, 16);
assert.equal(foundry("units")(smelter), 2);
smelter = 2;
for (let i = 0; i < 20; i++) smelter = foundry("state")(7, smelter, 0, 0, i, 0);
assert.equal(smelter, 2);
const ray = kernels.prism.fn("pixel");
const before = Object.fromEntries(
  Object.entries(kernels).map(([name, k]) => [
    name,
    k.api.memory.buffer.byteLength,
  ]),
);
let checksum = 0;
for (let sample = 0; sample < 8; sample++)
  for (let y = 0; y < 20; y++)
    for (let x = 0; x < 32; x++) {
      const pixel = ray(x, y, 32, 20, sample, 6324352, 0);
      assert.equal(pixel, ray(x, y, 32, 20, sample, 6324352, 0));
      assert.ok(pixel >= 0 && pixel < 2 ** 30);
      checksum = (checksum + pixel) >>> 0;
    }
for (const [name, k] of Object.entries(kernels))
  assert.equal(
    k.api.memory.buffer.byteLength,
    before[name],
    `${name} grew memory`,
  );
console.log(
  "truth tables, four-bit programs, prepared audio, pitch, powered refining, rails, deterministic rays, and fixed Wasm memory OK; ray checksum",
  checksum,
);
