# 2026-09-02 — a `Cell[T]` in a struct field never reclaimed through the array machinery

A cell is a one-element array box (`[cap|rc|len|slot]`, `emitCellNew`), so it
reclaims through the ARRAY helpers keyed on its instantiation element type —
`emitCellDropOnStack` does exactly that for a cell LOCAL, and `dropFnNameFor`
declines `Cell` so no `__drop_struct_Cell` can be generated against a layout it
cannot see.

A cell held in a struct FIELD took neither path. `appendChildDrop` had a `Map`
special case and no `Cell` one, so the field fell through to the generic
rc-tracked-child arm: `__fern_rc_dec` on the box. That decrements and returns.
Nothing else ever holds that reference, so a `Cell[string]`'s slot buffer and
the box itself were stranded.

## Measured

Conformance census (`FERN_RC_TRACE` pointer pairing, x86-64), unpaired
allocations:

| fixture                                     | before | after |
|---------------------------------------------|--------|-------|
| `cell_string_accumulate`                     | 4      | 2     |
| `cell_struct_field_wide`                     | 6      | 3     |
| `struct Box { c: Cell[string] }`, 1 append   | 1      | 0     |
| `struct Box { c: Cell[string] }`, 3 appends  | 1      | 0     |
| `struct Box { c: Cell[i32] }`                | 1      | 0     |
| two structs sharing one `Cell[string]`, 3 writes through it | 2 | 0 |

Corpus-wide: 32730 → 32725 unpaired, no row rising, no fixture crashing.

The leak was CONSTANT in the number of writes, not linear — each `set` already
released the slot's previous buffer correctly (#8077); only the last one and
the box survived. That is why it read as a small fixed cost rather than growth,
and why a long-running accumulator never made it visible.

## The guard that makes the shared case safe

Two structs can hold one cell — that is the whole point of a cell, and what
a capability bag handed out of a test double is. The drop is emitted inside
the struct's own `rc == 1` gate, but the CELL's rc may still be 2 there. It is
`__fern_drop_arr_str` / `__fern_arr_dec` that make this correct: both walk
elements only at their own `rc == 1` and otherwise just decrement, so the
first struct to drop dec's the cell and the second one frees it. Witnessed —
the shared-cell row above is that shape, and it pairs to 0.

## Trap

`FERN_LEAKCHECK=1` says nothing here. It counts over-*releases*; a leak reads
as a clean 0, and every probe of this shape came back silent. The census's
pointer pairing is what sees it, which is the same lesson as
`../TEST-GATES.md`'s over-retain note — worth repeating because the silent
detector is the one that runs by default.

## Still leaking: a cell held by the other container kinds

The field arm is the one this fixes. A cell reached as an ARRAY ELEMENT, a
TUPLE ELEMENT, or an ENUM PAYLOAD still strands exactly 1, where the same
container holding a plain heap string is clean — so it is the cell, not the
container:

| container                  | plain heap string | `Cell[string]` | `Cell[i32]` |
|----------------------------|-------------------|----------------|-------------|
| `T[]` element              | 0                 | 1              | 1           |
| `(i32, T)` element         | 0                 | 1              | —           |
| `enum H { Has(T), No }`    | 0                 | 1              | —           |

They are the same class but NOT the same fix site, which is why they are not
in this diff. The tuple and enum arms already reach the new `appendChildDrop`
branch — the lowering emits `__fern_drop_arr_str` once for each — so their
residual 1 has some other cause and wants its own narrowing. The array arm
does not reach it at all: `dropStructField`'s ArrayType case picks
`__fern_drop_arr_ptr` for an rc-tracked element, which walks with
`__fern_rc_dec` and so never frees a cell box. Fixing that one needs a walker
that applies the CELL drop per element, i.e. a new runtime helper on four
backends — a change of a different size from this branch.

## Next lead

Two further leaks turned up while narrowing this one, neither cell-shaped. An `Option[<struct>]` returned from a scan strands 1. And
passing a freshly built struct DIRECTLY as a call argument — `f(make())`,
never bound to a local — into a callee that returns a heap struct strands 3,
where binding it to a local first is clean at 0. The second is the sharper
lead: a temporary-argument ownership gap, not a container one.
