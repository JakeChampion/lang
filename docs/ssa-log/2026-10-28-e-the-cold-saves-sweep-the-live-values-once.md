# 2026-10-28 — the cold saves sweep the live values once

`ssa_cold_saves` in the x86-64 and arm64 emitters. Refs #8171.

## The shape

A cold call saves the caller-saved registers that hold a value live
across it. `ssa_live_caller_regs` found them for one position with a
scan of every value of the function. `ssa_cold_saves` called it once
for each cold position, so the work grew with the product of the two
counts. The scan also called `ssa_nregs` once for each value. The two
cost about 4.4 G Ir in the self-compile.

## What changed

`ssa.live_masks` makes the masks for all the cold positions of a
function in one sweep. It takes the values in the order of their
interval starts, from `order_by_lo`. A value joins the active list when
its interval starts before the position. It leaves the list for good
when its interval ends, because the positions only increase. The mask
at a position comes from the active values only. Both emitters now
collect their cold positions, call `live_masks` once, and delete their
own `ssa_live_caller_regs`.

## Measured

Callgrind, with a private certifier memo that one compile of the subject
warmed. Each compiler is built by itself from its own tree. The subject
is a frozen copy of main at 293eaa55e, with #13768.

| subject | main at 293eaa55e | this |
|---|--:|--:|
| `compiler/fern.fern`, the whole compiler | 125.239 G | 121.056 G (−3.34%) |

The whole-compiler binary is byte-identical to the base compiler's. The
arm64 output of the subject is byte-identical too.
