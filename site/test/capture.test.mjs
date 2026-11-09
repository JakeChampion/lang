// Unit tests for the assembly extraction in scripts/capture.mjs.

import assert from "node:assert/strict";
import { test } from "node:test";

import { mainFunction } from "../scripts/capture.mjs";

test("takes main and its local labels, without CFI or literal-pool directives", () => {
  const asm = [
    "__fn_helper:",
    "    ret",
    "__fn_main:",
    "    .cfi_startproc",
    "__fn_main.r:",
    "    mov x0, #75",
    ".Lssa_main_1:",
    "    ret",
    "    .cfi_endproc",
    "    .ltorg",
    "_start:",
    "    svc #0",
  ].join("\n");
  assert.equal(mainFunction(asm), ["__fn_main:", "__fn_main.r:", "    mov x0, #75", ".Lssa_main_1:", "    ret"].join("\n"));
});

test("gives an empty string when there is no main", () => {
  assert.equal(mainFunction("_start:\n    ret\n"), "");
});
