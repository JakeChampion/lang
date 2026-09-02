# 2026-09-02 — a `Cell[T]` in a tuple or an enum payload was dec'd, never reclaimed

The sibling of the struct-field case fixed earlier today
(`2026-09-02-struct-field-cell-drop.md`), at a third site. That one routed a
`Cell` child through `appendChildDrop`, which the GENERATED `__drop_struct_*`
/ `__drop_tuple_*` / `__drop_enum_*` use. This is the INLINE path.

## Why the generated drop fn was not enough

For a tuple or enum LOCAL the function-exit sweep does not call the generated
drop fn. It emits its own copy of the drop and hands each rc-tracked child to
`dropStructField`. The generated `__drop_tuple_*` is still emitted and still
correct — it just only ever reaches the reinit drop of the slot's PREVIOUS
value, which is null. Reading the op stream is what showed this; the
whole-program symbol counts do not, because both spellings contain the helper.

`dropStructField` frees a plain ARRAY child unconditionally
(`__fern_arr_dec` with the element stride) but had no `Cell` arm, so a cell
fell past it to `decValueOnStack(t, false)`. That function's `Cell` arm exists
but is gated on `mayFree`, which a child always passes as false — so the whole
reclaim collapsed to `__fern_rc_dec`, which decrements and returns.

The two arms side by side in one exit sweep, before:

| child        | emitted                                    |
|--------------|--------------------------------------------|
| `i32[]`      | `load; const 4; call __fern_arr_dec`       |
| `Cell[i32]`  | `load; rc.dec __fern_rc_dec`               |

## Measured

Conformance census, `FERN_RC_TRACE` pointer pairing, x86-64, unpaired
allocations:

| shape                                          | before | after |
|------------------------------------------------|--------|-------|
| `(i32, Cell[i32])`                              | 1      | 0     |
| `(i32, Cell[string])`, accumulating             | 1      | 0     |
| `enum H { Has(Cell[i32]) }`                     | 1      | 0     |
| `enum H { Has(Cell[string]) }`, via a binding   | 1      | 0     |
| two scalar-cell tuples in one function          | 2      | 0     |
| scalar-cell tuple + string-cell enum payload    | 2      | 0     |
| two accumulating string-cell tuples             | 2      | 1     |

No existing census row moved, and no fixture crashed. The same container
holding a plain `i32[]` was clean throughout, before and after — which is what
identified the leak as cell-specific rather than a container-drop gap.

## Why freeing here is safe without borrow tracking

`mayFree` is conservative because a child's borrow-ness is not tracked. The
array arm beside it already ignores that for the same reason this one can:
`__fern_arr_dec` and `__fern_drop_arr_str` walk and free only at the value's
OWN rc == 1, so a cell anything else still holds is merely decremented.
Witnessed rather than argued — the fixture builds one cell into two tuples and
pairs to 0, and `TestSelfHostStage2FixpointArm64` passes.

## Still leaking: the slot buffer when several string cells share a function

The last row above is the one thing this does not fix, and it is a different
mechanism. N accumulating `Cell[string]` values held by CONTAINERS strand
N − 1 slot buffers:

| accumulating string cells, each in its own tuple | unpaired |
|---------------------------------------------------|----------|
| 1                                                  | 0        |
| 2                                                  | 1        |
| 3                                                  | 2        |

The same cells as top-level LOCALS are clean at any count, and so are plain
accumulating `string` locals — so it is neither the accumulate nor the cell on
its own, but a container-held cell's slot. That is slot accounting rather than
child-drop routing, so it wants its own narrowing.

## Also still open, from the earlier entry

A cell as an ARRAY ELEMENT still strands 1. `dropStructField`'s array arm picks
`__fern_drop_arr_ptr`, which walks with `__fern_rc_dec` and can never free a
cell box, and `arrElemStructDropName` declines `Cell` because `info.Structs`
has no entry for it. The route is a GENERATED per-element loop in the
`genArrDynDropFn` mould — `__drop_arr_dyn_*` already walks a container calling
a per-element destructor — rather than a new runtime helper on four backends.
