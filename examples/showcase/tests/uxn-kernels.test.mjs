import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { bind, layout as L, putword, word } from "../web/uxn/kernel.mjs";
import { demos } from "../web/uxn/demos.mjs";
const module = await WebAssembly.compile(
  await readFile(new URL("../web/uxn/kernel.wasm", import.meta.url)),
);
const api = bind(module);
const fixture = JSON.parse(
  await readFile(new URL("./fixtures/uxn-0k.json", import.meta.url)),
);

function sample(op, seed) {
  const s = new Uint8Array(L.size);
  let n = seed + 1;
  for (let i = 0; i < L.meta; i++) {
    n ^= n << 13;
    n ^= n >>> 17;
    n ^= n << 5;
    s[i] = n & 255;
  }
  s[L.meta + 2] = [0, 1, 254, 255][seed];
  s[L.meta + 3] = [255, 254, 1, 0][seed];
  s[L.meta + 4] = 1;
  putword(s, L.meta, 1024);
  s[1024] = op;
  // The reference has no Varvara host: device tests use inert ports.
  if ((op & 31) === 22 || (op & 31) === 23) {
    const base = op & 64 ? L.rst : L.wst,
      ptr = s[L.meta + (op & 64 ? 3 : 2)];
    s[base + ((ptr - 1) & 255)] = 0xe0 + seed;
  }
  return s;
}
function snapshot(s) {
  return Buffer.concat([
    s.subarray(0, L.meta),
    s.subarray(L.meta + 2, L.meta + 4),
    s.subarray(L.meta, L.meta + 2),
    s.subarray(L.meta + 4, L.meta + 5),
  ]);
}
for (let op = 0; op < 256; op++) {
  const hash = createHash("sha256");
  for (let seed = 0; seed < 4; seed++)
    hash.update(snapshot(await api.run(sample(op, seed), 1)));
  assert.equal(
    hash.digest("hex"),
    fixture.hashes[op],
    `Uxn 0K reference opcode ${op.toString(16)}`,
  );
}
console.log(
  "All 256 instruction bytes match Uxn 0K reference snapshots across 1,024 stack-boundary cases",
);

async function execute(source, budget = 20000) {
  return api.run(await api.boot(await api.assemble(source)), budget);
}
let s = await execute(demos.arithmetic.source);
assert.equal(word(s, L.wst), 720);
assert.equal(word(s, 0x200), 720);
assert.equal(s[L.meta + 2], 2);
s = await execute(demos.hello.source);
assert.equal(
  new TextDecoder().decode(
    s.slice(L.console, L.console + word(s, L.meta + 12)),
  ),
  "Hello, Uxn!\nRunning inside Fern WebAssembly.\n",
);
assert.equal(s[L.meta + 2], 0);
// A zero-page short wraps to 0x00, an absolute short to 0x0000.
s = await execute(
  "|0100 #1234 #ff STZ2 #ff LDZ2 #abcd #ffff STA2 #ffff LDA2 BRK",
);
assert.deepEqual([...s.slice(L.wst, L.wst + 4)], [0x12, 0x34, 0xab, 0xcd]);
assert.equal(s[0], 0xcd);
assert.equal(s[255], 0x12);
// Jumps and literal fetches also wrap the program counter.
s = await api.boot(Uint8Array.of(0));
s[65535] = 0xa0;
s[0] = 0x12;
s[1] = 0x34;
s = await api.run(s, 1, 65535);
assert.equal(word(s, L.meta), 2);
assert.equal(word(s, L.wst), 0x1234);
// Division by zero, keep mode, and return-stack calls.
s = await execute(
  "|0100 #07 #00 DIV #02 #03 ADDk ;sub JSR2 BRK @sub #09 STHr JMP2r",
);
assert.deepEqual([...s.slice(L.wst, L.wst + 5)], [0, 2, 3, 5, 9]);
s = await execute("|0100 #04 DEI BRK");
assert.equal(s[L.wst], 0, "System/wst reads after consuming the port");
s = await execute("|0100 #06 #04 DEO BRK");
assert.equal(s[L.meta + 2], 6, "System/wst replaces stack index");
// DEO2 calls only the low port, and DEI2 only the high port.
s = await execute("|0100 #4142 #18 DEO2 BRK");
assert.equal(word(s, L.meta + 12), 1);
assert.equal(s[L.console], 0x42);
s = await execute("|0100 #0000 #28 DEO2 #0000 #2a DEO2 #03 #2e DEO BRK");
assert.equal(s[L.screen], 3);
s = await execute(demos.paint.source);
assert.ok(
  s.slice(L.screen).some((n) => n === 3),
  "heart sprite",
);
// 1bpp and 2bpp blend table, foreground transparency, flip and automatic address.
const spriteColors = [
  [0, 0, 1, 2],
  [0, 1, 2, 3],
  [0, 2, 3, 1],
  [0, 3, 1, 2],
  [1, 0, 1, 2],
  [1, 1, 2, 3],
  [1, 2, 3, 1],
  [1, 3, 1, 2],
  [2, 0, 1, 2],
  [2, 1, 2, 3],
  [2, 2, 3, 1],
  [2, 3, 1, 2],
  [3, 0, 1, 2],
  [3, 1, 2, 3],
  [3, 2, 3, 1],
  [3, 3, 1, 2],
];
for (const mode of [0, 128])
  for (let color = 0; color < 16; color++) {
    s = await api.boot(Uint8Array.of(0));
    putword(s, L.dev + 0x2c, 0x200);
    s[0x200] = 0xc0;
    s[0x208] = 0x60;
    // Inject a DEO via a small program so Fern performs the device effect.
    s.set([0x80, mode | color, 0x80, 0x2f, 0x17, 0], 0x100);
    s = await api.run(s, 100);
    const channels = mode ? [1, 3, 2] : [1, 1, 0];
    for (let x = 0; x < channels.length; x++) {
      const channel = channels[x];
      const expected =
        channel === 0 && color % 5 === 0 ? 0 : spriteColors[color][channel];
      assert.equal(
        s[L.screen + x],
        expected,
        `sprite ${mode} ${color} channel ${channel}`,
      );
    }
  }
