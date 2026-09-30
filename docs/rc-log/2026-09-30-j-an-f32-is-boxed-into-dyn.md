# 2026-09-30 — an f32 is boxed into `dyn` (#10841)

Self-host (`ssasem.boxes_into_dyn`).

`2026-09-23-h` admitted every integer, boolean and `f64` into a `dyn` box, and
kept `f32` out because wasm had no f32 scratch local to stash it in. The
typed lowering therefore refused `var xs: dyn Show[] = [41, x]` with `x: f32`
(`array element type: f32 in dyn Show[]`), where native and the interpreter
answer.

The wasm backend holds every float in an f64 local, `f32` included, and
`dyn_prim_valtype` now says so (#10844): an `f32` box is stored and unboxed
through the same f64 scratch and `f64.store` / `f64.load` as an `f64` one.
The register backends store and reload the full word, as they already did.
So `boxes_into_dyn` admits every float.

## Measured

`TestSelfHostDynF32BoxIR`: a `dyn Show[]` over an `i32` and two `f32`s (2.5
and 0.1), and a `dyn cmp.Display[]` over an `f32` and an `i32`.

- x86-64, arm64 and wasm32 print the interpreter's `41;f2.5;f0.1;0.1;7;`.
  `0.1` prints as itself only when the value is read back at f32 width.
- The x86-64 and wasm census runs balance.
