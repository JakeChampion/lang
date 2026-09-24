# An ineligible string local releases its reference

2026-09-24. Native. #10117.

```
function tag(src: string, i: i32): i32 {
    var x: string = src;
    var out: string = "";
    if (i % 2 == 0) { out = x; }
    return out.len() + i;
}
```

Called four times on a fresh string, `-sanitize` freed none of the four on
x86-64 and two on arm64.

## Cause

Two halves, one per frame.

- `out = x` retains, so `out` holds a counted reference. `out` is not
  freeEligible, and the exit sweep's string arm skipped every ineligible
  string, so nothing gave the reference back. Every other type falls
  through to the flat `__fern_rc_dec`, which never frees. A string can take
  the same release when it holds a count, and a `string` local never holds
  a view (the checker refuses `str` into `string` with E003). But
  ineligible does not imply counted. The sweep skips moved locals and
  borrowed aliases, yet a local bound to a block's tail value
  (`var s = if (c) { var j = a + b; j } else { "" }`) takes `j`'s buffer
  with no retain. Released at exit after `j` freed that buffer, it is a
  use-after-free. The two-word form was missing too: on arm64 and wasm an
  inline string keeps its bytes in the data word, so a flat dec on `data`
  is unsafe.
- The caller's `line` stayed ineligible on x86-64 because
  `stringParamCounted` had no arm for the parameter as the VALUE of an
  assignment, so `tag` looked like it kept the string.

## Change

- The sweep releases an ineligible string without freeing it when every
  store into it was counted (`stringStoresCounted`): a literal, a fresh
  owned value, or an alias the store retains. That is the flat
  `__fern_rc_dec` on single-word x86-64, and a new `__fern_str_rc_dec(data,
  len)` on arm64 and wasm, which returns at once for an inline string and
  hands anything else to `__fern_rc_dec`. Any other ineligible string is
  left alone, as before.
- `stringParamCounted` credits `local = p`: the Assign lowering retains an
  ident source unless it is a move, and a frame-bound alias is never moved.

## Measured

- The issue's program: 4 of 4 blocks freed on x86-64, arm64 and wasm,
  from 0 / 2 / 2. The self-host's x86-64 build already freed 4 of 4.
- `TestConformanceLeakCensusX86_64`: `http_content_type` 5 → 0 unpaired,
  `http_cookies` 8 → 7.
- `TestIneligibleStringLocalReleasesItsReference` pins four shapes on all
  three targets. Seven of the twelve legs fail without the change. A fifth,
  the block-tail binding, is a use-after-free on x86-64 and arm64
  `-sanitize` if the sweep releases every ineligible string.

## Found on the way

A string `if` expression whose arm ends in a local it declares failed wasm
validation (#10187). `exprType` had no `BlockExpr` case, so the arm typed as
nil and the `if` got `(result i32)` instead of the two-word block type. Its
tail names a local not yet in scope, so the new case answers from the block's
own declaration. `TestBlockExprStringTailRuntimeCondition` pins it.
