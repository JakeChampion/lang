# 2026-10-06 — typed zero-filled arrays are builtins

`__alloc_i32`, `__alloc_i64`, `__alloc_bool`, the typed siblings of
`__alloc_u8`. Refs #8171. No emitted byte changes: the branch compiler and
main's build `checker.fern` for x86-64, arm64 and wasm and `fern.fern` for
x86-64 byte for byte.

## The shape

The compiler builds a zero-filled array by appending one zero at a time:

```fern
pub function zeros(n: i32): i32[] {
  let out: i32[] = [];
  let i: i32 = 0;
  while (i < n) { out = out.append(0); i = i + 1; }
  return out;
}
```

`ssa.zeros`, `ssalive.words_of`, `ssaunits.bits`, `ssadeps.flags` and
`ssadeps.indices`, and `ssa_lift.filled` are all this loop, and together
they cost about 300 M Ir of the 18.3 G stage-2 profile over
`checker.fern`: a bounds check, a push through `__fern_arr_push`, and a
capacity doubling every power of two, for an array whose every element is
known before the first store. `__alloc_u8` already answers the same need
for bytes in one runtime call.

## What changed

The three builtins take `n: i32` and return a fresh `i32[]`, `i64[]` or
`boolean[]` of n zeros, owned by the caller, on every engine:

- the checkers (`checker.fern`'s `intrinsic_result`, `intrinsic_params`,
  `registered_intrinsics` and `ow_fresh_builtins`; the Go checker's
  `FuncSigs`) type them and count the result as fresh;
- the typed lowering (`semsource.memory_contracts`,
  `ssarc.raw_memory_site`) lowers each to `alloc_u8` with the element width
  on the op (`ir.alloc_slots`): the register backends already allocate
  that op as n zeroed 8-byte slots through `__fern_alloc_u8`, so nothing
  changes there; the wasm backend now reads the width for its stride, a
  byte for the packed `u8[]` as before, 8 for an `i64[]` and 4 otherwise;
- both interpreters build the array of the element type's zero, and the Go
  IR oracle's builtin tables (`freshResultBuiltin`, `rhsTainted`,
  `rcInert`, `providedSigs`) carry the names.

`TestSelfHostAllocFilled` runs four programs over the three builtins on
x86-64, arm64 and wasm against the Go interpreter; the two intrinsic
parity tests pin the registries.

## What is left

The compiler's own sources cannot use the builtins until a published
stage0 compiles them (`docs/BOOTSTRAP.md`). Once the pin is refreshed,
the six loops above become one call each; that is the measured slice.
