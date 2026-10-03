# 2026-10-02 — three borrowed rows take `own`

`ir.cp_kill_after`, `ir.cp_loop_body_kill`, `ssalayout.release`. Refs
#8171. No emitted byte changes: the `selfhost-emit-hashes` sweep is
1,965 rows per compiler with 0 differing against a compiler built from
main at 173f70a4, and the `checker.fern` binaries are byte-identical.

## What the profile named

`__fn___fern_arr_slice`, the whole-array copy a `.with` on a shared array
makes, was 299 M on the 31.12 G stage-2 compile of `checker.fern`. Three
of its callers took a row borrowed, stored into it with `.with`, and
returned it to a caller that reassigned its own local through the call:
`cp_kill_after` (13k copies, 28 M), `cp_loop_body_kill` (10k, 26 M) and
`ssalayout.release` (35k, 24 M). Each copied the row once per call,
every call.

## What changed

Each takes the row as `own`, so the self-reassigning `.with` inside
stores in place, and the callers, which already reassign their local
through the call, hand it over.

## Measured

`checker.fern` to a binary under callgrind, 4-core x86-64 container.
"Stage 2" is the compiler the self-host compiler builds from each source
tree; both rows are built from main at 173f70a4 and this change on it.

| | main | this change |
|---|--:|--:|
| stage 2, total Ir | 31.12 G | 31.04 G (−0.27%) |
| stage 2, `const_propagate` inclusive Ir | 371 M | 315 M |
| stage 2, `region_order` inclusive Ir | 313 M | 275 M |
| stage 2, `cp_kill_after` inclusive Ir | 33 M | 4 M |

## A trap

The same run first carried a change to `ssa_lift.lift_impl`: its three
merges scan the slot range from the lowest written slot to the highest
and test each for a row (3.6 M iterations in the block-exit scan on this
compile, 9% of the function's self cost by address bucket), so they
were made to collect the written slots, insertion-sort them and walk
those. It measured 325 M SLOWER: the sort alone was 387 M, because a
scope's written-slot list is long (the main loop of a big function
writes hundreds of slots), and the range scan it replaced skips an
unwritten slot in a dozen instructions. The function's self cost did
not otherwise move: the address buckets the profile named are the ones
holding the `.with` and `append` calls around the scans, not the scans.
Reverted; the lift is as it was.

## Witnessed

`TestSelfHostIRConstNumeric`, `TestSelfHostIRStrengthPeephole`,
`TestSelfHostIRLicm*`, `TestSelfHostSSALoopTailBlockEmittedOnce`,
`TestSelfHostSSALiftGivesEachBlockOneID`,
`TestSelfHostSSABackendAgreesWithNative`,
`TestSelfHostSSAPhiCyclesAcrossSpills`, `TestSelfHostSSASemantic*`, the
lint ratchet, `make fmt-check`, and the emit-hash sweep.

## Next

The remaining `arr_slice` callers copy a row that has to be fresh
(`ssaunits.live_out_row` 60 M, `invariant` 57 M, `copy_row` 27 M) or
have a borrowed receiver with no field to key the in-place analysis on
(`util.NameIndex.added` 19 M). `lift_impl` is 1.07 G self; 23% of it by
address bucket is the main loop's back-edge parallel move, which is the
allocator's, not the source's.
