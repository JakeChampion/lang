# 2026-10-06 — an element handed beside its dying array is held

`ssaunits.held_elements`, every target; `semsource.header_phis`. Refs #8171.

## The shape

`semsource.define` appends a value's type to the state's `values` table and
rebuilds the state around it, the update that consumes its base's box. On
the stage-2 compile of `checker.fern` the out-of-line `define` still copied
that table and the block's instruction list on every call it reached:
89,499 calls, 139,249 element-array copies, 104 M instructions of
`__fern_arr_inc_elems` alone and the matching releases behind them.

Two causes, found with a trace of the unit plan (`choose_ids`) over the
compiler's own sources:

- **An element read handed beside its array.** `merge_binding` calls
  `define(s, 8, args, 0, "", s.values[first])`. The read `s.values[first]`
  is anchored to `s`, and a borrow anchored to a value keeps that value alive
  wherever the borrow is: `lent_owners` pinned `s` for the call, the one
  counted supply became a retain, and the callee found every buffer shared.
  `held_elements` already gives such a read a unit of its own when it
  outlives a consuming use of its array (#9849). It now does the same when
  the read is an operand of the very operation that consumes the array, and
  the array dies there: the hold retains the element at the read and drops
  it after the call, the dependency on the array is gone, and the array
  moves. The hold is only taken where the array dies at that operation, so
  a call that keeps reading the array afterwards pays nothing new.

  The same shape, checked on a program of its own: a callee handed `t` and
  `t.names[at]` beside it allocated 114 times for 32 appends and allocates 46
  with the hold (`TestSelfHostSemanticAllocationCounts`,
  `an-element-handed-beside-its-dying-array`).

- **`header_phis` read the state after the call.** It passed `s` to `define`
  and read `s.names[at]` in the statement after, so `s` was live across the
  call and retained for it. The name is read first now.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, main at d102187b against this branch. The generated code changes
by design, so the gates are the rc and ownership suites, the allocation
pins and the fixture corpus rather than byte identity:

| | main | this branch |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 18.687 G | 18.586 G (−0.54%) |
| `__fern_arr_inc_elems`, self | 231 M | 187 M |
| `__sem_release_typeinfo__Type`, self | 148 M | 125 M |
| `scripts/selfhost-alloc-bench`, `checker.fern` allocations | 28,245,506 | 28,203,934 (−0.15%) |

The allocation count stays inside the ratchet's tolerance against
`.github/alloc-baseline.txt`, which already trails main, so the baseline is
not moved here.

## What is left

The copies that remain reach `define` through `expr`, and the trace places
their origin above it: `stmt` hands its state to `expr`, and `stmts` to
`stmt`, as a retain of a value that is dead at the call and pinned by
nothing, so it is a value the plan does not own there. A phi of the
parameter and a projection out of `line_mark`'s `Value`, the one shape
`stmt` writes itself, moves cleanly on a program of its own, so the cause
is in what the inliner folds into `stmt` and is not yet named.
