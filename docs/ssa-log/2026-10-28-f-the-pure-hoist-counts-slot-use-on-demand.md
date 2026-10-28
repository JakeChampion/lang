# 2026-10-28 — the pure hoist counts slot use on demand

`ir.licm_hoist_pure` in loop-invariant code motion. Refs #8171.

## The shape

`licm_hoist_pure` runs once for each loop. Each run called
`licm_slot_use`, which counted the stores and loads of every slot over
the whole function. `licm_slot_use` also called `cp_max_slot`, another
scan of the whole function. So the work grew with the product of the
loop count and the function size. Most loops have no pure sequence to
move, and the counts were not used.

## What changed

`licm_pure_len` is now two parts. `licm_pure_store` finds the shape of a
pure sequence and returns the store at its end. `licm_stored_once`
tests the slot counts. `licm_hoist_pure` makes the counts only when it
finds the first shape. The counts depend only on the operations, so a
later count gives the same result.

`hoist_loop_invariants` already keeps `maxs`, a bound on every slot of
the function. `licm_hoist_pure` now takes `maxs + 1` as the size of its
arrays, so it does not call `cp_max_slot`. The bound can be larger than
the highest slot, but each test reads only the slots of the operations.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 121.256 G (−3.18%) |

The whole-compiler binary is byte-identical to the base compiler's.
