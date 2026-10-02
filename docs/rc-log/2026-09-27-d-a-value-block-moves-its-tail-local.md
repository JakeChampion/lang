# 2026-09-27 — a value block moves its tail local; a rebound closure local releases (#10393, #10392)

Three AST-lowering (`FERN_SEM_IR=`) leaks. The semantic lowering balanced all
of them already.

## A value block yielding its own array local (#10393)

```fern
let ps = { let q = [j, j + 1]; q };
```

`lower_value_block` stored the tail into a `$vbi` temp with `tmp = q`. That
assignment retains, because `q` is an array slot. The temp is not an array
slot, so nothing released the retain, and an unannotated `ps` was not an array
slot either. `q`'s own release then only dropped the count back to 1.

The block now moves the local out. When the tail names an array local the block
declared, and that local holds a count the exit sweep would release (not
borrowed, not ownership-flagged, not moved-elided, not an `ENVCAP:` read, and no
defer armed in the block), the block's value is the local's value and the slot
is set to null. Its rebind and exit releases then do nothing.

`lower_stmt_var` makes the binding an array slot whenever the block's tail
local is one, and copies the local's element typing (struct type, `string[]`,
`f64[]`, `f32[]`, `i64[]`, `boolean[]`). The binding holds one count either
way: the moved one, or the retain when the move is declined. Before this,
an unannotated binding had no element typing, so `e[0].len()` on a `string[]`
from a block read the wrong word.

Element credits: `collect_fresh_structarr_names` and `collect_fresh_strarr_names`
treat the binding as a fresh literal when the moved local was bound from one and
the statements between its `let` and the tail pass the class's escape gate for
it. The gate uses an empty borrowable list, so any call argument naming it
declines the credit.

## A closure local rebound in a loop (#10392)

```fern
while (i < 6) { let g: (i32) => i32 = mk(i); … }
```

`lower_stmt_var_closure` stored a fresh closure (a lambda, `__mkclo$`, or a
closure-returning call) without releasing what the slot held. Only the exit
sweep freed the last box. It now stores through `emit_arr_store` and releases
the old value under `arr_slot_shallow_release_ok`, the sweep's own condition.
The hoisted value-position match reaches the same bind, so it is fixed too.

The release parks the new box in a `$rea` temp, so the closure-call census
(`irlower_run.fern`'s `clo_census`) now follows a store of a single-written
temp back to that temp's own store. Without that, a returned closure moved from
`env_call` to `env_other`.

## A destructured scalar tuple literal

The tuple-pattern match desugar reads its scrutinee with
`let (e0, e1) = (i % 3, 0)`, and `lower_stmt_var_destructure` never released
the tuple box. A tuple literal whose elements are all scalar is now released
after its elements are read out. Without an `@` binding, nothing else can see
the box.

## Measured (x86-64, `FERN_LEAKCHECK=1`, AST lowering)

| program | before | after |
|---|---|---|
| #10393 scalar repro | 10 / 0 | 10 / 10 |
| #10393 struct repro | 20 / 10 | 20 / 20 |
| #10392 call repro | 6 / 1 | 6 / 6 |
| #10392 match repro | 4 / 0 | 4 / 4 |
| #10392 tuple-pattern repro | 10 / 0 | 10 / 10 |
| `vblockTailMoveSrc` | 151 / 80, exit 193 (interp 41) | 151 / 151, exit 41 |
| `closureRebindReleaseSrc` | 44 / 14 | 44 / 44 |

`TestSelfHostVblockClosureRelease{X86_64,Arm64,Wasm}` holds three programs to
the interpreter's answer and a balanced census on both lowerings, with the
sanitizer silent on x86-64. The nested-block program runs on the AST lowering
only, because the semantic lowering refuses it (#10436).

## Not covered

- #10437: a closure from a call stored straight into a struct field, an array
  literal or an `append` still leaks on the AST lowering.
- #10438: a value-position if, match or block passed as a call argument is not
  released after the call. A value block's own struct-array local gets no
  element credit.
- #10435: the native compiler miscompiles a struct array bound from a value
  block next to a call whose value block grows its local.
