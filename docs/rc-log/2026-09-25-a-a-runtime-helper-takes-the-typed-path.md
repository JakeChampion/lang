# A runtime helper takes the typed path

2026-09-25. Self-host typed path. Step 2 of retiring the AST lowering
(`SELFHOST-SEMANTIC-SOURCE.md`, "Retiring the AST lowering").

## What changed

The Fern-source runtime helpers (`asmcore.rt_src_*`) were lowered only by
`irlower`, through `emit_ir_runtime_fern_fn`, whichever lowering the program
itself took. The runtime tail now asks for the typed lowering first:

- `semlower.runtime_bodies(src)` parses a helper's source, checks it, and
  produces it with the same pipeline a module takes. It answers one body per
  function the source defines, or none if the source does not type-check, any
  function keeps the AST lowering, or one needs a drop helper. It neither
  prints the module tally nor exits under strict mode; `FERN_SEM_IR_REPORT`
  prints `runtime <name>: produced`.
- It reaches the backends as a function value, `ircore.Sub.rt_lower` and then
  `asmcore.EmitState.rt_lower`, which the CLI sets. A backend never links the
  pipeline, the same size decision as the substitution itself. Every other
  driver keeps `asmcore.no_rt_lower`.

A helper produces once its source type-checks, which the raw floor's types
decide. `chr`, `str_concat` and the four integer `to_string` helpers spell
their buffers as `usize` now and take the typed path; every other helper still
spells an address as an `i32`, fails the checker, and keeps the AST lowering.

`chr` itself had no typed contract, so any module calling it fell back to the
AST lowering, which leaks the strings it builds. It has one now.

## The native bug under it

The function-typed field made `CheckCtx`, which `asmcore.check_stmt` threads
by reassignment, a type without a wired deep drop. Native released a
reference the caller never handed over there, and the natively built compiler
corrupted its heap on any program with two functions. That is #10237, fixed
in #10238. #10239 wired closure fields, which keeps `EmitState` deep-drop
wired.

## Measured

- `TestSelfHostSemanticProduction`'s `runtime-helpers-take-the-typed-path`
  row: `chr` and string `+` in a loop. The report carries both
  `runtime __fern_chr: produced` and `runtime __fern_str_concat: produced` on
  x86-64 and arm64, and the sanitize leg reclaims whole. On main the module
  refuses on `chr`, so the row's produced count fails.
- The typed bodies of `chr` and `str_concat` emit the same instructions as the
  AST ones. A program using both, and a whole `checker.fern` compile, are
  byte-identical to main's output.
- The natively built compiler on `checker.fern`: 16.8 s, 942 MB, the same as
  main's.
