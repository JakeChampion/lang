# A wide scalar is boxed into `dyn` at its width

2026-09-23. On the self-host, a module that coerced an `i64` or `f64` to
`dyn Trait` did not build on any target (#10098). Native builds it.

- **The typed path refused it.** `ssasem.boxes_into_dyn` admitted only
  one-word values, because the wasm box held one i32.
- **The AST lowering refuses the module** ("not IR-eligible"), so nothing
  was left to fall back to.

## What changed

- **wasm.** `emit_wasm_dyn_box` stores the value at the width the
  primitive's name gives (`dyn_prim_valtype`): `i64.store` for `i64` /
  `u64`, `f64.store` for `f64`, `i32.store` for everything else. The
  dispatch's primitive arm unboxes with the matching load.
  - The op needed no new operand, because `op_dyn_box` already carries the
    primitive's name.
  - The register backends already stored and reloaded the full word.
- **Typed path.** `boxes_into_dyn` admits every integer, boolean and `f64`,
  and `ssarc.wide_element_site` lists `dyn_box`. That list is the
  construction check that a 64-bit operand has a store width. `f32` stays
  out, because wasm has no f32 scratch local to stash it in.

## Measured

`a-wide-scalar-dyn-value-is-boxed-at-its-width`: six trips over an `i64`
above 2^32, an `f64` with a fraction, and a boolean. Each is returned from
a function as `dyn Show` and dispatched.

- x86-64, arm64 and wasm32 all answer native's 31.
- The x86-64 sanitizer run frees 6 of 6 boxes.

The high half of the `i64` and the fraction of the `f64` both reach the
answer, so a store at the wrong width would change it.
