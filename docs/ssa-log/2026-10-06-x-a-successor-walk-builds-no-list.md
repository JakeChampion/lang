# 2026-10-06 — a successor walk builds no list

`ssa.successor_positions` and the copy `ssadeps` kept of it, the way every
graph walk in the register path reached a block's successors. Refs #8171.
No emitted byte changes: the compiler before and after builds
`checker.fern` for x86-64, arm64 and wasm, and `fern.fern` for x86-64, byte
for byte.

## What changed

Each returned the successors as a fresh array of one or two positions,
allocated, filled, walked and freed: 324 k times from the loop-weight,
forwarding and predecessor passes and 385 k times from the reachability
and dominator walks on a `checker.fern` compile. A terminator has at most
two branches, so the walks now read them by index through
`ssa.successor_id` and look each position up as they go. `read_sites`,
which stores the lists, keeps the list form; `ssadeps`' copy is gone.

## Measured

`checker.fern` built for x86-64-linux by the stage-2 compiler under
callgrind, 4-core x86-64 container. Both stage-2 compilers are built by the
same stage-1 compiler, built from main at 2e084b79 by the stage0 pin.

| | before | this change |
|---|--:|--:|
| stage 2, x86-64 target, total Ir | 16.862 G | 16.822 G (−0.24%) |
| `ssa.successor_positions`, self | 22.2 M | inlined into `read_sites`, its one caller |
| `ssadeps.successor_positions`, self | 13.3 M | gone |
| `__fern_arr_dec`, self | 439.7 M | 426.5 M |
| `__fern_alloc`, self | 343.4 M | 335.5 M |

`ssa.successor_id` is 15 M of calls in its place: the leaf inliner does
not splice it, so each branch read is a call.

## What is left

The same per-call list stands behind `ssalive.successor` (already by
index), `ssaunits.successors` and the layout's `l.succ` table, which is
built once per function and read in place.
