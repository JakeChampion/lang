// Unit tests for web/wasi-shim.js, the browser's preview-1 host.
//
// The shim is the page's half of running a Fern program, and it was missing
// the two things a compiler driver needs: it implemented no `fd_read` and
// reported `argc = 0`, so a guest could receive neither its input nor its
// mode. `examples/self_host/playground_run.fern` wants exactly those — source
// on stdin, `-check` / `-interp` in argv — so the self-hosted compiler could
// not be hosted in a page no matter how it was built.
//
// Every program here is compiled by the real `fern` with `-emit
// command-module` and run through the real shim, so the test pins the contract
// between the two rather than a restatement of either.

import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { before, describe, it } from "node:test";

import { runCoreWasm } from "../../wasi-shim.js";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
let fernBin;
let workDir;

before(() => {
  workDir = mkdtempSync(join(tmpdir(), "fern-shim-"));
  fernBin = join(workDir, "fern");
  execFileSync("go", ["build", "-o", fernBin, "./cmd/fern"], { cwd: repoRoot });
}, { timeout: 300000 });

// compile turns Fern source into the preview-1 command module the shim runs.
function compile(name, src) {
  const srcPath = join(workDir, `${name}.fern`);
  const outPath = join(workDir, `${name}.wasm`);
  writeFileSync(srcPath, src);
  execFileSync(
    fernBin,
    ["-target", "wasm32-wasi", "-emit", "command-module", "-o", outPath, srcPath, join(repoRoot, "internal/stdlib")],
    { cwd: repoRoot },
  );
  return readFileSync(outPath);
}

describe("stdin", () => {
  it("reaches the guest", async () => {
    const bin = compile("echo", `import "std/io";
function main(): i32 {
  print("got:" + io.read_all_stdin());
  return 0;
}`);
    const r = await runCoreWasm(bin, { stdin: "hello" });
    assert.equal(r.stdout, "got:hello\n");
    assert.equal(r.exit, 0);
  });

  // A reader loops until a zero-byte read, so the buffer must not be served
  // twice: a shim that restarted it would hang the guest forever.
  it("is consumed once, then reads empty", async () => {
    const bin = compile("twice", `import "std/io";
function main(): i32 {
  var a: string = io.read_all_stdin();
  var b: string = io.read_all_stdin();
  print("a=[" + a + "] b=[" + b + "]");
  return 0;
}`);
    const r = await runCoreWasm(bin, { stdin: "once" });
    assert.equal(r.stdout, "a=[once] b=[]\n");
  });

  // Nothing passed is an immediate EOF, not a missing import: the old shim
  // trapped with "unsupported WASI call: fd_read" instead.
  it("defaults to empty rather than trapping", async () => {
    const bin = compile("empty", `import "std/io";
function main(): i32 {
  print("[" + io.read_all_stdin() + "]");
  return 0;
}`);
    const r = await runCoreWasm(bin);
    assert.equal(r.stdout, "[]\n");
  });

  // Bytes, not characters: a shim splitting UTF-8 by code unit would corrupt
  // any non-ASCII source the page hands the compiler.
  it("carries non-ASCII bytes intact", async () => {
    const bin = compile("utf8", `import "std/io";
function main(): i32 {
  print(io.read_all_stdin());
  return 0;
}`);
    const r = await runCoreWasm(bin, { stdin: "héllo — ok" });
    assert.equal(r.stdout, "héllo — ok\n");
  });
});

describe("argv", () => {
  it("reaches the guest in order", async () => {
    const bin = compile("argv", `function main(): i32 {
  var n: i32 = 0;
  for a in args() {
    print(a);
    n = n + 1;
  }
  return n;
}`);
    const r = await runCoreWasm(bin, { args: ["prog.wasm", "-check", "x"] });
    assert.equal(r.stdout, "prog.wasm\n-check\nx\n");
    // The count comes back as the exit code, so args_sizes_get and args_get
    // agreeing is what makes this pass.
    assert.equal(r.exit, 3);
  });

  it("defaults to none", async () => {
    const bin = compile("noargv", `function main(): i32 {
  var n: i32 = 0;
  for a in args() { n = n + 1; }
  return n;
}`);
    const r = await runCoreWasm(bin);
    assert.equal(r.exit, 0);
  });
});

describe("the exit code", () => {
  // `_start` ends in proc_exit(main()), so a plain `return` is reported. It
  // used to be dropped, and the page showed 0 for every program that did not
  // call exit() itself.
  it("is main's own return", async () => {
    const bin = compile("ret", `function main(): i32 { return 20; }`);
    assert.equal((await runCoreWasm(bin)).exit, 20);
  });

  it("stays 0 for success", async () => {
    const bin = compile("ok", `function main(): i32 { return 0; }`);
    assert.equal((await runCoreWasm(bin)).exit, 0);
  });
});

describe("the streams", () => {
  it("stay separate", async () => {
    const bin = compile("streams", `function main(): i32 {
  print("out");
  eprint("err");
  return 0;
}`);
    const r = await runCoreWasm(bin);
    assert.equal(r.stdout, "out\n");
    assert.equal(r.stderr, "err\n");
  });
});
