# 2026-10-13 — an allocation sizes the array it returns

`ssabounds.proven_indices`, `ssarc.with_set_op`. Refs #8171. Emitted bytes
change: fewer bounds checks.

## The shape

The compiler builds its tables with `__alloc_i32(n)` and its siblings, fills
them under `while (i < n)` with `with`, and reads them back under the same
guard. The bounds pass proved an index only against a `length` read of the
array, so a guard against `n` proved nothing, and every such write and read
kept its check: a load of the length, a compare and a branch, in the loops
the register allocator, the lift and the liveness pass spend their time in.

A `with` had no way to be marked proven at all. The one unchecked `with` the
lowering emitted was the write-back right after a read of the same slot,
which `ssaunits.checked_writes` finds.

## What changed

- `length(b)` is also the size `b` was built at: the argument of the
  `__alloc_u8` / `__alloc_i32` / `__alloc_i64` / `__alloc_bool` call `b` is,
  carried through each `with` (which writes an element of an array of that
  size) and through a phi whose operands all carry one value, the phi assumed
  to as a loop carries the array it writes (`built_len`). An allocation
  aborts on a negative size, so after it the length is the size. A call
  result, an `append` or a slice carries nothing, so an array that passes
  through a helper is back to its `length` reads.
- A `with` whose index the pass proves is marked `imm` 1, and the lowering
  emits it `in_bounds`, as it already did for the plan's checked writes. The
  register backends skip the check; wasm keeps its own.
- `y + 1` is at least zero when `y` is, and a fact puts `y` strictly below
  any i32, not only below a `length`: `y < z` already caps `y + 1` at the
  largest i32.

## Measured

Static sites in the compiler's own emitted x86-64 asm (`-emit asm` of
`fern.fern`): `__fern_oob_abort` 11,647 → 11,411.

The stage-2 compile of `checker.fern` to a binary under callgrind, each
compiler built from its own tree by itself, both compiling one frozen copy of
main at 6fdb1671, each on a warm certifier memo:

| | Ir | bounds-check branches run |
|---|--:|--:|
| main | 14.867 G | 264.4 M |
| alloc-sized proofs | 14.825 G (−0.29%) | 238.5 M (−9.8%) |

`util.minus_ones` loses every check (46.9 M → 33.8 M self), and
`regalloc_linear`, `prune_dead`, `register_form`, `region_order` and
`compute_checked` each a few million. The pass itself costs 0.6 M more.

WHOLE_COMPILER_ROW

What is left runs on indexes the program's own invariants keep in range and
nothing syntactic does: value ids into per-value tables (`def_blk[a]`,
`sites.blk[i]`), positions computed as `bi * words + w`, and arrays that
pass through a helper (`row = add_use(row, ...)`), whose result length a call
does not carry. A per-function "the result has parameter k's length" summary
would reach the last of those; on this profile that is about a tenth of the
checks that remain.

`TestSelfHostGuardBounds{X86_64,Arm64,Wasm}` gain the alloc-sized shapes
(filled and summed, sized by a field, a row rewritten in a nested loop, a
mark table), two twins counting the checks removed, and six programs that
must still trap: a write at the size, a read of the first element of an
array whose size may be zero, a guard against another allocation's size, a
phi with a shorter operand, an array a call shrank, and one appended in the
loop.