s = await execute(
  "|0100 #0000 #28 DEO2 #0000 #2a DEO2 #82 #2e DEO #0002 #28 DEO2 #0002 #2a DEO2 #43 #2e DEO BRK",
);
assert.equal(s[L.screen], 2);
assert.equal(s[L.screen + 2 * 512 + 2], 14);
s = await execute("|0100 #0002 #28 DEO2 #0002 #2a DEO2 #b1 #2e DEO BRK");
assert.equal(s[L.screen], 1);
assert.equal(s[L.screen + 1 + 512], 1);
assert.equal(
  s[L.screen + 2 + 2 * 512],
  0,
  "quadrant fill excludes opposite edge",
);
s = await execute(
  "|0100 #000a #28 DEO2 #000a #2a DEO2 ;tile #2c DEO2 #15 #26 DEO #01 #2f DEO BRK @tile 80 00 00 00 00 00 00 00 80 00 00 00 00 00 00 00",
);
assert.equal(s[L.screen + 10 + 10 * 512], 1);
assert.equal(
  s[L.screen + 10 + 18 * 512],
  1,
  "sprite repeats move perpendicular to final auto increment",
);
assert.equal(word(s, L.dev + 0x28), 18);
assert.equal(word(s, L.dev + 0x2a), 10);
s = await execute("|0100 #ffff #22 DEO2 #0000 #24 DEO2 BRK");
assert.equal(word(s, L.dev + 0x22), 512);
assert.equal(word(s, L.dev + 0x24), 1);
s = await execute(
  "|0100 #01 #3f DEO #01 #ad DEO #0000 #02 DEO2 #01 #1f DEO BRK",
);
assert.equal(s[L.meta + 14], 15);
s = await execute(
  "|0100 #ff #22 DEO #ff #24 DEO #ffff #28 DEO2 #ffff #2a DEO2 #03 #2e DEO BRK",
);
assert.equal(
  word(s, L.meta + 16),
  256,
  "high-port writes cannot change actual screen bounds",
);
assert.equal(word(s, L.meta + 18), 192);
s = await execute("|0100 #80 #0f DEO #41 #18 DEO BRK", 3);
assert.equal(s[L.dev + 15], 128);
assert.equal(s[L.meta + 4], 1);
s = await api.run(s, 100);
assert.equal(s[L.meta + 4], 0);
assert.equal(
  s[L.console],
  65,
  "System/state finishes the current vector before exit",
);
// Long-running programs yield exactly at the budget, then resume.
s = await execute("|0100 @loop INC !loop", 17);
assert.equal(s[L.meta + 4], 1);
assert.equal(word(s, L.meta + 10), 17);
s = await api.run(s, 13);
assert.equal(word(s, L.meta + 10), 30);
// Relative runes are based on the PC after the instruction consuming the offset.
s = await execute("|0100 ,data LDR BRK @data 5a");
assert.equal(s[L.wst], 0x5a);
s = await execute("|0100 ( (nested) ) [named ] #01 DUPkk ADD #02 EQU BRK");
assert.equal(s[L.wst], 1);
assert.deepEqual(
  [...(await api.assemble("|0100 .high BRK |89ab @high"))].slice(0, 3),
  [0x80, 0xab, 0],
);
s = await execute("|0100 ;count LDA INC BRK @count _{ 01 02 03 }");
assert.equal(s[L.wst], 3);
for (const demo of Object.values(demos))
  assert.ok((await api.assemble(demo.source)).length);
// Address runes, scopes, macros, anonymous labels and forward resolution.
assert.deepEqual(
  [...(await api.assemble("|0100 #01 ?{ #02 } BRK"))],
  [0x80, 1, 0x20, 0, 2, 0x80, 2, 0],
);
s = await execute("%twice { DUP ADD } |0100 #03 twice BRK");
assert.equal(s[L.wst], 6);
s = await execute("|10 @Vars &a $1 |0100 #05 .Vars/a STZ .Vars/a LDZ BRK");
assert.equal(s[L.wst], 5);
for (const source of [
  "|0100 ;missing",
  "|0100 #zz",
  "( unclosed",
  "|0100 ?{ BRK",
  "|0000 01",
  "|ffff #01",
  "%recur { recur } |0100 recur",
  "|0100 @same @same BRK",
  "|0100 ,far BRK |0200 @far BRK",
]) {
  await assert.rejects(api.assemble(source), undefined, source);
}
assert.equal((await api.boot(new Uint8Array(65281))).length, 0);
assert.equal((await api.run(new Uint8Array(5), 1)).length, 0);
console.log(
  "Programs, assembler diagnostics, wrapping, calls, device ports, screen sprites/layers, bounds, and resumable slices OK",
);
